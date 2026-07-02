// Command reponite is the CLI entry point for the ref-aware code intelligence
// server. `version` and `demo` work in any build; index-backed commands
// (index/compat/diff/grep/search) need the sqlite + treesitter tags, and `mcp`
// needs sqlite + mcp — all via `make cli`. See PROGRESS.md.
package main

import (
	"fmt"
	"os"

	"github.com/vishwak02/reponite/internal/version"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("reponite %s (norm_ver=%d, go_target=%s)\n",
			version.Version, version.NormVer, version.GoTarget)
	case "help", "-h", "--help":
		usage()
	case "demo":
		runDemo()
	case "setup":
		setupCommand(os.Args[2:])
	case "mcp":
		mcpCommand(os.Args[2:])
	case "index", "compat", "diff", "grep", "search":
		indexBackedCommand(os.Args[1], os.Args[2:])
	case "init", "brief", "rootcause", "impact", "ximpact", "why", "arch",
		"refs", "sync", "status", "gc", "watch", "serve":
		notImplemented(os.Args[1])
	default:
		fmt.Fprintf(os.Stderr, "reponite: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func notImplemented(cmd string) {
	fmt.Fprintf(os.Stderr, "reponite %s: requires the index backend — build with the sqlite/treesitter/mcp tags (`make cli`).\n", cmd)
	fmt.Fprintln(os.Stderr, "Try `reponite demo` for an in-memory end-to-end run. See PROGRESS.md.")
	os.Exit(3)
}

func usage() {
	fmt.Fprint(os.Stderr, `reponite — ref-aware code intelligence

usage: reponite <command> [flags]

available in any build:
  version              print version and identity constants
  demo                 in-memory end-to-end run (compat / rootcause / grep as JSON)

index-backed (build with `+"`make cli`"+`):
  index <dir> [ref]    index a repo's Go files at a ref
  compat <symbol> [ref]   compatibility verdicts across the repo's other refs
  diff <from> <to>     symbol delta between two refs
  grep <pattern> [ref] trigram-prefiltered search with symbol fusion
  search <substr> [ref]   structural name search
  mcp [dir]            serve the tools over MCP (stdio) for an AI agent
  setup [dir]         register reponite as an MCP server in your agent config
`)
}
