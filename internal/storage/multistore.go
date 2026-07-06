// multistore.go aggregates several repo-scoped stores into one query.Store — the
// read side of a shared team/fleet server (roadmap 4.2). It is pure: it composes
// the query.Store interface and routes each per-repo call to the backing store
// that owns the repo, so cross-repo queries (e.g. query.XImpact, which iterates
// Repos()) work across a whole fleet with no new persistence. Backing stores can
// be in-memory (Mem) or SQLite — the aggregator doesn't care. Unit-tested
// in-sandbox against composed Mem stores (ADR-018).
package storage

import (
	"sort"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

// MultiStore presents several query.Store instances as one. A repo is owned by
// the first backing store that lists it (Repos()).
type MultiStore struct {
	stores []query.Store
	owner  map[string]query.Store
}

var _ query.Store = (*MultiStore)(nil)

// NewMultiStore builds an aggregator over the given stores (order = precedence
// for a repo listed by more than one).
func NewMultiStore(stores ...query.Store) *MultiStore {
	m := &MultiStore{stores: stores, owner: map[string]query.Store{}}
	for _, s := range stores {
		for _, r := range s.Repos() {
			if _, ok := m.owner[r]; !ok {
				m.owner[r] = s
			}
		}
	}
	return m
}

// Repos is the sorted union of every backing store's repos.
func (m *MultiStore) Repos() []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range m.stores {
		for _, r := range s.Repos() {
			if !seen[r] {
				seen[r] = true
				out = append(out, r)
			}
		}
	}
	sort.Strings(out)
	return out
}

func (m *MultiStore) Refs(repo string) []string {
	if s := m.owner[repo]; s != nil {
		return s.Refs(repo)
	}
	return nil
}

func (m *MultiStore) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	if s := m.owner[repo]; s != nil {
		return s.SymbolAt(repo, symbol, ref)
	}
	return query.SymbolRef{}, false
}

func (m *MultiStore) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	if s := m.owner[repo]; s != nil {
		return s.SymbolsAt(repo, ref)
	}
	return nil
}

func (m *MultiStore) Snapshot(repo, ref string) query.RefSnapshot {
	if s := m.owner[repo]; s != nil {
		return s.Snapshot(repo, ref)
	}
	return query.RefSnapshot{}
}

func (m *MultiStore) Files(repo, ref string) []query.File {
	if s := m.owner[repo]; s != nil {
		return s.Files(repo, ref)
	}
	return nil
}

func (m *MultiStore) Manifest(repo, ref string) (content.Manifest, bool) {
	if s := m.owner[repo]; s != nil {
		return s.Manifest(repo, ref)
	}
	return content.Manifest{}, false
}

func (m *MultiStore) ModulePath(repo string) string {
	if s := m.owner[repo]; s != nil {
		return s.ModulePath(repo)
	}
	return ""
}

// ExternalRefsTo fans out across every backing store — a symbol's callers can
// live in any repo of the fleet — and concatenates the hits. Each store returns
// its own hits sorted; the union is not globally re-sorted here, but ximpact
// (its only caller) sorts the merged caller set (repo, ref, caller).
func (m *MultiStore) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	var out []query.ExternalRefHit
	for _, s := range m.stores {
		out = append(out, s.ExternalRefsTo(module, name)...)
	}
	return out
}
