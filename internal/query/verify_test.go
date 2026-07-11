package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// VerifyEdit flags a signature change to a symbol that has a confirmed caller as
// unsafe, and lists the exact call site that breaks; a body-only change (same
// signature hash) is safe.
func TestVerifyEdit(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "svc/api.go",
		Content: "package svc\nfunc GetUser(id int) string { return \"\" }\n",
		Symbols: []query.SymbolSpan{{Name: "GetUser", StartLine: 2, EndLine: 2}},
	})
	// A caller in another file, wired in the call graph (confirmed).
	m.PutFile("r", "HEAD", query.File{
		Path:    "web/handler.go",
		Content: "package web\nfunc Handle() { svc.GetUser(1) }\n",
		Symbols: []query.SymbolSpan{{Name: "Handle", StartLine: 2, EndLine: 2}},
	})
	m.Put("r", "HEAD", "web.Handle", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "svc.GetUser", ResolutionMethod: "name-resolved", Confidence: 0.9}},
	})
	m.Put("r", "HEAD", "svc.GetUser", storage.SymbolRecord{})

	old := []query.EditedSymbol{{Name: "GetUser", SignatureHash: content.Hash("sigA")}}

	// Signature change → the caller's call site breaks → unsafe.
	changed := []query.EditedSymbol{{Name: "GetUser", SignatureHash: content.Hash("sigB")}}
	res := query.VerifyEdit(m, "r", "HEAD", "svc/api.go", old, changed)
	if res.Safe {
		t.Fatal("a signature change with a confirmed caller must be unsafe")
	}
	if len(res.Changed) != 1 || res.Changed[0] != "GetUser" {
		t.Fatalf("changed = %v", res.Changed)
	}
	if len(res.Impacts) != 1 || len(res.Impacts[0].Breaks) == 0 {
		t.Fatalf("expected the breaking call site in impacts, got %+v", res.Impacts)
	}
	if res.Impacts[0].Breaks[0].In != "Handle" {
		t.Fatalf("break should be inside Handle, got %+v", res.Impacts[0].Breaks[0])
	}

	// Body-only change (same signature hash) → safe.
	sameSig := []query.EditedSymbol{{Name: "GetUser", SignatureHash: content.Hash("sigA")}}
	if !query.VerifyEdit(m, "r", "HEAD", "svc/api.go", old, sameSig).Safe {
		t.Fatal("a body-only change (identical signature) must be safe")
	}

	// Adding a new symbol is safe; removing an uncalled one is safe.
	added := []query.EditedSymbol{{Name: "GetUser", SignatureHash: content.Hash("sigA")}, {Name: "GetUserV2", SignatureHash: content.Hash("v2")}}
	ar := query.VerifyEdit(m, "r", "HEAD", "svc/api.go", old, added)
	if !ar.Safe || len(ar.Added) != 1 || ar.Added[0] != "GetUserV2" {
		t.Fatalf("adding a symbol should be safe with added=[GetUserV2], got safe=%v added=%v", ar.Safe, ar.Added)
	}
}
