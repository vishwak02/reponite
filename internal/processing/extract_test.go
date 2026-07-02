package processing

import (
	"bytes"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
)

type fakeNode struct {
	typ, text string
	kids      []content.AST
}

func (f *fakeNode) Type() string            { return f.typ }
func (f *fakeNode) Text() string            { return f.text }
func (f *fakeNode) IsNamed() bool           { return true }
func (f *fakeNode) Children() []content.AST { return f.kids }

func leaf(typ, text string) *fakeNode { return &fakeNode{typ: typ, text: text} }
func tok(s string) *fakeNode          { return &fakeNode{typ: s, text: s} }
func comment(s string) *fakeNode      { return &fakeNode{typ: "comment", text: s} }
func comp(typ string, kids ...content.AST) *fakeNode {
	return &fakeNode{typ: typ, kids: kids}
}

func call(name string) *fakeNode {
	return comp("call_expression", leaf("identifier", name), comp("argument_list", tok("("), tok(")")))
}

func find(syms []Symbol, name string) *Symbol {
	for i := range syms {
		if syms[i].Name == name {
			return &syms[i]
		}
	}
	return nil
}

func TestExtractFunctionsMethodsTypes(t *testing.T) {
	root := comp("source_file",
		comp("package_clause", tok("package"), leaf("package_identifier", "billing")),
		comment("// Charge bills the card."),
		comp("function_declaration", tok("func"), leaf("identifier", "Charge"),
			comp("parameter_list", tok("("), tok(")")),
			comp("block", comp("return_statement", tok("return"), call("validateCard")))),
		comp("method_declaration", tok("func"),
			comp("parameter_list", tok("("), comp("parameter_declaration", leaf("identifier", "s"), leaf("type_identifier", "*Store")), tok(")")),
			leaf("field_identifier", "Save"),
			comp("parameter_list", tok("("), tok(")")),
			comp("block")),
		comp("type_declaration", tok("type"),
			comp("type_spec", leaf("type_identifier", "User"), comp("struct_type", tok("struct"), tok("{"), tok("}")))),
	)
	syms := ExtractGo(root, 1)
	if len(syms) != 3 {
		t.Fatalf("want 3 symbols, got %d", len(syms))
	}
	charge := find(syms, "Charge")
	if charge == nil || charge.Kind != "function" {
		t.Fatalf("Charge missing/kind: %+v", charge)
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
	if s := find(syms, "Save"); s == nil || s.Kind != "method" {
		t.Fatalf("Save missing/kind: %+v", s)
	}
	if u := find(syms, "User"); u == nil || u.Kind != "type" || len(u.CanonBody) != 0 || u.Signature == "" {
		t.Fatalf("User type wrong: %+v", u)
	}
}

func TestExtractSignatureBodyIndependent(t *testing.T) {
	mk := func(callee string) *fakeNode {
		return comp("source_file", comp("function_declaration", tok("func"), leaf("identifier", "Foo"),
			comp("parameter_list", tok("("), tok(")")),
			comp("block", comp("expression_statement", call(callee)))))
	}
	a := ExtractGo(mk("x"), 1)[0]
	b := ExtractGo(mk("y"), 1)[0]
	if a.Signature != b.Signature {
		t.Fatal("signature must be body-independent (same shape)")
	}
	if string(a.CanonBody) == string(b.CanonBody) {
		t.Fatal("body canon must differ when the body differs")
	}
	if a.Callees[0] != "x" || b.Callees[0] != "y" {
		t.Fatalf("callees a=%v b=%v", a.Callees, b.Callees)
	}
}

func TestExtractCalleesDedupAndSelector(t *testing.T) {
	body := comp("block",
		comp("expression_statement", call("validateCard")),
		comp("expression_statement", comp("call_expression",
			comp("selector_expression", leaf("identifier", "log"), tok("."), leaf("field_identifier", "Info")),
			comp("argument_list", tok("("), tok(")")))),
		comp("expression_statement", call("validateCard")), // duplicate
	)
	got := extractCallees(body)
	if len(got) != 2 || got[0] != "validateCard" || got[1] != "Info" {
		t.Fatalf("callees = %v (want validateCard, Info deduped)", got)
	}
}

func TestExtractDocResetsOnNonComment(t *testing.T) {
	root := comp("source_file",
		comment("// stray comment"),
		comp("import_declaration", tok("import"), leaf("interpreted_string_literal", `"fmt"`)),
		comp("function_declaration", tok("func"), leaf("identifier", "F"), comp("parameter_list", tok("("), tok(")")), comp("block")),
	)
	f := find(ExtractGo(root, 1), "F")
	if f == nil || f.Doc != nil {
		t.Fatalf("doc must reset after a non-comment sibling: %+v", f)
	}
}

func TestExtractCalleesFiltersBuiltins(t *testing.T) {
	body := comp("block",
		comp("expression_statement", call("append")),
		comp("expression_statement", call("len")),
		comp("expression_statement", call("int")),
		comp("expression_statement", call("validateCard")),
	)
	if got := extractCallees(body); len(got) != 1 || got[0] != "validateCard" {
		t.Fatalf("builtins/conversions must be filtered, kept: %v", got)
	}
}
