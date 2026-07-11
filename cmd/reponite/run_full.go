//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
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
	case "blast-radius":
		cmdBlastRadius(args)
	case "repos":
		cmdRepos(args)
	case "semsearch":
		cmdSemSearch(args)
	case "investigate":
		cmdInvestigate(args)
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

// parseCmd defines a per-command flag set and parses args so flags may appear
// before OR after the positional arguments (reponite commands interleave them,
// e.g. `diff v1 v2 --changed-only`). It returns the positionals in order. On an
// unknown flag or `-h`/`--help` it prints the command's usage and exits — every
// command gets validation and a --help for free (replacing ad-hoc popValue).
func parseCmd(name, usage string, args []string, define func(fs *flag.FlagSet)) []string {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: reponite %s\n", usage)
		fs.PrintDefaults()
	}
	if define != nil {
		define(fs)
	}
	var positional []string
	rest := args
	for len(rest) > 0 {
		// ExitOnError: a bad flag or -h prints usage and exits here.
		_ = fs.Parse(rest)
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0]) // consume one positional, keep parsing
		rest = rest[1:]
	}
	return positional
}

// arg returns the i-th positional or a default when absent.
func arg(pos []string, i int, def string) string {
	if i < len(pos) {
		return pos[i]
	}
	return def
}

func cmdIndex(args []string) {
	var gitRev string
	pos := parseCmd("index", "index [<dir>] [ref] [--git <rev>]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&gitRev, "git", "", "index a git revision's tree (tag/branch/SHA/HEAD~3) instead of the working tree")
	})
	dir := arg(pos, 0, ".")
	ref := arg(pos, 1, "HEAD")
	if len(pos) < 2 && gitRev != "" {
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

func shortHash(h string) string {
	if len(h) > 12 {
		return h[:12]
	}
	return h
}

// moduleNote reports the detected module path for cross-repo impact, or a hint
// when none was found (so a user knows ximpact will fall back to name matching).
func moduleNote(st interface{ ModulePath(string) string }, repo string) string {
	if m := st.ModulePath(repo); m != "" {
		return " [module " + m + "]"
	}
	return " [no module manifest — ximpact name-based]"
}

func cmdCompat(args []string) {
	pos := parseCmd("compat", "compat <symbol> [ref]", args, nil)
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite compat <symbol> [ref]"))
	}
	ref := arg(pos, 1, "HEAD")
	repo := repoName(".")
	st := openStore(".")
	defer st.Close()
	var targets []query.RepoRef
	for _, r := range st.Refs(repo) {
		if r != ref {
			targets = append(targets, query.RepoRef{Repo: repo, Ref: r})
		}
	}
	rep, err := query.CompatSymbol(st, query.RepoRef{Repo: repo, Ref: ref}, pos[0], targets)
	if err != nil {
		fail(err)
	}
	printJSON(interfaces.CompatJSON(rep))
}

func cmdDiff(args []string) {
	var changedOnly bool
	var pkg string
	var conf float64
	pos := parseCmd("diff", "diff <from-ref> <to-ref> [--changed-only] [--package P] [--confidence-min F]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&changedOnly, "changed-only", false, "drop unchanged symbols")
		fs.StringVar(&pkg, "package", "", "keep only symbols whose package has this prefix")
		fs.Float64Var(&conf, "confidence-min", 0, "drop changes below this confidence")
	})
	if len(pos) < 2 {
		fail(fmt.Errorf("usage: reponite diff <from-ref> <to-ref> [--changed-only] [--package P] [--confidence-min F]"))
	}
	opt := query.DiffOptions{ChangedOnly: changedOnly, Package: pkg, MinConfidence: conf}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.DiffJSON(query.DiffRefsBy(st, repoName("."), pos[0], pos[1], opt)))
}

func cmdGrep(args []string) {
	pos := parseCmd("grep", "grep <pattern> [ref]", args, nil)
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite grep <pattern> [ref]"))
	}
	st := openStore(".")
	defer st.Close()
	res, err := query.GrepRepo(st, repoName("."), arg(pos, 1, "HEAD"), pos[0], query.GrepOptions{Fixed: true})
	if err != nil {
		fail(err)
	}
	printJSON(interfaces.GrepJSON(res))
}

func cmdSearch(args []string) {
	var tests bool
	pos := parseCmd("search", "search <substr> [ref] [--tests]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols (Test*/Benchmark*/…)")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite search <substr> [ref] [--tests]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.SearchJSON(query.SearchName(st, repoName("."), arg(pos, 1, "HEAD"), pos[0], tests)))
}

