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
	CompactOutput = true // agents consume this; single-line JSON saves tokens
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
		mcp.WithString("ref", mcp.Description("ref to search (default HEAD)")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("tests", mcp.Description(`"true" to include Test*/Benchmark*/Example*/Fuzz* symbols (default excluded)`))))
	add(mcp.NewTool("reponite_grep",
		mcp.WithDescription("Trigram-prefiltered literal/regex search; each hit fused with its enclosing symbol."),
		mcp.WithString("pattern", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("fixed", mcp.Description(`"true" for literal (default), else regex`))))
	add(mcp.NewTool("reponite_compat",
		mcp.WithDescription("Compatibility verdicts (absent/shape/behavior/compatible) for a symbol across the repo's other refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("ref", mcp.Description("origin ref (default HEAD)"))))
	add(mcp.NewTool("reponite_context",
		mcp.WithDescription("Direct callers and callees of a symbol at a ref."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("tests", mcp.Description(`"true" to include test callers/callees (default excluded)`))))
	add(mcp.NewTool("reponite_diff",
		mcp.WithDescription("Per-symbol delta between two refs (added/removed/shape/behavior/unchanged)."),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("changed_only", mcp.Description(`"true" to hide unchanged symbols`)),
		mcp.WithString("package", mcp.Description("keep only symbols whose package has this prefix")),
		mcp.WithString("confidence_min", mcp.Description("hide changes below this confidence (0..1)"))))
	add(mcp.NewTool("reponite_rootcause",
		mcp.WithDescription("Walk a behavior-changed symbol to its mutation-site frontier between two refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_rootcause_trace",
		mcp.WithDescription("Paste a stack trace (Go/Python/JS/Java); maps frames to symbols and drills down the failing path to the mutation site between two refs."),
		mcp.WithString("stacktrace", mcp.Required(), mcp.Description("the raw stack trace / traceback")),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_brief",
		mcp.WithDescription("Everything needed to edit a symbol in one token-budgeted bundle: full body, callees+callers (preview+handle), covering tests, and the compat snapshot. Replaces 5-6 file reads."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("budget", mcp.Description("token budget (default 3000)"))))
	add(mcp.NewTool("reponite_semsearch",
		mcp.WithDescription("Semantic symbol search — 'where is the thing that does X'. Ranks symbols by identifier-aware similarity to a natural-language query (no model needed)."),
		mcp.WithString("query", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("limit", mcp.Description("max hits (default 10)"))))
	add(mcp.NewTool("reponite_ximpact",
		mcp.WithDescription("Cross-repo impact: who across every indexed repo calls this (external) symbol — the question before changing an exported API. Source-call-graph, name-based (RPC invisible)."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("restrict each repo to this ref (default: all indexed refs)"))))
	add(mcp.NewTool("reponite_investigate",
		mcp.WithDescription("Ask a natural-language question about the code (\"how does X work?\", \"where is the picking workflow?\") and get ONE dense, cited dossier: the most relevant symbols across the whole fleet, each with a body preview and its callers/callees, budget-filled. Start here to understand a feature — it replaces the semsearch→brief→context loop."),
		mcp.WithString("question", mcp.Required(), mcp.Description("what you want to understand, in plain language")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("budget", mcp.Description("token budget for the dossier (default ~4000)"))))
	add(mcp.NewTool("reponite_usages",
		mcp.WithDescription("Every call site of a symbol across the fleet — the exact calling line, its file:line, and the enclosing function — with `confirmed` when that function is a resolved caller in the call graph (vs a lexical match in a comment/string or a dynamic call). Use before changing a signature to see how it's actually called."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_blast_radius",
		mcp.WithDescription("Pre-edit macro: everything that could break if you change this symbol, in one call — in-repo callers, cross-repo (fleet) callers, covering tests, and whether the API contract already moved across refs. Call this BEFORE editing a load-bearing symbol."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("repo that defines the symbol (defaults to current)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_refs",
		mcp.WithDescription("List indexed refs for the repo."),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_repos",
		mcp.WithDescription("Fleet orientation: every indexed repo with its module path and per-ref stats (symbols, call edges, files). Call this first to learn what's mounted and where a feature might live — search/grep/semsearch then default to fleet-wide.")))

	return server.ServeStdio(s)
}
