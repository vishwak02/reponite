// context.go returns a symbol's direct callers and callees at a ref — the
// navigation primitive an agent reaches for most. Pure over a Store's snapshot
// (callees are direct; callers are the reverse edges), so it is tested
// in-sandbox (ADR-018).
package query

import "sort"

// CalleeEdge is one outgoing CALLS edge with its resolution provenance, so an
// agent sees not just what a symbol calls but how confidently each edge is known
// (invariant 5).
type CalleeEdge struct {
	Name             string
	ResolutionMethod string
	Confidence       float64
}

// ContextResult is the direct neighborhood of a symbol in the call graph.
type ContextResult struct {
	Symbol      string
	Ref         string
	Callers     []string
	Callees     []string     // callee names, sorted (kept for simple consumers)
	CalleeEdges []CalleeEdge // same edges with resolution_method + confidence
	Meta        Meta
}

// Context computes the direct callers and callees of symbol at a ref. Test entry
// points (IsTestName) are excluded from callers/callees unless includeTests, so
// production navigation isn't cluttered with test functions.
func Context(s Store, repo, ref, symbol string, includeTests bool) ContextResult {
	sym := symbol
	if names := ResolveSymbol(s, repo, ref, symbol); len(names) > 0 {
		sym = names[0] // resolve bare -> package-qualified id
	}
	snap := s.Snapshot(repo, ref)
	// Non-nil empty slices so absent neighbors marshal as [] not null (consistent
	// JSON for agents).
	callees := []string{}
	edges := []CalleeEdge{}
	for _, c := range snap.Callees[sym] {
		if !includeTests && IsTestName(baseName(c.Name)) {
			continue
		}
		callees = append(callees, c.Name)
		edges = append(edges, CalleeEdge{Name: c.Name, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence})
	}
	callers := []string{}
	for name, cs := range snap.Callees {
		if !includeTests && IsTestName(baseName(name)) {
			continue
		}
		for _, c := range cs {
			if c.Name == sym {
				callers = append(callers, name)
				break
			}
		}
	}
	sort.Strings(callers)
	sort.Strings(callees)
	sort.Slice(edges, func(i, j int) bool { return edges[i].Name < edges[j].Name })
	return ContextResult{Symbol: sym, Ref: ref, Callers: callers, Callees: callees, CalleeEdges: edges, Meta: Meta{Repo: repo, Ref: ref}}
}
