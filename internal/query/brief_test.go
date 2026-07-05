package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// rec builds a symbol record with name-resolved callee edges.
func rec(sym, beh string, callees ...string) storage.SymbolRecord {
	cs := make([]query.Callee, len(callees))
	for i, c := range callees {
		cs[i] = query.Callee{Name: c, ResolutionMethod: "name-resolved", Confidence: 0.9}
	}
	return storage.SymbolRecord{
		SymbolHash: content.Hash(sym), SignatureHash: content.Hash("sig-" + sym),
		BehaviorHash: content.Hash(beh), BehaviorConf: 0.9, DirectConf: 0.9, Callees: cs,
	}
}

const briefSrc = "package billing\n" + // 1
	"\n" + // 2
	"func Charge() error {\n" + // 3
	"\treturn validateCard()\n" + // 4
	"}\n" + // 5
	"\n" + // 6
	"func validateCard() error {\n" + // 7
	"\treturn nil\n" + // 8
	"}\n" + // 9
	"\n" + // 10
	"func Pay() error {\n" + // 11
	"\treturn Charge()\n" + // 12
	"}\n" + // 13
	"\n" + // 14
	"func TestCharge(t *T) {\n" + // 15
	"\tCharge()\n" + // 16
	"}\n" // 17

func briefStore() *storage.Mem {
	m := storage.NewMem()
	m.Put("billing", "HEAD", "billing.Charge", rec("charge", "behHEAD", "billing.validateCard"))
	m.Put("billing", "HEAD", "billing.validateCard", rec("vc", "vc"))
	m.Put("billing", "HEAD", "billing.Pay", rec("pay", "pay", "billing.Charge"))
	m.Put("billing", "HEAD", "billing.TestCharge", rec("tc", "tc", "billing.Charge"))
	// A second ref so the exported target gets a compat verdict (behavior differs).
	m.Put("billing", "prod", "billing.Charge", rec("charge", "behPROD", "billing.validateCard"))
	m.PutFile("billing", "HEAD", query.File{
		Path:    "billing/charge.go",
		Content: briefSrc,
		Symbols: []query.SymbolSpan{
			{Name: "Charge", StartLine: 3, EndLine: 5},
			{Name: "validateCard", StartLine: 7, EndLine: 9},
			{Name: "Pay", StartLine: 11, EndLine: 13},
			{Name: "TestCharge", StartLine: 15, EndLine: 17},
		},
	})
	return m
}

func TestBriefAssembly(t *testing.T) {
	b := query.Brief(briefStore(), "billing", "HEAD", "Charge", 0, nil)

	if b.Symbol != "billing.Charge" {
		t.Fatalf("symbol resolved to %q", b.Symbol)
	}
	if b.Target.Name != "Charge" || !b.Target.Exported || b.Target.Path != "billing/charge.go" {
		t.Fatalf("target = %+v", b.Target)
	}
	if !strings.Contains(b.Target.Body, "validateCard()") {
		t.Fatalf("target body missing its source: %q", b.Target.Body)
	}
	// Callee: validateCard, with a handle to fetch it and edge provenance.
	if len(b.Callees) != 1 || b.Callees[0].Name != "validateCard" || b.Callees[0].Handle != "billing.validateCard" {
		t.Fatalf("callees = %+v", b.Callees)
	}
	if b.Callees[0].ResolutionMethod != "name-resolved" || b.Callees[0].Preview == "" {
		t.Fatalf("callee edge/preview missing: %+v", b.Callees[0])
	}
	// Caller: Pay (blast radius); the test caller is routed to Tests, not Callers.
	if len(b.Callers) != 1 || b.Callers[0].Name != "Pay" {
		t.Fatalf("callers = %+v", b.Callers)
	}
	if len(b.Tests) != 1 || b.Tests[0] != "billing.TestCharge" {
		t.Fatalf("covering tests = %+v", b.Tests)
	}
	// Compat snapshot for the exported target: prod behavior differs.
	found := false
	for _, c := range b.Compat {
		if c.Ref == "prod" && c.Verdict == string(query.BehaviorChanged) {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected prod behavior_changed compat, got %+v", b.Compat)
	}
	// No intent provider wired -> recorded as omitted, not silently absent.
	if !contains(b.Omitted, "intent(no provider)") {
		t.Fatalf("omitted = %v", b.Omitted)
	}
}

func TestBriefTokenBudgetTruncates(t *testing.T) {
	b := query.Brief(briefStore(), "billing", "HEAD", "Charge", 3, nil)
	if b.Target.Body == "" {
		t.Fatal("target body should still be present (truncated), not dropped")
	}
	// A tiny budget must drop lower-priority sections and say so.
	if !contains(b.Omitted, "callees(budget)") {
		t.Fatalf("tiny budget must omit callees; omitted = %v", b.Omitted)
	}
}

func TestBriefUnknownSymbol(t *testing.T) {
	b := query.Brief(briefStore(), "billing", "HEAD", "Ghost", 0, nil)
	if len(b.Meta.Warnings) == 0 {
		t.Fatal("unknown symbol should warn, not panic")
	}
}

func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
