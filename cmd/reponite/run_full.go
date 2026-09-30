//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
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
	case "usages":
		cmdUsages(args)
	case "topics":
		cmdTopics(args)
	case "verify-edit":
		cmdVerifyEdit(args)
	case "repos":
		cmdRepos(args)
	case "fleet":
		cmdFleet(args)
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

// joinNote appends a scope note to a result note without doubling separators.
func joinNote(note, extra string) string {
	if extra == "" {
		return note
	}
	if note == "" {
		return extra
	}
	return note + " — " + extra
}

// arg returns the i-th positional or a default when absent.
func arg(pos []string, i int, def string) string {
	if i < len(pos) {
		return pos[i]
	}
	return def
}

// excludeFlags collects repeatable --exclude values, each optionally
// comma-separated (gitignore-syntax patterns).
type excludeFlags []string

func (e *excludeFlags) String() string { return strings.Join(*e, ",") }
func (e *excludeFlags) Set(v string) error {
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			*e = append(*e, p)
		}
	}
	return nil
}

func cmdIndex(args []string) {
	var gitRev, tagGlob string
	var excludes excludeFlags
	var submodules, force bool
	pos := parseCmd("index", "index [<dir>] [ref] [--git <rev>[,<rev>...]] [--tags GLOB] [--submodules] [--force] [--exclude GLOB]...", args, func(fs *flag.FlagSet) {
		fs.StringVar(&gitRev, "git", "", "index git revisions' trees (tag/branch/SHA, comma-separated) instead of the working tree")
		fs.StringVar(&tagGlob, "tags", "", "also index every tag matching this glob (e.g. '3.7.*'), each under its own name")
		fs.BoolVar(&submodules, "submodules", false, "also index each submodule at the exact commit the revision pins, as ref <repo>@<label>")
		fs.BoolVar(&force, "force", false, "re-index revisions whose commit is already indexed under this ruleset")
		fs.Var(&excludes, "exclude", "exclude paths matching this gitignore-syntax pattern (repeatable, comma-separable); adds to the defaults (vendor/, third_party/, node_modules/, .git/, testdata/) and .reponiteignore")
	})
	dir := arg(pos, 0, ".")
	repo := repoName(dir)
	st := openStore(dir)
	defer st.Close()

	// Peer stores (the other registered repos) let index time capture the
	// contract each cross-repo reference was written against — §8B.3's
	// per-caller skew. Without them the skew is honestly "unknown".
	peers := openPeers(dir)
	defer peers.Close()
	opt := processing.IndexOptions{Excludes: excludes, Peers: peers.Store, Report: printIndexSummary}
	if gitRev == "" && tagGlob == "" {
		ref := arg(pos, 1, "HEAD")
		if err := processing.IndexDirWith(st, repo, ref, dir, version.NormVer, opt); err != nil {
			fail(err)
		}
		if err := st.SetIndexVersion(repo, ref, version.IndexVer); err != nil {
			fail(err)
		}
		registerRepo(repo, dir, st.ModulePath(repo)) // join the persistent fleet (§8B.7)
		fmt.Printf("indexed %s@%s%s — refs now: %v\n", repo, ref, moduleNote(st, repo), st.Refs(repo))
		return
	}
	var revList []string
	for _, r := range strings.Split(gitRev, ",") {
		if r = strings.TrimSpace(r); r != "" {
			revList = append(revList, r)
		}
	}
	revs, err := processing.ResolveGitRevs(dir, revList, tagGlob)
	if err != nil {
		fail(err)
	}
	if len(pos) >= 2 {
		if len(revs) != 1 {
			fail(fmt.Errorf("a ref label names ONE revision; %d were given — drop the label to store each under its own name", len(revs)))
		}
		revs[0].Label = pos[1]
	}
	if len(revs) == 0 {
		fail(fmt.Errorf("no revision matched (--git %q, --tags %q)", gitRev, tagGlob))
	}
	for i, rv := range revs {
		if len(revs) > 1 {
			fmt.Printf("[%d/%d] ", i+1, len(revs))
		}
		indexOneRev(st, repo, dir, rv, opt, force)
		if submodules {
			indexSubmodules(repo, dir, rv, excludes, force)
		}
	}
	registerRepo(repo, dir, st.ModulePath(repo)) // join the persistent fleet (§8B.7)
	fmt.Printf("refs now: %v\n", st.Refs(repo))
}

