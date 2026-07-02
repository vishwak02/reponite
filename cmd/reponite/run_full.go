//go:build sqlite && treesitter

package main

import (
	"fmt"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/version"
)

func indexBackedCommand(cmd string, args []string) {
	switch cmd {
	case "index":
		cmdIndex(args)
	case "compat":
		cmdCompat(args)
	case "diff":
		cmdDiff(args)
	case "grep":
		cmdGrep(args)
	case "search":
		cmdSearch(args)
	default:
		notImplemented(cmd)
	}
}

func cmdIndex(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	repo := repoName(dir)
	st := openStore(dir)
	defer st.Close()
	if err := processing.IndexDir(st, repo, ref, dir, version.NormVer); err != nil {
		fail(err)
	}
	fmt.Printf("indexed %s@%s — refs now: %v\n", repo, ref, st.Refs(repo))
}

func cmdCompat(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite compat <symbol> [ref]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	repo := repoName(".")
	st := openStore(".")
	defer st.Close()
	var targets []query.RepoRef
	for _, r := range st.Refs(repo) {
		if r != ref {
			targets = append(targets, query.RepoRef{Repo: repo, Ref: r})
		}
	}
	rep, err := query.CompatSymbol(st, query.RepoRef{Repo: repo, Ref: ref}, args[0], targets)
	if err != nil {
		fail(err)
	}
	printJSON(interfaces.CompatJSON(rep))
}

func cmdDiff(args []string) {
	if len(args) < 2 {
		fail(fmt.Errorf("usage: reponite diff <from-ref> <to-ref>"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.DiffJSON(query.DiffRefsBy(st, repoName("."), args[0], args[1])))
}

func cmdGrep(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite grep <pattern> [ref]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	st := openStore(".")
	defer st.Close()
	res, err := query.GrepRepo(st, repoName("."), ref, args[0], query.GrepOptions{Fixed: true})
	if err != nil {
		fail(err)
	}
	printJSON(interfaces.GrepJSON(res))
}

func cmdSearch(args []string) {
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite search <substr> [ref]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.SearchJSON(query.SearchName(st, repoName("."), ref, args[0])))
}
