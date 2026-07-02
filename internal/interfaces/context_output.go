package interfaces

import "github.com/vishwak02/reponite/internal/query"

type contextDTO struct {
	Symbol  string   `json:"symbol"`
	Ref     string   `json:"ref"`
	Callers []string `json:"callers"`
	Callees []string `json:"callees"`
	Meta    metaDTO  `json:"_meta"`
}

// ContextJSON renders callers/callees for a symbol.
func ContextJSON(r query.ContextResult) (string, error) {
	return marshal(contextDTO{
		Symbol: r.Symbol, Ref: r.Ref, Callers: r.Callers, Callees: r.Callees,
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
