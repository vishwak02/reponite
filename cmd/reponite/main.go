// Command reponite is the CLI entry point for the ref-aware code intelligence
// server. `version` and `demo` work in any build; index-backed commands
// (index/compat/diff/grep/search/context/rootcause/refs) need the sqlite +
// treesitter tags, and `mcp` needs sqlite + mcp — all via `make cli`. See
// PROGRESS.md.
package main

import (
	"fmt"
	"os"
	"runtime/pprof"
	"time"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/version"
)

func main() {
	// REPONITE_CPUPROFILE=<file> records a CPU profile of one command (read it
	// with `go tool pprof`), for finding where a slow answer spends its time.
	if p := os.Getenv("REPONITE_CPUPROFILE"); p != "" {
		if f, err := os.Create(p); err == nil {
			if pprof.StartCPUProfile(f) == nil {
				defer pprof.StopCPUProfile()
			}
		}
	}
	// REPONITE_MEMPROFILE=<file> rewrites a heap profile every few seconds
	// while the command runs, so the last one survives a run that is killed
	// for using too much memory.
	if p := os.Getenv("REPONITE_MEMPROFILE"); p != "" {
		go func() {
			for {
				time.Sleep(5 * time.Second)
				if f, err := os.Create(p); err == nil {
					_ = pprof.WriteHeapProfile(f)
					f.Close()
				}
			}
		}()
	}
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	switch os.Args[1] {
	case "version", "--version", "-v":
		fmt.Printf("reponite %s (norm_ver=%d, index_ver=%d, go_target=%s)\n",
			version.Version, version.NormVer, version.IndexVer, version.GoTarget)
	case "help", "-h", "--help":
		usage()
	case "demo":
		runDemo()
	case "guide":
		fmt.Println(query.Guide)
	case "setup":
		setupCommand(os.Args[2:])
	case "mcp":
		mcpCommand(os.Args[2:])
	case "watch":
		watchCommand(os.Args[2:])
	case "serve":
		serveCommand(os.Args[2:])
	case "index", "compat", "diff", "grep", "search", "semsearch", "investigate", "rootcause", "rootcause-trace", "ci-check", "ximpact", "blast-radius", "usages", "topics", "logsite", "impls", "routes", "verify-edit", "repos", "fleet", "context", "brief", "refs", "why", "errors", "tests", "ui", "map", "explain", "trace", "models", "config", "coverage", "links":
		indexBackedCommand(os.Args[1], os.Args[2:])
	case "init", "impact", "arch",
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
  guide                which command answers which question (the agent decision table)

index-backed (build with `+"`make cli`"+`):
  index <dir> [ref]    index a repo's source files at a ref (working tree)
  index --git <rev> [dir]   index a git revision's tree (tag/branch/SHA/HEAD~3) with its real commit
  compat <symbol> [ref]   compatibility verdicts across the repo's other refs
  diff <from> <to> [--changed-only] [--package P] [--confidence-min F]   symbol delta between two refs
  ci-check --base <ref> --head <ref>   exit non-zero on any exported API break (PR gate)
  ximpact <symbol> [--ref R]   who across every indexed repo calls this external symbol
  blast-radius <symbol> [ref]  pre-edit dossier: in-repo + fleet callers, covering tests, cross-ref contract
  usages <symbol>      every call site fleet-wide (line + enclosing fn + call-graph confirmation)
  logsite "<log line>"  which code printed this log line (format strings with placeholders filled)
  impls <Base[::method]>  fleet-wide C++ subclasses/plugins of a base class and their override of method
  routes [path]        HTTP edges: client calls (fetch/axios/requests…) linked to server routes (Django/FastAPI/Flask/Express/Go)
  ui [route|component] [ref] [--uses] [--budget N] [--format text]   frontend: pages (client routes -> component), a component tree with props/state/effects/handlers -> API -> server route, or where a component is rendered
  models [model] [ref] [--budget N]   data layer: models/tables, fields, relations, serializers, routes, readers + writers
  config [key] [ref] [--budget N]    config surface: env/flags/settings/feature flags/ROS params read (with defaults) vs set
  links <path|symbol>  references written as data: paths, ids, template includes, config values naming code
  coverage [--repo R]  what the index stored of each repo, and every skipped file kind with its reason
  topics [name]        ROS comms graph: publishers <-> subscribers linked by name (the call graph can't see it)
  verify-edit <path>   pre-commit check: what breaks if you save your edits to this file
  repos                fleet overview: every indexed repo with its module + per-ref stats
  fleet [list|add <dir>|remove <dir|repo>|path]   the persistent registry serve/mcp mount by default
  grep <pattern> [ref] trigram-prefiltered search with symbol fusion
  search <substr> [ref]   structural name search
  semsearch <query> [ref] [--limit N]   "where is the thing that does X": KEYWORD ranking (term-idf) unless an embeddings endpoint is configured (neural build)
  investigate <question...> [--budget N] [--json]   one cited dossier answering "how does X work?" (fleet-wide)
  context <symbol> [ref]  direct callers/callees, each edge with resolution_method + confidence
  rootcause <symbol> <from> <to>   drill a behavior change down to its mutation sites
  rootcause-trace <file|-> --from <ref> --to <ref>   seed rootcause from a pasted stack trace
  brief <symbol> [ref] [--budget N]   one bundle to edit a symbol: body + callees/callers + tests + compat
  refs                 list the repo's indexed refs
  why <symbol> [ref] [--budget N]   the commits that changed it (git log -L per indexed ref) + PR/issue + first ref shipping each + compat history
  errors ["<message>"|code] [ref]   where an error comes from (raise/throw/errors.New/HTTP 4xx…) and who catches it; no arg = error catalogue
  tests <symbol> [ref] [--depth N]   which tests exercise it (direct + via callers), framework, and the command to run just those
  map [ref] [--repo R] [--budget N] [--format text]   orientation: entry points, modules, hubs, deps (no --repo = fleet + cross-repo links)
  explain <symbol|path[:line]|dir|"METHOD /route"> [ref] [--budget N]   understand one thing: doc, body, callers/callees, routes/topics, tests
  trace <entry> [ref] [--direction down|up] [--depth N] [--budget N] [--format text]   "what happens when …": call tree from a symbol, "VERB /route", path:line, CLI command or ROS topic, across HTTP/topic/impl boundaries, with side effects
  mcp [dir...]         serve the tools over MCP (stdio) for an AI agent (no dir = the registered fleet)
  setup [dir]         register reponite as an MCP server in your agent config
  watch [dir]         auto-reindex HEAD on source changes (fsnotify); keeps a mounted server fresh
  serve [dir...] [--addr host:port]   web dashboard + JSON API (default 127.0.0.1:8899); no dir = the registered fleet
`)
}
