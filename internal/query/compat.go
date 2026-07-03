// compat.go implements the Compatibility Oracle's verdict logic (architecture
// §8): given a symbol's identity at an origin ref and at a target ref, decide
// absent / shape-changed / behavior-changed / compatible. It is a pure
// comparison over ref_history snapshots (§9.4) — no re-analysis — so it is
// unit-tested in-sandbox against fixtures (ADR-018). Confidence follows the
// evidence (invariant 5): a behavior verdict inherits the minimum behavior_conf
// of the two snapshots and is never asserted as more certain than computed.
package query

import "github.com/vishwak02/reponite/internal/content"

// Verdict is one of the three-tier compatibility outcomes (architecture §8.1).
type Verdict string

const (
	Absent          Verdict = "absent"
	ShapeChanged    Verdict = "shape_changed"
	BehaviorChanged Verdict = "behavior_changed"
	Compatible      Verdict = "compatible"
)

// SymbolRef is a symbol's identity at one ref (mirrors ref_history, §9.4).
type SymbolRef struct {
	Present       bool
	SignatureHash content.Hash
	BehaviorHash  content.Hash
	BehaviorConf  float64 // transitive-subgraph minimum
	DirectConf    float64 // this symbol's own direct edges only
}

// CompatResult is the verdict for one target ref. Confidence is the honest
// transitive floor (behavior_conf minimum); DirectConfidence reports the
// confidence of the symbols' own direct edges, which is often higher — a change
// may be well-resolved directly even when a deep stdlib call caps the floor.
type CompatResult struct {
	Verdict          Verdict
	Confidence       float64
	DirectConfidence float64
	Detail           string
}

// Compat compares an origin snapshot against a target snapshot (§8.1). The
// origin is assumed present (it is the symbol being asked about). The tiers are
// evaluated in strict precedence: absent, then shape, then behavior.
func Compat(origin, target SymbolRef) CompatResult {
	if !target.Present {
		return CompatResult{Verdict: Absent, Confidence: 1.0}
	}
	if origin.SignatureHash != target.SignatureHash {
		return CompatResult{Verdict: ShapeChanged, Confidence: 1.0, Detail: "signature differs"}
	}
	if origin.BehaviorHash != target.BehaviorHash {
		return CompatResult{
			Verdict:          BehaviorChanged,
			Confidence:       minConf(origin.BehaviorConf, target.BehaviorConf),
			DirectConfidence: minConf(origin.DirectConf, target.DirectConf),
			Detail:           "identical signature; resolved call graph differs",
		}
	}
	return CompatResult{Verdict: Compatible, Confidence: 1.0}
}

// Target is a repo/ref snapshot to compare against.
type Target struct {
	Repo, Ref string
	Snapshot  SymbolRef
}

// CompatVerdict is a per-target verdict (architecture §8.3).
type CompatVerdict struct {
	Repo, Ref string
	CompatResult
}

// CompatAcross runs the Oracle over every target ref/repo, preserving order.
func CompatAcross(origin SymbolRef, targets []Target) []CompatVerdict {
	out := make([]CompatVerdict, 0, len(targets))
	for _, t := range targets {
		out = append(out, CompatVerdict{Repo: t.Repo, Ref: t.Ref, CompatResult: Compat(origin, t.Snapshot)})
	}
	return out
}

func minConf(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
