// ignore.go decides which paths are excluded from indexing: a default vendored
// set, the repo's .reponiteignore (gitignore syntax), and any --exclude globs.
// Vendored trees (a repo bundling external/ros_comm, a node_modules, …) drown
// search/grep/investigate in third-party noise and inflate the index; excluding
// them is an INDEX-time decision so every downstream surface benefits. Pure and
// stdlib-only (ADR-018) — the build-tagged index adapters (IndexDir/IndexGitRef)
// consult this from their walks.
package processing

import (
	"path"
	"strings"
)

// DefaultIgnorePatterns is the always-on exclusion set: vendored/generated
// trees that should never count as repo symbols. `.reponiteignore` and
// --exclude ADD to it (it cannot be negated away — a repo that truly wants its
// vendor/ indexed can rename it, which is the same stance git takes on .git).
var DefaultIgnorePatterns = []string{
	"vendor/",
	"third_party/",
	"node_modules/",
	".git/",
	"testdata/",
}

// IndexOptions carries caller-supplied indexing filters (CLI --exclude).
type IndexOptions struct {
	// Excludes are extra ignore patterns (gitignore syntax), applied after the
	// defaults and the repo's .reponiteignore.
	Excludes []string
}

// Ignore is an ordered list of gitignore-style patterns; the LAST matching
// pattern decides (so a later "!keep" re-includes), with git's one hard rule:
// once a parent DIRECTORY is excluded, nothing below it can be re-included.
type Ignore struct {
	pats []ignorePat
}

type ignorePat struct {
	segs     []string // pattern split on "/", after cleaning the markers below
	negate   bool     // leading "!" re-includes
	dirOnly  bool     // trailing "/" matches directories only
	anchored bool     // pattern contained a "/" → matched against the full path from the repo root
}

// NewIgnore builds the effective ignore list: the defaults, then the
// .reponiteignore content (may be ""), then any --exclude patterns.
func NewIgnore(reponiteignore string, excludes []string) *Ignore {
	ig := &Ignore{}
	for _, p := range DefaultIgnorePatterns {
		ig.add(p)
	}
	for _, line := range strings.Split(reponiteignore, "\n") {
		ig.add(line)
	}
	for _, p := range excludes {
		ig.add(p)
	}
	return ig
}

// add parses one gitignore-syntax line. Supported (the documented subset):
// blank lines and "#" comments skipped; "!" negation; trailing "/" = directory
// only; a pattern containing "/" is anchored to the repo root, one without
// matches the basename at any depth; "*" (within a segment), "?", "[...]" and
// "**" (any number of segments) wildcards.
func (ig *Ignore) add(line string) {
	p := strings.TrimSpace(line)
	if p == "" || strings.HasPrefix(p, "#") {
		return
	}
	var pat ignorePat
	if strings.HasPrefix(p, "!") {
		pat.negate = true
		p = p[1:]
	}
	if strings.HasSuffix(p, "/") {
		pat.dirOnly = true
		p = strings.TrimSuffix(p, "/")
	}
	if strings.HasPrefix(p, "/") {
		pat.anchored = true // "/build" matches only at the repo root
		p = strings.TrimPrefix(p, "/")
	}
	if p == "" {
		return
	}
	pat.anchored = pat.anchored || strings.Contains(p, "/")
	pat.segs = strings.Split(p, "/")
	ig.pats = append(ig.pats, pat)
}

// Excluded reports whether relPath should be skipped. isDir=true asks about
// the directory itself (a filepath.Walk SkipDir probe); for a file, every
// ancestor directory is checked first — git's rule that an excluded parent
// cannot be re-included from below.
func (ig *Ignore) Excluded(relPath string, isDir bool) bool {
	relPath = strings.Trim(strings.ReplaceAll(relPath, "\\", "/"), "/")
	if relPath == "" || relPath == "." {
		return false
	}
	segs := strings.Split(relPath, "/")
	// Ancestor directories: excluded parent → excluded subtree, no re-include.
	for i := 1; i < len(segs); i++ {
		if ig.decide(segs[:i], true) {
			return true
		}
	}
	return ig.decide(segs, isDir)
}

// decide runs the ordered pattern list over one path; last match wins.
func (ig *Ignore) decide(pathSegs []string, isDir bool) bool {
	excluded := false
	for _, p := range ig.pats {
		if p.dirOnly && !isDir {
			continue
		}
		var hit bool
		if p.anchored {
			hit = matchSegs(p.segs, pathSegs)
		} else {
			// No "/" in the pattern: match the basename (any depth).
			hit = matchSeg(p.segs[0], pathSegs[len(pathSegs)-1])
		}
		if hit {
			excluded = !p.negate
		}
	}
	return excluded
}

// matchSegs matches pattern segments against path segments; "**" spans any
// number of segments (including zero).
func matchSegs(pat, segs []string) bool {
	if len(pat) == 0 {
		return len(segs) == 0
	}
	if pat[0] == "**" {
		for i := 0; i <= len(segs); i++ {
			if matchSegs(pat[1:], segs[i:]) {
				return true
			}
		}
		return false
	}
	if len(segs) == 0 {
		return false
	}
	return matchSeg(pat[0], segs[0]) && matchSegs(pat[1:], segs[1:])
}

// matchSeg matches one pattern segment against one path segment ("*", "?",
// "[...]" — path.Match, which never crosses a separator). A malformed pattern
// simply doesn't match; it never breaks the walk.
func matchSeg(pat, seg string) bool {
	if pat == "**" {
		return true
	}
	ok, err := path.Match(pat, seg)
	return err == nil && ok
}
