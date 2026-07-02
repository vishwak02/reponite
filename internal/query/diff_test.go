package query

import "testing"

func TestDiffAddedRemoved(t *testing.T) {
	a := map[string]SymbolRef{"Old": ref(true, "s", "b", 1)}
	b := map[string]SymbolRef{"New": ref(true, "s", "b", 1)}
	got := DiffRefs(a, b) // sorted: New(added), Old(removed)
	if len(got) != 2 {
		t.Fatalf("want 2, got %d", len(got))
	}
	if got[0].Name != "New" || got[0].Kind != ChangeAdded {
		t.Fatalf("want New added, got %+v", got[0])
	}
	if got[1].Name != "Old" || got[1].Kind != ChangeRemoved {
		t.Fatalf("want Old removed, got %+v", got[1])
	}
}

func TestDiffShapeAndBehavior(t *testing.T) {
	a := map[string]SymbolRef{
		"Shape": ref(true, "sigA", "b", 1),
		"Beh":   ref(true, "sig", "behA", 1),
		"Same":  ref(true, "sig", "beh", 1),
	}
	b := map[string]SymbolRef{
		"Shape": ref(true, "sigB", "b", 1),
		"Beh":   ref(true, "sig", "behB", 0.5),
		"Same":  ref(true, "sig", "beh", 1),
	}
	kinds := map[string]ChangeKind{}
	confs := map[string]float64{}
	for _, c := range DiffRefs(a, b) {
		kinds[c.Name], confs[c.Name] = c.Kind, c.Confidence
	}
	if kinds["Shape"] != ChangeShape {
		t.Fatalf("Shape: %s", kinds["Shape"])
	}
	if kinds["Beh"] != ChangeBehavior || confs["Beh"] != 0.5 {
		t.Fatalf("Beh: %s conf %v", kinds["Beh"], confs["Beh"])
	}
	if kinds["Same"] != ChangeUnchanged {
		t.Fatalf("Same: %s", kinds["Same"])
	}
}

func TestDiffDeterministicOrder(t *testing.T) {
	a := map[string]SymbolRef{"b": ref(true, "s", "x", 1), "a": ref(true, "s", "x", 1)}
	b := map[string]SymbolRef{"a": ref(true, "s", "x", 1), "b": ref(true, "s", "x", 1)}
	got := DiffRefs(a, b)
	if got[0].Name != "a" || got[1].Name != "b" {
		t.Fatal("diff must be sorted by name")
	}
}
