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
	"sync"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/filemode"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/vishwak02/reponite/internal/scip"
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

	// the revision's own SCIP index, committed at its root: its monikers, as
	// a working tree's
	var scipDocs map[string]scip.Document
	var scipLocal map[string]bool
	if f, ferr := tree.File(SCIPFileName); ferr == nil {
		if rd, rerr := f.Reader(); rerr == nil {
			if data, derr := io.ReadAll(rd); derr == nil {
				scipDocs, scipLocal = decodeSCIP(data)
			}
			rd.Close()
		}
	}

	pool := newParsePool()
	var skipped []SkippedFile
	manifests := map[string][]byte{} // module-manifest files by tree path (§8B.2)
	var tracked []string
	cfiles := map[string]string{} // a precise driver's inputs -> blob id
	cextra := map[string]string{} // of the excluded files, those a unit may include
	clinks := map[string]bool{}   // of them, symbolic links (the blob is the target)
	err = tree.Files().ForEach(func(f *object.File) error {
		tracked = append(tracked, f.Name)
		blob := f.Blob.Hash
		if opt.PreciseDriver != nil && opt.preciseInput(f.Name) && f.Mode.IsFile() {
			if ig.Excluded(f.Name, false) {
				cextra[f.Name] = blob.String()
			} else {
				cfiles[f.Name] = blob.String()
			}
			if f.Mode == filemode.Symlink {
				clinks[f.Name] = true
			}
		}
		c, cerr := classifyFile(fileInput{
			path: f.Name, size: f.Size, read: f.Contents,
			load: func() (string, error) { return blobString(r, blob) },
		}, ig, repo, normVer, func(pf *ParsedFile) { pf.SCIP = scipFor(scipDocs, scipLocal, pf.Path, pf.Spans) })
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
	units := opt.Precise
	if opt.PreciseDriver != nil && len(cfiles) > 0 {
		read := func(rel string) ([]byte, error) {
			f, err := tree.File(rel)
			if err != nil {
				return nil, err
			}
			s, err := f.Contents()
			return []byte(s), err
		}
		// the submodules are read only when the driver needs them: a ref none
		// of whose units were compiled does not walk them
		var libs map[string]string
		var readLib func(string) ([]byte, error)
		var libsOnce sync.Once
		loadLibs := func() map[string]string {
			libsOnce.Do(func() { libs, readLib = submoduleInputs(repoDir, rev, 1) })
			return libs
		}
		units = append(units, opt.PreciseDriver(PreciseSource{
			Files: cfiles,
			Extra: cextra,
			Dir:   repoDir,
			Read:  read,
			Libs:  loadLibs,
			Materialize: func() (string, func(), error) {
				// the revision's C/C++ sources, headers and manifests copied
				// out for a compiler to read (like typeCheckTree's Go files),
				// and its submodules' beside them
				removeStaleCopies()
				tmp, err := os.MkdirTemp("", "reponite-cxx-")
				if err != nil {
					return "", nil, err
				}
				src := filepath.Join(tmp, "src")
				put := func(dir, rel string, b []byte, link bool) error {
					p := filepath.Join(dir, filepath.FromSlash(rel))
					if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
						return err
					}
					if link {
						return os.Symlink(string(b), p) // a symlinked header stays a link
					}
					return os.WriteFile(p, b, 0o644)
				}
				// a partial copy would compile into wrong results that are
				// then cached: any failure abandons the compile
				fail := func(err error) (string, func(), error) {
					os.RemoveAll(tmp)
					return "", nil, err
				}
				for rel := range mergedKeys(cfiles, cextra) {
					b, err := read(rel)
					if err == nil {
						err = put(src, rel, b, clinks[rel])
					}
					if err != nil {
						return fail(fmt.Errorf("copying %s out for the compiler: %w", rel, err))
					}
				}
				for rel := range loadLibs() {
					b, err := readLib(rel)
					if err == nil {
						err = put(LibDir(src), rel, b, false)
					}
					if err != nil {
						return fail(fmt.Errorf("copying submodule file %s out for the compiler: %w", rel, err))
					}
				}
				return src, func() { os.RemoveAll(tmp) }, nil
			},
		})...)
	}
	if err := indexFiles(w, repo, ref, normVer, files, precise, opt.Peers, goMods, units); err != nil {
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

// removeStaleCopies removes source copies an interrupted --precise on run
// left in the temp directory (a day old: none still in use).
func removeStaleCopies() {
	old, _ := filepath.Glob(filepath.Join(os.TempDir(), "reponite-cxx-*"))
	for _, d := range old {
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > 24*time.Hour {
			os.RemoveAll(d)
		}
	}
}

// submoduleInputs lists the C/C++ files of every submodule the revision pins
// whose objects are on this machine (recursively), by their path in the
// parent's tree, and reads them: what a C/C++ build of the revision compiles
// against.
func submoduleInputs(repoDir, rev string, depth int) (map[string]string, func(string) ([]byte, error)) {
	files := map[string]string{}
	type src struct {
		repo *git.Repository
		hash plumbing.Hash
	}
	from := map[string]src{}
	var walk func(dir, rev, prefix string, depth int)
	walk = func(dir, rev, prefix string, depth int) {
		pins, err := GitSubmodulePins(dir, rev)
		if err != nil || depth > 5 {
			return
		}
		for _, p := range pins {
			if p.RepoDir == "" {
				continue
			}
			r, err := git.PlainOpen(p.RepoDir)
			if err != nil {
				continue
			}
			c, err := r.CommitObject(plumbing.NewHash(p.Commit))
			if err != nil {
				continue
			}
			t, err := c.Tree()
			if err != nil {
				continue
			}
			base := path.Join(prefix, p.Path)
			_ = t.Files().ForEach(func(f *object.File) error {
				if preciseInput(f.Name) && f.Mode.IsFile() && f.Mode != filemode.Symlink {
					rel := path.Join(base, f.Name)
					files[rel] = f.Blob.Hash.String()
					from[rel] = src{r, f.Blob.Hash}
				}
				return nil
			})
			walk(p.RepoDir, p.Commit, base, depth+1)
		}
	}
	walk(repoDir, rev, "", depth)
	read := func(rel string) ([]byte, error) {
		s, ok := from[rel]
		if !ok {
			return nil, os.ErrNotExist
		}
		str, err := blobString(s.repo, s.hash)
		return []byte(str), err
	}
	return files, read
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

// mergedKeys is the set of keys of both maps.
func mergedKeys(a, b map[string]string) map[string]bool {
	out := make(map[string]bool, len(a)+len(b))
	for k := range a {
		out[k] = true
	}
	for k := range b {
		out[k] = true
	}
	return out
}
