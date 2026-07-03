package interfaces

import "github.com/vishwak02/reponite/internal/query"

type calleeEdgeDTO struct {
	Name             string  `json:"name"`
	ResolutionMethod string  `json:"resolution_method"`
	Confidence       float64 `json:"confidence"`
}

type contextDTO struct {
	Symbol      string          `json:"symbol"`
	Ref         string          `json:"ref"`
	Callers     []string        `json:"callers"`
	Callees     []string        `json:"callees"`
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
	return marshal(contextDTO{
		Symbol: r.Symbol, Ref: r.Ref, Callers: r.Callers, Callees: r.Callees, CalleeEdges: edges,
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
