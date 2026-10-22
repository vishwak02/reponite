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

// An explicitly instantiated generic function's call names the function, not
// its type argument: tree-sitter reads F[T](x) alone as a conversion to a
// generic type, and keeps a call's type arguments beside its callee.
func TestExtractGoGenericCalls(t *testing.T) {
	src := []byte(`package p

func Run(xs []int) {
	b := ring.New[Item](5)
	c := Make[Item](3)
	d := Pair[string, int]()
	sel.F[Item](x)
	Build[Item](4).Push(x)
	_ = []byte("s")
}
`)
	root, err := ParseGo(src)
	if err != nil {
		t.Fatal(err)
	}
	var run *Symbol
	syms := ExtractGo(root, 1)
	for i := range syms {
		if syms[i].Name == "Run" {
			run = &syms[i]
		}
	}
	if run == nil {
		t.Fatal("Run not extracted")
	}
	got := map[string]QualifiedCall{}
	for _, qc := range run.QualifiedCalls {
		got[qc.Name] = qc
	}
	if qc, ok := got["New"]; !ok || qc.Qualifier != "ring" || qc.Chain != 2 {
		t.Errorf("ring.New[Item](5): got %+v (all %+v)", qc, run.QualifiedCalls)
	}
	for _, n := range []string{"Make", "Pair"} {
		if qc, ok := got[n]; !ok || qc.Qualifier != "" || qc.Chain != 1 {
			t.Errorf("%s[...](): got %+v (all %+v)", n, qc, run.QualifiedCalls)
		}
	}
	if qc, ok := got["F"]; !ok || qc.Qualifier != "sel" {
		t.Errorf("sel.F[Item](x): got %+v (all %+v)", qc, run.QualifiedCalls)
	}
	if qc, ok := got["Push"]; !ok || qc.Qualifier != "Build" || !qc.Result {
		t.Errorf("Build[Item](4).Push(x): got %+v (all %+v)", qc, run.QualifiedCalls)
	}
	for _, bad := range []string{"Item", "string", "int", "byte"} {
		if _, ok := got[bad]; ok {
			t.Errorf("a type argument or conversion read as a call: %q in %+v", bad, run.QualifiedCalls)
		}
	}
}
