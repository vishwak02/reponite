package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestContextCallersCallees(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "A", storage.SymbolRecord{Callees: []query.Callee{{Name: "B", Confidence: 1}}})
	m.Put("r", "HEAD", "C", storage.SymbolRecord{Callees: []query.Callee{{Name: "B", Confidence: 1}}})
	m.Put("r", "HEAD", "B", storage.SymbolRecord{})
	b := query.Context(m, "r", "HEAD", "B")
	if len(b.Callers) != 2 || b.Callers[0] != "A" || b.Callers[1] != "C" {
		t.Fatalf("B callers = %v (want A,C)", b.Callers)
	}
	if len(b.Callees) != 0 {
		t.Fatalf("B has no callees, got %v", b.Callees)
	}
	a := query.Context(m, "r", "HEAD", "A")
	if len(a.Callees) != 1 || a.Callees[0] != "B" {
		t.Fatalf("A callees = %v (want B)", a.Callees)
	}
}
