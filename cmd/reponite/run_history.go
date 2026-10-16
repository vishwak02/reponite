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
