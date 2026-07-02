// mem.go is a pure, in-memory implementation of query.Store for tests and dev
// (and the default backend until the SQLite adapter lands in storage/sqlite). It
// holds the same records the SQLite store will persist, so the query logic can
// be exercised end-to-end in-sandbox (ADR-018). No external dependencies, so
// this package stays compilable/testable in the build sandbox.
package storage

import (
	"sort"

	"github.com/reponite/reponite/internal/content"
	"github.com/reponite/reponite/internal/query"
)

var _ query.Store = (*Mem)(nil)

// SymbolRecord is one symbol's stored facts at a ref.
type SymbolRecord struct {
	SymbolHash    content.Hash
	SignatureHash content.Hash
	BehaviorHash  content.Hash
	BehaviorConf  float64
	Callees       []query.Callee
}

type refKey struct{ repo, ref string }

// Mem is an in-memory query.Store.
type Mem struct {
	repos map[string]struct{}
	refs  map[string]map[string]struct{}
	syms  map[refKey]map[string]SymbolRecord
	files map[refKey][]query.File
	mans  map[refKey]content.Manifest
}

// NewMem returns an empty in-memory store.
func NewMem() *Mem {
	return &Mem{
		repos: map[string]struct{}{},
		refs:  map[string]map[string]struct{}{},
		syms:  map[refKey]map[string]SymbolRecord{},
		files: map[refKey][]query.File{},
		mans:  map[refKey]content.Manifest{},
	}
}

func (m *Mem) touch(repo, ref string) {
	m.repos[repo] = struct{}{}
	if m.refs[repo] == nil {
		m.refs[repo] = map[string]struct{}{}
	}
	m.refs[repo][ref] = struct{}{}
	if m.syms[refKey{repo, ref}] == nil {
		m.syms[refKey{repo, ref}] = map[string]SymbolRecord{}
	}
}

// Put records a symbol at a ref (also registering the repo and ref).
func (m *Mem) Put(repo, ref, name string, rec SymbolRecord) {
	m.touch(repo, ref)
	m.syms[refKey{repo, ref}][name] = rec
}

// PutFile records a file at a ref.
func (m *Mem) PutFile(repo, ref string, f query.File) {
	m.touch(repo, ref)
	k := refKey{repo, ref}
	m.files[k] = append(m.files[k], f)
}

// PutManifest records a ref's manifest.
func (m *Mem) PutManifest(repo, ref string, man content.Manifest) {
	m.touch(repo, ref)
	m.mans[refKey{repo, ref}] = man
}

func (m *Mem) Repos() []string { return sortedKeys(m.repos) }

func (m *Mem) Refs(repo string) []string { return sortedKeys(m.refs[repo]) }

func (m *Mem) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	rec, ok := m.syms[refKey{repo, ref}][symbol]
	if !ok {
		return query.SymbolRef{Present: false}, false
	}
	return asRef(rec), true
}

func (m *Mem) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	out := map[string]query.SymbolRef{}
	for name, rec := range m.syms[refKey{repo, ref}] {
		out[name] = asRef(rec)
	}
	return out
}

func (m *Mem) Snapshot(repo, ref string) query.RefSnapshot {
	snap := query.RefSnapshot{
		Symbols: map[string]query.SymbolFacts{},
		Callees: map[string][]query.Callee{},
	}
	for name, rec := range m.syms[refKey{repo, ref}] {
		snap.Symbols[name] = query.SymbolFacts{
			SymbolHash: rec.SymbolHash, SignatureHash: rec.SignatureHash, BehaviorHash: rec.BehaviorHash,
		}
		if len(rec.Callees) > 0 {
			snap.Callees[name] = rec.Callees
		}
	}
	return snap
}

func (m *Mem) Files(repo, ref string) []query.File { return m.files[refKey{repo, ref}] }

func (m *Mem) Manifest(repo, ref string) (content.Manifest, bool) {
	man, ok := m.mans[refKey{repo, ref}]
	return man, ok
}

func asRef(rec SymbolRecord) query.SymbolRef {
	return query.SymbolRef{
		Present:       true,
		SignatureHash: rec.SignatureHash,
		BehaviorHash:  rec.BehaviorHash,
		BehaviorConf:  rec.BehaviorConf,
	}
}

func sortedKeys(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
