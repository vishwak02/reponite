package query

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
)

func facts(sym, sig, beh string) SymbolFacts {
	return SymbolFacts{SymbolHash: content.Hash(sym), SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(beh)}
}
func cal(names ...string) []Callee {
	cs := make([]Callee, len(names))
	for i, n := range names {
		cs[i] = Callee{Name: n, Confidence: 1.0}
	}
	return cs
}

func TestRootCauseNoChange(t *testing.T) {
	snap := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "b")}, Callees: map[string][]Callee{}}
	if RootCause("A", snap, snap).Changed {
		t.Fatal("identical behavior must report no change")
	}
}

func TestRootCauseDirectMutation(t *testing.T) {
	from := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("a0", "s", "b0")}, Callees: map[string][]Callee{}}
	to := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("a1", "s", "b1")}, Callees: map[string][]Callee{}}
	r := RootCause("A", from, to)
	if !r.Changed || len(r.Origins) != 1 || r.Origins[0].Name != "A" || r.Origins[0].Kind != KindText || r.Origins[0].Depth != 0 {
		t.Fatalf("want single depth-0 text origin A, got %+v", r)
	}
}

func TestRootCauseDeepOriginNotPropagation(t *testing.T) {
	from := RefSnapshot{
		Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A0"), "B": facts("B", "s", "B0"), "C": facts("C0", "s", "C0")},
		Callees: map[string][]Callee{"A": cal("B"), "B": cal("C")},
	}
	to := RefSnapshot{
		Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A1"), "B": facts("B", "s", "B1"), "C": facts("C1", "s", "C1")},
		Callees: map[string][]Callee{"A": cal("B"), "B": cal("C")},
	}
	r := RootCause("A", from, to)
	if len(r.Origins) != 1 || r.Origins[0].Name != "C" || r.Origins[0].Kind != KindText || r.Origins[0].Depth != 2 {
		t.Fatalf("origin must be C (text) at depth 2, with A/B as propagation; got %+v", r.Origins)
	}
}

func TestRootCauseSignatureKind(t *testing.T) {
	from := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A0"), "C": facts("C0", "sig0", "C0")}, Callees: map[string][]Callee{"A": cal("C")}}
	to := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A1"), "C": facts("C0", "sig1", "C1")}, Callees: map[string][]Callee{"A": cal("C")}}
	r := RootCause("A", from, to)
	if len(r.Origins) != 1 || r.Origins[0].Name != "C" || r.Origins[0].Kind != KindSignature {
		t.Fatalf("want C signature_changed, got %+v", r.Origins)
	}
}

func TestRootCauseEdgesKind(t *testing.T) {
	from := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A0"), "B": facts("B", "s", "Bx")}, Callees: map[string][]Callee{"A": cal("B")}}
	to := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A1"), "B": facts("B", "s", "Bx"), "D": facts("D", "s", "Dz")}, Callees: map[string][]Callee{"A": cal("B", "D")}}
	r := RootCause("A", from, to)
	if len(r.Origins) != 1 || r.Origins[0].Name != "A" || r.Origins[0].Kind != KindEdges || r.Origins[0].Depth != 0 {
		t.Fatalf("want A edges_changed at depth 0 (added callee D), got %+v", r.Origins)
	}
}

func TestRootCauseConfidenceMinAlongPath(t *testing.T) {
	from := RefSnapshot{
		Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A0"), "B": facts("B", "s", "B0"), "C": facts("C0", "s", "C0")},
		Callees: map[string][]Callee{"A": {{Name: "B", Confidence: 1.0}}, "B": {{Name: "C", Confidence: 0.5}}},
	}
	to := RefSnapshot{
		Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A1"), "B": facts("B", "s", "B1"), "C": facts("C1", "s", "C1")},
		Callees: map[string][]Callee{"A": {{Name: "B", Confidence: 1.0}}, "B": {{Name: "C", Confidence: 0.5}}},
	}
	r := RootCause("A", from, to)
	if len(r.Origins) != 1 || r.Origins[0].Name != "C" || r.Origins[0].Confidence != 0.5 {
		t.Fatalf("origin C must carry min path confidence 0.5, got %+v", r.Origins)
	}
}

func TestRootCauseExternalCallee(t *testing.T) {
	// A's behavior changed but A itself is unchanged and its only callee X is
	// external (not in the snapshot) — the origin is outside; report a note.
	from := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A0")}, Callees: map[string][]Callee{"A": cal("X")}}
	to := RefSnapshot{Symbols: map[string]SymbolFacts{"A": facts("A", "s", "A1")}, Callees: map[string][]Callee{"A": cal("X")}}
	r := RootCause("A", from, to)
	if !r.Changed || len(r.Origins) != 0 || r.Note == "" {
		t.Fatalf("want changed with no internal origin + explanatory note, got %+v", r)
	}
}
