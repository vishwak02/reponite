//go:build sqlite && treesitter

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

// fleetStore is a set of opened per-repo stores presented as one query.Store.
// Discovery commands (search, grep, usages, ximpact, …) read the whole fleet
// so their answers match what the MCP server and dashboard return; per-repo
// commands (compat, diff, brief, …) keep using openStore directly.
type fleetStore struct {
	query.Store
	closers []*sqlite.Store
	// Names is the repo list this store aggregates. It is deliberately NOT
	// called Repos: that would shadow the embedded query.Store.Repos() method.
	Names []string
	Local string // the repo that owns the working directory
}

// Close releases every opened store.
func (f *fleetStore) Close() {
	for _, s := range f.closers {
		s.Close()
	}
}

// openFleet opens the working directory's store plus every live repo in the
// registry, aggregated into one MultiStore. This is what makes cross-repo
// answers actually cross-repo from the CLI: ximpact's SCIP/module tiers and
// per-caller skew can only match a caller in repo A to a definition in repo B
// when both are in the SAME store.
//
// local=true (the --local flag) restricts to the working directory, which is
// also the automatic behavior when no registry exists. Stale registry entries
// are reported, never silently skipped.
func openFleet(local bool) *fleetStore {
	localRepo := repoName(".")
	if local {
		st := openStore(".")
		return &fleetStore{Store: st, closers: []*sqlite.Store{st}, Names: []string{localRepo}, Local: localRepo}
	}

	dirs, stale, err := fleetDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: fleet registry unreadable (%v); using this repo only\n", err)
	}
	for _, e := range stale {
		fmt.Fprintf(os.Stderr, "reponite: registered repo %s has no index at %s — skipping (reponite fleet remove %s)\n", e.Repo, e.Dir, e.Repo)
	}

	f := &fleetStore{Local: localRepo}
	seen := map[string]bool{}
	// The working directory comes first so it stays the default repo for
	// commands that need one, even when the registry lists others.
	for _, dir := range append([]string{"."}, dirs...) {
		repo := repoName(dir)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		st := openStore(dir)
		f.closers = append(f.closers, st)
		f.Names = append(f.Names, repo)
	}
	stores := make([]query.Store, 0, len(f.closers))
	for _, s := range f.closers {
		stores = append(stores, s)
	}
	if len(stores) == 1 {
		f.Store = stores[0]
	} else {
		f.Store = storage.NewMultiStore(stores...)
	}
	// REPONITE_REFS pins repos to refs for every fleet-backed command (context,
	// brief, blast-radius, search, grep, usages, topics, ...), so a caller that
	// knows which versions a deployed system runs sets it once.
	if pins := os.Getenv(refsEnv); pins != "" {
		p, err := storage.ParsePins(pins)
		if err != nil {
			fail(fmt.Errorf("%s: %w", refsEnv, err))
		}
		// The pins stand in for the default ref only: an explicit other ref
		// (a compat target, `context X 5.6.7`) is read as asked.
		f.Store = &storage.Pinned{Inner: f.Store, Pins: p, Only: "HEAD"}
	}
	if hideTests() {
		f.Store = &storage.NoTests{Inner: f.Store}
	}
	return f
}

// RefFor and IndexVersion forward the wrapped store's optional interfaces:
// fleetStore embeds query.Store, which does not carry them, so without these a
// pinned fleet looked unpinned to every coverage check ("nothing searched").
func (f *fleetStore) RefFor(repo, ref string) string { return query.EffectiveRef(f.Store, repo, ref) }
func (f *fleetStore) IndexVersion(repo, ref string) (int, bool) {
	if iv, ok := f.Store.(query.IndexVersioner); ok {
		return iv.IndexVersion(repo, ref)
	}
	return 0, false
}

// noTestsEnv hides test code from the commands that take --no-tests.
const noTestsEnv = "REPONITE_NO_TESTS"

// noTestsFlag is --no-tests; noTestsCmd marks a command that honors it (and
// the env), so a command whose answer IS about tests (brief's covering tests,
// blast-radius) never silently loses them to an exported env var.
var noTestsFlag, noTestsCmd bool