// indexOneRev indexes one revision, or reuses an index of the same commit
// under the current ruleset (resume: an interrupted batch re-run skips what
// is done; a second label for the same commit copies rows, not work).
func indexOneRev(st *sqlite.Store, repo, dir string, rv processing.GitRev, opt processing.IndexOptions, force bool) {
	if !force {
		if have, ok := st.RefByCommit(repo, rv.Commit, version.IndexVer); ok {
			if have == rv.Label {
				fmt.Printf("%s@%s already indexed at %s — skipped\n", repo, rv.Label, shortHash(rv.Commit))
				return
			}
			if err := st.AliasRef(repo, have, rv.Label); err != nil {
				fail(err)
			}
			fmt.Printf("%s@%s = %s@%s (same commit %s) — reused\n", repo, rv.Label, repo, have, shortHash(rv.Commit))
			return
		}
	}
	commit, err := processing.IndexGitRefWith(st, repo, rv.Label, dir, rv.Rev, version.NormVer, opt)
	if err != nil {
		fail(err)
	}
	if err := st.AddRef(repo, rv.Label, commit, ""); err != nil {
		fail(err)
	}
	if err := st.SetIndexVersion(repo, rv.Label, version.IndexVer); err != nil {
		fail(err)
	}
	fmt.Printf("indexed %s@%s (git %s @ %s)%s\n", repo, rv.Label, rv.Rev, shortHash(commit), moduleNote(st, repo))
}

// indexSubmodules indexes every submodule of parent@rv at its pinned commit,
// as ref "<parent>@<label>" in the submodule's own store — the exact code that
// version of the parent builds with. A submodule whose objects are not on
// this machine is named, never skipped silently.
func indexSubmodules(parent, dir string, rv processing.GitRev, excludes excludeFlags, force bool) {
	pins, err := processing.GitSubmodulePins(dir, rv.Rev)
	if err != nil {
		fail(err)
	}
	for _, p := range pins {
		if p.RepoDir == "" {
			fmt.Fprintf(os.Stderr, "reponite: submodule %s (%s) pinned at %s: commit not available locally (git submodule update --init %s) — not indexed\n",
				p.Name, p.Path, shortHash(p.Commit), p.Path)
			continue
		}
		sub := repoName(p.RepoDir)
		if strings.HasSuffix(filepath.ToSlash(p.RepoDir), "/.git/modules/"+p.Name) {
			sub = filepath.Base(p.Name)
		}
		sst := openStoreAs(p.RepoDir, sub)
		label := parent + "@" + rv.Label
		fmt.Printf("  submodule ")
		indexOneRev(sst, sub, p.RepoDir, processing.GitRev{Label: label, Rev: p.Commit, Commit: p.Commit},
			processing.IndexOptions{Excludes: excludes, Report: printIndexSummary}, force)
		registerRepo(sub, p.RepoDir, sst.ModulePath(sub))
		sst.Close()
	}
}

