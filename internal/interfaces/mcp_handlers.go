// mcp_handlers.go is the pure dispatch layer behind the MCP server: it maps a
// tool name + string args to the query coordinators and returns a JSON envelope.
// No MCP-SDK dependency, so it is unit-tested in-sandbox against storage.Mem
// (ADR-018); mcp_server.go is the thin SDK glue over this.
package interfaces

import (
	"fmt"

	"github.com/vishwak02/reponite/internal/query"
)

// ToolServer answers reponite tool calls against a Store, scoped to one repo.
type ToolServer struct {
	Store query.Store
	Repo  string
}

// Call dispatches a tool by name; args are string-valued (as MCP delivers them).
func (t *ToolServer) Call(tool string, args map[string]string) (string, error) {
	ref := args["ref"]
	if ref == "" {
		ref = "HEAD"
	}
	includeTests := args["tests"] == "true"
	switch tool {
	case "reponite_search":
		return SearchJSON(query.SearchName(t.Store, t.Repo, ref, args["query"], includeTests))
	case "reponite_grep":
		res, err := query.GrepRepo(t.Store, t.Repo, ref, args["pattern"], query.GrepOptions{Fixed: args["fixed"] != "false"})
		if err != nil {
			return "", err
		}
		return GrepJSON(res)
	case "reponite_compat":
		var targets []query.RepoRef
		for _, r := range t.Store.Refs(t.Repo) {
			if r != ref {
				targets = append(targets, query.RepoRef{Repo: t.Repo, Ref: r})
			}
		}
		rep, err := query.CompatSymbol(t.Store, query.RepoRef{Repo: t.Repo, Ref: ref}, args["symbol"], targets)
		if err != nil {
			return "", err
		}
		return CompatJSON(rep)
	case "reponite_context":
		return ContextJSON(query.Context(t.Store, t.Repo, ref, args["symbol"], includeTests))
	case "reponite_diff":
		return DiffJSON(query.DiffRefsBy(t.Store, t.Repo, args["from"], args["to"]))
	case "reponite_rootcause":
		return RootCauseJSON(query.RootCauseBy(t.Store, t.Repo, args["symbol"], args["from"], args["to"]))
	case "reponite_refs":
		return RefsJSON(t.Repo, t.Store.Refs(t.Repo))
	default:
		return "", fmt.Errorf("unknown tool %q", tool)
	}
}
