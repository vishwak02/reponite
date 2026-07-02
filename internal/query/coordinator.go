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

// CompatSymbol resolves the origin symbol and compares it across target refs/repos.
func CompatSymbol(s Store, origin RepoRef, symbol string, targets []RepoRef) (CompatReport, error) {
	o, ok := s.SymbolAt(origin.Repo, symbol, origin.Ref)
	if !ok || !o.Present {
		return CompatReport{}, fmt.Errorf("symbol %q not found at %s@%s", symbol, origin.Repo, origin.Ref)
	}
	var (
		ts    []Target
		warns []string
	)
	for _, t := range targets {
		snap, found := s.SymbolAt(t.Repo, symbol, t.Ref)
		if !found && !refIndexed(s, t.Repo, t.Ref) {
			warns = append(warns, fmt.Sprintf("%s@%s not indexed", t.Repo, t.Ref))
		}
		ts = append(ts, Target{Repo: t.Repo, Ref: t.Ref, Snapshot: snap})
	}
	return CompatReport{
		Symbol: symbol, Origin: origin,
		Verdicts: CompatAcross(o, ts),
		Meta:     Meta{Repo: origin.Repo, Ref: origin.Ref, Warnings: warns},
	}, nil
}

// DiffReport is a ref-to-ref delta with _meta.
type DiffReport struct {
	Repo     string
	From, To string
	Changes  []SymbolChange
	Meta     Meta
}

// DiffRefsBy diffs two refs of a repo via the Store.
func DiffRefsBy(s Store, repo, from, to string) DiffReport {
	var warns []string
	if !refIndexed(s, repo, from) {
		warns = append(warns, from+" not indexed")
	}
	if !refIndexed(s, repo, to) {
		warns = append(warns, to+" not indexed")
	}
	return DiffReport{
		Repo: repo, From: from, To: to,
		Changes: DiffRefs(s.SymbolsAt(repo, from), s.SymbolsAt(repo, to)),
		Meta:    Meta{Repo: repo, Ref: to, Warnings: warns},
	}
}

// RootCauseBy runs the drill-down between two refs of a repo via the Store.
func RootCauseBy(s Store, repo, target, from, to string) RootCauseResult {
	return RootCause(target, s.Snapshot(repo, from), s.Snapshot(repo, to))
}

// GrepRepo builds the ref's trigram index and runs a search.
func GrepRepo(s Store, repo, ref, pattern string, opt GrepOptions) (GrepResult, error) {
	res, err := BuildTrigramIndex(s.Files(repo, ref)).Grep(pattern, opt)
	if err != nil {
		return GrepResult{}, err
	}
	if !refIndexed(s, repo, ref) {
		res.Note = strings.TrimSpace(res.Note + " (ref not indexed)")
	}
	return res, nil
}

// SearchHit is a structural name-search result.
type SearchHit struct {
	Name string
	Ref  string
}

// SearchName returns symbols whose name contains substr, sorted.
func SearchName(s Store, repo, ref, substr string) []SearchHit {
	var hits []SearchHit
	for name := range s.SymbolsAt(repo, ref) {
		if strings.Contains(name, substr) {
			hits = append(hits, SearchHit{Name: name, Ref: ref})
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Name < hits[j].Name })
	return hits
}

func refIndexed(s Store, repo, ref string) bool {
	for _, r := range s.Refs(repo) {
		if r == ref {
			return true
		}
	}
	return false
}
