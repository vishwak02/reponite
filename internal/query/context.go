// context.go returns a symbol's direct callers and callees at a ref — the
// navigation primitive an agent reaches for most. Pure over a Store's snapshot
// (callees are direct; callers are the reverse edges), so it is tested
// in-sandbox (ADR-018).
package query

import "sort"

// ContextResult is the direct neighborhood of a symbol in the call graph.
type ContextResult struct {
	Symbol  string
	Ref     string
	Callers []string
	Callees []string
	Meta    Meta
}

// Context computes the direct callers and callees of symbol at a ref.
func Context(s Store, repo, ref, symbol string) ContextResult {
	snap := s.Snapshot(repo, ref)
	var callees []string
	for _, c := range snap.Callees[symbol] {
		callees = append(callees, c.Name)
	}
	var callers []string
	for name, cs := range snap.Callees {
		for _, c := range cs {
			if c.Name == symbol {
				callers = append(callers, name)
				break
			}
		}
	}
	sort.Strings(callers)
	sort.Strings(callees)
	return ContextResult{Symbol: symbol, Ref: ref, Callers: callers, Callees: callees, Meta: Meta{Repo: repo, Ref: ref}}
}
