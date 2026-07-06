// blast.go is the pre-edit macro (§2, blueprint): before an agent changes a
// symbol it calls BlastRadius once and learns everything that could break —
// in-repo callers, cross-repo (fleet) callers, covering tests, whether the API
// contract already moved across refs — instead of gluing ximpact + context +
// compat together itself. Pure composition over the existing coordinators.
package query

import (
	"fmt"
	"sort"
)

// BlastRadiusResult is the fused impact dossier for one symbol.
type BlastRadiusResult struct {
	Symbol        string
	Repo          string
	Definitions   []XImpactDef    // where the symbol is defined across the fleet (+ signature)
	InRepoCallers []string        // direct callers in the symbol's own repo
	FleetCallers  []XImpactCaller // cross-repo callers (module-resolved + name-based)
	CoveringTests []string        // tests that reference the symbol
	Compat        []CompatVerdict // the symbol across the repo's other refs (is it moving?)
	Modules       []string        // the target's module identity (for the fleet match)
	// ContractChanged is true when the signature already differs across the refs
	// the symbol is defined at — callers on older refs may expect a stale shape.
	ContractChanged bool
	Summary         string
	Note            string
	Meta            Meta
}

// BlastRadius assembles the impact of changing symbol at repo@ref: in-repo
// callers + covering tests (from Context), fleet callers + definitions +
// contract state (from XImpact), and the compat verdict across the repo's other
// refs. Resolves symbol (bare or qualified) to a stored id first.
func BlastRadius(s Store, repo, ref, symbol string) BlastRadiusResult {
	res := BlastRadiusResult{Symbol: symbol, Repo: repo, Meta: Meta{Repo: repo, Ref: ref}}
	names := ResolveSymbol(s, repo, ref, symbol)
	if len(names) == 0 {
		res.Note = fmt.Sprintf("symbol %q not found at %s@%s", symbol, repo, ref)
		return res
	}
	qid := names[0]
	res.Symbol = qid
	if len(names) > 1 {
		res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf("ambiguous %q; used %s", symbol, qid))
	}

	// In-repo callers + covering tests (Context splits by the test-name heuristic).
	ctx := Context(s, repo, ref, qid, true)
	for _, c := range ctx.Callers {
		if IsTestName(baseName(c)) {
			res.CoveringTests = append(res.CoveringTests, c)
		} else {
			res.InRepoCallers = append(res.InRepoCallers, c)
		}
	}
	sort.Strings(res.InRepoCallers)
	sort.Strings(res.CoveringTests)

	// Cross-repo (fleet) callers + the target's definition/contract state.
	xi := XImpact(s, baseName(qid), "")
	res.FleetCallers = xi.Callers
	res.Definitions = xi.Definitions
	res.ContractChanged = xi.ContractChanged
	res.Modules = xi.Modules

	// Compat across the repo's other refs — is this symbol already moving?
	var targets []RepoRef
	for _, rf := range s.Refs(repo) {
		if rf != ref {
			targets = append(targets, RepoRef{Repo: repo, Ref: rf})
		}
	}
	if len(targets) > 0 {
		if rep, err := CompatSymbol(s, RepoRef{Repo: repo, Ref: ref}, qid, targets); err == nil {
			res.Compat = rep.Verdicts
		}
	}

	res.Summary = blastSummary(res)
	res.Note = "impact = in-repo callers + fleet callers (source call graph; RPC/HTTP invisible) + covering tests + cross-ref contract"
	return res
}

func blastSummary(r BlastRadiusResult) string {
	repos := map[string]bool{}
	for _, c := range r.FleetCallers {
		repos[c.Repo] = true
	}
	s := fmt.Sprintf("%d in-repo caller(s), %d fleet caller(s) across %d repo(s), %d covering test(s)",
		len(r.InRepoCallers), len(r.FleetCallers), len(repos), len(r.CoveringTests))
	if r.ContractChanged {
		s += "; ⚠ contract already differs across refs"
	}
	return s
}