func cmdRootCause(args []string) {
	pos := parseCmd("rootcause", "rootcause <symbol> <from-ref> <to-ref>", args, nil)
	if len(pos) < 3 {
		fail(fmt.Errorf("usage: reponite rootcause <symbol> <from-ref> <to-ref>"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.RootCauseJSON(query.RootCauseBy(st, repoName("."), pos[0], pos[1], pos[2])))
}

// cmdCICheck exits non-zero if any exported symbol broke (removed or
// shape_changed) between base and head — the obvious PR gate. Behavior changes
// are not treated as breaks (they don't break the API contract). "Exported" is
// decided per language (query.IsExportedName), not by the Go uppercase rule.
func cmdCICheck(args []string) {
	var baseRef, headRef string
	parseCmd("ci-check", "ci-check --base <ref> --head <ref>", args, func(fs *flag.FlagSet) {
		fs.StringVar(&baseRef, "base", "", "baseline ref")
		fs.StringVar(&headRef, "head", "", "candidate ref")
	})
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
		if query.IsExportedName(c.Lang, base) { // per-language public-API rule
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
	var limit int
	pos := parseCmd("semsearch", "semsearch <query> [ref] [--limit N]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 0, "max results (0 = default)")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite semsearch <query> [ref] [--limit N]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.SemanticJSON(query.SemanticSearch(st, repoName("."), arg(pos, 1, "HEAD"), pos[0], limit, nil)))
}

// cmdXImpact reports who across every indexed repo calls an external symbol.
func cmdXImpact(args []string) {
	var ref string
	pos := parseCmd("ximpact", "ximpact <symbol> [--ref R]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "restrict each repo to this ref")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite ximpact <symbol> [--ref R]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.XImpactJSON(query.XImpact(st, pos[0], ref)))
}

// cmdInvestigate answers a natural-language question with one cited dossier of
// the most relevant symbols across the fleet. Prints the markdown dossier
// (--json for the structured form).
func cmdInvestigate(args []string) {
	var budget int
	var asJSON bool
	pos := parseCmd("investigate", "investigate <question...> [--budget N] [--json]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "token budget (default ~4000)")
		fs.BoolVar(&asJSON, "json", false, "emit structured JSON instead of the markdown dossier")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite investigate <question...> [--budget N] [--json]"))
	}
	question := strings.Join(pos, " ")
	st := openStore(".")
	defer st.Close()
	res := query.Investigate(st, query.FleetRepo, "HEAD", question, budget)
	if asJSON {
		printJSON(interfaces.InvestigateJSON(res))
		return
	}
	fmt.Println(res.Dossier)
}

// cmdBlastRadius fuses in-repo callers, fleet callers, covering tests, and
// cross-ref contract state into one pre-edit impact dossier.
func cmdBlastRadius(args []string) {
	pos := parseCmd("blast-radius", "blast-radius <symbol> [ref]", args, nil)
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite blast-radius <symbol> [ref]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.BlastRadiusJSON(query.BlastRadius(st, repoName("."), arg(pos, 1, "HEAD"), pos[0])))
}

// cmdRepos lists every indexed repo with its module + per-ref stats.
func cmdRepos(args []string) {
	parseCmd("repos", "repos", args, nil)
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.OverviewJSON(query.Overview(st), nil))
}

func cmdBrief(args []string) {
	var budget int
	pos := parseCmd("brief", "brief <symbol> [ref] [--budget N]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite brief <symbol> [ref] [--budget N]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.BriefJSON(query.Brief(st, repoName("."), arg(pos, 1, "HEAD"), pos[0], budget, processing.NewGitIntent("."))))
}

// cmdRootCauseTrace reads a stack trace (from a file arg or stdin) and drills
// down along the failing path between two refs.
func cmdRootCauseTrace(args []string) {
	var fromRef, toRef string
	pos := parseCmd("rootcause-trace", "rootcause-trace <file|-> --from <ref> --to <ref>", args, func(fs *flag.FlagSet) {
		fs.StringVar(&fromRef, "from", "", "ref the trace worked at")
		fs.StringVar(&toRef, "to", "", "ref the trace fails at")
	})
	if fromRef == "" || toRef == "" {
		fail(fmt.Errorf("usage: reponite rootcause-trace <file|-> --from <ref> --to <ref>"))
	}
	var data []byte
	var err error
	if len(pos) == 0 || pos[0] == "-" {
		data, err = io.ReadAll(os.Stdin)
	} else {
		data, err = os.ReadFile(pos[0])
	}
	if err != nil {
		fail(err)
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.RootCauseTraceJSON(query.RootCauseTrace(st, repoName("."), fromRef, toRef, string(data))))
}

func cmdContext(args []string) {
	var tests bool
	pos := parseCmd("context", "context <symbol> [ref] [--tests]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols among callers")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite context <symbol> [ref] [--tests]"))
	}
	st := openStore(".")
	defer st.Close()
	printJSON(interfaces.ContextJSON(query.Context(st, repoName("."), arg(pos, 1, "HEAD"), pos[0], tests)))
}

func cmdRefs(args []string) {
	parseCmd("refs", "refs", args, nil)
	st := openStore(".")
	defer st.Close()
	repo := repoName(".")
	printJSON(interfaces.RefsJSON(repo, st.Refs(repo)))
}
