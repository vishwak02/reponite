package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Usages returns each call site with its line + enclosing function, and marks
// a site `confirmed` when that function is a resolved caller in the call graph.
func TestUsages(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path: "billing/charge.go",
		Content: "package billing\n" +
			"func Charge() error {\n" +
			"\treturn validateCard(n)\n" + // line 3: a real call inside Charge
			"}\n" +
			"func validateCard(n string) bool { return true }\n", // line 5: the definition (not a usage)
		Symbols: []query.SymbolSpan{
			{Name: "Charge", StartLine: 2, EndLine: 4},
			{Name: "validateCard", StartLine: 5, EndLine: 5},
		},
	})
	// Call graph: Charge calls validateCard (resolved).
	m.Put("r", "HEAD", "billing.Charge", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "billing.validateCard", ResolutionMethod: "name-resolved", Confidence: 0.9}},
	})
	m.Put("r", "HEAD", "billing.validateCard", storage.SymbolRecord{})

	res := query.Usages(m, "r", "HEAD", "validateCard")
	if res.Total != 1 {
		t.Fatalf("want 1 usage (the call in Charge; the definition excluded), got %d: %+v", res.Total, res.Usages)
	}
	u := res.Usages[0]
	if u.Line != 3 {
		t.Errorf("usage line = %d, want 3", u.Line)
	}
	if !strings.Contains(u.Text, "validateCard(n)") {
		t.Errorf("usage text should be the calling line, got %q", u.Text)
	}
	if u.In != "Charge" {
		t.Errorf("enclosing function = %q, want Charge", u.In)
	}
	if !u.Confirmed {
		t.Error("usage inside Charge (a resolved caller) must be confirmed")
	}
}

// Usages is the ground-truth call-site list for verify_edit/blast_radius, so it
// must NOT inherit grep's default 50-match window (P0-2 regression).
func TestUsagesNotCappedByGrepDefault(t *testing.T) {
	m := storage.NewMem()
	var b strings.Builder
	b.WriteString("package p\n")
	for i := 0; i < 60; i++ {
		b.WriteString("\thotpath(x)\n") // 60 call sites > grep's default 50
	}
	m.PutFile("r", "HEAD", query.File{Path: "p/many.go", Content: b.String()})
	res := query.Usages(m, "r", "HEAD", "hotpath")
	if res.Total != 60 || len(res.Usages) != 60 {
		t.Fatalf("usages must list every call site (60), got total=%d returned=%d", res.Total, len(res.Usages))
	}
}

// A lexical hit outside any known caller comes back unconfirmed, not dropped.
func TestUsagesUnconfirmedLexical(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "notes.go",
		Content: "package x\nfunc note() {\n\t// call helper() here later\n\thelper()\n}\n",
		Symbols: []query.SymbolSpan{{Name: "note", StartLine: 2, EndLine: 5}},
	})
	// No call-graph edge recorded for note->helper (e.g. helper unresolved).
	m.Put("r", "HEAD", "x.note", storage.SymbolRecord{})
	res := query.Usages(m, "r", "HEAD", "helper")
	if res.Total == 0 {
		t.Fatal("lexical call sites should still be reported")
	}
	for _, u := range res.Usages {
		if u.Confirmed {
			t.Errorf("no call-graph edge exists, so usages must be unconfirmed: %+v", u)
		}
	}
}
