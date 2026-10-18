//go:build sqlite && treesitter

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

func indexBackedCommand(cmd string, args []string) {
	installFactsIfBuilt()
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
	case "coverage":
		cmdCoverage(args)
	case "links":
		cmdLinks(args)
	case "logsite":
		cmdLogSite(args)
	case "impls":
		cmdImpls(args)
	case "routes":
		cmdRoutes(args)
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
	case "why":
		cmdWhy(args)
	case "errors":
		cmdErrors(args)
	case "tests":
		cmdTests(args)
	case "ui":
		cmdUI(args)
	case "map":
		cmdMap(args)
	case "explain":
		cmdExplain(args)
	case "trace":
		cmdTrace(args)
	case "models":
		cmdModels(args)
	case "config":
		cmdConfig(args)
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
	if os.Getenv("GOGC") == "" {
		// indexing allocates and drops a great deal: collecting at 1.5x the live
		// heap instead of 2x lowers the peak of a very large tree by ~15%
		debug.SetGCPercent(50)
	}
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
			indexSubmodules(repo, dir, rv, excludes, force, 1)
		}
	}
	registerRepo(repo, dir, st.ModulePath(repo)) // join the persistent fleet (§8B.7)
	fmt.Printf("refs now: %v\n", st.Refs(repo))
}

// indexOneRev indexes one revision, or reuses an index of the same commit
// under the current ruleset (resume: an interrupted batch re-run skips what
// is done; a second label for the same commit copies rows, not work).
func indexOneRev(st *sqlite.Store, repo, dir string, rv processing.GitRev, opt processing.IndexOptions, force bool) {
	filter := refFilter(opt.Excludes)
	if !force && (filter != "" || st.RefFilter(repo, rv.Label) != "") {
		// --exclude makes a label a different file set from another label of the
		// same commit: the label is skipped only when it was indexed at this
		// commit with exactly this filter, and never aliased to another label.
		if st.RefAtCommit(repo, rv.Label, rv.Commit, version.IndexVer) && st.RefFilter(repo, rv.Label) == filter {
			fmt.Printf("%s@%s already indexed at %s — skipped\n", repo, rv.Label, shortHash(rv.Commit))
			return
		}
	} else if !force {
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
	if err := st.SetRefFilter(repo, rv.Label, filter); err != nil {
		fail(err)
	}
	fmt.Printf("indexed %s@%s (git %s @ %s)%s\n", repo, rv.Label, rv.Rev, shortHash(commit), moduleNote(st, repo))
	// derive the per-repo facts questions need now, so no question pays for them
	query.WarmFacts(st, repo, rv.Label)
}

const maxSubmoduleDepth = 5

// indexSubmodules indexes every submodule of parent@rv at its pinned commit,
// as ref "<parent>@<label>" in the submodule's own store — the exact code that
// version of the parent builds with. A submodule whose objects are not on
// this machine is named, never skipped silently.
//
// Submodules of submodules are part of the build too, so they are followed
// (up to maxSubmoduleDepth levels), labeled with the same top-level
// "<parent>@<label>" so one pin names every repo that version builds.
func indexSubmodules(parent, dir string, rv processing.GitRev, excludes excludeFlags, force bool, depth int) {
	pins, err := processing.GitSubmodulePins(dir, rv.Rev)
	if err != nil {
		if depth > 1 {
			return // a nested submodule's own pins are best-effort
		}
		fail(err)
	}
	var missing []string
	for _, p := range pins {
		if p.RepoDir == "" {
			missing = append(missing, p.Path+"@"+shortHash(p.Commit))
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
		if depth < maxSubmoduleDepth {
			indexSubmodules(parent, p.RepoDir, processing.GitRev{Label: rv.Label, Rev: p.Commit, Commit: p.Commit},
				excludes, force, depth+1)
		}
	}
	// One line for every submodule whose pinned commit is not on this machine:
	// a monorepo with dozens of uninitialized submodules otherwise buries the
	// index result under a screen of identical warnings.
	if n := len(missing); n > 0 {
		shown := missing
		if n > 8 {
			shown = append(append([]string{}, missing[:8]...), fmt.Sprintf("+%d more", n-8))
		}
		fmt.Fprintf(os.Stderr, "reponite: %d submodule(s) not indexed — their pinned commits are not on this machine (git submodule update --init --recursive): %s\n",
			n, strings.Join(shown, ", "))
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
	var loud []processing.SkippedFile // expected skips (binaries, tool dirs, ignore rules) go to `coverage`
	for _, sk := range sum.Skipped {
		if !sk.Quiet {
			loud = append(loud, sk)
		}
	}
	sum.Skipped = loud
	if n := len(sum.Skipped); n > 0 {
		fmt.Printf("  %d file(s) not parsed (too large to be source, or binary):", n)
		for i, s := range sum.Skipped {
			if i == 5 {
				fmt.Printf(" (+%d more)", n-5)
				break
			}
			fmt.Printf(" %s [%s]", s.Path, s.Reason)
		}
		fmt.Println()
	}
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
	var repoFlag string
	pos := parseCmd("compat", "compat <symbol> [ref] [--repo R] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite compat <symbol> [ref] [--repo R] [--no-tests]"))
	}
	f := openFleet(false)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	repo := f.repoFor(pos[0], ref, repoFlag)
	// The origin is the ref actually read (a pin's label under REPONITE_REFS),
	// so the report names it and never compares the pinned ref to itself.
	origin := query.EffectiveRef(f, repo, ref)
	var targets []query.RepoRef
	for _, r := range f.Refs(repo) {
		if r != origin {
			targets = append(targets, query.RepoRef{Repo: repo, Ref: r})
		}
	}
	rep, err := query.CompatSymbol(f, query.RepoRef{Repo: repo, Ref: origin}, pos[0], targets)
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
	pos := parseCmd("grep", "grep <pattern> [ref] [--fixed] [--limit N] [--offset N] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&fixed, "fixed", false, "treat the pattern as a literal string, not a regex")
		fs.IntVar(&limit, "limit", 0, "max matches returned (0 = default 50, -1 = all)")
		fs.IntVar(&offset, "offset", 0, "matches to skip — page through a truncated result")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite grep <pattern> [ref] [--fixed] [--limit N] [--offset N] [--no-tests]"))
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
	pos := parseCmd("search", "search <substr> [ref] [--tests] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols (Test*/Benchmark*/…)")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		registerNoTests(fs)
	})
	tests = tests && !hideTests()
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite search <substr> [ref] [--tests] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	hits := query.SearchName(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], tests)
	if len(hits) == 0 && !tests {
		if n := len(query.SearchName(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], true)); n > 0 {
			fmt.Fprintf(os.Stderr, "reponite search: no non-test symbol matches %q; %d test symbol(s) do — add --tests\n", pos[0], n)
		}
	}
	printJSON(interfaces.SearchJSON(hits))
}

