package storage

import (
	"testing"

	"github.com/reponite/reponite/internal/content"
	"github.com/reponite/reponite/internal/query"
)

func rec(sym, sig, beh string, conf float64, callees ...string) SymbolRecord {
	cs := make([]query.Callee, len(callees))
	for i, c := range callees {
		cs[i] = query.Callee{Name: c, Confidence: 1.0}
	}
	return SymbolRecord{SymbolHash: content.Hash(sym), SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(beh), BehaviorConf: conf, Callees: cs}
}

func TestMemReposAndRefsSorted(t *testing.T) {
	m := NewMem()
	m.Put("svc", "v2", "F", rec("F", "s", "b", 1))
	m.Put("svc", "v1", "F", rec("F", "s", "b", 1))
	m.Put("app", "main", "G", rec("G", "s", "b", 1))
	if got := m.Repos(); len(got) != 2 || got[0] != "app" || got[1] != "svc" {
		t.Fatalf("repos %v", got)
	}
	if got := m.Refs("svc"); len(got) != 2 || got[0] != "v1" || got[1] != "v2" {
		t.Fatalf("refs %v", got)
	}
}

func TestMemSymbolAtPresentAndAbsent(t *testing.T) {
	m := NewMem()
	m.Put("svc", "v1", "Charge", rec("c0", "sig", "beh", 0.9))
	sr, ok := m.SymbolAt("svc", "Charge", "v1")
	if !ok || !sr.Present || sr.SignatureHash != "sig" || sr.BehaviorConf != 0.9 {
		t.Fatalf("present lookup wrong: %+v ok=%v", sr, ok)
	}
	if absent, ok := m.SymbolAt("svc", "Missing", "v1"); ok || absent.Present {
		t.Fatal("missing symbol must report absent")
	}
}

// End-to-end: the store feeds the pure Oracle. Reproduces the moat across refs.
func TestMemFeedsCompatOracle(t *testing.T) {
	m := NewMem()
	m.Put("billing", "HEAD", "Charge", rec("c1", "sig", "behNEW", 1))
	m.Put("billing", "prod", "Charge", rec("c1", "sig", "behOLD", 1)) // same shape, diff behavior
	origin, _ := m.SymbolAt("billing", "Charge", "HEAD")
	prod, _ := m.SymbolAt("billing", "Charge", "prod")
	v1, _ := m.SymbolAt("billing", "Charge", "v1") // absent
	if query.Compat(origin, prod).Verdict != query.BehaviorChanged {
		t.Fatal("prod must be behavior_changed")
	}
	if query.Compat(origin, v1).Verdict != query.Absent {
		t.Fatal("v1 must be absent")
	}
}

func TestMemFeedsDiff(t *testing.T) {
	m := NewMem()
	m.Put("r", "a", "Keep", rec("k", "s", "b", 1))
	m.Put("r", "a", "Gone", rec("g", "s", "b", 1))
	m.Put("r", "b", "Keep", rec("k", "s", "b", 1))
	m.Put("r", "b", "New", rec("n", "s", "b", 1))
	kinds := map[string]query.ChangeKind{}
	for _, c := range query.DiffRefs(m.SymbolsAt("r", "a"), m.SymbolsAt("r", "b")) {
		kinds[c.Name] = c.Kind
	}
	if kinds["Gone"] != query.ChangeRemoved || kinds["New"] != query.ChangeAdded || kinds["Keep"] != query.ChangeUnchanged {
		t.Fatalf("diff kinds via store: %+v", kinds)
	}
}

func TestMemFeedsRootCause(t *testing.T) {
	m := NewMem()
	m.Put("r", "from", "A", rec("A", "s", "A0", 1, "B"))
	m.Put("r", "from", "B", rec("B0", "s", "B0", 1))
	m.Put("r", "to", "A", rec("A", "s", "A1", 1, "B")) // A behavior changed (propagation)
	m.Put("r", "to", "B", rec("B1", "s", "B1", 1))     // B text changed (origin)
	res := query.RootCause("A", m.Snapshot("r", "from"), m.Snapshot("r", "to"))
	if !res.Changed || len(res.Origins) != 1 || res.Origins[0].Name != "B" || res.Origins[0].Kind != query.KindText {
		t.Fatalf("rootcause via store: %+v", res)
	}
}

func TestMemManifestAndFiles(t *testing.T) {
	m := NewMem()
	m.PutManifest("r", "v1", content.Manifest{Ref: "v1", Blobs: []content.Hash{"sha256:a"}})
	if man, ok := m.Manifest("r", "v1"); !ok || len(man.Blobs) != 1 {
		t.Fatalf("manifest %+v ok=%v", man, ok)
	}
	m.PutFile("r", "v1", query.File{Path: "x.go", Content: "func F(){}", Symbols: []query.SymbolSpan{{Name: "F", StartLine: 1, EndLine: 1}}})
	if fs := m.Files("r", "v1"); len(fs) != 1 || fs[0].Path != "x.go" {
		t.Fatalf("files %+v", fs)
	}
}
