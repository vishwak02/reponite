// mcp_handlers.go is the pure dispatch layer behind the MCP server: it maps a
// tool name + string args to the query coordinators and returns a JSON envelope.
// No MCP-SDK dependency, so it is unit-tested in-sandbox against storage.Mem
// (ADR-018); mcp_server.go is the thin SDK glue over this.
package interfaces

import (
	"fmt"
	"strconv"

	"github.com/vishwak02/reponite/internal/query"
)

// ToolServer answers reponite tool calls against a Store, scoped to one repo.
// Intent is an optional provenance provider for reponite_brief (nil when no
// git-backed linkage is wired), keeping the dispatch layer pure.
type ToolServer struct {
	Store  query.Store
	Repo   string
	Intent query.IntentProvider
}

// Call dispatches a tool by name; args are string-valued (as MCP delivers them).
func (t *ToolServer) Call(tool string, args map[string]string) (string, error) {
	ref := args["ref"]
	if ref == "" {
		ref = "HEAD"
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
		res, err := query.GrepRepo(t.Store, discoverRepo, ref, args["pattern"], query.GrepOptions{Fixed: args["fixed"] != "false"})
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
		return ContextJSON(query.Context(t.Store, repo, ref, args["symbol"], includeTests))
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
	case "reponite_blast_radius":
		if len(query.ResolveSymbol(t.Store, repo, ref, args["symbol"])) == 0 {
			return notFound("symbol", args["symbol"])
		}
		return BlastRadiusJSON(query.BlastRadius(t.Store, repo, ref, args["symbol"]))
	case "reponite_semsearch":
		limit, _ := strconv.Atoi(args["limit"])
		return SemanticJSON(query.SemanticSearch(t.Store, discoverRepo, ref, args["query"], limit, nil))
	case "reponite_refs":
		return RefsJSON(repo, t.Store.Refs(repo))
	default:
		return "", fmt.Errorf("unknown tool %q", tool)
	}
}
