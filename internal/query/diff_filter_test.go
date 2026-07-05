package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

func TestFilterChanges(t *testing.T) {
	changes := []query.SymbolChange{
		{Name: "internal/query.Foo", Kind: query.ChangeUnchanged, Confidence: 1},
		{Name: "internal/query.Bar", Kind: query.ChangeBehavior, Confidence: 0.5},
		{Name: "internal/storage.Baz", Kind: query.ChangeShape, Confidence: 1},
	}
	// changed-only drops the unchanged Foo.
	if got := query.FilterChanges(changes, query.DiffOptions{ChangedOnly: true}); len(got) != 2 {
		t.Fatalf("changed-only: %+v", got)
	}
	// package prefix keeps only internal/query.*.
	got := query.FilterChanges(changes, query.DiffOptions{Package: "internal/query"})
	if len(got) != 2 {
		t.Fatalf("package filter: %+v", got)
	}
	// confidence floor drops the 0.5 Bar.
	got = query.FilterChanges(changes, query.DiffOptions{MinConfidence: 0.8})
	for _, c := range got {
		if c.Name == "internal/query.Bar" {
			t.Fatalf("confidence-min should have dropped Bar: %+v", got)
		}
	}
	// zero options = passthrough.
	if got := query.FilterChanges(changes, query.DiffOptions{}); len(got) != 3 {
		t.Fatalf("zero opts must not filter: %+v", got)
	}
}
