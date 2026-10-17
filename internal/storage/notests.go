// notests.go is a query.Store view without test code (--no-tests,
// REPONITE_NO_TESTS=1). Test symbols, mocks and fixtures answer "who calls
// this / who publishes that / what changed" as loudly as production code, and
// an agent reading a mixed answer takes a test publisher for the real one.
// Filtering at the STORE, not per command, makes every surface (usages,
// context, impls, topics, routes, compat, rootcause, grep, search, semsearch)
// drop the same things by the same rule: a symbol is test code by its
// index-time flag (query.IsTestSymbol), a file by its path (query.IsTestPath)
// — the classification the indexer itself stamps. Pure (ADR-018).
package storage

import (
	"sync"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

// NoTests wraps a store and hides test code from every read.
type NoTests struct {
	Inner query.Store

	mu    sync.Mutex
	tests map[[2]string]map[string]bool // (repo, ref) -> test qids
}

var _ query.Store = (*NoTests)(nil)

func (n *NoTests) testSet(repo, ref string) map[string]bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	k := [2]string{repo, ref}
	if t, ok := n.tests[k]; ok {
		return t
	}
	t := map[string]bool{}
	for qid, sym := range n.Inner.SymbolsAt(repo, ref) {
		if query.IsTestSymbol(sym, qid) {
			t[qid] = true
		}
	}
	if n.tests == nil {
		n.tests = map[[2]string]map[string]bool{}
	}
	n.tests[k] = t
	return t
}

func (n *NoTests) Repos() []string           { return n.Inner.Repos() }
func (n *NoTests) Refs(repo string) []string { return n.Inner.Refs(repo) }

func (n *NoTests) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	sym, ok := n.Inner.SymbolAt(repo, symbol, ref)
	if ok && query.IsTestSymbol(sym, symbol) {
		return query.SymbolRef{}, false
	}
	return sym, ok
}

func (n *NoTests) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	all := n.Inner.SymbolsAt(repo, ref)
	out := make(map[string]query.SymbolRef, len(all))
	for qid, sym := range all {
		if !query.IsTestSymbol(sym, qid) {
			out[qid] = sym
		}
	}
	return out
}

func (n *NoTests) Snapshot(repo, ref string) query.RefSnapshot {
	snap := n.Inner.Snapshot(repo, ref)
	tests := n.testSet(repo, ref)
	if len(tests) == 0 {
		return snap
	}
	out := query.RefSnapshot{Symbols: make(map[string]query.SymbolFacts, len(snap.Symbols)),
		Callees: make(map[string][]query.Callee, len(snap.Callees))}
	for qid, f := range snap.Symbols {
		if !tests[qid] {
			out.Symbols[qid] = f
		}
	}
	for qid, cs := range snap.Callees {
		if tests[qid] {
			continue
		}
		kept := make([]query.Callee, 0, len(cs))
		for _, c := range cs {
			if !tests[c.Name] {
				kept = append(kept, c)
			}
		}
		out.Callees[qid] = kept
	}
	return out
}

func (n *NoTests) Files(repo, ref string) []query.File {
	all := n.Inner.Files(repo, ref)
	out := make([]query.File, 0, len(all))
	for _, f := range all {
		if !query.IsTestPath(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

// FilesMatching hides test files from a path-filtered read too.
func (n *NoTests) FilesMatching(repo, ref string, keep func(string) bool) []query.File {
	k := func(p string) bool { return !query.IsTestPath(p) && keep(p) }
	if r, ok := n.Inner.(query.PathFilesReader); ok {
		return r.FilesMatching(repo, ref, k)
	}
	var out []query.File
	for _, f := range n.Inner.Files(repo, ref) {
		if k(f.Path) {
			out = append(out, f)
		}
	}
	return out
}

func (n *NoTests) Manifest(repo, ref string) (content.Manifest, bool) {
	return n.Inner.Manifest(repo, ref)
}
func (n *NoTests) ModulePath(repo string) string { return n.Inner.ModulePath(repo) }
func (n *NoTests) MonikersAt(repo, ref string) map[string]string {
	return n.Inner.MonikersAt(repo, ref)
}
func (n *NoTests) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	return n.keep(n.Inner.ExternalRefsTo(module, name))
}
func (n *NoTests) ExternalRefsToSymbol(moniker string) []query.ExternalRefHit {
	return n.keep(n.Inner.ExternalRefsToSymbol(moniker))
}

// keep drops cross-repo callers that are test code.
func (n *NoTests) keep(hits []query.ExternalRefHit) []query.ExternalRefHit {
	out := hits[:0:0]
	for _, h := range hits {
		if n.testSet(h.Repo, h.Ref)[h.Caller] {
			continue
		}
		out = append(out, h)
	}
	return out
}

// RefFor and IndexVersion forward the optional interfaces of the wrapped
// store, so pins and ruleset checks see through the filter.
func (n *NoTests) RefFor(repo, ref string) string { return query.EffectiveRef(n.Inner, repo, ref) }
func (n *NoTests) IndexVersion(repo, ref string) (int, bool) {
	if iv, ok := n.Inner.(query.IndexVersioner); ok {
		return iv.IndexVersion(repo, ref)
	}
	return 0, false
}
