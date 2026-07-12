// output.go renders coordinator results as the self-describing JSON envelope
// (§10.3, §8.3): stable lowercase keys and a _meta block, decoupled from the
// internal Go types. This is the wire format the CLI (--json) and MCP tools emit.
package interfaces

import (
	"encoding/json"
	"sort"
	"strconv"

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
	Repo   string `json:"repo,omitempty"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Symbol string `json:"symbol,omitempty"`
}

type grepDTO struct {
	Matches []matchDTO `json:"matches"`
	// total counts every matching line (ground truth); matches is the
	// [offset, offset+limit) window of them; truncated means more matches
	// exist past this window; scanned counts candidate FILES examined.
	Total     int    `json:"total"`
	Truncated bool   `json:"truncated"`
	Offset    int    `json:"offset,omitempty"`
	Scanned   int    `json:"scanned"`
	Note      string `json:"note,omitempty"`
}

// GrepJSON renders a lexical search result (ext §10A).
func GrepJSON(r query.GrepResult) (string, error) {
	dto := grepDTO{Total: r.Total, Truncated: r.Truncated, Offset: r.Offset, Scanned: r.Scanned, Note: r.Note}
	for _, m := range r.Matches {
		dto.Matches = append(dto.Matches, matchDTO{Repo: m.Repo, Path: m.Path, Line: m.Line, Text: m.Text, Symbol: m.Symbol})
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
	Repo             string  `json:"repo"`
	Ref              string  `json:"ref"`
	Caller           string  `json:"caller"`
	Module           string  `json:"module,omitempty"`
	ResolutionMethod string  `json:"resolution_method"`
	Confidence       float64 `json:"confidence"`
}

type ximpactDefDTO struct {
	Repo          string `json:"repo"`
	Ref           string `json:"ref"`
	Symbol        string `json:"symbol"`
	Module        string `json:"module,omitempty"`
	SignatureHash string `json:"signature_hash,omitempty"`
}

type ximpactDTO struct {
	Target          string             `json:"target"`
	Modules         []string           `json:"modules,omitempty"`
	Callers         []ximpactCallerDTO `json:"callers"`
	Definitions     []ximpactDefDTO    `json:"definitions,omitempty"`
	ContractChanged bool               `json:"contract_changed"`
	Note            string             `json:"note,omitempty"`
	Meta            metaDTO            `json:"_meta"`
}

// XImpactJSON renders the cross-repo caller set fused with the target's contract
// state (ext §8B). Each caller carries its resolution_method so an agent can
// tell module-resolved (precise) callers from name-based (fallback) ones.
func XImpactJSON(r query.XImpactResult) (string, error) {
	dto := ximpactDTO{
		Target:          r.Target,
		Modules:         r.Modules,
		Callers:         make([]ximpactCallerDTO, 0, len(r.Callers)),
		ContractChanged: r.ContractChanged,
		Note:            r.Note,
		Meta:            metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, c := range r.Callers {
		dto.Callers = append(dto.Callers, ximpactCallerDTO{
			Repo: c.Repo, Ref: c.Ref, Caller: c.Caller, Module: c.Module,
			ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence,
		})
	}
	for _, d := range r.Definitions {
		dto.Definitions = append(dto.Definitions, ximpactDefDTO{Repo: d.Repo, Ref: d.Ref, Symbol: d.Symbol, Module: d.Module, SignatureHash: d.SignatureHash})
	}
	return marshal(dto)
}

type blastCompatDTO struct {
	Ref        string  `json:"ref"`
	Verdict    string  `json:"verdict"`
	Confidence float64 `json:"confidence"`
}

type blastDTO struct {
	Symbol          string             `json:"symbol"`
	Repo            string             `json:"repo"`
	Summary         string             `json:"summary"`
	Modules         []string           `json:"modules,omitempty"`
	ContractChanged bool               `json:"contract_changed"`
	Definitions     []ximpactDefDTO    `json:"definitions,omitempty"`
	InRepoCallers   []string           `json:"in_repo_callers"`
	FleetCallers    []ximpactCallerDTO `json:"fleet_callers"`
	CoveringTests   []string           `json:"covering_tests"`
	Compat          []blastCompatDTO   `json:"compat_across_refs,omitempty"`
	Note            string             `json:"note,omitempty"`
	Meta            metaDTO            `json:"_meta"`
}

// BlastRadiusJSON renders the pre-edit impact dossier (§2): everything that
// could break if the symbol changes, in one payload.
func BlastRadiusJSON(r query.BlastRadiusResult) (string, error) {
	dto := blastDTO{
		Symbol: r.Symbol, Repo: r.Repo, Summary: r.Summary, Modules: r.Modules,
		ContractChanged: r.ContractChanged, Note: r.Note,
		InRepoCallers: nonNil(r.InRepoCallers), CoveringTests: nonNil(r.CoveringTests),
		FleetCallers: make([]ximpactCallerDTO, 0, len(r.FleetCallers)),
		Meta:         metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, c := range r.FleetCallers {
		dto.FleetCallers = append(dto.FleetCallers, ximpactCallerDTO{Repo: c.Repo, Ref: c.Ref, Caller: c.Caller, Module: c.Module, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence})
	}
	for _, d := range r.Definitions {
		dto.Definitions = append(dto.Definitions, ximpactDefDTO{Repo: d.Repo, Ref: d.Ref, Symbol: d.Symbol, Module: d.Module, SignatureHash: d.SignatureHash})
	}
	for _, v := range r.Compat {
		dto.Compat = append(dto.Compat, blastCompatDTO{Ref: v.Ref, Verdict: string(v.Verdict), Confidence: v.Confidence})
	}
	return marshal(dto)
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

type semanticHitDTO struct {
	Repo   string  `json:"repo,omitempty"`
	Path   string  `json:"path"`
	Symbol string  `json:"symbol"`
	Line   int     `json:"line"`
	Score  float64 `json:"score"`
}

// ReposJSON renders the list of repos in the store (team/fleet landing data).
func ReposJSON(repos []string) (string, error) {
	if repos == nil {
		repos = []string{}
	}
	return marshal(map[string][]string{"repos": repos})
}

type suggestionDTO struct {
	Repo string `json:"repo,omitempty"`
	Name string `json:"name"`
}

// SuggestJSON renders a self-healing "not found" envelope: instead of an empty
// result, the tool tells the agent the query missed and offers the nearest
// indexed names (fleet-wide), each with its repo — so it can retry precisely
// (§ agent-optimized UX). kind labels what was being looked up ("symbol"/"search").
func SuggestJSON(kind, query string, suggestions []query.SearchHit) (string, error) {
	sug := make([]suggestionDTO, 0, len(suggestions))
	for _, s := range suggestions {
		sug = append(sug, suggestionDTO{Repo: s.Repo, Name: s.Name})
	}
	msg := "no " + kind + " matched " + strconv.Quote(query)
	if len(sug) > 0 {
		msg += " — did you mean one of these?"
	} else {
		msg += " and nothing similar is indexed"
	}
	return marshal(map[string]interface{}{
		"found":        false,
		"query":        query,
		"message":      msg,
		"did_you_mean": sug,
	})
}

type dbTableDTO struct {
	Name string `json:"name"`
	Rows int64  `json:"rows"`
}

type refStatDTO struct {
	Ref     string `json:"ref"`
	Commit  string `json:"commit,omitempty"`
	Symbols int    `json:"symbols"`
	Edges   int    `json:"edges"`
	Files   int    `json:"files"`
}

type repoOverviewDTO struct {
	Repo   string       `json:"repo"`
	Module string       `json:"module,omitempty"`
	DBPath string       `json:"db_path,omitempty"`
	Tables []dbTableDTO `json:"tables,omitempty"`
	Refs   []refStatDTO `json:"refs"`
}

// OverviewJSON renders the index summary for every repo (the dashboard's
// Overview/database view): per-ref logical stats from query.Overview, enriched
// with each repo's physical database path + per-table row counts via dbFor
// (nil-safe — the in-memory store has no file, so those fields are omitted).
func OverviewJSON(ovs []query.RepoOverview, dbFor func(repo string) (string, map[string]int64)) (string, error) {
	repos := make([]repoOverviewDTO, 0, len(ovs))
	for _, ov := range ovs {
		dto := repoOverviewDTO{Repo: ov.Repo, Module: ov.Module, Refs: make([]refStatDTO, 0, len(ov.Refs))}
		for _, rs := range ov.Refs {
			dto.Refs = append(dto.Refs, refStatDTO{Ref: rs.Ref, Commit: rs.Commit, Symbols: rs.Symbols, Edges: rs.Edges, Files: rs.Files})
		}
		if dbFor != nil {
			if path, tables := dbFor(ov.Repo); path != "" || len(tables) > 0 {
				dto.DBPath = path
				dto.Tables = sortedTables(tables)
			}
		}
		repos = append(repos, dto)
	}
	return marshal(map[string][]repoOverviewDTO{"repos": repos})
}

// sortedTables renders table row-counts largest-first (the natural reading order
// for the magnitude bars in the DB view).
func sortedTables(counts map[string]int64) []dbTableDTO {
	out := make([]dbTableDTO, 0, len(counts))
	for name, rows := range counts {
		out = append(out, dbTableDTO{Name: name, Rows: rows})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rows != out[j].Rows {
			return out[i].Rows > out[j].Rows
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// SemanticJSON renders semantic-search hits (ext §10A.2, the semantic rung).
func SemanticJSON(hits []query.SemanticHit) (string, error) {
	out := make([]semanticHitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, semanticHitDTO{Repo: h.Repo, Path: h.Path, Symbol: h.Symbol, Line: h.Line, Score: h.Score})
	}
	return marshal(out)
}

type investigateFindingDTO struct {
	Repo    string   `json:"repo"`
	Path    string   `json:"path"`
	Symbol  string   `json:"symbol"`
	Line    int      `json:"line"`
	Score   float64  `json:"score"`
	Preview string   `json:"preview,omitempty"`
	Uses    []string `json:"uses,omitempty"`
	UsedBy  []string `json:"used_by,omitempty"`
}

type investigateDTO struct {
	Question string                  `json:"question"`
	Dossier  string                  `json:"dossier"`
	Findings []investigateFindingDTO `json:"findings"`
	Omitted  int                     `json:"omitted"`
	Meta     metaDTO                 `json:"_meta"`
}

// InvestigateJSON renders the investigate dossier: a dense markdown synthesis
// (the primary agent-facing field) plus the structured findings behind it.
func InvestigateJSON(r query.InvestigateResult) (string, error) {
	dto := investigateDTO{
		Question: r.Question, Dossier: r.Dossier, Omitted: r.Omitted,
		Findings: make([]investigateFindingDTO, 0, len(r.Findings)),
		Meta:     metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, f := range r.Findings {
		dto.Findings = append(dto.Findings, investigateFindingDTO{
			Repo: f.Repo, Path: f.Path, Symbol: f.Symbol, Line: f.Line, Score: f.Score,
			Preview: f.Preview, Uses: f.Callees, UsedBy: f.Callers,
		})
	}
	return marshal(dto)
}

type usageDTO struct {
	Repo      string `json:"repo,omitempty"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Text      string `json:"text"`
	In        string `json:"in,omitempty"`
	Confirmed bool   `json:"confirmed"`
}

