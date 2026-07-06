// overview.go computes the dashboard's index summary: for every repo and ref in
// the store, how much is indexed (symbols, resolved call edges, files) plus the
// repo's module identity and each ref's commit. Pure over the Store interface,
// so it works for both the in-memory and SQLite backends and is unit-tested
// in-sandbox (ADR-018). Physical database facts (file path, per-table row
// counts) are layered on at the interface adapter, not here.
package query

import "sort"

// RefStat is what one ref contributes to the index.
type RefStat struct {
	Ref     string
	Commit  string
	Symbols int
	Edges   int // resolved CALLS edges (sum over symbols' callees)
	Files   int
}

// RepoOverview is one repo's index summary across its refs.
type RepoOverview struct {
	Repo   string
	Module string
	Refs   []RefStat
}

// Overview summarizes every repo/ref in the store, sorted (repo, ref) for
// determinism.
func Overview(s Store) []RepoOverview {
	repos := s.Repos()
	out := make([]RepoOverview, 0, len(repos))
	for _, repo := range repos {
		ov := RepoOverview{Repo: repo, Module: s.ModulePath(repo)}
		refs := s.Refs(repo)
		sort.Strings(refs)
		for _, ref := range refs {
			rs := RefStat{Ref: ref}
			rs.Symbols = len(s.SymbolsAt(repo, ref))
			snap := s.Snapshot(repo, ref)
			for _, callees := range snap.Callees {
				rs.Edges += len(callees)
			}
			rs.Files = len(s.Files(repo, ref))
			if man, ok := s.Manifest(repo, ref); ok {
				rs.Commit = man.Commit
			}
			ov.Refs = append(ov.Refs, rs)
		}
		out = append(out, ov)
	}
	return out
}
