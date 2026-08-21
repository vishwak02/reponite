package interfaces

import "github.com/vishwak02/reponite/internal/query"

type calleeEdgeDTO struct {
	Name             string  `json:"name"`
	ResolutionMethod string  `json:"resolution_method"`
	Confidence       float64 `json:"confidence"`
}

type callerEdgeDTO struct {
	Name   string `json:"name"`
	IsTest bool   `json:"is_test"`
}

type contextDTO struct {
	Symbol string `json:"symbol"`
	Ref    string `json:"ref"`
	// callers/callees are plain names; caller_edges/callee_edges are the same
	// neighbors as objects with their provenance, matching how brief returns
	// neighbors so an agent meets one shape, not two.
	Callers     []string        `json:"callers"`
	Callees     []string        `json:"callees"`
	CallerEdges []callerEdgeDTO `json:"caller_edges"`
	CalleeEdges []calleeEdgeDTO `json:"callee_edges"`
	Meta        metaDTO         `json:"_meta"`
}

// ContextJSON renders callers/callees for a symbol, each callee edge carrying its
// resolution_method and confidence (invariant 5).
func ContextJSON(r query.ContextResult) (string, error) {
	edges := make([]calleeEdgeDTO, 0, len(r.CalleeEdges))
	for _, e := range r.CalleeEdges {
		edges = append(edges, calleeEdgeDTO{Name: e.Name, ResolutionMethod: e.ResolutionMethod, Confidence: e.Confidence})
	}
	callerEdges := make([]callerEdgeDTO, 0, len(r.CallerEdges))
	for _, c := range r.CallerEdges {
		callerEdges = append(callerEdges, callerEdgeDTO{Name: c.Name, IsTest: c.IsTest})
	}
	return marshal(contextDTO{
		Symbol: r.Symbol, Ref: r.Ref, Callers: r.Callers, Callees: r.Callees,
		CallerEdges: callerEdges, CalleeEdges: edges,
		Meta: metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	})
}

type refsDTO struct {
	Repo string   `json:"repo"`
	Refs []string `json:"refs"`
}

// RefsJSON renders the indexed refs of a repo.
func RefsJSON(repo string, refs []string) (string, error) {
	return marshal(refsDTO{Repo: repo, Refs: refs})
}
