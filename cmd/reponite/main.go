// Command reponite is the CLI entry point for the ref-aware code intelligence
// server. `version` and `demo` work in any build; index-backed commands
// (index/compat/diff/grep/search/context/rootcause/refs) need the sqlite +
// treesitter tags, and `mcp` needs sqlite + mcp — all via `make cli`. See
// PROGRESS.md.
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
	case "watch":
		watchCommand(os.Args[2:])
	case "serve":
		serveCommand(os.Args[2:])
	case "index", "compat", "diff", "grep", "search", "semsearch", "rootcause", "rootcause-trace", "ci-check", "ximpact", "context", "brief", "refs":
		indexBackedCommand(os.Args[1], os.Args[2:])
	case "init", "impact", "why", "arch",
		"sync", "status", "gc":
		planned(os.Args[1])
	default:
		fmt.Fprintf(os.Stderr, "reponite: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

// notImplemented is for a command that IS built but needs the adapter build tags
// (the default, dependency-free binary stubs the index-backed commands).
func notImplemented(cmd string) {
	fmt.Fprintf(os.Stderr, "reponite %s: requires the index backend — build with the sqlite/treesitter/mcp tags (`make cli`).\n", cmd)
	fmt.Fprintln(os.Stderr, "Try `reponite demo` for an in-memory end-to-end run. See PROGRESS.md.")
	os.Exit(3)
}

// planned is for a command that is on the roadmap but not implemented in any
// build yet — distinct from notImplemented, so `make cli` is not a false remedy.
func planned(cmd string) {
	fmt.Fprintf(os.Stderr, "reponite %s: not implemented yet (planned command). This is not a build-tag gap — the command has no implementation in any build.\n", cmd)
	fmt.Fprintln(os.Stderr, "Run `reponite help` to see what's available today. Track the roadmap in docs/BUILD_PLAN.md.")
	os.Exit(3)
}

func usage() {
	fmt.Fprint(os.Stderr, `reponite — ref-aware code intelligence

usage: reponite <command> [flags]

available in any build:
  version              print version and identity constants
  demo                 in-memory end-to-end run (compat / rootcause / grep as JSON)

index-backed (build with `+"`make cli`"+`):
  index <dir> [ref]    index a repo's source files at a ref (working tree)
  index --git <rev> [dir]   index a git revision's tree (tag/branch/SHA/HEAD~3) with its real commit
  compat <symbol> [ref]   compatibility verdicts across the repo's other refs
  diff <from> <to> [--changed-only] [--package P] [--confidence-min F]   symbol delta between two refs
  ci-check --base <ref> --head <ref>   exit non-zero on any exported API break (PR gate)
  ximpact <symbol> [--ref R]   who across every indexed repo calls this external symbol
  grep <pattern> [ref] trigram-prefiltered search with symbol fusion
  search <substr> [ref]   structural name search
  semsearch <query> [ref] [--limit N]   semantic search ("where is the thing that does X")
  context <symbol> [ref]  direct callers/callees, each edge with resolution_method + confidence
  rootcause <symbol> <from> <to>   drill a behavior change down to its mutation sites
  rootcause-trace <file|-> --from <ref> --to <ref>   seed rootcause from a pasted stack trace
  brief <symbol> [ref] [--budget N]   one bundle to edit a symbol: body + callees/callers + tests + compat
  refs                 list the repo's indexed refs
  mcp [dir]            serve the tools over MCP (stdio) for an AI agent
  setup [dir]         register reponite as an MCP server in your agent config
  watch [dir]         auto-reindex HEAD on source changes (fsnotify); keeps a mounted server fresh
  serve [dir...] [--addr host:port]   web dashboard + JSON API (default 127.0.0.1:8899); multiple dirs = team/fleet view
`)
}
