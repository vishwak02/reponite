package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Investigate turns a natural-language question into one dossier: the relevant
// symbol, a body preview, and its connections (used-by / uses), cited.
func TestInvestigate(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "billing/card.go",
		Content: "package billing\nfunc validateCardNumber(n string) bool {\n\treturn luhn(n)\n}\n",
		Symbols: []query.SymbolSpan{{Name: "validateCardNumber", StartLine: 2, EndLine: 4}},
	})
	m.Put("r", "HEAD", "billing.validateCardNumber", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "billing.luhn", ResolutionMethod: "name-resolved", Confidence: 0.9}},
	})
	m.Put("r", "HEAD", "billing.Charge", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "billing.validateCardNumber", ResolutionMethod: "name-resolved", Confidence: 0.9}},
	})

	res := query.Investigate(m, "r", "HEAD", "validate a credit card number", 4000)
	if len(res.Findings) == 0 {
		t.Fatal("investigate returned no findings")
	}
	top := res.Findings[0]
	if !strings.Contains(top.Symbol, "validateCardNumber") {
		t.Fatalf("top finding should be validateCardNumber, got %q", top.Symbol)
	}
	if !hasStr(top.Callers, "Charge") {
		t.Errorf("finding should show it is used by Charge; callers=%v", top.Callers)
	}
	if !hasStr(top.Callees, "luhn") {
		t.Errorf("finding should show it uses luhn; callees=%v", top.Callees)
	}
	// The dossier is the primary agent payload: cited + drill-in ready.
	for _, want := range []string{"validateCardNumber", "billing/card.go", "used by", "Charge"} {
		if !strings.Contains(res.Dossier, want) {
			t.Errorf("dossier missing %q:\n%s", want, res.Dossier)
		}
	}
}

// A question with no match returns an empty, non-crashing result with guidance.
func TestInvestigateNoMatch(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "r.Foo", storage.SymbolRecord{})
	res := query.Investigate(m, "r", "HEAD", "quantum teleportation subsystem", 0)
	if len(res.Findings) != 0 {
		t.Fatalf("expected no findings, got %+v", res.Findings)
	}
	if res.Dossier == "" {
		t.Fatal("dossier should still carry a guidance message on no match")
	}
}
