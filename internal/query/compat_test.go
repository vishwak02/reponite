package query

import (
	"testing"

	"github.com/reponite/reponite/internal/content"
)

func ref(present bool, sig, beh string, conf float64) SymbolRef {
	return SymbolRef{Present: present, SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(beh), BehaviorConf: conf}
}

func TestCompatAbsent(t *testing.T) {
	if Compat(ref(true, "sig", "beh", 1), SymbolRef{Present: false}).Verdict != Absent {
		t.Fatal("want absent")
	}
}

func TestCompatShapeChanged(t *testing.T) {
	if Compat(ref(true, "sigA", "beh", 1), ref(true, "sigB", "beh", 1)).Verdict != ShapeChanged {
		t.Fatal("want shape_changed")
	}
}

func TestCompatBehaviorChangedTakesMinConf(t *testing.T) {
	r := Compat(ref(true, "sig", "behA", 1.0), ref(true, "sig", "behB", 0.6))
	if r.Verdict != BehaviorChanged {
		t.Fatalf("want behavior_changed, got %s", r.Verdict)
	}
	if r.Confidence != 0.6 {
		t.Fatalf("behavior verdict must carry min confidence, got %v", r.Confidence)
	}
}

func TestCompatCompatible(t *testing.T) {
	if Compat(ref(true, "sig", "beh", 1), ref(true, "sig", "beh", 1)).Verdict != Compatible {
		t.Fatal("want compatible")
	}
}

func TestCompatAbsentPrecedence(t *testing.T) {
	if Compat(ref(true, "sigA", "behA", 1), SymbolRef{Present: false, SignatureHash: "sigB"}).Verdict != Absent {
		t.Fatal("absent must take precedence over shape")
	}
}

func TestCompatAcrossFleet(t *testing.T) {
	origin := ref(true, "sig1", "beh1", 1)
	targets := []Target{
		{"user-service", "v2.1.0", SymbolRef{Present: false}},
		{"billing", "v4.0.0", ref(true, "sig2", "beh1", 1)},
		{"analytics", "prod", ref(true, "sig1", "beh2", 0.72)},
		{"gateway", "prod", ref(true, "sig1", "beh1", 1)},
	}
	got := CompatAcross(origin, targets)
	want := []Verdict{Absent, ShapeChanged, BehaviorChanged, Compatible}
	if len(got) != len(want) {
		t.Fatalf("want %d verdicts, got %d", len(want), len(got))
	}
	for i, v := range want {
		if got[i].Verdict != v {
			t.Fatalf("target %d: want %s, got %s", i, v, got[i].Verdict)
		}
	}
	if got[2].Confidence != 0.72 {
		t.Fatalf("behavior verdict confidence must propagate, got %v", got[2].Confidence)
	}
}
