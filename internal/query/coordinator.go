// coordinator.go wires the pure query logic to a Store: it resolves refs, fetches
// records, runs the Oracle / diff / root-cause / grep / search, and attaches the
// self-describing _meta envelope (§10.3). These are the functions the CLI and MCP
// tools call. They are Store-agnostic, so they run against the in-memory store
// in-sandbox and the SQLite adapter in production (ADR-018). Freshness and
// resolution-mix fields are added to Meta when the indexer + staleness land.
package query

import (
	"fmt"
	"sort"
	"strings"
)

// RepoRef identifies a repository at a ref.
type RepoRef struct{ Repo, Ref string }

// Meta is the self-describing envelope on coordinator results (§10.3).
type Meta struct {
	Repo     string
	Ref      string
	Warnings []string
}

// CompatReport is the Oracle result for one symbol across targets, with _meta.
type CompatReport struct {
	Symbol   string
	Origin   RepoRef
	Verdicts []CompatVerdict
	Meta     Meta
}

// CompatSymbol resolves the origin symbol (bare or package-qualified) and
// compares it across target refs/repos, using the same qualified id at each ref.
func CompatSymbol(s Store, origin RepoRef, symbol string, targets []RepoRef) (CompatReport, error) {
	names := ResolveSymbol(s, origin.Repo, origin.Ref, symbol)
	if len(names) == 0 {
		return CompatReport{}, fmt.Errorf("symbol %q not found at %s@%s", symbol, origin.Repo, origin.Ref)
	}
	name := names[0]
	var warns []string
	if len(names) > 1 {
		warns = append(warns, fmt.Sprintf("ambiguous %q; using %s (also: %s)", symbol, name, strings.Join(names[1:], ", ")))
	}
	o, ok := s.SymbolAt(origin.Repo, name, origin.Ref)
	if !ok || !o.Present {
		return CompatReport{}, fmt.Errorf("symbol %q not found at %s@%s", symbol, origin.Repo, origin.Ref)
	}
	var ts []Target
	for _, t := range targets {
		snap, found := s.SymbolAt(t.Repo, name, t.Ref)
		if !found && !refIndexed(s, t.Repo, t.Ref) {
			warns = append(warns, fmt.Sprintf("%s@%s not indexed", t.Repo, t.Ref))
		}
		ts = append(ts, Target{Repo: t.Repo, Ref: t.Ref, Snapshot: snap})
	}
	verdicts := CompatAcross(o, ts)
	// Enrich behavior_changed verdicts with the specific differing callees, so
	// compat connects to rootcause without a second call (roadmap 3.1).
	var originSnap RefSnapshot
	loaded := false
	for i := range verdicts {
		if verdicts[i].Verdict != BehaviorChanged {
			continue
		}
		if !loaded {
			originSnap = s.Snapshot(origin.Repo, origin.Ref)
			loaded = true
		}
		// from = target (the other ref), to = origin (the ref asked about), so
		// "+" means origin added the callee, "-" means origin removed it.
		verdicts[i].ChangedCallees = ChangedCallees(name, s.Snapshot(verdicts[i].Repo, verdicts[i].Ref), originSnap)
	}
	return CompatReport{
		Symbol: name, Origin: origin,
		Verdicts: verdicts,
		Meta:     Meta{Repo: origin.Repo, Ref: origin.Ref, Warnings: warns},
	}, nil
}

