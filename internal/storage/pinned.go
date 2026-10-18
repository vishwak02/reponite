// pinned.go gives a fleet query one ref PER REPO. A deployed system is never
// "every repo at HEAD": an application runs at one tag, which pins its base
// platform at another, whose submodules sit at exact commits. A fleet-wide
// question (who calls this? who publishes that topic?) is only answered for
// that system when each repo is read at its own ref. Pinned wraps any
// query.Store and rewrites the ref of every per-repo call for pinned repos,
// leaving the rest unchanged. Pure (ADR-018).
package storage

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

// Pinned is a query.Store view in which repo r is always read at Pins[r].
type Pinned struct {
	Inner query.Store
	Pins  map[string]string
	// Only, when set, is the one requested ref the pins stand in for (the
	// query's default, "HEAD"): any other ref is an explicit request — a
	// compat target, a diff side — and is read as asked. Empty pins every ref.
	Only string
}

var _ query.Store = (*Pinned)(nil)

// ParsePins reads "repoA=ref1,repoB=ref2".
func ParsePins(s string) (map[string]string, error) {
	out := map[string]string{}
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.Index(part, "=")
		if i <= 0 || i == len(part)-1 {
			return nil, fmt.Errorf("pin %q: want repo=ref", part)
		}
		out[strings.TrimSpace(part[:i])] = strings.TrimSpace(part[i+1:])
	}
	return out, nil
}

// RefFor is the ref repo is read at when a query names ref (query.RefPinner).
func (p *Pinned) RefFor(repo, ref string) string { return p.at(repo, ref) }

func (p *Pinned) at(repo, ref string) string {
	if p.Only != "" && ref != p.Only {
		return ref
	}
	if r, ok := p.Pins[repo]; ok {
		return r
	}
	return ref
}

// Coverage reports, for a query made at ref, which repos are read at an
// indexed ref and which are not (and at which ref). A repo missing its ref
// contributes nothing — which must be said, never passed off as "no results".
func Coverage(s query.Store, ref string) (read []string, missing []string) {
	for _, repo := range s.Repos() {
		r := query.EffectiveRef(s, repo, ref) // any pinning view, wrapped or not
		found := false
		for _, x := range s.Refs(repo) {
			if x == r {
				found = true
				break
			}
		}
		if found {
			read = append(read, repo+"@"+r)
		} else {
			missing = append(missing, repo+"@"+r)
		}
	}
	sort.Strings(read)
	sort.Strings(missing)
	return read, missing
}

func (p *Pinned) Repos() []string           { return p.Inner.Repos() }
func (p *Pinned) Refs(repo string) []string { return p.Inner.Refs(repo) }
func (p *Pinned) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	return p.Inner.SymbolAt(repo, symbol, p.at(repo, ref))
}
func (p *Pinned) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	return p.Inner.SymbolsAt(repo, p.at(repo, ref))
}
func (p *Pinned) Snapshot(repo, ref string) query.RefSnapshot {
	return p.Inner.Snapshot(repo, p.at(repo, ref))
}
func (p *Pinned) Files(repo, ref string) []query.File { return p.Inner.Files(repo, p.at(repo, ref)) }

// FilesMatching reads the pinned ref, path-filtered when the inner store can.
func (p *Pinned) FilesMatching(repo, ref string, keep func(string) bool) []query.File {
	if r, ok := p.Inner.(query.PathFilesReader); ok {
		return r.FilesMatching(repo, p.at(repo, ref), keep)
	}
	var out []query.File
	for _, f := range p.Files(repo, ref) {
		if keep(f.Path) {
			out = append(out, f)
		}
	}
	return out
}
func (p *Pinned) Manifest(repo, ref string) (content.Manifest, bool) {
	return p.Inner.Manifest(repo, p.at(repo, ref))
}
func (p *Pinned) ModulePath(repo string) string { return p.Inner.ModulePath(repo) }
func (p *Pinned) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	return p.keepPinned(p.Inner.ExternalRefsTo(module, name))
}
func (p *Pinned) ExternalRefsToSymbol(moniker string) []query.ExternalRefHit {
	return p.keepPinned(p.Inner.ExternalRefsToSymbol(moniker))
}
func (p *Pinned) MonikersAt(repo, ref string) map[string]string {
	return p.Inner.MonikersAt(repo, p.at(repo, ref))
}

