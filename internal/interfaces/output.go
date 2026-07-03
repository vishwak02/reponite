// output.go renders coordinator results as the self-describing JSON envelope
// (§10.3, §8.3): stable lowercase keys and a _meta block, decoupled from the
// internal Go types. This is the wire format the CLI (--json) and MCP tools emit.
package interfaces

import (
	"encoding/json"

	"github.com/vishwak02/reponite/internal/query"
)

func marshal(v interface{}) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

type refDTO struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref"`
}

type metaDTO struct {
	Repo     string   `json:"repo"`
	Ref      string   `json:"ref"`
	Warnings []string `json:"warnings,omitempty"`
}

type verdictDTO struct {
	Repo             string  `json:"repo"`
	Ref              string  `json:"ref"`
	Verdict          string  `json:"verdict"`
	Confidence       float64 `json:"confidence"`
	DirectConfidence float64 `json:"direct_confidence,omitempty"`
	Detail           string  `json:"detail,omitempty"`
}

type compatDTO struct {
	Symbol   string       `json:"symbol"`
	Origin   refDTO       `json:"origin"`
	Verdicts []verdictDTO `json:"verdicts"`
	Meta     metaDTO      `json:"_meta"`
}

// CompatJSON renders an Oracle report (§8.3).
func CompatJSON(r query.CompatReport) (string, error) {
	dto := compatDTO{
		Symbol: r.Symbol,
		Origin: refDTO{Repo: r.Origin.Repo, Ref: r.Origin.Ref},
		Meta:   metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, v := range r.Verdicts {
		dto.Verdicts = append(dto.Verdicts, verdictDTO{
			Repo: v.Repo, Ref: v.Ref, Verdict: string(v.Verdict), Confidence: v.Confidence,
			DirectConfidence: v.DirectConfidence, Detail: v.Detail,
		})
	}
	return marshal(dto)
}

type changeDTO struct {
	Name       string  `json:"name"`
	Change     string  `json:"change"`
	Confidence float64 `json:"confidence"`
}

type diffDTO struct {
	Repo    string      `json:"repo"`
	From    string      `json:"from"`
	To      string      `json:"to"`
	Changes []changeDTO `json:"changes"`
	Meta    metaDTO     `json:"_meta"`
}

// DiffJSON renders a ref-to-ref delta.
func DiffJSON(r query.DiffReport) (string, error) {
	dto := diffDTO{
		Repo: r.Repo, From: r.From, To: r.To,
		Meta: metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, c := range r.Changes {
		dto.Changes = append(dto.Changes, changeDTO{Name: c.Name, Change: string(c.Kind), Confidence: c.Confidence})
	}
	return marshal(dto)
}

type originDTO struct {
	Name       string  `json:"name"`
	Kind       string  `json:"kind"`
	Depth      int     `json:"depth"`
	Confidence float64 `json:"confidence"`
}

type rootcauseDTO struct {
	Target  string      `json:"target"`
	Changed bool        `json:"changed"`
	Origins []originDTO `json:"origins"`
	Note    string      `json:"note,omitempty"`
}

// RootCauseJSON renders a drill-down result (ext §8A).
func RootCauseJSON(r query.RootCauseResult) (string, error) {
	dto := rootcauseDTO{Target: r.Target, Changed: r.Changed, Note: r.Note}
	for _, o := range r.Origins {
		dto.Origins = append(dto.Origins, originDTO{Name: o.Name, Kind: string(o.Kind), Depth: o.Depth, Confidence: o.Confidence})
	}
	return marshal(dto)
}

type matchDTO struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Symbol string `json:"symbol,omitempty"`
}

type grepDTO struct {
	Matches   []matchDTO `json:"matches"`
	Total     int        `json:"total"`
	Truncated bool       `json:"truncated"`
	Scanned   int        `json:"scanned"`
	Note      string     `json:"note,omitempty"`
}

// GrepJSON renders a lexical search result (ext §10A).
func GrepJSON(r query.GrepResult) (string, error) {
	dto := grepDTO{Total: r.Total, Truncated: r.Truncated, Scanned: r.Scanned, Note: r.Note}
	for _, m := range r.Matches {
		dto.Matches = append(dto.Matches, matchDTO{Path: m.Path, Line: m.Line, Text: m.Text, Symbol: m.Symbol})
	}
	return marshal(dto)
}

// SearchJSON renders structural name-search hits.
func SearchJSON(hits []query.SearchHit) (string, error) {
	type hitDTO struct {
		Name   string `json:"name"`
		Ref    string `json:"ref"`
		IsTest bool   `json:"is_test"`
	}
	out := make([]hitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, hitDTO{Name: h.Name, Ref: h.Ref, IsTest: h.IsTest})
	}
	return marshal(out)
}
