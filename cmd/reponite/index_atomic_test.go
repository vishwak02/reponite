//go:build sqlite && treesitter

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// `reponite index` of a revision (--git, --tags, --submodules) re-indexed its
// label by clearing it and writing it back piece by piece, with the commit,
// ruleset and filter stamped last: a server reading the store meanwhile saw
// the label cleared or half-written, and a run interrupted (Ctrl-C, an
// error) left it that way.
func TestIndexGitRevNeverLeavesTheLabelHalfWritten(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := filepath.Join(t.TempDir(), "proj")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range map[string]string{
		"app.py":  "def handle():\n    return render()\n\n\ndef render():\n    return 1\n",
		"util.py": "from app import handle\n\n\ndef main():\n    return handle()\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=t", "-c", "user.email=t@example.com", "commit", "-q", "-m", "c1"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	revs, err := processing.ResolveGitRevs(dir, []string{"HEAD"}, "")
	if err != nil || len(revs) != 1 {
		t.Fatalf("resolve HEAD: %v %v", revs, err)
	}
	rv := revs[0]
	rv.Label = "v1"
	repo := repoName(dir)
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	const filter = "build/"
	if _, err := indexGitRev(context.Background(), st, repo, dir, rv, processing.IndexOptions{}, filter, nil); err != nil {
		t.Fatal(err)
	}
	wantSyms, _, wantFiles, _ := st.RefCounts(repo, "v1")
	if wantSyms == 0 || wantFiles == 0 {
		t.Fatalf("first index stored %d symbols, %d files", wantSyms, wantFiles)
	}
	// complete fails unless the label reads as the complete first index:
	// its symbols and files, ruleset, commit and filter
	complete := func(t *testing.T, when string) {
		t.Helper()
		iv, _ := st.IndexVersion(repo, "v1")
		syms, _, files, _ := st.RefCounts(repo, "v1")
		if iv != version.IndexVer || syms != wantSyms || files != wantFiles {
			t.Fatalf("%s: %s@v1 reads ruleset %d, %d symbols, %d files; want the complete index (ruleset %d, %d symbols, %d files)",
				when, repo, iv, syms, files, version.IndexVer, wantSyms, wantFiles)
		}
		if !st.RefAtCommit(repo, "v1", rv.Commit, version.IndexVer) {
			t.Fatalf("%s: %s@v1 is not recorded at commit %s", when, repo, rv.Commit)
		}
		if got := st.RefFilter(repo, "v1"); got != filter {
			t.Fatalf("%s: %s@v1 filter %q, want %q", when, repo, got, filter)
		}
	}
	complete(t, "after the first index")

	t.Run("a reader during a re-index reads the previous index", func(t *testing.T) {
		// Report runs after every symbol and file of the re-index is written
		iv, syms, files := -1, -1, -1
		opt := processing.IndexOptions{Report: func(processing.IndexSummary) {
			iv, _ = st.IndexVersion(repo, "v1")
			syms, _, files, _ = st.RefCounts(repo, "v1")
		}}
		if _, err := indexGitRev(context.Background(), st, repo, dir, rv, opt, filter, nil); err != nil {
			t.Fatal(err)
		}
		if iv != version.IndexVer || syms != wantSyms || files != wantFiles {
			t.Fatalf("mid-index: %s@v1 reads ruleset %d, %d symbols, %d files; want the complete index (ruleset %d, %d symbols, %d files)",
				repo, iv, syms, files, version.IndexVer, wantSyms, wantFiles)
		}
		complete(t, "after the re-index")
	})

	t.Run("an interrupted re-index leaves the previous index", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		opt := processing.IndexOptions{Report: func(processing.IndexSummary) { cancel() }}
		if _, err := indexGitRev(ctx, st, repo, dir, rv, opt, filter, nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("interrupted re-index returned %v, want context.Canceled", err)
		}
		complete(t, "after an interrupted re-index")
	})

	t.Run("a label of an indexed commit is copied whole", func(t *testing.T) {
		// the --git fast path: the same commit under a second label
		indexOneRev(st, repo, dir, processing.GitRev{Label: "v2", Rev: rv.Rev, Commit: rv.Commit}, processing.IndexOptions{}, false, nil)
		iv, _ := st.IndexVersion(repo, "v2")
		syms, _, files, _ := st.RefCounts(repo, "v2")
		if iv != version.IndexVer || syms != wantSyms || files != wantFiles {
			t.Fatalf("%s@v2 reads ruleset %d, %d symbols, %d files; want a whole copy of v1", repo, iv, syms, files)
		}
		complete(t, "after copying it")
	})

	t.Run("a working-tree index under a label", func(t *testing.T) {
		var iv int
		opt := processing.IndexOptions{Report: func(processing.IndexSummary) { iv, _ = st.IndexVersion(repo, "v1") }}
		// the working tree is the same code: index it over the label v1
		if err := indexDirRef(context.Background(), st, repo, "v1", dir, opt); err != nil {
			t.Fatal(err)
		}
		if iv != version.IndexVer {
			t.Fatalf("mid-index: %s@v1 reads ruleset %d, want %d (the previous complete index)", repo, iv, version.IndexVer)
		}
	})
}
