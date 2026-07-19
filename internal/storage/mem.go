// mem.go is a pure, in-memory implementation of query.Store (and the indexer's
// write interface) for tests and dev, until the SQLite adapter lands. No
// external dependencies, so this package stays compilable/testable in the build
// sandbox (ADR-018). Write methods return error (always nil) to share one
// interface with the SQLite adapter.
package storage

import (
	"sort"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

var _ query.Store = (*Mem)(nil)

// SymbolRecord is one symbol's stored facts at a ref.
type SymbolRecord struct {
	Lang          string // language name (lang.go)
	SymbolHash    content.Hash
	SignatureHash content.Hash
	BehaviorHash  content.Hash
	BehaviorConf  float64 // min confidence over the transitive subgraph (invariant 5)
	DirectConf    float64 // min confidence over this symbol's own direct edges
	Callees       []query.Callee
}

type refKey struct{ repo, ref string }

// Mem is an in-memory query.Store.
type Mem struct {
	repos   map[string]struct{}
	refs    map[string]map[string]struct{}
	syms    map[refKey]map[string]SymbolRecord
	files   map[refKey][]query.File
	mans    map[refKey]content.Manifest
	extrefs map[refKey][]query.ExternalRef
	modules map[string]string // repo -> module_path (§8B)
}

// NewMem returns an empty in-memory store.
func NewMem() *Mem {
	return &Mem{
		repos:   map[string]struct{}{},
		refs:    map[string]map[string]struct{}{},
		syms:    map[refKey]map[string]SymbolRecord{},
		files:   map[refKey][]query.File{},
		mans:    map[refKey]content.Manifest{},
		extrefs: map[refKey][]query.ExternalRef{},
		modules: map[string]string{},
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

// ClearRef drops a ref's symbols, files, and external refs so a reindex replaces them.
func (m *Mem) ClearRef(repo, ref string) error {
	k := refKey{repo, ref}
	delete(m.syms, k)
	delete(m.files, k)
	delete(m.extrefs, k)
	return nil
}

// Put records a symbol at a ref (also registering the repo and ref).
func (m *Mem) Put(repo, ref, name string, rec SymbolRecord) error {
	m.touch(repo, ref)
	m.syms[refKey{repo, ref}][name] = rec
	return nil
}

// PutFile records a file at a ref.
func (m *Mem) PutFile(repo, ref string, f query.File) error {
	m.touch(repo, ref)
	k := refKey{repo, ref}
	m.files[k] = append(m.files[k], f)
	return nil
}

// PutManifest records a ref's manifest.
func (m *Mem) PutManifest(repo, ref string, man content.Manifest) error {
	m.touch(repo, ref)
	m.mans[refKey{repo, ref}] = man
	return nil
}

// PutExternalRefs records a ref's cross-repo dependency edges (§8B).
func (m *Mem) PutExternalRefs(repo, ref string, refs []query.ExternalRef) error {
	m.touch(repo, ref)
	k := refKey{repo, ref}
	if len(refs) == 0 {
		delete(m.extrefs, k)
		return nil
	}
	m.extrefs[k] = refs
	return nil
}

// SetModulePath records repo's module/package identity.
func (m *Mem) SetModulePath(repo, modulePath string) error {
	m.repos[repo] = struct{}{}
	if modulePath != "" {
		m.modules[repo] = modulePath
	}
	return nil
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

func (m *Mem) ModulePath(repo string) string { return m.modules[repo] }

// ExternalRefsTo returns every external reference resolving to (module, name),
// across all repos/refs, sorted (repo, ref, caller) for determinism.
func (m *Mem) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	var out []query.ExternalRefHit
	for k, refs := range m.extrefs {
		for _, r := range refs {
			if r.Module == module && r.Name == name {
				out = append(out, query.ExternalRefHit{
					Repo: k.repo, Ref: k.ref, Caller: r.From,
					Module: r.Module, Name: r.Name,
					ResolutionMethod: r.ResolutionMethod, Confidence: r.Confidence,
					TargetSignatureHash: r.TargetSignatureHash,
				})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Caller < b.Caller
	})
	return out
}

func asRef(rec SymbolRecord) query.SymbolRef {
	return query.SymbolRef{
		Present:       true,
		Lang:          rec.Lang,
		SignatureHash: rec.SignatureHash,
		BehaviorHash:  rec.BehaviorHash,
		BehaviorConf:  rec.BehaviorConf,
		DirectConf:    rec.DirectConf,
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
