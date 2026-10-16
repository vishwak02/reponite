//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"
	"strings"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
)

// formatFlag is --format json|text, shared by why/errors/tests.
type formatFlag struct{ v string }

func (f *formatFlag) register(fs *flag.FlagSet) {
	fs.StringVar(&f.v, "format", "json", "output format: json (default) or text (terse, for reading inline)")
}

func (f *formatFlag) text() bool {
	switch strings.ToLower(f.v) {
	case "json", "":
		return false
	case "text", "txt":
		return true
	}
	fail(fmt.Errorf("--format %q: want json or text", f.v))
	return false
}

// emit prints the text rendering or the JSON one.
func emit(text bool, render func() string, js func() (string, error)) {
	if text {
		fmt.Print(render())
		return
	}
	printJSON(js())
}

// cmdWhy: the commits that made a symbol what it is, plus its compat history.
func cmdWhy(args []string) {
	var budget int
	var local bool
	var repoFlag string
	var ff formatFlag
	pos := parseCmd("why", "why <symbol | Class::method> [ref] [--repo R] [--budget N] [--format text] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, fmt.Sprintf("token budget (0 = default ~%d)", query.DefaultWhyBudget))
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
		ff.register(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite why <symbol> [ref] [--budget N]"))
	}
	text := ff.text()
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	repo := f.repoFor(pos[0], ref, repoFlag)
	dirs := []string{"."}
	if !local {
		dirs = historyDirs()
	}
	res := query.Why(f, newHistoryProvider(dirs), repo, ref, pos[0], budget)
	emit(text, func() string { return interfaces.WhyText(res) }, func() (string, error) { return interfaces.WhyJSON(res) })
}

// cmdErrors: where an error message/type/status comes from and who handles
// it; with no argument, the error catalogue.
func cmdErrors(args []string) {
	var budget int
	var local bool
	var repoFlag string
	var ff formatFlag
	var rf refFlags
	pos := parseCmd("errors", "errors [\"<message>\" | Type | status] [ref] [--repo R] [--ref R] [--refs repo=ref,...] [--budget N] [--format text] [--no-tests] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, fmt.Sprintf("token budget (0 = default ~%d)", query.DefaultErrorsBudget))
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		fs.StringVar(&repoFlag, "repo", "", "scope to one repo (default: fleet-wide)")
		ff.register(fs)
		rf.register(fs)
		registerNoTests(fs)
	})
	text := ff.text()
	if len(pos) >= 2 {
		rf.ref = pos[1]
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	scope := query.FleetRepo
	if repoFlag != "" {
		if !containsName(f.Names, repoFlag) {
			fail(fmt.Errorf("--repo %s is not in the fleet (%v)", repoFlag, f.Names))
		}
		scope = repoFlag
	}
	res := query.Errors(s, scope, rf.ref, arg(pos, 0, ""), budget)
	res.Note = joinNote(res.Note, cov)
	emit(text, func() string { return interfaces.ErrorsText(res) }, func() (string, error) { return interfaces.ErrorsJSON(res) })
}

// cmdTests: which tests exercise a symbol and the command running just them.
func cmdTests(args []string) {
	var budget, depth int
	var local bool
	var repoFlag string
	var ff formatFlag
	pos := parseCmd("tests", "tests <symbol | Class::method> [ref] [--depth N] [--repo R] [--budget N] [--format text] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&depth, "depth", query.DefaultTestDepth, "max call hops between a test and the symbol")
		fs.IntVar(&budget, "budget", 0, fmt.Sprintf("token budget (0 = default ~%d)", query.DefaultTestsBudget))
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
		ff.register(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite tests <symbol> [ref] [--depth N]"))
	}
	text := ff.text()
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	res := query.Tests(f, f.repoFor(pos[0], ref, repoFlag), ref, pos[0], depth, budget)
	emit(text, func() string { return interfaces.TestsText(res) }, func() (string, error) { return interfaces.TestsJSON(res) })
}