// printIndexSummary shows where the index came from. A repo that vendors
// third-party code under a non-standard path (not vendor/ or third_party/) is
// otherwise invisible: it inflates the index and outranks first-party code in
// search. Seeing "775 files from internal/foo/internal" is what prompts a
// .reponiteignore entry.
func printIndexSummary(sum processing.IndexSummary) {
	if sum.Files == 0 {
		fmt.Fprintln(os.Stderr, "reponite: no supported source files found — nothing was indexed")
		return
	}
	fmt.Printf("  %d files (%d test)", sum.Files, sum.TestFiles)
	shown := 0
	for _, d := range sum.TopDirs {
		// Only call out directories big enough to matter, and at most three.
		if shown >= 3 || d.Files*10 < sum.Files {
			break
		}
		if shown == 0 {
			fmt.Printf("; largest: ")
		} else {
			fmt.Printf(", ")
		}
		fmt.Printf("%s (%d)", d.Dir, d.Files)
		shown++
	}
	fmt.Println()
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
	var fixed bool
	var limit, offset int
	var local bool
	pos := parseCmd("grep", "grep <pattern> [ref] [--fixed] [--limit N] [--offset N] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&fixed, "fixed", false, "treat the pattern as a literal string, not a regex")
		fs.IntVar(&limit, "limit", 0, "max matches returned (0 = default 50, -1 = all)")
		fs.IntVar(&offset, "offset", 0, "matches to skip — page through a truncated result")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite grep <pattern> [ref] [--fixed] [--limit N] [--offset N]"))
	}
	f := openFleet(local)
	defer f.Close()
	res, err := query.GrepRepo(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], query.GrepOptions{Fixed: fixed, Limit: limit, Offset: offset})
	if err != nil {
		fail(err)
	}
	res.Note = joinNote(res.Note, f.fleetNote())
	printJSON(interfaces.GrepJSON(res))
}

func cmdSearch(args []string) {
	var tests bool
	var local bool
	pos := parseCmd("search", "search <substr> [ref] [--tests] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols (Test*/Benchmark*/…)")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite search <substr> [ref] [--tests] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	printJSON(interfaces.SearchJSON(query.SearchName(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], tests)))
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
	repo := repoName(".")

	// A gate that cannot see a ref must FAIL, not pass. Diffing against an
	// unindexed ref yields an empty or nonsense comparison that reads exactly
	// like "nothing broke" — so a typo'd base, or a CI job that forgot to index
	// it, would turn this gate into a rubber stamp. Exit 2 distinguishes
	// "couldn't check" from exit 1 "checked, and it breaks".
	if unindexed := query.UnindexedRefs(st, repo, baseRef, headRef); len(unindexed) > 0 {
		fmt.Fprintf(os.Stderr,
			"reponite ci-check: cannot check — ref(s) not indexed in %s: %s\n"+
				"Index them first, e.g. `reponite index . --git %s`. Indexed refs: %v\n",
			repo, strings.Join(unindexed, ", "), unindexed[0], st.Refs(repo))
		os.Exit(2)
	}

	rep := query.DiffRefsBy(st, repo, baseRef, headRef, query.DiffOptions{ChangedOnly: true})
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
	var local bool
	pos := parseCmd("semsearch", "semsearch <query> [ref] [--limit N] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 0, "max results (0 = default)")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite semsearch <query> [ref] [--limit N] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	res := query.SemanticSearch(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], limit, semanticRanker())
	res.Note = joinNote(res.Note, f.fleetNote())
	printJSON(interfaces.SemanticJSON(res))
}

