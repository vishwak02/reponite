//go:build sqlite

package sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vishwak02/reponite/internal/query"
)

// fillRef indexes ref the way the indexer re-indexes one: clear, then write
// it back piece by piece, then stamp the ruleset.
func fillRef(w *Store, ref string, names ...string) error {
	if err := w.ClearRef("r", ref); err != nil {
		return err
	}
	for _, n := range names {
		if err := w.Put("r", ref, n, rec(n, "sig", "beh", 1)); err != nil {
			return err
		}
		if err := w.PutFile("r", ref, query.File{Path: n + ".py", Content: "def " + n + "(): pass\n"}); err != nil {
			return err
		}
	}
	return w.SetIndexVersion("r", ref, 7)
}

// whole reports that r@HEAD reads as the complete a,b index.
func whole(t *testing.T, st *Store, when string) {
	t.Helper()
	iv, _ := st.IndexVersion("r", "HEAD")
	syms, _, files, _ := st.RefCounts("r", "HEAD")
	if iv != 7 || syms != 2 || files != 2 {
		t.Fatalf("%s: r@HEAD reads index_ver=%d, %d symbols, %d files; want the complete index (7, 2, 2)", when, iv, syms, files)
	}
	if _, ok := st.SymbolAt("r", "a", "HEAD"); !ok {
		t.Fatalf("%s: symbol a is not defined", when)
	}
}

func TestSQLiteReplaceIsAllOrNothing(t *testing.T) {
	for _, path := range []string{":memory:", filepath.Join(t.TempDir(), "index.db")} {
		st, err := Open(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.Replace(context.Background(), func(w *Store) error { return fillRef(w, "HEAD", "a", "b") }); err != nil {
			t.Fatal(err)
		}
		whole(t, st, path+": after the first index")

		// a re-index that fails half-way: nothing of it lands
		boom := errors.New("boom")
		err = st.Replace(context.Background(), func(w *Store) error {
			if err := w.ClearRef("r", "HEAD"); err != nil {
				return err
			}
			if err := w.Put("r", "HEAD", "c", rec("c", "sig", "beh", 1)); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("%s: Replace returned %v, want the run's error", path, err)
		}
		whole(t, st, path+": after a failed re-index")
		if _, ok := st.SymbolAt("r", "c", "HEAD"); ok {
			t.Fatalf("%s: a failed re-index's write is visible", path)
		}

		// a re-index that completes replaces the ref whole
		if err := st.Replace(context.Background(), func(w *Store) error { return fillRef(w, "HEAD", "a", "b") }); err != nil {
			t.Fatal(err)
		}
		whole(t, st, path+": after a second index")
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

// Readers of the store — a server's tool calls — keep the previous complete
// index while a re-index writes, and a re-index abandoned mid-way (the
// server shutting down) leaves it as it was.
func TestSQLiteReplaceReadersAndAbandon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Replace(context.Background(), func(w *Store) error { return fillRef(w, "HEAD", "a", "b") }); err != nil {
		t.Fatal(err)
	}
	whole(t, st, "before")

	// mid-write, this store and another process's see the previous index
	other, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	err = st.Replace(context.Background(), func(w *Store) error {
		if err := w.ClearRef("r", "HEAD"); err != nil {
			return err
		}
		if err := w.Put("r", "HEAD", "a", rec("a", "sig", "beh", 1)); err != nil {
			return err
		}
		whole(t, st, "during a re-index (same store)")
		whole(t, other, "during a re-index (another process)")
		// the writer itself reads what it wrote
		if syms, _, _, _ := w.RefCounts("r", "HEAD"); syms != 1 {
			t.Fatalf("the writer reads %d symbols of its own, want 1", syms)
		}
		return fillRef(w, "HEAD", "a", "b")
	})
	if err != nil {
		t.Fatal(err)
	}
	whole(t, st, "after the re-index")

	// cancelled while its transaction is open: rolled back, Close does not hang
	ctx, cancel := context.WithCancel(context.Background())
	err = st.Replace(ctx, func(w *Store) error {
		if err := w.ClearRef("r", "HEAD"); err != nil {
			return err
		}
		cancel()
		time.Sleep(10 * time.Millisecond) // the abandon runs on its own goroutine
		// later writes fail rather than land
		return w.Put("r", "HEAD", "c", rec("c", "sig", "beh", 1))
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("abandoned Replace returned %v, want context.Canceled", err)
	}
	whole(t, st, "after an abandoned re-index")
	whole(t, other, "after an abandoned re-index (another process)")
	done := make(chan error, 1)
	go func() { done <- st.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close hung after an abandoned Replace")
	}
}
