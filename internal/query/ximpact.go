// ximpact.go answers "who across the fleet depends on this symbol" (architecture
// ext §8B / ADR-016): the question before changing an exported API. It reuses
// the external CALLS edges the resolver already records — a call that doesn't
// resolve inside its own repo is an unresolved-external edge (resolve.go), i.e. a
// dependency on something outside. Scanning every repo/ref in the store for
// external edges to a given name yields the cross-repo caller set with no new
// indexing. Pure over the Store, tested in-sandbox (ADR-018).
//
// Honest scope (stated in Note, per §8B.5): this is source-call-graph impact,
// name-based — RPC/HTTP/gRPC/queue calls are invisible, and matching is by
// exported name, not module path (the module-resolved precision upgrade and the
// fleet-wide global registry are the deferred "Large" half of §8B).
package query

import "sort"

// ExternalResolution is the resolution_method the resolver stamps on a call that
// does not resolve within its own repo (mirrors processing.MethodExternal). Such
// edges are exactly the cross-repo dependency signal ximpact consumes.
const ExternalResolution = "unresolved-external"

// XImpactCaller is one caller (in some repo/ref) of an external symbol.
type XImpactCaller struct {
	Repo       string
	Ref        string
	Caller     string
	Confidence float64
}

// XImpactDef is one definition site of the target symbol in the store, with its
// signature hash at that ref — the "what is the current contract" half of the
// deploy-safety picture (§8B.3).
type XImpactDef struct {
	Repo          string
	Ref           string
	Symbol        string
	SignatureHash string
}

// XImpactResult is the fleet caller set for a target symbol name, fused with the
// target's own definition/contract state.
type XImpactResult struct {
	Target string
	// Callers depend on the target (external edges), grouped by repo/ref.
	Callers []XImpactCaller
	// Definitions are where the target is itself defined+indexed in the store.
	Definitions []XImpactDef
	// ContractChanged is true when the target's signature differs across the refs
	// it is defined at — i.e. the API shape moved, so callers pinned to older refs
	// may expect a stale contract (the deploy-safety signal, §8B.3).
	ContractChanged bool
	Note            string
	Meta            Meta
}

// XImpact finds every symbol, across all repos/refs in the store, that has an
// unresolved-external CALLS edge to target (matched by bare name). ref, if
// non-empty, restricts each repo to that ref; otherwise every indexed ref is
// scanned. Results are sorted (repo, ref, caller) for determinism.
func XImpact(s Store, target, ref string) XImpactResult {
	res := XImpactResult{Target: target}
	sigs := map[string]bool{} // distinct signatures across the target's definition sites
	for _, repo := range s.Repos() {
		refs := s.Refs(repo)
		if ref != "" {
			refs = []string{ref}
		}
		for _, rf := range refs {
			snap := s.Snapshot(repo, rf)
			for caller, callees := range snap.Callees {
				for _, c := range callees {
					if c.ResolutionMethod == ExternalResolution && baseName(c.Name) == target {
						res.Callers = append(res.Callers, XImpactCaller{Repo: repo, Ref: rf, Caller: caller, Confidence: c.Confidence})
						break
					}
				}
			}
			// Definition sites: symbols actually defined here whose bare name is the
			// target, plus their signature hash (for the contract-change signal).
			for name, facts := range snap.Symbols {
				if baseName(name) == target {
					res.Definitions = append(res.Definitions, XImpactDef{Repo: repo, Ref: rf, Symbol: name, SignatureHash: string(facts.SignatureHash)})
					sigs[string(facts.SignatureHash)] = true
				}
			}
		}
	}
	res.ContractChanged = len(sigs) > 1
	sort.Slice(res.Definitions, func(i, j int) bool {
		a, b := res.Definitions[i], res.Definitions[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Symbol < b.Symbol
	})
	sort.Slice(res.Callers, func(i, j int) bool {
		a, b := res.Callers[i], res.Callers[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Caller < b.Caller
	})
	res.Note = "source-call-graph impact via unresolved-external edges (name-based; RPC/HTTP invisible; module-path resolution + fleet registry deferred, §8B)"
	res.Meta = Meta{Repo: "", Ref: ref}
	return res
}
