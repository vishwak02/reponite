//go:build sqlite && treesitter

package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// An MCP server refreshes HEAD in the background while it answers tool calls
// (autoIndexOnMount), and stops when its client closes stdin, refresh done or
// not. A refresh used to clear the ref and write it back piece by piece:
// calls in between answered "not defined", and a refresh cut short left the
// repo empty — 0 files, ruleset 0 — for every later session.
func TestIndexHeadNeverLeavesTheRefEmpty(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "proj")
	files := map[string]string{
		"app.py":  "def handle():\n    return render()\n\n\ndef render():\n    return 1\n",
		"util.py": "from app import handle\n\n\ndef main():\n    return handle()\n",
		"ext.c":   "int ext_main(void) { return 0; }\n", // makes the indexer run its precise driver
	}
	write := func() {
		for name, src := range files {
			if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	write()
	repo := repoName(dir)
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := indexHead(context.Background(), st, repo, dir, processing.IndexOptions{}); err != nil {
		t.Fatal(err)
	}
	wantSyms, _, wantFiles, _ := st.RefCounts(repo, "HEAD")
	if wantSyms == 0 || wantFiles == 0 {
		t.Fatalf("first index stored %d symbols, %d files", wantSyms, wantFiles)
	}
	// whole fails unless HEAD reads as the complete first index
	whole := func(t *testing.T, when string, iv, syms, files int) {
		t.Helper()
		if iv != version.IndexVer || syms != wantSyms || files != wantFiles {
			t.Fatalf("%s: %s@HEAD reads ruleset %d, %d symbols, %d files; want the complete index (ruleset %d, %d symbols, %d files)",
				when, repo, iv, syms, files, version.IndexVer, wantSyms, wantFiles)
		}
	}
	read := func(t *testing.T, when string) {
		t.Helper()
		iv, _ := st.IndexVersion(repo, "HEAD")
		syms, _, files, _ := st.RefCounts(repo, "HEAD")
		whole(t, when, iv, syms, files)
		if _, ok := st.SymbolAt(repo, "app.handle", "HEAD"); !ok {
			t.Fatalf("%s: app.handle is not defined", when)
		}
	}
	read(t, "after the first index")

	t.Run("tool calls during a refresh read the previous index", func(t *testing.T) {
		var iv, syms, nfiles int
		// Report runs after every symbol and file of the refresh is written
		opt := processing.IndexOptions{Report: func(processing.IndexSummary) {
			iv, _ = st.IndexVersion(repo, "HEAD")
			syms, _, nfiles, _ = st.RefCounts(repo, "HEAD")
		}}
		if err := indexHead(context.Background(), st, repo, dir, opt); err != nil {
			t.Fatal(err)
		}
		whole(t, "mid-refresh", iv, syms, nfiles)
		read(t, "after the refresh")
	})

	t.Run("a refresh that fails mid-write leaves the previous index", func(t *testing.T) {
		defer write()
		// the precise driver runs between parsing and writing: a file gone by
		// then fails the refresh at its file writes, after the symbol writes
		opt := processing.IndexOptions{PreciseDriver: func(processing.PreciseSource) []processing.PreciseUnit {
			if err := os.Remove(filepath.Join(dir, "app.py")); err != nil {
				t.Error(err)
			}
			return nil
		}}
		if err := indexHead(context.Background(), st, repo, dir, opt); err == nil {
			t.Fatal("the refresh succeeded with a source file gone mid-way")
		}
		read(t, "after a failed refresh")
	})

	t.Run("shutdown abandons a refresh in progress", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		// the client closes stdin while the refresh is writing
		opt := processing.IndexOptions{Report: func(processing.IndexSummary) { cancel() }}
		if err := indexHead(ctx, st, repo, dir, opt); !errors.Is(err, context.Canceled) {
			t.Fatalf("abandoned refresh returned %v, want context.Canceled", err)
		}
		read(t, "after an abandoned refresh")
	})

	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}
