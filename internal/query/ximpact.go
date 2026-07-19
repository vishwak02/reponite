// ximpact.go answers "who across the fleet depends on this symbol" (architecture
// ext §8B / ADR-016): the question before changing an exported API. It fuses two
// caller signals over the Store, in decreasing precision:
//
//  1. Module-resolved (import-path precise). At index time each caller file's
//     imports resolve its qualified calls to (module_path, name) external
//     references (§9A.2). If the target is itself defined+indexed, we know its
//     repo's module_path, so we match external_refs on (module, name) — a caller
//     in another repo that imports THIS module and calls THIS name, not merely a
//     name collision. This is the fleet-registry precision half of §8B.
//  2. Name-based (fallback). The original signal: any unresolved-external CALLS
//     edge whose bare name matches the target. Kept for callers whose imports
//     weren't captured (older indexes, dynamic dispatch) and for targets with no
//     indexed definition (unknown module). Deduped against (1) by repo/ref/caller.
//
// Pure over the Store, tested in-sandbox (ADR-018). Honest scope stays in Note
// (§8B.5): source-call-graph only — RPC/HTTP/gRPC/queue calls are invisible.
package query

import "sort"

// ExternalResolution is the resolution_method the resolver stamps on a call that
// does not resolve within its own repo (mirrors processing.MethodExternal). Such
// edges are the name-based cross-repo signal (fallback tier).
const ExternalResolution = "unresolved-external"

// ImportResolution is the resolution_method stamped on an import-path-resolved
// external reference (mirrors processing.MethodImport) — the precise tier.
const ImportResolution = "import-resolved"

// XImpactCaller is one caller (in some repo/ref) of an external symbol, labeled
// with how the dependency was resolved (invariant 5: never overclaim).
type XImpactCaller struct {
	Repo             string
	Ref              string
	Caller           string
	Module           string  // resolved target module ("" for a name-only match)
	ResolutionMethod string  // import-resolved (precise) | unresolved-external (name-based)
	Confidence       float64 // module-resolved > name-based
	// ExpectedSignature is the per-caller contract-skew verdict (§8B.3):
	// "current" — the contract captured when this caller was indexed matches
	// the target's current signature; "stale" — it doesn't (this caller still
	// expects the old shape); "" — unknown (no contract captured at index
	// time; reported, never guessed).
	ExpectedSignature string
}

// Per-caller skew verdicts.
const (
	SkewCurrent = "current"
	SkewStale   = "stale"
)

// XImpactDef is one definition site of the target symbol in the store, with its
// signature hash and its repo's module path — the "what is the current contract,
// and under what module identity" half of the deploy-safety picture (§8B.3).
type XImpactDef struct {
	Repo          string
	Ref           string
	Symbol        string
	Module        string
	SignatureHash string
}

// XImpactResult is the fleet caller set for a target symbol name, fused with the
// target's own definition/contract state.
type XImpactResult struct {
	Target string
	// Modules are the distinct module paths the target is defined under (usually
	// one); module-resolved callers matched against these.
	Modules []string
	// Callers depend on the target, grouped by repo/ref, precise tier first.
	Callers []XImpactCaller
	// Definitions are where the target is itself defined+indexed in the store.
	Definitions []XImpactDef
	// ContractChanged is true when the target's signature differs across the refs
	// it is defined at — the API shape moved, so callers pinned to older refs may
	// expect a stale contract (the deploy-safety signal, §8B.3).
	ContractChanged bool
	// StaleCallers counts callers whose captured contract no longer matches the
	// target's current signature — the "3 of 4 still expect the old shape"
	// number (§8B.3). Callers with no captured contract are not counted (their
	// skew is unknown, not assumed).
	StaleCallers int
	Note         string
	Meta         Meta
}

