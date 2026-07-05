package processing

import "testing"

func TestQualifyAndBaseName(t *testing.T) {
	if got := qualify("internal/storage", "Put"); got != "internal/storage.Put" {
		t.Fatalf("qualify = %q", got)
	}
	if got := qualify("", "Charge"); got != "Charge" {
		t.Fatalf("rootless qualify must stay bare: %q", got)
	}
	if got := BaseName("internal/storage.Put"); got != "Put" {
		t.Fatalf("BaseName = %q", got)
	}
	if got := BaseName("Charge"); got != "Charge" {
		t.Fatalf("BaseName of bare = %q", got)
	}
}

func TestPkgOf(t *testing.T) {
	if got := pkgOf("internal/storage/mem.go"); got != "internal/storage" {
		t.Fatalf("pkgOf = %q", got)
	}
	if got := pkgOf("main.go"); got != "" {
		t.Fatalf("root file must have no qualifier: %q", got)
	}
}

func TestResolveEdgesScoping(t *testing.T) {
	// Two packages each define Put; content defines a unique SymbolHash.
	nodeSet := map[string]bool{
		"internal/storage.Put":        true,
		"internal/storage/sqlite.Put": true,
		"internal/content.SymbolHash": true,
		"internal/storage.Mem":        true,
	}
	byBase := map[string][]string{
		"Put":        {"internal/storage.Put", "internal/storage/sqlite.Put"},
		"SymbolHash": {"internal/content.SymbolHash"},
		"Mem":        {"internal/storage.Mem"},
	}
	got := resolveEdges("internal/storage",
		[]string{"Put", "SymbolHash", "Println"}, nodeSet, byBase, nil)

	// In-package Put resolves to the caller's own package definition.
	if got[0].Name != "internal/storage.Put" || got[0].ResolutionMethod != MethodResolved || got[0].Confidence != ConfResolved {
		t.Fatalf("in-package call must resolve to own pkg: %+v", got[0])
	}
	// Repo-wide unique base resolves precisely across packages.
	if got[1].Name != "internal/content.SymbolHash" || got[1].ResolutionMethod != MethodResolved {
		t.Fatalf("unique cross-pkg call must resolve: %+v", got[1])
	}
	// Unknown name is external.
	if got[2].ResolutionMethod != MethodExternal || got[2].Confidence != ConfExternal {
		t.Fatalf("unknown call must be external: %+v", got[2])
	}
}

func TestResolveEdgesAmbiguous(t *testing.T) {
	nodeSet := map[string]bool{"a.Put": true, "b.Put": true}
	byBase := map[string][]string{"Put": {"a.Put", "b.Put"}}
	// Caller in a third package calling Put: two candidates, no in-package match.
	got := resolveEdges("c", []string{"Put"}, nodeSet, byBase, nil)
	if got[0].ResolutionMethod != MethodAmbiguous || got[0].Confidence != ConfAmbiguous || got[0].Name != "Put" {
		t.Fatalf("cross-pkg ambiguous call must be flagged, not silently picked: %+v", got[0])
	}
}

// A type-checker-proven target overrides the name-based fallback: an otherwise
// ambiguous base name resolves precisely at full confidence.
func TestResolveEdgesPreciseWins(t *testing.T) {
	nodeSet := map[string]bool{"a.Put": true, "b.Put": true}
	byBase := map[string][]string{"Put": {"a.Put", "b.Put"}}
	precise := map[string]string{"Put": "b.Put"} // type checker pinned it to b.Put
	got := resolveEdges("c", []string{"Put"}, nodeSet, byBase, precise)
	if got[0].Name != "b.Put" || got[0].ResolutionMethod != MethodTypes || got[0].Confidence != ConfTypes {
		t.Fatalf("precise edge must win at full confidence: %+v", got[0])
	}
}

// Confidence is monotonic with certainty.
func TestConfidenceMonotonic(t *testing.T) {
	if !(ConfTypes > ConfResolved && ConfResolved > ConfExternal && ConfExternal > ConfAmbiguous) {
		t.Fatalf("want types>resolved>external>ambiguous: %v %v %v %v",
			ConfTypes, ConfResolved, ConfExternal, ConfAmbiguous)
	}
}
