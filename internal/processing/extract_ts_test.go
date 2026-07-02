//go:build treesitter

package processing

import (
	"bytes"
	"testing"
)

// Validates the extractor's grammar-shape assumptions against real tree-sitter
// Go trees (runs in CI under -tags treesitter).
func TestExtractGoRealTree(t *testing.T) {
	src := []byte("package p\n\n// Charge bills.\nfunc Charge() error { return validateCard() }\n\nfunc validateCard() error { return nil }\n")
	root, err := ParseGo(src)
	if err != nil {
		t.Fatal(err)
	}
	syms := ExtractGo(root, 1)
	var charge *Symbol
	for i := range syms {
		if syms[i].Name == "Charge" {
			charge = &syms[i]
		}
	}
	if charge == nil {
		t.Fatalf("Charge not extracted; got %d symbols", len(syms))
	}
	if len(charge.Callees) != 1 || charge.Callees[0] != "validateCard" {
		t.Fatalf("Charge callees = %v", charge.Callees)
	}
	if !bytes.Contains(charge.Doc, []byte("Charge bills")) {
		t.Fatalf("Charge doc = %q", charge.Doc)
	}
	if len(charge.CanonBody) == 0 {
		t.Fatal("Charge canon body empty")
	}
}