// registerNoTests adds --no-tests to a command.
func registerNoTests(fs *flag.FlagSet) {
	noTestsCmd = true
	fs.BoolVar(&noTestsFlag, "no-tests", false, "drop test code (symbols and files classified as test: test/ dirs, *_test.*, test_*.py, mocks …) from the answer; also "+noTestsEnv+"=1")
}

func hideTests() bool {
	if !noTestsCmd {
		return false
	}
	if noTestsFlag {
		return true
	}
	switch strings.ToLower(os.Getenv(noTestsEnv)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// refsEnv pins repos to refs fleet-wide: "repoA=ref1,repoB=ref2".
const refsEnv = "REPONITE_REFS"

// openPeers opens every registered repo EXCEPT the one at dir, as a read-only
// view for index-time cross-repo lookups (§8B.3 contract capture). Returns an
// empty fleetStore (nil Store) when nothing else is registered, which callers
// treat as "no peer view" rather than an error.
func openPeers(dir string) *fleetStore {
	self := repoName(dir)
	dirs, _, err := fleetDirs()
	if err != nil || len(dirs) == 0 {
		return &fleetStore{}
	}
	f := &fleetStore{Local: self}
	seen := map[string]bool{self: true}
	for _, d := range dirs {
		repo := repoName(d)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		st := openStore(d)
		f.closers = append(f.closers, st)
		f.Names = append(f.Names, repo)
	}
	if len(f.closers) == 0 {
		return &fleetStore{}
	}
	stores := make([]query.Store, 0, len(f.closers))
	for _, s := range f.closers {
		stores = append(stores, s)
	}
	if len(stores) == 1 {
		f.Store = stores[0]
	} else {
		f.Store = storage.NewMultiStore(stores...)
	}
	return f
}

// repoFor finds which repo in this store actually defines symbol at ref, so a
// symbol-scoped command (brief, context, compat, rootcause, blast-radius)
// answers about the repo that HAS it rather than silently returning a blank
// result for the working directory. Preference order: --repo, the local repo,
// then a unique fleet match. C++ spellings (Class::method, ns::Class::method)
// resolve like reponite's own dir.Class.method ids.
//
// It never guesses: a symbol defined in several repos prints the candidates
// (each with its repo and the ref read) as the answer, and a miss says WHICH
// miss it is — the ref is not indexed (nothing could match), or the symbol is
// in no searched repo — with the nearest names and their repos.
func (f *fleetStore) repoFor(symbol, ref, prefer string) string {
	at := func(repo string) string { return repo + "@" + query.EffectiveRef(f, repo, ref) }
	if prefer != "" {
		if !containsName(f.Names, prefer) {
			fail(fmt.Errorf("--repo %s is not in the fleet (%v)", prefer, f.Names))
		}
		if len(query.ResolveSymbol(f, prefer, ref, symbol)) > 0 {
			return prefer
		}
		f.missFail(symbol, ref, []string{prefer})
	}
	if len(query.ResolveSymbol(f, f.Local, ref, symbol)) > 0 {
		return f.Local
	}
	var found []string
	for _, repo := range f.Names {
		if repo == f.Local {
			continue
		}
		if len(query.ResolveSymbol(f, repo, ref, symbol)) > 0 {
			found = append(found, repo)
		}
	}
	switch len(found) {
	case 1:
		fmt.Fprintf(os.Stderr, "reponite: %q is not in %s; answering for %s\n", symbol, at(f.Local), at(found[0]))
		return found[0]
	case 0:
		f.missFail(symbol, ref, f.Names)
	}
	// Several repos define it: the candidates ARE the answer.
	type cand struct {
		Repo    string   `json:"repo"`
		Ref     string   `json:"ref"`
		Symbols []string `json:"symbols"`
	}
	out := struct {
		Symbol     string `json:"symbol"`
		Ambiguous  bool   `json:"ambiguous"`
		Candidates []cand `json:"candidates"`
		Note       string `json:"note"`
	}{Symbol: symbol, Ambiguous: true,
		Note: fmt.Sprintf("%q is not in %s and is defined in %d other repos — re-run with --repo <repo> (or a more qualified name)", symbol, at(f.Local), len(found))}
	for _, repo := range found {
		out.Candidates = append(out.Candidates, cand{Repo: repo, Ref: query.EffectiveRef(f, repo, ref), Symbols: query.ResolveSymbol(f, repo, ref, symbol)})
	}
	b, _ := json.MarshalIndent(out, "", "  ")
	fmt.Println(string(b))
	os.Exit(0)
	return ""
}

// missFail explains a symbol miss precisely: no ref matched (nothing was
// readable at the requested/pinned ref) is a different problem from "the
// symbol is not in this repo", and both differ from "not anywhere".
func (f *fleetStore) missFail(symbol, ref string, repos []string) {
	var readable, unreadable []string
	for _, repo := range repos {
		eff := query.EffectiveRef(f, repo, ref)
		if query.RefIndexed(f, repo, ref) {
			readable = append(readable, repo+"@"+eff)
		} else {
			refs := f.Refs(repo)
			if len(refs) > 5 {
				refs = append(append([]string{}, refs[len(refs)-5:]...), fmt.Sprintf("+%d more", len(refs)-5))
			}
			unreadable = append(unreadable, fmt.Sprintf("%s@%s (indexed: %v)", repo, eff, refs))
		}
	}
	if len(unreadable) > 6 {
		unreadable = append(unreadable[:5], fmt.Sprintf("… and %d more repos", len(unreadable)-5))
	}
	if len(readable) == 0 {
		fail(fmt.Errorf("no ref matched: %s — nothing could be searched for %q; index the ref (reponite index <dir> <ref> --git <rev>) or pass the indexed ref", strings.Join(unreadable, "; "), symbol))
	}
	msg := fmt.Sprintf("symbol %q is not defined in %s", symbol, strings.Join(readable, ", "))
	if len(repos) > 1 {
		msg = fmt.Sprintf("symbol %q is not defined in any of the %d searched repos at their refs (local %s@%s)", symbol, len(readable), f.Local, query.EffectiveRef(f, f.Local, ref))
	}
	if len(unreadable) > 0 {
		msg += fmt.Sprintf("; not searched (ref not indexed): %s", strings.Join(unreadable, "; "))
	}
	scope := query.FleetRepo
	if len(repos) == 1 {
		scope = repos[0]
	}
	if near := query.Suggest(f, scope, ref, symbol, 6); len(near) > 0 {
		names := make([]string, 0, len(near))
		for _, n := range near {
			names = append(names, fmt.Sprintf("%s (%s@%s)", n.Name, n.Repo, n.Ref))
		}
		msg += "; near matches: " + strings.Join(names, ", ")
	}
	fail(fmt.Errorf("%s", msg))
}

// view is the fleet read at ref, with any repo=ref pins applied, plus a note
// naming what was actually read. A repo not indexed at its ref contributes
// nothing to the answer; the note says so, so an empty result is never taken
// for "no usages" when it means "not indexed".
func (f *fleetStore) view(ref, pins string) (query.Store, string) {
	var s query.Store = f.Store
	if pins != "" {
		p, err := storage.ParsePins(pins)
		if err != nil {
			fail(err)
		}
		for repo := range p {
			if !containsName(f.Names, repo) {
				fail(fmt.Errorf("--refs names %q, which is not in the fleet (%v)", repo, f.Names))
			}
		}
		s = &storage.Pinned{Inner: f.Store, Pins: p, Only: ref}
	}
	read, missing := storage.Coverage(s, ref)
	note := ""
	switch {
	case len(read) == 0:
		note = fmt.Sprintf("NOTHING READ: no repo is indexed at the requested ref (%v) — index it (reponite index <dir> <ref> --git <rev>) or pass --ref / --refs repo=ref", missing)
	case len(missing) > 0:
		note = fmt.Sprintf("read %v; not indexed at the requested ref, so not searched: %v", read, missing)
	default:
		note = fmt.Sprintf("read %v", read)
	}
	return s, note
}

func containsName(names []string, n string) bool {
	for _, x := range names {
		if x == n {
			return true
		}
	}
	return false
}

// fleetNote describes the scope a fleet-wide command actually searched, so a
// result is never mistaken for a narrower or wider one than it is.
func (f *fleetStore) fleetNote() string {
	if len(f.Names) <= 1 {
		return ""
	}
	return fmt.Sprintf("searched %d registered repos: %v", len(f.Names), f.Names)
}
