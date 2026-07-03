// resolve.go is the CALLS-edge resolution policy: it classifies each heuristic
// callee name against the symbols indexed in the same ref and assigns the edge a
// resolution_method and confidence (architecture §7, invariant 5). This is the
// single place edge confidence is decided — replacing the former flat constant —
// so every edge honestly records HOW it was resolved, not just a number. Pure
// and stdlib-only, so it is unit-tested in-sandbox (ADR-018).
package processing

// Resolution methods label how a CALLS edge's target was resolved. The method is
// part of the edge's identity (invariant 5, content.EdgeHash) and drives its
// confidence: a callee we can see the definition of is more trustworthy than an
// opaque external one, and a type-checker-proven edge is certain.
const (
	// MethodResolved: the callee name matches exactly one symbol indexed in the
	// same ref, so its definition is visible and its behavior is captured.
	MethodResolved = "name-resolved"
	// MethodExternal: the callee is not in the indexed set (stdlib, third-party,
	// or a cross-repo symbol) and is treated as an opaque behavior leaf.
	MethodExternal = "unresolved-external"
	// MethodTypes: proven by the Go type checker (reserved for precise
	// resolution; assigned confidence 1.0 when that path lands).
	MethodTypes = "go-types"
)

// Confidence per resolution method (§7, invariant 5). Monotonic with certainty:
// a type-proven edge is 1.0; an in-repo name match is high but not certain
// (Go allows same-named methods on different types, so a name match can be
// wrong); an unresolved external edge is genuinely uncertain.
const (
	ConfResolved = 0.9
	ConfExternal = 0.6
	ConfTypes    = 1.0
)

// ResolvedCallee is a callee name classified by how it resolved, with the
// resulting edge confidence.
type ResolvedCallee struct {
	Name       string
	Method     string
	Confidence float64
}

// Resolve classifies each callee name against indexed, the set of symbol names
// present in the same ref: a name in the set resolves to a visible definition
// (MethodResolved); anything else is an opaque external leaf (MethodExternal).
// Order is preserved and the input is assumed already deduped by the extractor.
func Resolve(callees []string, indexed map[string]bool) []ResolvedCallee {
	out := make([]ResolvedCallee, 0, len(callees))
	for _, c := range callees {
		if indexed[c] {
			out = append(out, ResolvedCallee{Name: c, Method: MethodResolved, Confidence: ConfResolved})
		} else {
			out = append(out, ResolvedCallee{Name: c, Method: MethodExternal, Confidence: ConfExternal})
		}
	}
	return out
}
