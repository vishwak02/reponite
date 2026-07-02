//go:build mcp

// mcp_server.go exposes reponite's read tools over the Model Context Protocol
// (stdio), so an agent (Cowork, Claude Code, …) calls reponite_search/grep/
// context/compat/diff/rootcause/refs instead of reading files. Thin glue over
// ToolServer (pure); behind the `mcp` build tag (github.com/mark3labs/mcp-go).
package interfaces

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ServeStdio registers reponite's tools and serves MCP over stdio until EOF.
func ServeStdio(ts *ToolServer) error {
	s := server.NewMCPServer("reponite", "0.1.0")

	add := func(tool mcp.Tool) {
		s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]string{}
			if m, ok := any(req.Params.Arguments).(map[string]interface{}); ok {
				for k, v := range m {
					if sv, ok := v.(string); ok {
						args[k] = sv
					}
				}
			}
			out, err := ts.Call(req.Params.Name, args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out), nil
		})
	}

	add(mcp.NewTool("reponite_search",
		mcp.WithDescription("Structural symbol-name search at a ref."),
		mcp.WithString("query", mcp.Required(), mcp.Description("substring of the symbol name")),
		mcp.WithString("ref", mcp.Description("ref to search (default HEAD)"))))
	add(mcp.NewTool("reponite_grep",
		mcp.WithDescription("Trigram-prefiltered literal/regex search; each hit fused with its enclosing symbol."),
		mcp.WithString("pattern", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("fixed", mcp.Description(`"true" for literal (default), else regex`))))
	add(mcp.NewTool("reponite_compat",
		mcp.WithDescription("Compatibility verdicts (absent/shape/behavior/compatible) for a symbol across the repo's other refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("origin ref (default HEAD)"))))
	add(mcp.NewTool("reponite_context",
		mcp.WithDescription("Direct callers and callees of a symbol at a ref."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_diff",
		mcp.WithDescription("Per-symbol delta between two refs (added/removed/shape/behavior/unchanged)."),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required())))
	add(mcp.NewTool("reponite_rootcause",
		mcp.WithDescription("Walk a behavior-changed symbol to its mutation-site frontier between two refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required())))
	add(mcp.NewTool("reponite_refs",
		mcp.WithDescription("List indexed refs for the repo.")))

	return server.ServeStdio(s)
}