// cmdXImpact reports who across every indexed repo calls an external symbol.
func cmdXImpact(args []string) {
	var ref string
	var local bool
	pos := parseCmd("ximpact", "ximpact <symbol> [--ref R] [--local]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&ref, "ref", "", "restrict each repo to this ref")
		fs.BoolVar(&local, "local", false, "consider only this repo instead of the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite ximpact <symbol> [--ref R] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	res := query.XImpact(f, pos[0], ref)
	res.Note = joinNote(res.Note, f.fleetNote())
	printJSON(interfaces.XImpactJSON(res))
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
	f := openFleet(false)
	defer f.Close()
	res := query.InvestigateWith(f, query.FleetRepo, "HEAD", question, budget, semanticRanker())
	if asJSON {
		printJSON(interfaces.InvestigateJSON(res))
		return
	}
	fmt.Println(res.Dossier)
}

// cmdBlastRadius fuses in-repo callers, fleet callers, covering tests, and
// cross-ref contract state into one pre-edit impact dossier.
func cmdBlastRadius(args []string) {
	var local bool
	pos := parseCmd("blast-radius", "blast-radius <symbol> [ref] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "consider only this repo instead of the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite blast-radius <symbol> [ref] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	printJSON(interfaces.BlastRadiusJSON(query.BlastRadius(f, f.repoFor(pos[0], ref), ref, pos[0])))
}

// cmdVerifyEdit compares the (edited) working-tree file at path against its
// indexed version and reports what breaks — the pre-commit safety check. The
// path must be repo-relative (as indexed).
func cmdVerifyEdit(args []string) {
	pos := parseCmd("verify-edit", "verify-edit <path>", args, nil)
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite verify-edit <path>   (path relative to the repo, as indexed)"))
	}
	path := pos[0]
	newContent, err := os.ReadFile(path)
	if err != nil {
		fail(err)
	}
	st := openStore(".")
	defer st.Close()
	repo := repoName(".")
	var oldContent string
	for _, f := range st.Files(repo, "HEAD") {
		if f.Path == path {
			oldContent = f.Content
			break
		}
	}
	old := processing.ParseEditedSymbols(path, oldContent, version.NormVer)
	nw := processing.ParseEditedSymbols(path, string(newContent), version.NormVer)
	printJSON(interfaces.VerifyEditJSON(query.VerifyEdit(st, repo, "HEAD", path, old, nw)))
}

// cmdUsages lists every call site of a symbol (fleet-wide), each with its line
// and whether it's a confirmed call-graph caller.
// refFlags are the ref selectors shared by fleet-wide commands: one ref for
// every repo (--ref), and per-repo pins for a deployed combination of versions
// (--refs rr_sootballs=3.7.2,rr_gbc=ec4818b).
type refFlags struct{ ref, pins string }

func (r *refFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&r.ref, "ref", "HEAD", "ref to read in every repo")
	fs.StringVar(&r.pins, "refs", "", "per-repo refs, repo=ref,... (overrides --ref for those repos)")
}

func cmdUsages(args []string) {
	var local bool
	var rf refFlags
	pos := parseCmd("usages", "usages <symbol> [--ref R] [--refs repo=ref,...] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		rf.register(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite usages <symbol> [--ref R] [--refs repo=ref,...] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.Usages(s, query.FleetRepo, rf.ref, pos[0])
	res.Note = joinNote(res.Note, cov)
	printJSON(interfaces.UsagesJSON(res))
}

// cmdTopics renders the ROS communication graph fleet-wide (pub/sub/service/
// action edges linked by name). With a name argument it focuses on that one
// topic/service/action: who produces it and who consumes it.
func cmdTopics(args []string) {
	var local bool
	var rf refFlags
	pos := parseCmd("topics", "topics [name] [--ref R] [--refs repo=ref,...] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		rf.register(fs)
	})
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.CommGraph(s, query.FleetRepo, rf.ref)
	if len(pos) >= 1 {
		res = query.Topic(s, query.FleetRepo, rf.ref, pos[0])
	}
	res.Note = joinNote(res.Note, cov)
	printJSON(interfaces.TopicsJSON(res))
}

// cmdRepos lists every indexed repo with its module + per-ref stats.
func cmdRepos(args []string) {
	parseCmd("repos", "repos", args, nil)
	f := openFleet(false)
	defer f.Close()
	printJSON(interfaces.OverviewJSON(query.Overview(f), nil))
}

func cmdBrief(args []string) {
	var budget int
	var local bool
	pos := parseCmd("brief", "brief <symbol> [ref] [--budget N] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite brief <symbol> [ref] [--budget N] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	printJSON(interfaces.BriefJSON(query.Brief(f, f.repoFor(pos[0], ref), ref, pos[0], budget, processing.NewGitIntent("."))))
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
	var local bool
	pos := parseCmd("context", "context <symbol> [ref] [--tests] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols among callers")
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite context <symbol> [ref] [--tests] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	printJSON(interfaces.ContextJSON(query.Context(f, f.repoFor(pos[0], ref), ref, pos[0], tests)))
}

func cmdRefs(args []string) {
	parseCmd("refs", "refs", args, nil)
	st := openStore(".")
	defer st.Close()
	repo := repoName(".")
	printJSON(interfaces.RefsJSON(repo, st.Refs(repo)))
}
