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
	// Refs is the ref each repo was actually read at, set when the store pins
	// repos to their own refs (REPONITE_REFS / --refs) for a fleet-wide answer.
	Refs map[string]string
}

// NewMeta is the _meta of an answer about repo at ref, read through s. When s
// pins repos to their own refs (REPONITE_REFS / --refs), the ref reported is
// the one actually read — the pin's label — never the ref the query was
// phrased in: "HEAD" on an answer read at a release tag is a lie about what
// was analyzed. A fleet-wide answer lists every repo's ref in Refs.
func NewMeta(s Store, repo, ref string) Meta {
	m := Meta{Repo: repo, Ref: ref}
	p, ok := s.(RefPinner)
	if !ok {
		return m
	}
	if repo != FleetRepo && repo != "" {
		m.Ref = p.RefFor(repo, ref)
		return m
	}
	pinned := 0
	repos := s.Repos()
	m.Refs = make(map[string]string, len(repos))
	for _, r := range repos {
		got := p.RefFor(r, ref)
		m.Refs[r] = got
		if got != ref {
			pinned++
		}
	}
	switch {
	case pinned == 0:
		m.Refs = nil
	case pinned == len(repos):
		m.Ref = "pinned per repo (see refs)"
	default:
		m.Ref = ref + " where not pinned (see refs)"
	}
	return m
}

