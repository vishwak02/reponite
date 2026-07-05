// output.go renders coordinator results as the self-describing JSON envelope
// (§10.3, §8.3): stable lowercase keys and a _meta block, decoupled from the
// internal Go types. This is the wire format the CLI (--json) and MCP tools emit.
package interfaces

import (
	"encoding/json"

	"github.com/vishwak02/reponite/internal/query"
)

// CompactOutput, when true, emits single-line JSON instead of indented — set by
// the MCP server to save agent tokens; the CLI leaves it false for humans.
var CompactOutput bool

func marshal(v interface{}) (string, error) {
	if CompactOutput {
		b, err := json.Marshal(v)
		return string(b), err
	}
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
	Repo             string   `json:"repo"`
	Ref              string   `json:"ref"`
	Verdict          string   `json:"verdict"`
	Confidence       float64  `json:"confidence"`
	DirectConfidence float64  `json:"direct_confidence,omitempty"`
	Detail           string   `json:"detail,omitempty"`
	ChangedCallees   []string `json:"changed_callees,omitempty"`
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
			DirectConfidence: v.DirectConfidence, Detail: v.Detail, ChangedCallees: v.ChangedCallees,
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

type briefNeighborDTO struct {
	Name             string  `json:"name"`
	Handle           string  `json:"handle"`
	Path             string  `json:"path,omitempty"`
	Preview          string  `json:"preview,omitempty"`
	ResolutionMethod string  `json:"resolution_method,omitempty"`
	Confidence       float64 `json:"confidence,omitempty"`
}

type briefTargetDTO struct {
	Name      string `json:"name"`
	Path      string `json:"path,omitempty"`
	Exported  bool   `json:"exported"`
	StartLine int    `json:"start_line,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	Body      string `json:"body,omitempty"`
}

type briefCompatDTO struct {
	Ref              string  `json:"ref"`
	Verdict          string  `json:"verdict"`
	Confidence       float64 `json:"confidence"`
	DirectConfidence float64 `json:"direct_confidence,omitempty"`
}

type briefIntentDTO struct {
	Commit  string   `json:"commit,omitempty"`
	PRs     []string `json:"prs,omitempty"`
	Tickets []string `json:"tickets,omitempty"`
	Summary string   `json:"summary,omitempty"`
}

type briefDTO struct {
	Symbol  string             `json:"symbol"`
	Ref     string             `json:"ref"`
	Target  briefTargetDTO     `json:"target"`
	Callees []briefNeighborDTO `json:"callees"`
	Callers []briefNeighborDTO `json:"callers"`
	Tests   []string           `json:"tests"`
	Compat  []briefCompatDTO   `json:"compat,omitempty"`
	Intent  *briefIntentDTO    `json:"intent,omitempty"`
	Omitted []string           `json:"omitted"`
	Meta    metaDTO            `json:"_meta"`
}

func briefNeighbors(ns []query.BriefNeighbor) []briefNeighborDTO {
	out := make([]briefNeighborDTO, 0, len(ns))
	for _, n := range ns {
		out = append(out, briefNeighborDTO{
			Name: n.Name, Handle: n.Handle, Path: n.Path, Preview: n.Preview,
			ResolutionMethod: n.ResolutionMethod, Confidence: n.Confidence,
		})
	}
	return out
}

// BriefJSON renders the token-budgeted editing bundle (ext §8C / ADR-014).
func BriefJSON(b query.BriefResult) (string, error) {
	dto := briefDTO{
		Symbol: b.Symbol, Ref: b.Ref,
		Target: briefTargetDTO{
			Name: b.Target.Name, Path: b.Target.Path, Exported: b.Target.Exported,
			StartLine: b.Target.StartLine, EndLine: b.Target.EndLine, Body: b.Target.Body,
		},
		Callees: briefNeighbors(b.Callees),
		Callers: briefNeighbors(b.Callers),
		Tests:   b.Tests,
		Omitted: b.Omitted,
		Meta:    metaDTO{Repo: b.Meta.Repo, Ref: b.Meta.Ref, Warnings: b.Meta.Warnings},
	}
	if dto.Tests == nil {
		dto.Tests = []string{}
	}
	for _, c := range b.Compat {
		dto.Compat = append(dto.Compat, briefCompatDTO{Ref: c.Ref, Verdict: c.Verdict, Confidence: c.Confidence, DirectConfidence: c.DirectConfidence})
	}
	if b.Intent != nil {
		dto.Intent = &briefIntentDTO{Commit: b.Intent.Commit, PRs: b.Intent.PRs, Tickets: b.Intent.Tickets, Summary: b.Intent.Summary}
	}
	return marshal(dto)
}

type mappedFrameDTO struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Symbol   string `json:"symbol,omitempty"`
}

type rootcauseTraceDTO struct {
	Frames    []mappedFrameDTO `json:"frames"`
	Unmapped  []string         `json:"unmapped_frames,omitempty"`
	RootCause rootcauseDTO     `json:"rootcause"`
	Note      string           `json:"note,omitempty"`
	Meta      metaDTO          `json:"_meta"`
}

// RootCauseTraceJSON renders a stack-trace-seeded drill-down (ext §8A.4).
func RootCauseTraceJSON(r query.RootCauseTraceResult) (string, error) {
	dto := rootcauseTraceDTO{
		Frames:   make([]mappedFrameDTO, 0, len(r.Frames)),
		Unmapped: r.Unmapped,
		Note:     r.Note,
		Meta:     metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, f := range r.Frames {
		dto.Frames = append(dto.Frames, mappedFrameDTO{File: f.File, Function: f.Function, Symbol: f.Symbol})
	}
	dto.RootCause = rootcauseDTO{Target: r.Result.Target, Changed: r.Result.Changed, Note: r.Result.Note}
	for _, o := range r.Result.Origins {
		dto.RootCause.Origins = append(dto.RootCause.Origins, originDTO{Name: o.Name, Kind: string(o.Kind), Depth: o.Depth, Confidence: o.Confidence})
	}
	return marshal(dto)
}

type ximpactCallerDTO struct {
	Repo       string  `json:"repo"`
	Ref        string  `json:"ref"`
	Caller     string  `json:"caller"`
	Confidence float64 `json:"confidence"`
}

type ximpactDTO struct {
	Target  string             `json:"target"`
	Callers []ximpactCallerDTO `json:"callers"`
	Note    string             `json:"note,omitempty"`
	Meta    metaDTO            `json:"_meta"`
}

// XImpactJSON renders the cross-repo caller set (ext §8B).
func XImpactJSON(r query.XImpactResult) (string, error) {
	dto := ximpactDTO{
		Target:  r.Target,
		Callers: make([]ximpactCallerDTO, 0, len(r.Callers)),
		Note:    r.Note,
		Meta:    metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, c := range r.Callers {
		dto.Callers = append(dto.Callers, ximpactCallerDTO{Repo: c.Repo, Ref: c.Ref, Caller: c.Caller, Confidence: c.Confidence})
	}
	return marshal(dto)
}

type semanticHitDTO struct {
	Path   string  `json:"path"`
	Symbol string  `json:"symbol"`
	Line   int     `json:"line"`
	Score  float64 `json:"score"`
}

// SemanticJSON renders semantic-search hits (ext §10A.2, the semantic rung).
func SemanticJSON(hits []query.SemanticHit) (string, error) {
	out := make([]semanticHitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, semanticHitDTO{Path: h.Path, Symbol: h.Symbol, Line: h.Line, Score: h.Score})
	}
	return marshal(out)
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
