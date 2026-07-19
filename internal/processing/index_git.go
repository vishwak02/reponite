//go:build treesitter

// index_git.go indexes a git ref's *tree content* directly (via go-git), without
// touching the working tree — so a tag, branch, or historical commit can be
// indexed as a real ref with its true commit hash, instead of relabelling
// whatever the working directory currently holds. go-git is pure Go (no CGO); it
// rides the treesitter tag because it reuses parseFile (ParseGo). See ADR-018.
package processing

import (
	"fmt"
	"path/filepath"
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
	return IndexGitRefWith(w, repo, ref, repoDir, rev, normVer, IndexOptions{})
}

// IndexGitRefWith is IndexGitRef with caller-supplied filters (CLI --exclude).
func IndexGitRefWith(w Indexer, repo, ref, repoDir, rev string, normVer int, opt IndexOptions) (string, error) {
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

	// The exclusion set reads .reponiteignore from the TREE being indexed (not
	// the working tree), so a historical ref is filtered by its own rules.
	ignoreContent := ""
	if f, ferr := tree.File(".reponiteignore"); ferr == nil {
		if s, cerr := f.Contents(); cerr == nil {
			ignoreContent = s
		}
	}
	ig := NewIgnore(ignoreContent, opt.Excludes)

	var files []ParsedFile
	manifests := map[string][]byte{} // module-manifest files by tree path (§8B.2)
	err = tree.Files().ForEach(func(f *object.File) error {
		if ig.Excluded(f.Name, false) || strings.HasPrefix(f.Name, ".") || strings.Contains(f.Name, "/.") {
			return nil // ignore set + dot-dirs/dot-files, mirroring IndexDir
		}
		if IsManifestFile(f.Name) {
			if src, err := f.Contents(); err == nil {
				manifests[f.Name] = []byte(src)
			}
			return nil
		}
		if IsROSFile(f.Name) {
			src, err := f.Contents()
			if err != nil {
				return err
			}
			if pf, ok := rosFile(f.Name, src); ok {
				files = append(files, pf)
			}
			return nil
		}
		rules, ok := RulesForExt(filepath.Ext(f.Name))
		if !ok {
			return nil
		}
		src, err := f.Contents()
		if err != nil {
			return err
		}
		root, spans, perr := parseFileRules([]byte(src), filepath.Ext(f.Name), rules)
		if perr != nil {
			return perr
		}
		if root == nil {
			return nil // no grammar bound for this extension; skip
		}
		files = append(files, ParsedFile{
			Path: f.Name, Content: src, Lang: rules.Name,
			Symbols: Extract(root, rules, normVer), Spans: spans, Imports: Imports(root, rules),
		})
		return nil
	})
	if err != nil {
		return "", err
	}
	if err := IndexFiles(w, repo, ref, normVer, files); err != nil {
		return "", err
	}
	if mod, ok := DetectModulePath(manifests); ok {
		if err := w.SetModulePath(repo, mod); err != nil {
			return "", err
		}
	}
	return h.String(), nil
}

