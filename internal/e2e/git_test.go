//go:build sqlite && treesitter

package e2e

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// IndexGitRef reads a commit's tree straight from the object store (no working-
// tree checkout) and records the real commit hash.
func TestIndexGitRefFromCommitTree(t *testing.T) {
	dir := t.TempDir()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(dir, "billing.go"),
		"package billing\n\nfunc Charge() error { return validateCard() }\nfunc validateCard() error { return nil }\n")
	if _, err := wt.Add("billing.go"); err != nil {
		t.Fatal(err)
	}
	h, err := wt.Commit("init", &git.CommitOptions{
		Author: &object.Signature{Name: "t", Email: "t@e", When: time.Unix(0, 0).UTC()},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Mutate the working tree AFTER the commit: git-ref indexing must read the
	// committed content, not what's on disk now.
	write(t, filepath.Join(dir, "billing.go"), "package billing\n// wiped\n")

	st, err := sqlite.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	commit, err := processing.IndexGitRef(st, "billing", "v1", dir, "HEAD", version.NormVer)
	if err != nil {
		t.Fatal(err)
	}
	if commit != h.String() {
		t.Fatalf("recorded commit %s != HEAD %s", commit, h.String())
	}
	// Charge is at repo root -> bare id; indexed from the commit, not the wiped file.
	if _, ok := st.SymbolAt("billing", "Charge", "v1"); !ok {
		t.Fatal("Charge must be indexed from the committed tree")
	}
}
