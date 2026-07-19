// externalref.go defines the cross-repo dependency model: an external reference
// is a call that resolves to a symbol *outside* the caller's own repo, captured
// at index time from the caller's import bindings (§8B / §9A.2 / ADR-016). A
// symbol's identity across the repo boundary is (module_path, name) — symbol_hash
// deliberately can't match across repos (§8B.2) — so these rows are what let
// ximpact answer "who across the fleet calls this exported symbol" precisely,
// instead of by bare name alone. Pure types in the query layer, next to Callee.
package query

// ExternalRef is one caller symbol's resolved dependency on a symbol outside its
// own repo, produced at index time and stored per (repo, ref). It records the
// module the call resolves to and the exported name within it, with the method
// and confidence of that resolution (invariant 5: every edge is labeled).
type ExternalRef struct {
	From             string // caller qid in this repo/ref
	Module           string // module/package path the call resolves to (from an import binding)
	Name             string // exported symbol name within that module
	ResolutionMethod string
	Confidence       float64
	// TargetSignatureHash is the target's signature hash AS SEEN when this
	// caller was indexed — captured only when the target was resolvable in the
	// same store at index time (shared/fleet store, monorepo); "" otherwise
	// (per-caller skew then reads unknown, never guessed). §8B.3: comparing it
	// against the target's current signature answers "which callers still
	// expect the old shape".
	TargetSignatureHash string
}

// ExternalRefHit is one fleet-wide caller of an exported symbol, returned by
// Store.ExternalRefsTo — the module-resolved half of ximpact. It carries the
// owning repo/ref and the resolution provenance so the aggregate stays honest.
type ExternalRefHit struct {
	Repo             string
	Ref              string
	Caller           string
	Module           string
	Name             string
	ResolutionMethod string
	Confidence       float64
	// TargetSignatureHash: the contract this caller was indexed against ("" =
	// not captured; see ExternalRef.TargetSignatureHash).
	TargetSignatureHash string
}