// ResolveSymbol maps a user-supplied symbol (bare or package-qualified) to the
// stored qualified symbol ids at a ref: the exact id if present, else every id
// whose bare name matches, sorted. Lets a caller pass `Put` or
// `internal/storage.Put`, and surfaces ambiguity rather than guessing silently.
func ResolveSymbol(s Store, repo, ref, q string) []string {
	syms := s.SymbolsAt(repo, ref)
	if _, ok := syms[q]; ok {
		return []string{q}
	}
	var out []string
	for k := range syms {
		if baseName(k) == q {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// baseName is the bare name of a qualified id (segment after the last "."); the
// directory qualifier uses "/" so it never contains a ".".
func baseName(qid string) string {
	if i := strings.LastIndex(qid, "."); i >= 0 {
		return qid[i+1:]
	}
	return qid
}

// DiffReport is a ref-to-ref delta with _meta.
type DiffReport struct {
	Repo     string
	From, To string
	Changes  []SymbolChange
	Meta     Meta
}

// DiffRefsBy diffs two refs of a repo via the Store, applying opt (zero value =
// no filtering).
func DiffRefsBy(s Store, repo, from, to string, opt DiffOptions) DiffReport {
	var warns []string
	if !refIndexed(s, repo, from) {
		warns = append(warns, from+" not indexed")
	}
	if !refIndexed(s, repo, to) {
		warns = append(warns, to+" not indexed")
	}
	return DiffReport{
		Repo: repo, From: from, To: to,
		Changes: FilterChanges(DiffRefs(s.SymbolsAt(repo, from), s.SymbolsAt(repo, to)), opt),
		Meta:    Meta{Repo: repo, Ref: to, Warnings: warns},
	}
}

// RootCauseBy runs the drill-down between two refs of a repo via the Store,
// resolving target (bare or package-qualified) to a stored id.
func RootCauseBy(s Store, repo, target, from, to string) RootCauseResult {
	names := ResolveSymbol(s, repo, to, target)
	if len(names) == 0 {
		return RootCauseResult{Target: target, Note: "target not present at " + to}
	}
	name := names[0]
	res := RootCause(name, s.Snapshot(repo, from), s.Snapshot(repo, to))
	if len(names) > 1 {
		res.Note = strings.TrimSpace(res.Note + " (ambiguous target; used " + name + ")")
	}
	return res
}

// GrepRepo builds the ref's trigram index and runs a search. repo may be
// FleetRepo ("*") to grep every repo in the store, each match tagged with its
// repo; per-repo Totals/Scanned are summed and the merged matches re-sorted.
func GrepRepo(s Store, repo, ref, pattern string, opt GrepOptions) (GrepResult, error) {
	repos := reposFor(s, repo)
	if len(repos) == 1 {
		res, err := BuildTrigramIndex(s.Files(repos[0], ref)).Grep(pattern, opt)
		if err != nil {
			return GrepResult{}, err
		}
		for i := range res.Matches {
			res.Matches[i].Repo = repos[0]
		}
		if !refIndexed(s, repos[0], ref) {
			res.Note = strings.TrimSpace(res.Note + " (ref not indexed)")
		}
		return res, nil
	}
	var out GrepResult
	for _, rp := range repos {
		res, err := BuildTrigramIndex(s.Files(rp, ref)).Grep(pattern, opt)
		if err != nil {
			return GrepResult{}, err
		}
		for i := range res.Matches {
			res.Matches[i].Repo = rp
		}
		out.Matches = append(out.Matches, res.Matches...)
		out.Total += res.Total
		out.Scanned += res.Scanned
		out.Truncated = out.Truncated || res.Truncated
	}
	sort.Slice(out.Matches, func(i, j int) bool {
		a, b := out.Matches[i], out.Matches[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	out.Note = strings.TrimSpace(out.Note + " (fleet-wide)")
	return out, nil
}

// SearchHit is a structural name-search result. Repo is set so fleet-wide
// searches (repo="*") stay attributable to their source repo.
type SearchHit struct {
	Repo   string
	Name   string
	Ref    string
	IsTest bool
}

// FleetRepo is the wildcard repo selector: passing it (or "") to a search-style
// coordinator scans every repo in the store — the "boundary-less" default an
// agent wants when it doesn't yet know where a feature lives (§ fleet awareness).
const FleetRepo = "*"

// reposFor expands a repo selector: FleetRepo → every repo in the store, else
// just that repo.
func reposFor(s Store, repo string) []string {
	if repo == FleetRepo || repo == "" {
		return s.Repos()
	}
	return []string{repo}
}

// SearchName returns symbols whose name contains substr, sorted. repo may be
// FleetRepo ("*") to search every repo in the store. Go test entry points
// (Test*/Benchmark*/Example*/Fuzz*) are excluded unless includeTests, so
// code-intelligence queries aren't drowned in test noise.
func SearchName(s Store, repo, ref, substr string, includeTests bool) []SearchHit {
	hits := []SearchHit{}
	for _, rp := range reposFor(s, repo) {
		for name := range s.SymbolsAt(rp, ref) {
			if !strings.Contains(name, substr) {
				continue
			}
			test := IsTestName(baseName(name))
			if test && !includeTests {
				continue
			}
			hits = append(hits, SearchHit{Repo: rp, Name: name, Ref: ref, IsTest: test})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Repo != hits[j].Repo {
			return hits[i].Repo < hits[j].Repo
		}
		return hits[i].Name < hits[j].Name
	})
	return hits
}

// IsTestName reports whether name is a Go test entry point by the testing
// package's convention: a Test/Benchmark/Example/Fuzz prefix not immediately
// followed by a lowercase letter (so "TestMain" and "Test" qualify, "Testable"
// does not). This is a name heuristic — it does not catch lowercase test helpers
// (a limitation the package-qualified rework addresses).
func IsTestName(name string) bool {
	for _, p := range []string{"Test", "Benchmark", "Example", "Fuzz"} {
		if !strings.HasPrefix(name, p) {
			continue
		}
		rest := name[len(p):]
		if rest == "" || rest[0] < 'a' || rest[0] > 'z' {
			return true
		}
	}
	return false
}

func refIndexed(s Store, repo, ref string) bool {
	for _, r := range s.Refs(repo) {
		if r == ref {
			return true
		}
	}
	return false
}
