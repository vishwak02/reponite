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
	b := query.Context(m, "r", "HEAD", "B", false)
	if len(b.Callers) != 2 || b.Callers[0] != "A" || b.Callers[1] != "C" {
		t.Fatalf("B callers = %v (want A,C)", b.Callers)
	}
	if len(b.Callees) != 0 {
		t.Fatalf("B has no callees, got %v", b.Callees)
	}
	a := query.Context(m, "r", "HEAD", "A", false)
	if len(a.Callees) != 1 || a.Callees[0] != "B" {
		t.Fatalf("A callees = %v (want B)", a.Callees)
	}
}

// Test callers are hidden by default and shown with includeTests.
func TestContextExcludesTestCallers(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "A", storage.SymbolRecord{Callees: []query.Callee{{Name: "B", Confidence: 1}}})
	m.Put("r", "HEAD", "TestB", storage.SymbolRecord{Callees: []query.Callee{{Name: "B", Confidence: 1}}})
	m.Put("r", "HEAD", "B", storage.SymbolRecord{})
	if got := query.Context(m, "r", "HEAD", "B", false).Callers; len(got) != 1 || got[0] != "A" {
		t.Fatalf("default context must hide TestB caller: %v", got)
	}
	if got := query.Context(m, "r", "HEAD", "B", true).Callers; len(got) != 2 {
		t.Fatalf("includeTests must show TestB and A: %v", got)
	}
}

// An ambiguous bare symbol (several package-qualified ids share the base name)
// must be surfaced in _meta.warnings, not silently resolved to one (invariant:
// never lie; consistent with compat/rootcause).
func TestContextWarnsOnAmbiguity(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "storage.Mem.Put", storage.SymbolRecord{})
	m.Put("r", "HEAD", "storage/sqlite.Store.Put", storage.SymbolRecord{})
	c := query.Context(m, "r", "HEAD", "Put", false)
	if len(c.Meta.Warnings) == 0 {
		t.Fatal("ambiguous \"Put\" must produce a warning")
	}
	// A uniquely-named symbol produces no ambiguity warning.
	m.Put("r", "HEAD", "storage.Unique", storage.SymbolRecord{})
	if w := query.Context(m, "r", "HEAD", "Unique", false).Meta.Warnings; len(w) != 0 {
		t.Fatalf("unambiguous symbol must not warn: %v", w)
	}
}

// Callee edges carry resolution provenance, not just names (invariant 5).
func TestContextCalleeEdgesProvenance(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "A", storage.SymbolRecord{Callees: []query.Callee{
		{Name: "B", ResolutionMethod: "name-resolved", Confidence: 0.9},
		{Name: "log", ResolutionMethod: "unresolved-external", Confidence: 0.6},
	}})
	a := query.Context(m, "r", "HEAD", "A", false)
	if len(a.CalleeEdges) != 2 {
		t.Fatalf("want 2 callee edges, got %+v", a.CalleeEdges)
	}
	// sorted by name: B, log
	if e := a.CalleeEdges[0]; e.Name != "B" || e.ResolutionMethod != "name-resolved" || e.Confidence != 0.9 {
		t.Fatalf("resolved edge provenance wrong: %+v", e)
	}
	if e := a.CalleeEdges[1]; e.Name != "log" || e.ResolutionMethod != "unresolved-external" || e.Confidence != 0.6 {
		t.Fatalf("external edge provenance wrong: %+v", e)
	}
}
