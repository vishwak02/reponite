// mcp_handlers.go is the pure dispatch layer behind the MCP server: it maps a
// tool name + string args to the query coordinators and returns a JSON envelope.
// No MCP-SDK dependency, so it is unit-tested in-sandbox against storage.Mem
// (ADR-018); mcp_server.go is the thin SDK glue over this.
package interfaces

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// ToolServer answers reponite tool calls against a Store, scoped to one repo.
// Intent is an optional provenance provider for reponite_brief (nil when no
// git-backed linkage is wired), keeping the dispatch layer pure.
type ToolServer struct {
	Store  query.Store
	Repo   string
	Intent query.IntentProvider
	// ParseSymbols parses a file's content into its symbols for reponite_verify_edit.
	// Injected by the tree-sitter build (nil otherwise, so verify_edit reports it's
	// unavailable rather than failing) — keeps this dispatch layer pure (ADR-018).
	ParseSymbols func(path, content string) []query.EditedSymbol
	// Ranker overrides the semantic rung's strategy for semsearch/investigate
	// (ADR-020). Injected by the neural build when an endpoint is configured;
	// nil = the pure term-idf default. Keeps this dispatch layer pure.
	Ranker query.SemanticRanker
	// History reads repos' git object stores for reponite_why (nil = no
	// history; why still answers the compat history).
	History query.HistoryProvider
}

