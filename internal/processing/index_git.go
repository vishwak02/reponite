//go:build treesitter

// index_git.go indexes a git ref's *tree content* directly (via go-git), without
// touching the working tree — so a tag, branch, or historical commit can be
// indexed as a real ref with its true commit hash, instead of relabelling
// whatever the working directory currently holds. go-git is pure Go (no CGO); it
// rides the treesitter tag because it reuses parseFile (ParseGo). See ADR-018.
package processing

import (
	"fmt"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// IndexGitRef indexes the Go files in repoDir's git revision rev (a tag, branch,
// SHA, or expression like HEAD~3) under the ref label, reading blob contents from
// the object store rather than the working tree. It returns the resolved commit
// hash so the caller can record it.
func IndexGitRef(w Indexer, repo, ref, repoDir, rev string, normVer int) (string, error) {
	r, err := git.PlainOpen(repoDir)
	if err != nil {
		return "", fmt.Errorf("open git repo %s: %w", repoDir, err)
	}
	h, err := r.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return "", fmt.Errorf("resolve revision %q: %w", rev, err)
	}
	commit, err := r.CommitObject(*h)
	if err != nil {
		return "", err
	}
	tree, err := commit.Tree()
	if err != nil {
		return "", err
	}

	var files []ParsedFile
	err = tree.Files().ForEach(func(f *object.File) error {
		if !strings.HasSuffix(f.Name, ".go") || skipPath(f.Name) {
			return nil
		}
		src, err := f.Contents()
		if err != nil {
			return err
		}
		root, spans, perr := parseFile([]byte(src))
		if perr != nil {
			return perr
		}
		files = append(files, ParsedFile{Path: f.Name, Content: src, Symbols: ExtractGo(root, normVer), Spans: spans})
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := IndexFiles(w, repo, ref, normVer, files); err != nil {
		return "", err
	}
	return h.String(), nil
}

// skipPath drops vendored/generated trees that shouldn't count as repo symbols.
func skipPath(path string) bool {
	for _, seg := range strings.Split(path, "/") {
		if seg == "vendor" || seg == "node_modules" || seg == "testdata" {
			return true
		}
	}
	return false
}