type usagesDTO struct {
	Symbol string     `json:"symbol"`
	Total  int        `json:"total"`
	Usages []usageDTO `json:"usages"`
	Note   string     `json:"note,omitempty"`
	Meta   metaDTO    `json:"_meta"`
}

// UsagesJSON renders the call sites of a symbol (each with its calling line,
// enclosing function, and call-graph confirmation).
func UsagesJSON(r query.UsagesResult) (string, error) {
	dto := usagesDTO{
		Symbol: r.Symbol, Total: r.Total, Note: r.Note,
		Usages: make([]usageDTO, 0, len(r.Usages)),
		Meta:   metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, u := range r.Usages {
		dto.Usages = append(dto.Usages, usageDTO{Repo: u.Repo, Path: u.Path, Line: u.Line, Text: u.Text, In: u.In, Confirmed: u.Confirmed})
	}
	return marshal(dto)
}

type commEndpointDTO struct {
	Repo    string `json:"repo,omitempty"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Role    string `json:"role"`
	Name    string `json:"name"`
	Raw     string `json:"raw,omitempty"`
	MsgType string `json:"msg_type,omitempty"`
	// How msg_type was inferred: template | callback-param | positional-arg
	// (best-effort source inference, labeled — see the result note).
	MsgTypeSource string `json:"msg_type_source,omitempty"`
	In            string `json:"in,omitempty"`
	Text          string `json:"text,omitempty"`
}

type commGroupDTO struct {
	Family     string            `json:"family"`
	Name       string            `json:"name"`
	Connected  bool              `json:"connected"`
	Confidence float64           `json:"confidence"`
	Producers  []commEndpointDTO `json:"producers"`
	Consumers  []commEndpointDTO `json:"consumers"`
}

type topicsDTO struct {
	Groups     []commGroupDTO `json:"groups"`
	Endpoints  int            `json:"endpoints"`
	Unresolved int            `json:"unresolved,omitempty"`
	Note       string         `json:"note,omitempty"`
	Meta       metaDTO        `json:"_meta"`
}

func commEndpointsToDTO(eps []query.CommEndpoint) []commEndpointDTO {
	out := make([]commEndpointDTO, 0, len(eps))
	for _, e := range eps {
		out = append(out, commEndpointDTO{
			Repo: e.Repo, Path: e.Path, Line: e.Line, Role: e.Role,
			Name: e.Name, Raw: e.Raw, MsgType: e.MsgType, MsgTypeSource: e.MsgTypeSource, In: e.In, Text: e.Text,
		})
	}
	return out
}

// TopicsJSON renders the ROS communication graph — producers and consumers
// linked by topic/service/action name, the cross-process edges the call graph
// can't see.
func TopicsJSON(r query.CommGraphResult) (string, error) {
	dto := topicsDTO{
		Endpoints: r.Endpoints, Unresolved: r.Unresolved, Note: r.Note,
		Groups: make([]commGroupDTO, 0, len(r.Groups)),
		Meta:   metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, g := range r.Groups {
		dto.Groups = append(dto.Groups, commGroupDTO{
			Family: g.Family, Name: g.Name, Connected: g.Connected(), Confidence: g.Confidence,
			Producers: commEndpointsToDTO(g.Producers),
			Consumers: commEndpointsToDTO(g.Consumers),
		})
	}
	return marshal(dto)
}

type editImpactDTO struct {
	Symbol string     `json:"symbol"`
	Kind   string     `json:"kind"`
	Breaks []usageDTO `json:"breaks"`
}

type verifyDTO struct {
	Path    string          `json:"path"`
	Safe    bool            `json:"safe"`
	Added   []string        `json:"added,omitempty"`
	Removed []string        `json:"removed,omitempty"`
	Changed []string        `json:"changed,omitempty"`
	Impacts []editImpactDTO `json:"impacts"`
	Note    string          `json:"note,omitempty"`
	Meta    metaDTO         `json:"_meta"`
}

// VerifyEditJSON renders the pre-commit safety report for a proposed edit: what
// changed and, for each breaking change, the confirmed call sites that break.
func VerifyEditJSON(r query.VerifyResult) (string, error) {
	dto := verifyDTO{
		Path: r.Path, Safe: r.Safe, Added: r.Added, Removed: r.Removed, Changed: r.Changed, Note: r.Note,
		Impacts: make([]editImpactDTO, 0, len(r.Impacts)),
		Meta:    metaDTO{Repo: r.Meta.Repo, Ref: r.Meta.Ref, Warnings: r.Meta.Warnings},
	}
	for _, im := range r.Impacts {
		e := editImpactDTO{Symbol: im.Symbol, Kind: string(im.Kind), Breaks: make([]usageDTO, 0, len(im.Breaks))}
		for _, u := range im.Breaks {
			e.Breaks = append(e.Breaks, usageDTO{Repo: u.Repo, Path: u.Path, Line: u.Line, Text: u.Text, In: u.In, Confirmed: u.Confirmed})
		}
		dto.Impacts = append(dto.Impacts, e)
	}
	return marshal(dto)
}

// SearchJSON renders structural name-search hits.
func SearchJSON(hits []query.SearchHit) (string, error) {
	type hitDTO struct {
		Repo   string `json:"repo,omitempty"`
		Name   string `json:"name"`
		Ref    string `json:"ref"`
		IsTest bool   `json:"is_test"`
	}
	out := make([]hitDTO, 0, len(hits))
	for _, h := range hits {
		out = append(out, hitDTO{Repo: h.Repo, Name: h.Name, Ref: h.Ref, IsTest: h.IsTest})
	}
	return marshal(out)
}