// IndexVersion forwards the ruleset stamp at the pinned ref.
// RefCounts forwards at the pinned ref (query.RefCounter).
func (p *Pinned) RefCounts(repo, ref string) (int, int, int, bool) {
	if rc, ok := p.Inner.(query.RefCounter); ok {
		return rc.RefCounts(repo, p.at(repo, ref))
	}
	return 0, 0, 0, false
}

func (p *Pinned) IndexVersion(repo, ref string) (int, bool) {
	if iv, ok := p.Inner.(query.IndexVersioner); ok {
		return iv.IndexVersion(repo, p.at(repo, ref))
	}
	return 0, false
}

// keepPinned drops cross-repo hits recorded at a ref other than a pinned
// repo's pin: a caller at another version is not a caller in this system.
func (p *Pinned) keepPinned(hits []query.ExternalRefHit) []query.ExternalRefHit {
	out := hits[:0:0]
	for _, h := range hits {
		if r, ok := p.Pins[h.Repo]; ok && h.Ref != r {
			continue
		}
		out = append(out, h)
	}
	return out
}

// Skipped reads the pinned ref (query.SkipReader).
func (p *Pinned) Skipped(repo, ref string) []query.SkippedPath {
	if r, ok := p.Inner.(query.SkipReader); ok {
		return r.Skipped(repo, p.at(repo, ref))
	}
	return nil
}

// DefaultPins pins every repo that has no pin and no indexed HEAD to
// its newest indexed ref, so a query at HEAD (every command's default) reads
// something rather than nothing for a repo indexed only at tags. The repo's own
// tags are preferred over refs it was indexed under as another repo's
// submodule ("app@1.2.0"), and refs are ordered as versions (3.10 after 3.9).
// It returns the pins with the defaults added, and how many were added.
func DefaultPins(s query.Store, pins map[string]string) (map[string]string, int) {
	out := make(map[string]string, len(pins))
	for k, v := range pins {
		out[k] = v
	}
	added := 0
	for _, repo := range s.Repos() {
		if _, ok := out[repo]; ok {
			continue
		}
		refs := s.Refs(repo)
		if len(refs) == 0 || containsRef(refs, "HEAD") {
			continue
		}
		if r := newestRef(refs); r != "" {
			out[repo] = r
			added++
		}
	}
	return out, added
}

func containsRef(refs []string, ref string) bool {
	for _, r := range refs {
		if r == ref {
			return true
		}
	}
	return false
}

// newestRef is the highest version among the repo's own tags, or among its
// submodule-of refs when it has no tags of its own.
func newestRef(refs []string) string {
	var own, sub []string
	for _, r := range refs {
		if isSubmoduleRef(r) {
			sub = append(sub, r)
		} else {
			own = append(own, r)
		}
	}
	pick := own
	if len(pick) == 0 {
		pick = sub
	}
	best := ""
	for _, r := range pick {
		if best == "" || versionLess(best, r) {
			best = r
		}
	}
	return best
}

// isSubmoduleRef: "parent@1.2.0", the ref a repo was indexed under as another
// repo's submodule (a version@sha label like "1.2.0@abc123" is the repo's own).
func isSubmoduleRef(r string) bool {
	i := strings.Index(r, "@")
	return i > 0 && (r[0] < '0' || r[0] > '9') && !(r[0] == 'v' && len(r) > 1 && r[1] >= '0' && r[1] <= '9')
}

// versionLess orders refs as versions, ignoring a submodule-of prefix.
func versionLess(a, b string) bool {
	if i := strings.LastIndex(a, "@"); i >= 0 && isSubmoduleRef(a) {
		a = a[i+1:]
	}
	if i := strings.LastIndex(b, "@"); i >= 0 && isSubmoduleRef(b) {
		b = b[i+1:]
	}
	return query.VersionLess(a, b)
}
