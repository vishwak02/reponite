//go:build treesitter

// index_git.go indexes a git ref's *tree content* directly (via go-git), without
// touching the working tree — so a tag, branch, or historical commit can be
// indexed as a real ref with its true commit hash, instead of relabelling
// whatever the working directory currently holds. go-git is pure Go (no CGO); it
// rides the treesitter tag because it reuses parseFile (ParseGo). See ADR-018.
package processing

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
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
		return "", fmt.Errorf("resolve revision %q: %w%s", rev, err, alternatesHint(repoDir))
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

	pool := newParsePool()
	var skipped []SkippedFile
	manifests := map[string][]byte{} // module-manifest files by tree path (§8B.2)
	var tracked []string
	err = tree.Files().ForEach(func(f *object.File) error {
		tracked = append(tracked, f.Name)
		blob := f.Blob.Hash
		c, cerr := classifyFile(fileInput{
			path: f.Name, size: f.Size, read: f.Contents,
			load: func() (string, error) { return blobString(r, blob) },
		}, ig, repo, normVer, nil)
		if cerr != nil {
			return cerr
		}
		switch {
		case c.skip != nil:
			skipped = append(skipped, *c.skip)
		case c.job != nil:
			pool.submit(c.job)
		case c.pf != nil:
			pool.add(*c.pf)
		}
		if c.manifest != nil {
			manifests[f.Name] = c.manifest
		}
		return nil
	})
	files, perr := pool.wait()
	if err == nil {
		err = perr
	}
	if err != nil {
		return "", err
	}
	// Peers let index time capture each cross-repo reference's target contract
	// (§8B.3 skew) — the same as a working-tree index, not silently "unknown".
	goMods := goModules(manifests)
	var precise map[string]map[string][]string
	if len(goMods) > 0 {
		// type-check the revision's Go code like a working tree's
		precise = typeCheckTree(tracked, manifests, func(p string) (string, bool) {
			f, ferr := tree.File(p)
			if ferr != nil {
				return "", false
			}
			s, cerr := f.Contents()
			return s, cerr == nil
		})
	}
	if err := indexFiles(w, repo, ref, normVer, files, precise, opt.Peers, goMods); err != nil {
		return "", err
	}
	if err := recordSkipped(w, repo, ref, skipped); err != nil {
		return "", err
	}
	if opt.Report != nil {
		opt.Report(summarize(files, skipped))
	}
	if mod, ok := DetectModulePath(manifests); ok || len(manifests) > 0 {
		// A tree with manifests but no single repo-level one (a workspace)
		// clears an identity an earlier index recorded.
		if err := w.SetModulePath(repo, mod); err != nil {
			return "", err
		}
	}
	return h.String(), nil
}

// alternatesHint explains the one resolution failure that is not the user's
// revision: a clone made with --shared/--reference keeps its objects in
// another repository (objects/info/alternates), which go-git does not follow.
func alternatesHint(repoDir string) string {
	for _, p := range []string{filepath.Join(repoDir, ".git", "objects", "info", "alternates"), filepath.Join(repoDir, "objects", "info", "alternates")} {
		if _, err := os.Stat(p); err == nil {
			return " — this clone borrows its objects from another repository (" + p + "), which the git reader cannot follow; index that repository instead, or run `git repack -a -d` here"
		}
	}
	return ""
}

// GitRev is one revision to index: the label it is stored under and the
// commit it resolves to.
type GitRev struct {
	Label  string
	Rev    string
	Commit string
}

// ResolveGitRevs expands revisions and a tag glob (path.Match syntax, e.g.
// "3.7.*") into commits, in the order given; tags come sorted by name.
func ResolveGitRevs(repoDir string, revs []string, tagGlob string) ([]GitRev, error) {
	r, err := git.PlainOpen(repoDir)
	if err != nil {
		return nil, fmt.Errorf("open git repo %s: %w", repoDir, err)
	}
	var out []GitRev
	for _, rev := range revs {
		h, err := r.ResolveRevision(plumbing.Revision(rev))
		if err != nil {
			return nil, fmt.Errorf("resolve revision %q: %w%s", rev, err, alternatesHint(repoDir))
		}
		out = append(out, GitRev{Label: rev, Rev: rev, Commit: h.String()})
	}
	if tagGlob != "" {
		iter, err := r.Tags()
		if err != nil {
			return nil, err
		}
		var tags []GitRev
		_ = iter.ForEach(func(ref *plumbing.Reference) error {
			name := ref.Name().Short()
			if ok, _ := path.Match(tagGlob, name); !ok {
				return nil
			}
			h, err := r.ResolveRevision(plumbing.Revision("refs/tags/" + name + "^{commit}"))
			if err != nil {
				return nil // a tag of a non-commit object: nothing to index
			}
			tags = append(tags, GitRev{Label: name, Rev: name, Commit: h.String()})
			return nil
		})
		sort.Slice(tags, func(i, j int) bool { return tags[i].Label < tags[j].Label })
		out = append(out, tags...)
	}
	return out, nil
}

