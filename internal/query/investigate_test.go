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

// The ranker scores one entry per symbol SPAN, so a symbol with two spans (a
// C++ class in a header plus its definition) produced two hits that resolved to
// the same qualified id — the dossier ranked the same symbol twice and claimed
// it had found more than it had.
func TestInvestigateDedupesSameSymbol(t *testing.T) {
	m := storage.NewMem()
	// Two spans, same symbol name, same file — exactly what a C++ header +
	// in-file definition produces.
	m.PutFile("r", "HEAD", query.File{
		Path:    "pgs.hpp",
		Content: "class PickerGuidingSystem {\n  void assignPickerToRobot();\n};\n",
		Symbols: []query.SymbolSpan{
			{Name: "PickerGuidingSystem", StartLine: 1, EndLine: 3},
			{Name: "PickerGuidingSystem", StartLine: 1, EndLine: 3},
		},
	})
	m.Put("r", "HEAD", "PickerGuidingSystem", storage.SymbolRecord{SignatureHash: "s"})

	res := query.Investigate(m, "r", "HEAD", "picker guiding system assign", 4000)
	seen := map[string]int{}
	for _, f := range res.Findings {
		seen[f.Repo+"/"+f.Symbol]++
	}
	for k, n := range seen {
		if n > 1 {
			t.Fatalf("symbol %q ranked %d times; findings must be deduped by (repo, qid)", k, n)
		}
	}
}

// A weak ranking must SAY it is weak. investigate is the tool an agent calls
// first on an unfamiliar repo, and it used to present incidental word overlap
// with the same confidence as a real match.
func TestInvestigateReportsWeakMatchAndCoverage(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "driver.cpp",
		Content: "void run() { spin(); }\n",
		Symbols: []query.SymbolSpan{{Name: "run", StartLine: 1, EndLine: 1}},
	})
	m.Put("r", "HEAD", "run", storage.SymbolRecord{SignatureHash: "s"})

	res := query.Investigate(m, "r", "HEAD", "run", 4000)
	if res.Considered != 1 {
		t.Fatalf("Considered must report the corpus size actually ranked, got %d", res.Considered)
	}
	// The dossier states its coverage so a reader can judge the ranking.
	if !strings.Contains(res.Dossier, "ranked from 1 indexed symbols") {
		t.Fatalf("dossier must state how many symbols it ranked:\n%s", res.Dossier)
	}
	// And a genuinely weak query is flagged, at the top.
	weak := query.Investigate(m, "r", "HEAD", "quantum cryptography billing ledger", 4000)
	if len(weak.Findings) > 0 || len(weak.Meta.Warnings) > 0 {
		if len(weak.Findings) > 0 && len(weak.Meta.Warnings) == 0 {
			t.Fatalf("a low-scoring ranking must carry a caveat, got findings=%d warnings=%v",
				len(weak.Findings), weak.Meta.Warnings)
		}
	}
}
