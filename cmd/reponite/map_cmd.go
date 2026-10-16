//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
)

// cmdMap prints the orientation map: one repo (--repo, --local, or a
// one-repo fleet) or the whole fleet with its cross-repo links.
func cmdMap(args []string) {
	var repoFlag, format, pins string
	var budget int
	var local bool
	pos := parseCmd("map", "map [ref] [--repo R] [--budget N] [--format json|text] [--refs repo=ref,...] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repoFlag, "repo", "", "map this repo in depth (default: the fleet, or the only repo)")
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
		fs.StringVar(&format, "format", "json", "json | text")
		fs.StringVar(&pins, "refs", "", "per-repo refs, repo=ref,... (overrides ref for those repos)")
		fs.BoolVar(&local, "local", false, "map only this repo")
		registerNoTests(fs)
	})
	ref := arg(pos, 0, "HEAD")
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(ref, pins)
	repo := repoFlag
	if repo == "" && (local || len(f.Names) == 1) {
		repo = f.Local
	}
	if repo != "" {
		if !containsName(f.Names, repo) {
			fail(fmt.Errorf("--repo %s is not in the fleet (%v)", repo, f.Names))
		}
		res := query.Map(s, repo, ref, budget)
		if format == "text" {
			fmt.Print(interfaces.MapText(res))
			return
		}
		printJSON(interfaces.MapJSON(res))
		return
	}
	res := query.FleetMap(s, ref, budget)
	res.Next = append(res.Next, cov)
	if format == "text" {
		fmt.Print(interfaces.FleetMapText(res))
		return
	}
	printJSON(interfaces.FleetMapJSON(res))
}

// cmdExplain prints the understanding bundle for one handle.
func cmdExplain(args []string) {
	var repoFlag, format, pins string
	var budget int
	var local bool
	pos := parseCmd("explain", `explain <symbol | path[:line] | dir | "METHOD /route"> [ref] [--repo R] [--budget N] [--format json|text] [--refs repo=ref,...] [--local] [--no-tests]`, args, func(fs *flag.FlagSet) {
		fs.StringVar(&repoFlag, "repo", "", "resolve only in this repo (default: this repo first, then the fleet)")
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
		fs.StringVar(&format, "format", "json", "json | text")
		fs.StringVar(&pins, "refs", "", "per-repo refs, repo=ref,... (overrides ref for those repos)")
		fs.BoolVar(&local, "local", false, "consider only this repo")
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf(`usage: reponite explain <symbol | path[:line] | dir | "METHOD /route"> [ref]`))
	}
	ref := arg(pos, 1, "HEAD")
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(ref, pins)
	if repoFlag != "" && !containsName(f.Names, repoFlag) {
		fail(fmt.Errorf("--repo %s is not in the fleet (%v)", repoFlag, f.Names))
	}
	res := query.Explain(s, ref, pos[0], query.ExplainOptions{Repo: repoFlag, Prefer: f.Local, Budget: budget})
	if res.Kind == "not_found" || res.Kind == "route" {
		res.Note = joinNote(res.Note, cov)
	}
	if format == "text" {
		fmt.Print(interfaces.ExplainText(res))
		return
	}
	printJSON(interfaces.ExplainJSON(res))
}