func cmdRootCause(args []string) {
	var repoFlag string
	pos := parseCmd("rootcause", "rootcause <symbol> <from-ref> <to-ref> [--repo R] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
		registerNoTests(fs)
	})
	if len(pos) < 3 {
		fail(fmt.Errorf("usage: reponite rootcause <symbol> <from-ref> <to-ref> [--repo R] [--no-tests]"))
	}
	f := openFleet(false)
	defer f.Close()
	repo := f.repoFor(pos[0], pos[2], repoFlag)
	printJSON(interfaces.RootCauseJSON(query.RootCauseBy(f, repo, pos[0], pos[1], pos[2])))
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
	pos := parseCmd("semsearch", "semsearch <query> [ref] [--limit N] [--local] [--no-tests]\n  Ranks symbols by KEYWORD similarity (term-idf over identifiers and bodies) unless an embeddings\n  endpoint is configured (REPONITE_EMBED_ENDPOINT + REPONITE_EMBED_MODEL, neural build): it is not\n  semantic search by default. Per-repo term indexes are cached next to each index.", args, func(fs *flag.FlagSet) {
		fs.IntVar(&limit, "limit", 0, "max results (0 = default)")
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite semsearch <query> [ref] [--limit N] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ranker, why := semanticRankerWhy()
	res := query.SemanticSearchCached(f, query.FleetRepo, arg(pos, 1, "HEAD"), pos[0], limit, ranker,
		&termCache{store: f, noTests: hideTests()})
	if ranker == nil {
		res.Note = joinNote(res.Note, why)
	}
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
	printJSON(interfaces.BlastRadiusJSON(query.BlastRadius(f, f.repoFor(pos[0], ref, ""), ref, pos[0])))
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
// (--refs app=3.7.2,lib=ec4818b).
type refFlags struct{ ref, pins string }

func (r *refFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&r.ref, "ref", "HEAD", "ref to read in every repo")
	fs.StringVar(&r.pins, "refs", "", "per-repo refs, repo=ref,... (overrides --ref for those repos)")
}

func cmdUsages(args []string) {
	var local bool
	var rf refFlags
	pos := parseCmd("usages", "usages <symbol> [--ref R] [--refs repo=ref,...] [--local]", args, func(fs *flag.FlagSet) {
		registerFormat(fs)
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		rf.register(fs)
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite usages <symbol> [--ref R] [--refs repo=ref,...] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.Usages(s, query.FleetRepo, rf.ref, pos[0])
	res.Note = joinNote(res.Note, cov)
	if outFormat == "text" {
		fmt.Print(interfaces.UsagesText(res))
		return
	}
	printJSON(interfaces.UsagesJSON(res))
}

// cmdLogSite maps a line from a real log to the code that prints it.
func cmdLogSite(args []string) {
	var local bool
	var limit int
	var rf refFlags
	pos := parseCmd("logsite", "logsite \"<log line>\" [--ref R] [--refs repo=ref,...] [--limit N] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "search only this repo instead of the registered fleet")
		fs.IntVar(&limit, "limit", 10, "max sites")
		rf.register(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite logsite \"<log line>\""))
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.LogSites(s, query.FleetRepo, rf.ref, strings.Join(pos, " "), limit)
	res.Note = joinNote(res.Note, cov)
	printJSON(interfaces.LogSitesJSON(res))
}

// cmdImpls lists the classes deriving from a base fleet-wide (plugins and
// subclasses in other repos included) and each one's own override of method.
func cmdImpls(args []string) {
	var local bool
	var limit int
	var rf refFlags
	pos := parseCmd("impls", "impls <Base | Base::method | Base.method> [--ref R] [--refs repo=ref,...] [--limit N] [--local]", args, func(fs *flag.FlagSet) {
		registerFormat(fs)
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		fs.IntVar(&limit, "limit", 50, "max classes")
		rf.register(fs)
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite impls <Base[::method]>"))
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.Impls(s, query.FleetRepo, rf.ref, pos[0], limit)
	res.Note = joinNote(res.Note, cov)
	if outFormat == "text" {
		fmt.Print(interfaces.ImplsText(res))
		return
	}
	printJSON(interfaces.ImplsJSON(res))
}

// cmdRoutes links HTTP client calls to the server routes they reach,
// fleet-wide; with a path argument, only that endpoint's slice.
func cmdRoutes(args []string) {
	var local bool
	var limit int
	var gateways, upstreams string
	var rf refFlags
	pos := parseCmd("routes", "routes [path substring] [--gateway caddy.json,...] [--upstream host:port=repo,...] [--ref R] [--refs repo=ref,...] [--limit N] [--local]", args, func(fs *flag.FlagSet) {
		registerFormat(fs)
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		fs.IntVar(&limit, "limit", 50, "max routes")
		fs.StringVar(&gateways, "gateway", "", "reverse-proxy gateway config(s) in Caddy JSON, comma-separated: follow each request through its mount")
		fs.StringVar(&upstreams, "upstream", "", "which repo serves a gateway upstream, host:port=repo,... (default: matched by name)")
		rf.register(fs)
		registerNoTests(fs)
	})
	opt, err := interfaces.LoadGateways(gateways, upstreams)
	if err != nil {
		fail(err)
	}
	filter := ""
	if len(pos) > 0 {
		filter = pos[0]
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.RoutesWith(s, query.FleetRepo, rf.ref, filter, limit, opt)
	res.Note = joinNote(res.Note, cov)
	if outFormat == "text" {
		fmt.Print(interfaces.RoutesText(res))
		return
	}
	printJSON(interfaces.RoutesJSON(res))
}

// cmdTopics renders the ROS communication graph fleet-wide (pub/sub/service/
// action edges linked by name). With a name argument it focuses on that one
// topic/service/action: who produces it and who consumes it.
func cmdTopics(args []string) {
	var local bool
	var rf refFlags
	pos := parseCmd("topics", "topics [name] [--ref R] [--refs repo=ref,...] [--local]", args, func(fs *flag.FlagSet) {
		registerFormat(fs)
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		rf.register(fs)
		registerNoTests(fs)
	})
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	var res query.CommGraphResult
	if len(pos) >= 1 {
		res = query.Topic(s, query.FleetRepo, rf.ref, pos[0])
	} else {
		res = query.CommGraph(s, query.FleetRepo, rf.ref)
	}
	res.Note = joinNote(res.Note, cov)
	if outFormat == "text" {
		fmt.Print(interfaces.TopicsText(res))
		return
	}
	printJSON(interfaces.TopicsJSON(res))
}

// cmdRepos lists every indexed repo with its module + per-ref stats.
func cmdRepos(args []string) {
	parseCmd("repos", "repos", args, nil)
	f := openFleet(false)
	defer f.Close()
	printJSON(interfaces.OverviewJSON(query.Overview(f.Store), nil))
}

func cmdBrief(args []string) {
	var budget int
	var local bool
	var repoFlag string
	pos := parseCmd("brief", "brief <symbol | Class::method> [ref] [--repo R] [--budget N] [--local]", args, func(fs *flag.FlagSet) {
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite brief <symbol> [ref] [--budget N] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	printJSON(interfaces.BriefJSON(query.Brief(f, f.repoFor(pos[0], ref, repoFlag), ref, pos[0], budget, processing.NewGitIntent("."))))
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
	var repoFlag string
	pos := parseCmd("context", "context <symbol | Class::method | ns::Class::method> [ref] [--repo R] [--tests] [--no-tests] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&tests, "tests", false, "include test symbols among callers")
		fs.BoolVar(&local, "local", false, "only consider this repo, not the registered fleet")
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the symbol (default: this repo, else the one fleet repo that has it)")
		registerNoTests(fs)
	})
	tests = tests && !hideTests()
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite context <symbol> [ref] [--tests] [--local]"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 1, "HEAD")
	repo := f.repoFor(pos[0], ref, repoFlag)
	res := query.Context(f, repo, ref, pos[0], tests)
	printJSON(interfaces.ContextJSON(query.WithCrossRepoDispatch(f, repo, ref, res, tests)))
}

func cmdRefs(args []string) {
	parseCmd("refs", "refs", args, nil)
	st := openStore(".")
	defer st.Close()
	repo := repoName(".")
	printJSON(interfaces.RefsJSON(repo, st.Refs(repo)))
}

// cmdUI is the frontend view: pages, a component tree, or a component's uses.
func cmdUI(args []string) {
	var local, uses bool
	var budget int
	var format, gateways, upstreams string
	var rf refFlags
	pos := parseCmd("ui", "ui [page-route | component] [ref] [--uses] [--budget N] [--format json|text] [--gateway caddy.json,...] [--upstream host:port=repo,...] [--ref R] [--refs repo=ref,...] [--local] [--no-tests]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "scan only this repo instead of the registered fleet")
		fs.BoolVar(&uses, "uses", false, "list where the component is rendered, passed or routed to")
		fs.IntVar(&budget, "budget", 0, "token budget (0 = default ~3000)")
		fs.StringVar(&format, "format", "json", "json | text")
		fs.StringVar(&gateways, "gateway", "", "reverse-proxy gateway config(s) in Caddy JSON, comma-separated: follow API calls through each mount")
		fs.StringVar(&upstreams, "upstream", "", "which repo serves a gateway upstream, host:port=repo,...")
		rf.register(fs)
		registerNoTests(fs)
	})
	opt, err := interfaces.LoadGateways(gateways, upstreams)
	if err != nil {
		fail(err)
	}
	target := arg(pos, 0, "")
	if len(pos) > 1 {
		rf.ref = pos[1]
	}
	if uses && target == "" {
		fail(fmt.Errorf("usage: reponite ui <component> --uses"))
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(rf.ref, rf.pins)
	res := query.UI(s, query.FleetRepo, rf.ref, target, query.UIOptions{Budget: budget, Uses: uses, Routes: opt})
	res.Note = joinNote(res.Note, cov)
	if format == "text" {
		fmt.Print(interfaces.UIText(res))
		return
	}
	printJSON(interfaces.UIJSON(res))
}

// cmdTrace answers "what happens when …": the call tree below (or above) an
// entry — a symbol, "VERB /route", path:line, a CLI command or a ROS topic —
// crossing HTTP, topic, implementation and import boundaries fleet-wide, with
// each node's side effects.
func cmdTrace(args []string) {
	var local bool
	var depth, budget int
	var direction, format, repoFlag, skip, gateways, upstreams string
	var tests bool
	var rf refFlags
	pos := parseCmd("trace", "trace <symbol | \"VERB /path\" | path:line | cli-command | topic> [ref] [--direction down|up] [--depth N] [--budget N] [--format json|text] [--repo R] [--skip prefix,...] [--gateway caddy.json] [--refs repo=ref,...] [--tests] [--no-tests] [--local]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "only walk this repo instead of the registered fleet")
		fs.IntVar(&depth, "depth", 0, "levels to walk below the entry (0 = default 5)")
		fs.IntVar(&budget, "budget", 0, "token budget of the text rendering (0 = default 2000)")
		fs.StringVar(&direction, "direction", "down", "down: what this sets off; up: how execution gets here")
		fs.StringVar(&format, "format", "json", "json (nested tree) or text (indented tree)")
		fs.StringVar(&repoFlag, "repo", "", "the repo defining the entry symbol (default: every repo that has it)")
		fs.StringVar(&skip, "skip", "", "extra library/handle prefixes to treat as noise, comma-separated")
		fs.StringVar(&gateways, "gateway", "", "reverse-proxy gateway config(s) in Caddy JSON, comma-separated")
		fs.StringVar(&upstreams, "upstream", "", "which repo serves a gateway upstream, host:port=repo,...")
		fs.BoolVar(&tests, "tests", false, "walk into test code too")
		rf.register(fs)
		registerNoTests(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("usage: reponite trace <entry> [ref] [--direction down|up] [--depth N] [--budget N] [--format text]"))
	}
	ref := rf.ref
	if len(pos) > 1 {
		ref = pos[1]
	}
	ropt, err := interfaces.LoadGateways(gateways, upstreams)
	if err != nil {
		fail(err)
	}
	f := openFleet(local)
	defer f.Close()
	s, cov := f.view(ref, rf.pins)
	opt := query.TraceOptions{Direction: direction, Depth: depth, Budget: budget, IncludeTests: tests && !hideTests(),
		Repo: repoFlag, Routes: ropt, Edges: query.UITraceEdges(s, query.FleetRepo, ref)}
	for _, p := range strings.Split(skip, ",") {
		if p = strings.TrimSpace(p); p != "" {
			opt.Skip = append(opt.Skip, p)
		}
	}
	res := query.Trace(s, pos[0], ref, opt)
	res.Note = joinNote(res.Note, cov)
	printJSON(interfaces.TraceOutput(res, format))
}

// refFilter is the canonical form of a set of --exclude patterns.
func refFilter(ex []string) string {
	x := append([]string(nil), ex...)
	sort.Strings(x)
	return strings.Join(x, ",")
}

// cmdCoverage prints what the index stored of each repo and why the rest was not.
func cmdCoverage(args []string) {
	var local bool
	var rf refFlags
	var repoFlag, format string
	pos := parseCmd("coverage", "coverage [ref] [--repo R] [--local] [--format json|text]", args, func(fs *flag.FlagSet) {
		fs.BoolVar(&local, "local", false, "only this repo")
		fs.StringVar(&repoFlag, "repo", "", "only this repo of the fleet")
		fs.StringVar(&format, "format", "json", "json or text")
		rf.register(fs)
	})
	f := openFleet(local)
	defer f.Close()
	ref := arg(pos, 0, "HEAD")
	if rf.ref != "" && rf.ref != "HEAD" {
		ref = rf.ref
	}
	s, _ := f.view(ref, rf.pins)
	repo := query.FleetRepo
	if repoFlag != "" {
		repo = repoFlag
	} else if local {
		repo = f.Local
	}
	res := query.Coverage(s, repo, ref)
	if format == "text" {
		for _, r := range res.Repos {
			fmt.Printf("%s@%s: %d stored, %d skipped\n", r.Repo, r.Ref, r.Stored, r.Skipped)
			for reason, n := range r.Reasons {
				fmt.Printf("  skipped %5d  %s  (e.g. %s)\n", n, reason, strings.Join(r.Examples[reason], ", "))
			}
		}
		return
	}
	b, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(b))
}

// cmdLinks prints the references to and from a file (or naming a symbol) that
// are written as data: paths, ids, template includes, config values.
func cmdLinks(args []string) {
	var local bool
	var rf refFlags
	var repoFlag string
	pos := parseCmd("links", "links <path|symbol> [--repo R] [--local]", args, func(fs *flag.FlagSet) {
		registerFormat(fs)
		fs.BoolVar(&local, "local", false, "only this repo")
		fs.StringVar(&repoFlag, "repo", "", "only this repo of the fleet")
		rf.register(fs)
	})
	if len(pos) < 1 {
		fail(fmt.Errorf("links needs a path or a symbol name"))
	}
	f := openFleet(local)
	defer f.Close()
	ref := rf.ref
	if ref == "" {
		ref = "HEAD"
	}
	s, _ := f.view(ref, rf.pins)
	repo := query.FleetRepo
	if repoFlag != "" {
		repo = repoFlag
	} else if local {
		repo = f.Local
	}
	lr := query.LinksFor(s, repo, ref, pos[0])
	if outFormat == "text" {
		fmt.Print(interfaces.LinksText(lr))
		return
	}
	b, _ := json.MarshalIndent(lr, "", "  ")
	fmt.Println(string(b))
}

// outFormat is the --format of the commands with a compact text rendering.
var outFormat = "json"

func registerFormat(fs *flag.FlagSet) {
	fs.StringVar(&outFormat, "format", "json", "json, or text (compact: grouped, no repeated field names)")
}