// SubmodulePin is a submodule as a revision of its parent pins it.
type SubmodulePin struct {
	Name    string // .gitmodules name
	Path    string // path inside the parent tree
	Commit  string // the commit the parent's tree points at (gitlink)
	RepoDir string // a directory git can open for the submodule's objects
}

// GitSubmodulePins lists the submodules the parent's revision rev pins, with
// the exact commit of each. A deployed version is its whole pinned tree, and
// the gitlink commit — not the submodule's HEAD — is what that version runs.
// A submodule whose objects are not available locally is returned with an
// empty RepoDir, so the caller can say so instead of silently skipping it.
func GitSubmodulePins(repoDir, rev string) ([]SubmodulePin, error) {
	r, err := git.PlainOpen(repoDir)
	if err != nil {
		return nil, fmt.Errorf("open git repo %s: %w", repoDir, err)
	}
	h, err := r.ResolveRevision(plumbing.Revision(rev))
	if err != nil {
		return nil, fmt.Errorf("resolve revision %q: %w", rev, err)
	}
	commit, err := r.CommitObject(*h)
	if err != nil {
		return nil, err
	}
	tree, err := commit.Tree()
	if err != nil {
		return nil, err
	}
	names := map[string]string{} // path -> name, from the revision's own .gitmodules
	if f, ferr := tree.File(".gitmodules"); ferr == nil {
		if s, cerr := f.Contents(); cerr == nil {
			names = parseGitmodules(s)
		}
	}
	var out []SubmodulePin
	w := object.NewTreeWalker(tree, true, nil)
	defer w.Close()
	for {
		name, entry, err := w.Next()
		if err != nil {
			break
		}
		if entry.Mode != filemode.Submodule {
			continue
		}
		pin := SubmodulePin{Name: names[name], Path: name, Commit: entry.Hash.String()}
		if pin.Name == "" {
			pin.Name = filepath.Base(name)
		}
		// a checkout's submodule dir, its .git/modules entry, or — when repoDir is
		// itself a submodule's git dir — that dir's modules/ entry
		for _, cand := range []string{filepath.Join(repoDir, name), filepath.Join(repoDir, ".git", "modules", pin.Name),
			filepath.Join(repoDir, "modules", pin.Name)} {
			sr, oerr := git.PlainOpen(cand)
			if oerr != nil {
				continue
			}
			if _, cerr := sr.CommitObject(entry.Hash); cerr == nil {
				pin.RepoDir = cand
				break
			}
		}
		out = append(out, pin)
	}
	return out, nil
}

// parseGitmodules maps each submodule path to its name.
func parseGitmodules(s string) map[string]string {
	out := map[string]string{}
	name := ""
	for _, ln := range strings.Split(s, "\n") {
		ln = strings.TrimSpace(ln)
		if strings.HasPrefix(ln, "[submodule") {
			name = strings.Trim(strings.TrimPrefix(ln, "[submodule"), " \"]")
			continue
		}
		if k, v, ok := strings.Cut(ln, "="); ok && strings.TrimSpace(k) == "path" && name != "" {
			out[strings.TrimSpace(v)] = name
		}
	}
	return out
}

// blobString reads one blob of r by hash.
func blobString(r *git.Repository, h plumbing.Hash) (string, error) {
	b, err := r.BlobObject(h)
	if err != nil {
		return "", err
	}
	rd, err := b.Reader()
	if err != nil {
		return "", err
	}
	defer rd.Close()
	var sb strings.Builder
	sb.Grow(int(b.Size))
	_, err = io.Copy(&sb, rd)
	return sb.String(), err
}