// XImpact finds every caller of target across the store, fusing module-resolved
// external references (precise) with the name-based unresolved-external fallback.
// ref, if non-empty, restricts each repo to that ref for the name-based scan and
// the definition scan; the module-resolved scan is inherently fleet-wide.
func XImpact(s Store, target, ref string) XImpactResult {
	res := XImpactResult{Target: target}

	// --- definition sites + the target's module identity/contract state ---
	// Signatures are grouped by the fully-qualified identity (repo, qid), so
	// "contract changed" means the SAME symbol's shape moved across the refs it's
	// defined at — not that two different symbols happen to share a bare name
	// (e.g. storage.Mem.Put vs sqlite.Store.Put, or a C++ header decl vs its impl).
	sigsByID := map[[2]string]map[string]bool{}
	moduleSet := map[string]bool{}
	for _, repo := range s.Repos() {
		module := s.ModulePath(repo)
		for _, rf := range refsOf(s, repo, ref) {
			snap := s.Snapshot(repo, rf)
			for name, facts := range snap.Symbols {
				if baseName(name) == target {
					res.Definitions = append(res.Definitions, XImpactDef{
						Repo: repo, Ref: rf, Symbol: name, Module: module,
						SignatureHash: string(facts.SignatureHash),
					})
					id := [2]string{repo, name}
					if sigsByID[id] == nil {
						sigsByID[id] = map[string]bool{}
					}
					sigsByID[id][string(facts.SignatureHash)] = true
					if module != "" {
						moduleSet[module] = true
					}
				}
			}
		}
	}
	for _, set := range sigsByID {
		if len(set) > 1 { // one concrete symbol with >1 signature across refs = a real contract move
			res.ContractChanged = true
			break
		}
	}
	res.Modules = sortedSet(moduleSet)

	// The target's CURRENT contract set: per definition identity (repo, qid),
	// the signature at its preferred ref (HEAD when indexed, else the latest).
	// A caller's captured contract is compared against this for per-caller skew.
	currentSigs := currentSignatures(res.Definitions)

	// --- tier 1: module-resolved callers (precise), fleet-wide ---
	seen := map[[3]string]bool{} // (repo, ref, caller) already counted
	for _, module := range res.Modules {
		for _, h := range s.ExternalRefsTo(module, target) {
			key := [3]string{h.Repo, h.Ref, h.Caller}
			if seen[key] {
				continue
			}
			seen[key] = true
			skew := ""
			if h.TargetSignatureHash != "" && len(currentSigs) > 0 {
				if currentSigs[h.TargetSignatureHash] {
					skew = SkewCurrent
				} else {
					skew = SkewStale
					res.StaleCallers++
				}
			}
			res.Callers = append(res.Callers, XImpactCaller{
				Repo: h.Repo, Ref: h.Ref, Caller: h.Caller, Module: h.Module,
				ResolutionMethod: h.ResolutionMethod, Confidence: h.Confidence,
				ExpectedSignature: skew,
			})
		}
	}

	// --- tier 2: name-based unresolved-external callers (fallback) ---
	for _, repo := range s.Repos() {
		for _, rf := range refsOf(s, repo, ref) {
			snap := s.Snapshot(repo, rf)
			for caller, callees := range snap.Callees {
				key := [3]string{repo, rf, caller}
				if seen[key] {
					continue // already counted precisely
				}
				for _, c := range callees {
					if c.ResolutionMethod == ExternalResolution && baseName(c.Name) == target {
						seen[key] = true
						res.Callers = append(res.Callers, XImpactCaller{
							Repo: repo, Ref: rf, Caller: caller,
							ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence,
						})
						break
					}
				}
			}
		}
	}

	sortDefs(res.Definitions)
	sortCallers(res.Callers)
	res.Note = ximpactNote(len(res.Modules) > 0)
	res.Meta = Meta{Repo: "", Ref: ref}
	return res
}

// currentSignatures picks, per definition identity (repo, qid), the signature
// at that identity's preferred ref — HEAD when present, else the lexically
// newest — and returns the set. This is "the target's current contract" that a
// caller's captured contract is compared against for skew (§8B.3).
func currentSignatures(defs []XImpactDef) map[string]bool {
	type id struct{ repo, sym string }
	best := map[id]XImpactDef{}
	for _, d := range defs {
		k := id{d.Repo, d.Symbol}
		cur, ok := best[k]
		if !ok || d.Ref == "HEAD" || (cur.Ref != "HEAD" && d.Ref > cur.Ref) {
			best[k] = d
		}
	}
	out := map[string]bool{}
	for _, d := range best {
		if d.SignatureHash != "" {
			out[d.SignatureHash] = true
		}
	}
	return out
}

func refsOf(s Store, repo, ref string) []string {
	if ref != "" {
		return []string{ref}
	}
	return s.Refs(repo)
}

func sortedSet(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortDefs(defs []XImpactDef) {
	sort.Slice(defs, func(i, j int) bool {
		a, b := defs[i], defs[j]
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Symbol < b.Symbol
	})
}

// sortCallers orders callers precise-tier-first (import-resolved before
// name-based), then by (repo, ref, caller) for determinism.
func sortCallers(callers []XImpactCaller) {
	sort.Slice(callers, func(i, j int) bool {
		a, b := callers[i], callers[j]
		ap, bp := a.ResolutionMethod == ImportResolution, b.ResolutionMethod == ImportResolution
		if ap != bp {
			return ap // precise tier first
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Ref != b.Ref {
			return a.Ref < b.Ref
		}
		return a.Caller < b.Caller
	})
}

func ximpactNote(moduleResolved bool) string {
	base := "source-call-graph impact (RPC/HTTP invisible; version skew defaults to each caller's indexed ref, §8B.5)"
	if moduleResolved {
		return "module-resolved callers (import-path precise) fused with name-based fallback; " + base
	}
	return "name-based only — target module unknown (not indexed), so callers matched by bare name; " + base
}