// EffectiveRef is the ref repo is actually read at when a query names ref.
func EffectiveRef(s Store, repo, ref string) string {
	if p, ok := s.(RefPinner); ok {
		return p.RefFor(repo, ref)
	}
	return ref
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
		if w := indexVerWarning(s, origin, t); w != "" {
			warns = append(warns, w)
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
	return resolveIn(s.SymbolsAt(repo, ref), q)
}

func resolveIn(syms map[string]SymbolRef, q string) []string {
	if _, ok := syms[q]; ok {
		return []string{q}
	}
	if strings.Contains(q, "::") {
		return resolveCpp(syms, q)
	}
	// A partly qualified name (Class.method, dir.Class.method) matches every id
	// ending in it on a "." boundary; a bare name matches by base name.
	var out []string
	for k := range syms {
		if baseName(k) == q || strings.HasSuffix(k, "."+q) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// resolveCpp resolves a C++-spelled name (Class::method, ns::Class::method,
// ::ns::f) against reponite's ids, which qualify by directory and class with
// "." and carry no C++ namespace: leading qualifiers are dropped one at a time
// until a match, keeping Class::method at least two segments so a method name
// alone never matches every class. A namespace-qualified free function
// (lower-case qualifier, ns::f) may fall back to its bare name.
func resolveCpp(syms map[string]SymbolRef, q string) []string {
	parts := strings.Split(strings.Trim(strings.ReplaceAll(q, "::", "."), "."), ".")
	for i := 0; i < len(parts); i++ {
		if len(parts)-i < 2 && !(len(parts) >= 2 && isLowerIdent(parts[len(parts)-2])) {
			break
		}
		if r := resolveIn(syms, strings.Join(parts[i:], ".")); len(r) > 0 {
			return r
		}
	}
	return nil
}

func isLowerIdent(s string) bool { return s != "" && s[0] >= 'a' && s[0] <= 'z' }

// NormalizeSymbol spells a C++ qualified name (A::b) the way reponite ids
// qualify (A.b), for matching and suggestions.
func NormalizeSymbol(q string) string {
	return strings.Trim(strings.ReplaceAll(q, "::", "."), ".")
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
	if w := indexVerWarning(s, RepoRef{Repo: repo, Ref: from}, RepoRef{Repo: repo, Ref: to}); w != "" {
		warns = append(warns, w)
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
	if w := indexVerWarning(s, RepoRef{Repo: repo, Ref: from}, RepoRef{Repo: repo, Ref: to}); w != "" {
		res.Note = strings.TrimSpace(res.Note + " " + w)
	}
	return res
}

// GrepRepo builds the ref's trigram index and runs a search. repo may be
// FleetRepo ("*") to grep every repo in the store, each match tagged with its
// repo; per-repo Totals/Scanned are summed and the merged matches re-sorted.
func GrepRepo(s Store, repo, ref, pattern string, opt GrepOptions) (GrepResult, error) {
	repos := reposFor(s, repo)
	if len(repos) == 1 {
		res, err := ScanGrep(s.Files(repos[0], ref), pattern, opt)
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
	// Fleet paging: the merged window is [Offset, Offset+Limit) over the
	// GLOBAL (repo, path, line) order. Each repo is asked for its first
	// Offset+Limit matches (offset 0) — a superset of what the global window
	// can draw from that repo — then the window is applied to the merge.
	if opt.Offset < 0 {
		opt.Offset = 0
	}
	sub := opt
	sub.Offset = 0
	limit := effectiveLimit(opt.Limit)
	if limit >= 0 {
		sub.Limit = opt.Offset + limit
	}
	var out GrepResult
	var missing []string // repos that have no such ref — a silent 0 would lie
	for _, rp := range repos {
		if !refIndexed(s, rp, ref) {
			missing = append(missing, rp)
		}
		res, err := ScanGrep(s.Files(rp, ref), pattern, sub)
		if err != nil {
			return GrepResult{}, err
		}
		for i := range res.Matches {
			res.Matches[i].Repo = rp
		}
		out.Matches = append(out.Matches, res.Matches...)
		out.Total += res.Total
		out.Scanned += res.Scanned
		if res.Note != "" && out.Note == "" {
			out.Note = res.Note
		}
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
	start := opt.Offset
	if start > len(out.Matches) {
		start = len(out.Matches)
	}
	end := len(out.Matches)
	if limit >= 0 && start+limit < end {
		end = start + limit
	}
	out.Matches = out.Matches[start:end]
	out.Offset = opt.Offset
	out.Truncated = opt.Offset+len(out.Matches) < out.Total
	out.Note = strings.TrimSpace(out.Note + " (fleet-wide)")
	// The single-repo path already says "(ref not indexed)"; the fleet path must
	// too, or "0 matches" is indistinguishable from "searched and found none".
	switch {
	case len(missing) == len(repos):
		out.Note = strings.TrimSpace(out.Note + " — ref " + ref + " is not indexed in ANY searched repo; this result is empty because nothing was searched")
	case len(missing) > 0:
		out.Note = strings.TrimSpace(out.Note + " — ref " + ref + " is not indexed in " + strings.Join(missing, ", ") + "; those repos contributed nothing")
	}
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

// RefPinner is a Store that reads some repos at their own ref whatever ref a
// query names (storage.Pinned). Coverage checks ask it which ref a repo is
// really read at, so a pinned repo is not reported as "not indexed".
type RefPinner interface {
	RefFor(repo, ref string) string
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
		for name, sym := range s.SymbolsAt(rp, ref) {
			if !strings.Contains(name, substr) {
				continue
			}
			test := IsTestSymbol(sym, name)
			if test && !includeTests {
				continue
			}
			hits = append(hits, SearchHit{Repo: rp, Name: name, Ref: EffectiveRef(s, rp, ref), IsTest: test})
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

// IsTestSymbol reports whether a symbol is test code. It prefers the flag
// captured at index time from the file's PATH (§9A.1), which is the only signal
// that works for languages whose tests are named by convention on the file
// rather than the symbol — a C++ fixture in `test/`, a `test_x.py`, a
// `foo.spec.ts`. The Go name heuristic remains as a fallback so an index built
// before is_test existed keeps behaving as it did rather than reporting "no
// tests" for everything.
func IsTestSymbol(ref SymbolRef, qid string) bool {
	return ref.IsTest || IsTestName(baseName(qid))
}

// IsTestQID looks a symbol up and reports whether it is test code. Use this
// where only the qualified id is in hand (brief/blast/context walk caller name
// lists): the index-time path flag lives on the stored record, so a name alone
// cannot answer for any language whose tests are named on the file.
func IsTestQID(s Store, repo, ref, qid string) bool {
	if sym, ok := s.SymbolAt(repo, qid, ref); ok {
		return IsTestSymbol(sym, qid)
	}
	return IsTestName(baseName(qid))
}

// IsTestName reports whether name is a Go test entry point by the testing
// package's convention: a Test/Benchmark/Example/Fuzz prefix not immediately
// followed by a lowercase letter (so "TestMain" and "Test" qualify, "Testable"
// does not). NAME-only, so it is blind to every non-Go convention — prefer
// IsTestSymbol, which consults the index-time path flag first.
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

// UnindexedRefs returns which of refs are NOT indexed for repo, in the order
// given. A gate or comparison whose answer would be meaningless without a ref
// should call this first and refuse to answer, rather than produce an empty
// diff that reads as "nothing changed".
func UnindexedRefs(s Store, repo string, refs ...string) []string {
	var missing []string
	for _, r := range refs {
		if !refIndexed(s, repo, r) {
			missing = append(missing, r)
		}
	}
	return missing
}

// RefIndexed reports whether repo has ref indexed. Callers whose ANSWER would
// be meaningless without it — a CI gate, a diff — must check first: an
// unindexed ref yields an empty comparison, which reads exactly like "nothing
// changed" and is the most dangerous way for this tool to be wrong.
func RefIndexed(s Store, repo, ref string) bool { return refIndexed(s, repo, ref) }

// IndexVersioner is implemented by stores that record the ruleset each ref
// was indexed under (version.IndexVer).
type IndexVersioner interface {
	IndexVersion(repo, ref string) (int, bool)
}

// indexVerWarning explains a comparison across rulesets: identical code can
// hash differently because the edges were resolved by different rules.
func indexVerWarning(s Store, a, b RepoRef) string {
	iv, ok := s.(IndexVersioner)
	if !ok {
		return ""
	}
	va, oka := iv.IndexVersion(a.Repo, a.Ref)
	vb, okb := iv.IndexVersion(b.Repo, b.Ref)
	if !oka || !okb || va == vb {
		return ""
	}
	return fmt.Sprintf("%s@%s was indexed with ruleset v%d and %s@%s with v%d — edges were resolved by different rules, so a behavior difference may be the indexer, not the code; reindex both with the same reponite",
		a.Repo, a.Ref, va, b.Repo, b.Ref, vb)
}

func refIndexed(s Store, repo, ref string) bool {
	if p, ok := s.(RefPinner); ok {
		ref = p.RefFor(repo, ref)
	}
	for _, r := range s.Refs(repo) {
		if r == ref {
			return true
		}
	}
	return false
}
