package processing

import "testing"

// A callee present in the ref's indexed set resolves to a visible definition
// (name-resolved, high confidence); anything else is an opaque external leaf.
func TestResolveClassifiesAgainstIndexedSet(t *testing.T) {
	indexed := map[string]bool{"validateCard": true, "Charge": true}
	got := Resolve([]string{"validateCard", "Println", "log"}, indexed)
	if len(got) != 3 {
		t.Fatalf("want 3 resolved callees, got %d", len(got))
	}
	if got[0].Name != "validateCard" || got[0].Method != MethodResolved || got[0].Confidence != ConfResolved {
		t.Fatalf("in-repo callee must be name-resolved@%v: %+v", ConfResolved, got[0])
	}
	for _, ext := range got[1:] {
		if ext.Method != MethodExternal || ext.Confidence != ConfExternal {
			t.Fatalf("unindexed callee must be unresolved-external@%v: %+v", ConfExternal, ext)
		}
	}
}

func TestResolvePreservesOrderAndHandlesEmpty(t *testing.T) {
	if got := Resolve(nil, map[string]bool{}); len(got) != 0 {
		t.Fatalf("nil callees -> empty, got %+v", got)
	}
	all := map[string]bool{"a": true, "b": true, "c": true}
	got := Resolve([]string{"c", "a", "b"}, all)
	if got[0].Name != "c" || got[1].Name != "a" || got[2].Name != "b" {
		t.Fatalf("Resolve must preserve extractor order: %+v", got)
	}
}

// The confidence policy must be monotonic with certainty: a type-proven edge is
// the most confident, an in-repo name match less so, an external leaf least.
func TestResolveConfidenceMonotonic(t *testing.T) {
	if !(ConfTypes > ConfResolved && ConfResolved > ConfExternal) {
		t.Fatalf("confidence must be ordered types>resolved>external: %v %v %v", ConfTypes, ConfResolved, ConfExternal)
	}
}
