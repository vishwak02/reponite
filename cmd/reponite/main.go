// Command reponite is the CLI entry point for the ref-aware code intelligence
// server. This is the S0.1 scaffold: the command router exists and `version`
// works; every other subcommand is a placeholder wired up in a later build
// session. See PROGRESS.md for the session map.
package main

import (
	"fmt"
	"os"

	"github.com/reponite/reponite/internal/version"
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
	case "init", "index", "search", "diff", "compat", "brief", "rootcause",
		"impact", "ximpact", "why", "arch", "refs", "sync", "status", "gc",
		"watch", "mcp", "serve", "setup":
		notImplemented(os.Args[1])
	default:
		fmt.Fprintf(os.Stderr, "reponite: unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func notImplemented(cmd string) {
	fmt.Fprintf(os.Stderr, "reponite %s: not yet implemented (scaffold stage S0.1).\n", cmd)
	fmt.Fprintln(os.Stderr, "See PROGRESS.md for the build session map.")
	os.Exit(3)
}

func usage() {
	fmt.Fprint(os.Stderr, `reponite — ref-aware code intelligence

usage: reponite <command> [flags]

available now:
  version              print version and identity constants

planned (see PROGRESS.md build map):
  init index search    structural core         (M1)
  refs diff            content-addressed refs   (M2)
  compat               compatibility oracle     (M3)  <- flagship
  brief rootcause      agent-facing reads       (M3)
  watch sync status    freshness                (M4)
  mcp serve            interfaces               (M3/M7)
`)
}
