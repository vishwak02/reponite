package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestTokenizeIdentifiersAndSearch(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path: "billing.go",
		Content: "func validateCardNumber() bool { return luhnCheck() }\n" +
			"func renderTemplate() string { return html }\n" +
			"func parseConfig() Config { return cfg }\n",
		Symbols: []query.SymbolSpan{
			{Name: "validateCardNumber", StartLine: 1, EndLine: 1},
			{Name: "renderTemplate", StartLine: 2, EndLine: 2},
			{Name: "parseConfig", StartLine: 3, EndLine: 3},
		},
	})

	hits := query.SemanticSearch(m, "r", "HEAD", "validate a credit card", 3, nil)
	if len(hits) == 0 {
		t.Fatal("expected semantic hits")
	}
	// The card-validation function must rank first (camelCase split -> card, validate).
	if hits[0].Symbol != "validateCardNumber" {
		t.Fatalf("top hit = %q (want validateCardNumber); hits=%+v", hits[0].Symbol, hits)
	}

	// A query with no shared terms yields nothing.
	if h := query.SemanticSearch(m, "r", "HEAD", "quantum entanglement", 3, nil); len(h) != 0 {
		t.Fatalf("unrelated query should score zero, got %+v", h)
	}
}