// Call dispatches a tool by name; args are string-valued (as MCP delivers them).
func (t *ToolServer) Call(tool string, args map[string]string) (string, error) {
	ref := args["ref"]
	if ref == "" {
		ref = "HEAD"
	}
	// refs pins each repo to its own ref — the combination of versions a
	// deployed system actually runs (storage.Pinned). Applied to this call only.
	if p := args["refs"]; p != "" {
		pins, err := storage.ParsePins(p)
		if err != nil {
			return "", err
		}
		cp := *t
		cp.Store = &storage.Pinned{Inner: t.Store, Pins: pins}
		t = &cp
	}
	// coverage names what a fleet-wide answer actually read, so "0 usages"
	// can never stand in for "not indexed at that ref".
	coverage := func() string {
		read, missing := storage.Coverage(t.Store, ref)
		switch {
		case len(read) == 0:
			return fmt.Sprintf("NOTHING READ: no repo is indexed at the requested ref (%v); pass ref or refs=repo=ref,...", missing)
		case len(missing) > 0:
			return fmt.Sprintf("read %v; not indexed at the requested ref, so not searched: %v", read, missing)
		}
		return fmt.Sprintf("read %v", read)
	}
	repo := args["repo"]
	if repo == "" {
		repo = t.Repo
	}
	// Discovery tools ("where does X live?") default to fleet-wide when the agent
	// doesn't name a repo — it usually doesn't know yet (§ boundary-less search).
	// An explicit repo still scopes them.
	discoverRepo := repo
	if args["repo"] == "" {
		discoverRepo = query.FleetRepo
	}
	includeTests := args["tests"] == "true"
	// notFound returns a self-healing "did you mean" envelope (fleet-wide
	// suggestions) instead of an empty result, so the agent can retry precisely.
	notFound := func(kind, q string) (string, error) {
		return SuggestJSON(kind, q, query.Suggest(t.Store, query.FleetRepo, ref, q, 6))
	}
	switch tool {
	case "reponite_repos":
		return OverviewJSON(query.Overview(t.Store), nil)
	case "reponite_search":
		hits := query.SearchName(t.Store, discoverRepo, ref, args["query"], includeTests)
		if len(hits) == 0 {
			return notFound("symbol", args["query"])
		}
		return SearchJSON(hits)
	case "reponite_grep":
		// Regex by default (§14A): fixed=true opts into literal. The old
		// literal-by-default silently returned zero for `a|b` — the trigrams of
		// the raw alternation string exist in no file (P0 under-match bug).
		limit, _ := strconv.Atoi(args["limit"])
		offset, _ := strconv.Atoi(args["offset"])
		res, err := query.GrepRepo(t.Store, discoverRepo, ref, args["pattern"], query.GrepOptions{Fixed: args["fixed"] == "true", Limit: limit, Offset: offset})
		if err != nil {
			return "", err
		}
		return GrepJSON(res)
	case "reponite_compat":
		var targets []query.RepoRef
		for _, r := range t.Store.Refs(repo) {
			if r != ref {
				targets = append(targets, query.RepoRef{Repo: repo, Ref: r})
			}
		}
		rep, err := query.CompatSymbol(t.Store, query.RepoRef{Repo: repo, Ref: ref}, args["symbol"], targets)
		if err != nil {
			return "", err
		}
		return CompatJSON(rep)
	case "reponite_context":
		if len(query.ResolveSymbol(t.Store, repo, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		return ContextJSON(query.WithCrossRepoDispatch(t.Store, repo, ref, query.Context(t.Store, repo, ref, args["symbol"], includeTests), includeTests))
	case "reponite_diff":
		min, _ := strconv.ParseFloat(args["confidence_min"], 64)
		opt := query.DiffOptions{ChangedOnly: args["changed_only"] == "true", Package: args["package"], MinConfidence: min}
		return DiffJSON(query.DiffRefsBy(t.Store, repo, args["from"], args["to"], opt))
	case "reponite_rootcause":
		return RootCauseJSON(query.RootCauseBy(t.Store, repo, args["symbol"], args["from"], args["to"]))
	case "reponite_rootcause_trace":
		return RootCauseTraceJSON(query.RootCauseTrace(t.Store, repo, args["from"], args["to"], args["stacktrace"]))
	case "reponite_brief":
		if len(query.ResolveSymbol(t.Store, repo, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		budget, _ := strconv.Atoi(args["budget"])
		return BriefJSON(query.Brief(t.Store, repo, ref, args["symbol"], budget, t.Intent))
	case "reponite_ximpact":
		return XImpactJSON(query.XImpact(t.Store, args["symbol"], args["ref"]))
	case "reponite_usages":
		res := query.Usages(t.Store, discoverRepo, ref, args["symbol"])
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return UsagesText(res), nil
		}
		return UsagesJSON(res)
	case "reponite_logsite":
		limit, _ := strconv.Atoi(args["limit"])
		res := query.LogSites(t.Store, discoverRepo, ref, args["line"], limit)
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		return LogSitesJSON(res)
	case "reponite_impls":
		limit, _ := strconv.Atoi(args["limit"])
		res := query.Impls(t.Store, discoverRepo, ref, args["base"], limit)
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return ImplsText(res), nil
		}
		return ImplsJSON(res)
	case "reponite_routes":
		limit, _ := strconv.Atoi(args["limit"])
		opt, err := LoadGateways(args["gateway"], args["upstream"])
		if err != nil {
			return "", err
		}
		opt.Tests = includeTests
		res := query.RoutesWith(t.Store, discoverRepo, ref, args["path"], limit, opt)
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return RoutesText(res), nil
		}
		return RoutesJSON(res)
	case "reponite_ui":
		budget, _ := strconv.Atoi(args["budget"])
		opt, err := LoadGateways(args["gateway"], args["upstream"])
		if err != nil {
			return "", err
		}
		res := query.UI(t.Store, discoverRepo, ref, args["target"], query.UIOptions{Budget: budget, Uses: args["uses"] == "true", Routes: opt})
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return UIText(res), nil
		}
		return UIJSON(res)
	case "reponite_links":
		lr := query.LinksFor(t.Store, discoverRepo, ref, args["target"])
		if args["format"] == "text" {
			return LinksText(lr), nil
		}
		return snakeJSON(lr)
	case "reponite_coverage":
		return snakeJSON(query.Coverage(t.Store, discoverRepo, ref))
	case "reponite_topics":
		var res query.CommGraphResult
		if topic := args["topic"]; topic != "" {
			res = query.Topic(t.Store, discoverRepo, ref, topic)
		} else {
			res = query.CommGraph(t.Store, discoverRepo, ref)
		}
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return TopicsText(res), nil
		}
		return TopicsJSON(res)
	case "reponite_verify_edit":
		if t.ParseSymbols == nil {
			return "", fmt.Errorf("verify_edit needs the tree-sitter build (make cli)")
		}
		path := args["path"]
		var oldContent string
		for _, f := range t.Store.Files(repo, ref) {
			if f.Path == path {
				oldContent = f.Content
				break
			}
		}
		old := t.ParseSymbols(path, oldContent)
		nw := t.ParseSymbols(path, args["content"])
		return VerifyEditJSON(query.VerifyEdit(t.Store, repo, ref, path, old, nw))
	case "reponite_blast_radius":
		if len(query.ResolveSymbol(t.Store, repo, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		return BlastRadiusJSON(query.BlastRadius(t.Store, repo, ref, args["symbol"]))
	case "reponite_semsearch":
		limit, _ := strconv.Atoi(args["limit"])
		return SemanticJSON(query.SemanticSearch(t.Store, discoverRepo, ref, args["query"], limit, t.Ranker))
	case "reponite_investigate":
		budget, _ := strconv.Atoi(args["budget"])
		return InvestigateJSON(query.InvestigateWith(t.Store, discoverRepo, ref, args["question"], budget, t.Ranker))
	case "reponite_models", "reponite_config":
		return t.callData(tool, discoverRepo, ref, args, coverage)
	case "reponite_refs":
		return RefsJSON(repo, t.Store.Refs(repo))
	case "reponite_why":
		r := t.symbolRepo(repo, args["repo"] != "", ref, args["symbol"])
		if len(query.ResolveSymbol(t.Store, r, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		budget, _ := strconv.Atoi(args["budget"])
		res := query.Why(t.Store, t.History, r, ref, args["symbol"], budget)
		if args["format"] == "text" {
			return WhyText(res), nil
		}
		return WhyJSON(res)
	case "reponite_errors":
		budget, _ := strconv.Atoi(args["budget"])
		var st query.Store = t.Store
		if args["no_tests"] == "true" {
			st = &storage.NoTests{Inner: t.Store}
		}
		res := query.Errors(st, discoverRepo, ref, args["query"], budget)
		if discoverRepo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if args["format"] == "text" {
			return ErrorsText(res), nil
		}
		return ErrorsJSON(res)
	case "reponite_tests":
		r := t.symbolRepo(repo, args["repo"] != "", ref, args["symbol"])
		if len(query.ResolveSymbol(t.Store, r, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		budget, _ := strconv.Atoi(args["budget"])
		depth, _ := strconv.Atoi(args["depth"])
		res := query.Tests(t.Store, r, ref, args["symbol"], depth, budget)
		if args["format"] == "text" {
			return TestsText(res), nil
		}
		return TestsJSON(res)
	case "reponite_map", "reponite_explain":
		return t.mapExplain(tool, args, ref)
	case "reponite_guide":
		return GuideText(), nil
	case "reponite_trace":
		depth, _ := strconv.Atoi(args["depth"])
		budget, _ := strconv.Atoi(args["budget"])
		ropt, err := LoadGateways(args["gateway"], args["upstream"])
		if err != nil {
			return "", err
		}
		opt := query.TraceOptions{Direction: args["direction"], Depth: depth, Budget: budget, IncludeTests: includeTests,
			Repo: args["repo"], Routes: ropt, Edges: query.UITraceEdges(t.Store, query.FleetRepo, ref)}
		for _, p := range strings.Split(args["skip"], ",") {
			if p = strings.TrimSpace(p); p != "" {
				opt.Skip = append(opt.Skip, p)
			}
		}
		res := query.Trace(t.Store, args["entry"], ref, opt)
		res.Note = res.Note + " — " + coverage()
		return TraceOutput(res, args["format"])
	default:
		return "", fmt.Errorf("unknown tool %q", tool)
	}
}

// symbolRepo is the repo a symbol-scoped tool answers for: the named repo,
// else the default one when it defines the symbol, else the single fleet
// repo that does (several = the default; the agent passes repo to choose).
func (t *ToolServer) symbolRepo(repo string, explicit bool, ref, symbol string) string {
	if explicit || len(query.ResolveSymbol(t.Store, repo, ref, symbol)) > 0 {
		return repo
	}
	var found []string
	for _, r := range t.Store.Repos() {
		if len(query.ResolveSymbol(t.Store, r, ref, symbol)) > 0 {
			found = append(found, r)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return repo
}

// mapExplain serves reponite_map and reponite_explain: no_tests hides test
// code for this call, format=text returns the terse rendering.
func (t *ToolServer) mapExplain(tool string, args map[string]string, ref string) (string, error) {
	st := t.Store
	if args["no_tests"] == "true" {
		st = &storage.NoTests{Inner: st}
	}
	budget, _ := strconv.Atoi(args["budget"])
	text := args["format"] == "text"
	if tool == "reponite_map" {
		repo := args["repo"]
		if repo == "" && len(st.Repos()) == 1 {
			repo = st.Repos()[0]
		}
		if repo == "" {
			r := query.FleetMap(st, ref, budget)
			if text {
				return FleetMapText(r), nil
			}
			return FleetMapJSON(r)
		}
		r := query.Map(st, repo, ref, budget)
		if text {
			return MapText(r), nil
		}
		return MapJSON(r)
	}
	if args["target"] == "" {
		return "", fmt.Errorf("explain needs a target: a symbol, path[:line], directory or \"METHOD /route\"")
	}
	r := query.Explain(st, ref, args["target"], query.ExplainOptions{Repo: args["repo"], Prefer: t.Repo, Budget: budget})
	if text {
		return ExplainText(r), nil
	}
	return ExplainJSON(r)
}

// callData answers reponite_models / reponite_config (data_output.go).
func (t *ToolServer) callData(tool, repo, ref string, args map[string]string, coverage func() string) (string, error) {
	budget, _ := strconv.Atoi(args["budget"])
	text := args["format"] == "text"
	if tool == "reponite_models" {
		res := query.Models(t.Store, repo, ref, args["model"], query.ModelsOptions{Budget: budget, Kind: args["kind"]})
		if repo == query.FleetRepo {
			res.Note = res.Note + " — " + coverage()
		}
		if text {
			return ModelsText(res), nil
		}
		return ModelsJSON(res)
	}
	res := query.Config(t.Store, repo, ref, args["key"], query.ConfigOptions{Budget: budget, Kind: args["kind"]})
	if repo == query.FleetRepo {
		res.Note = res.Note + " — " + coverage()
	}
	if text {
		return ConfigText(res), nil
	}
	return ConfigJSON(res)
}
