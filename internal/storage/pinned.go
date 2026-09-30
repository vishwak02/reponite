// pinned.go gives a fleet query one ref PER REPO. A deployed system is never
// "every repo at HEAD": a site runs rr_sootballs at one tag, which pins
// rr_io_amr at another, whose submodules sit at exact commits. A fleet-wide
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

func (p *Pinned) at(repo, ref string) string {
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
		r := ref
		if p, ok := s.(*Pinned); ok {
			r = p.at(repo, ref)
		}
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
