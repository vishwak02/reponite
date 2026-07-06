//go:build sqlite && treesitter

package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

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
	case "rootcause":
		cmdRootCause(args)
	case "rootcause-trace":
		cmdRootCauseTrace(args)
	case "ci-check":
		cmdCICheck(args)
	case "ximpact":
		cmdXImpact(args)
	case "semsearch":
		cmdSemSearch(args)
	case "brief":
		cmdBrief(args)
	case "context":
		cmdContext(args)
	case "refs":
		cmdRefs(args)
	default:
		notImplemented(cmd)
	}
}

func cmdIndex(args []string) {
	gitRev, args := popValue(args, "--git")
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	} else if gitRev != "" {
		ref = gitRev // default the ref label to the revision
	}
	repo := repoName(dir)
	st := openStore(dir)
	defer st.Close()

	if gitRev != "" {
		commit, err := processing.IndexGitRef(st, repo, ref, dir, gitRev, version.NormVer)
		if err != nil {
			fail(err)
		}
		if err := st.AddRef(repo, ref, commit, ""); err != nil {
			fail(err)
		}
		fmt.Printf("indexed %s@%s (git %s @ %s)%s — refs now: %v\n", repo, ref, gitRev, shortHash(commit), moduleNote(st, repo), st.Refs(repo))
		return
	}
	if err := processing.IndexDir(st, repo, ref, dir, version.NormVer); err != nil {
		fail(err)
	}
	fmt.Printf("indexed %s@%s%s — refs now: %v\n", repo, ref, moduleNote(st, repo), st.Refs(repo))
}

// moduleNote reports the detected module path for cross-repo impact, or a hint
// when none was found (so a user knows ximpact will fall back to name matching).
func moduleNote(st interface{ ModulePath(string) string }, repo string) string {
	if m := st.ModulePath(repo); m != "" {
		return " [module " + m + "]"
	}
	return " [no module manifest — ximpact name-based]"
}

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// popValue removes "flag value" from args, returning the value ("" if absent).
func popValue(args []string, flag string) (string, []string) {
	rest := make([]string, 0, len(args))
	val := ""
	for i := 0; i < len(args); i++ {
		if args[i] == flag && i+1 < len(args) {
			val = args[i+1]
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	return val, rest
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
	changedOnly, args := popFlag(args, "--changed-only")
	pkg, args := popValue(args, "--package")
	confStr, args := popValue(args, "--confidence-min")
	if len(args) < 2 {
		fail(fmt.Errorf("usage: reponite diff <from-ref> <to-ref> [--changed-only] [--package P] [--confidence-min F]"))
	}
	conf := 0.0
	if confStr != "" {
		conf, _ = strconv.ParseFloat(confStr, 64)
	}
	opt := query.DiffOptions{ChangedOnly: changedOnly, Package: pkg, MinConfidence: conf}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.DiffJSON(query.DiffRefsBy(st, repoName("."), args[0], args[1], opt)))
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
	tests, args := popFlag(args, "--tests")
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite search <substr> [ref] [--tests]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.SearchJSON(query.SearchName(st, repoName("."), ref, args[0], tests)))
}

func cmdRootCause(args []string) {
	if len(args) < 3 {
		fail(fmt.Errorf("usage: reponite rootcause <symbol> <from-ref> <to-ref>"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.RootCauseJSON(query.RootCauseBy(st, repoName("."), args[0], args[1], args[2])))
}

// cmdCICheck exits non-zero if any exported symbol broke (removed or
// shape_changed) between base and head — the obvious PR gate. Behavior changes
// are not treated as breaks (they don't break the API contract).
func cmdCICheck(args []string) {
	baseRef, args := popValue(args, "--base")
	headRef, args := popValue(args, "--head")
	if baseRef == "" || headRef == "" {
		fail(fmt.Errorf("usage: reponite ci-check --base <ref> --head <ref>"))
	}
	st := openStore(".")
	defer st.Close()
	rep := query.DiffRefsBy(st, repoName("."), baseRef, headRef, query.DiffOptions{ChangedOnly: true})
	var breaks []query.SymbolChange
	for _, c := range rep.Changes {
		if c.Kind != query.ChangeRemoved && c.Kind != query.ChangeShape {
			continue
		}
		base := c.Name
		if i := strings.LastIndex(base, "."); i >= 0 {
			base = base[i+1:]
		}
		if base != "" && base[0] >= 'A' && base[0] <= 'Z' { // exported (Go convention)
			breaks = append(breaks, c)
		}
	}
	for _, b := range breaks {
		fmt.Printf("API BREAK: %s %s\n", b.Kind, b.Name)
	}
	if len(breaks) > 0 {
		fmt.Fprintf(os.Stderr, "reponite ci-check: %d exported API break(s) between %s and %s\n", len(breaks), baseRef, headRef)
		os.Exit(1)
	}
	fmt.Printf("reponite ci-check: no exported API breaks between %s and %s\n", baseRef, headRef)
}

// cmdSemSearch ranks symbols by semantic similarity to a natural-language query.
func cmdSemSearch(args []string) {
	limStr, args := popValue(args, "--limit")
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite semsearch <query> [ref] [--limit N]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	limit := 0
	if limStr != "" {
		limit, _ = strconv.Atoi(limStr)
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.SemanticJSON(query.SemanticSearch(st, repoName("."), ref, args[0], limit, nil)))
}

// cmdXImpact reports who across every indexed repo calls an external symbol.
func cmdXImpact(args []string) {
	ref, args := popValue(args, "--ref")
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite ximpact <symbol> [--ref R]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.XImpactJSON(query.XImpact(st, args[0], ref)))
}

func cmdBrief(args []string) {
	budgetStr, args := popValue(args, "--budget")
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite brief <symbol> [ref] [--budget N]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	budget := 0
	if budgetStr != "" {
		budget, _ = strconv.Atoi(budgetStr)
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.BriefJSON(query.Brief(st, repoName("."), ref, args[0], budget, processing.NewGitIntent("."))))
}

// cmdRootCauseTrace reads a stack trace (from a file arg or stdin) and drills
// down along the failing path between two refs.
func cmdRootCauseTrace(args []string) {
	fromRef, args := popValue(args, "--from")
	toRef, args := popValue(args, "--to")
	if fromRef == "" || toRef == "" {
		fail(fmt.Errorf("usage: reponite rootcause-trace <file|-> --from <ref> --to <ref>"))
	}
	var data []byte
	var err error
	if len(args) == 0 || args[0] == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(args[0])
	}
	if err != nil {
		fail(err)
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.RootCauseTraceJSON(query.RootCauseTrace(st, repoName("."), fromRef, toRef, string(data))))
}

func cmdContext(args []string) {
	tests, args := popFlag(args, "--tests")
	if len(args) < 1 {
		fail(fmt.Errorf("usage: reponite context <symbol> [ref] [--tests]"))
	}
	ref := "HEAD"
	if len(args) > 1 {
		ref = args[1]
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.ContextJSON(query.Context(st, repoName("."), ref, args[0], tests)))
}

// popFlag removes flag from args, reporting whether it was present.
func popFlag(args []string, flag string) (bool, []string) {
	rest := make([]string, 0, len(args))
	found := false
	for _, a := range args {
		if a == flag {
			found = true
			continue
		}
		rest = append(rest, a)
	}
	return found, rest
}

func cmdRefs(args []string) {
	st := openStore(".")
	defer st.Close()
	repo := repoName(".")
	printJSON(interfaces.RefsJSON(repo, st.Refs(repo)))
}
