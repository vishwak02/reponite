package content

import (
	"bytes"
	"testing"
)

// --- in-memory fake AST (stands in for the tree-sitter adapter) ---
type fakeNode struct {
	typ, text string
	named     bool
	kids      []AST
}

func (f *fakeNode) Type() string    { return f.typ }
func (f *fakeNode) Text() string    { return f.text }
func (f *fakeNode) Children() []AST { return f.kids }
func (f *fakeNode) IsNamed() bool   { return f.named }

func leaf(typ, text string) *fakeNode        { return &fakeNode{typ: typ, text: text, named: true} }
func tok(text string) *fakeNode              { return &fakeNode{typ: text, text: text} } // anon op/punct
func comp(typ string, kids ...AST) *fakeNode { return &fakeNode{typ: typ, named: true, kids: kids} }
func comment(text string) *fakeNode          { return &fakeNode{typ: "comment", text: text} }

func TestCanonGoldenFormat(t *testing.T) {
	tree := comp("binary_expression", leaf("identifier", "a"), tok("<"), leaf("identifier", "b"))
	got := string(Canon(tree, 1))
	want := "binary_expression(a\x1f<\x1fb)"
	if got != want {
		t.Fatalf("canon format changed:\n got=%q\nwant=%q", got, want)
	}
}

func TestReformatInvarianceCommentsDropped(t *testing.T) {
	withComment := comp("block",
		comment("// validate first"),
		comp("return_statement", tok("return"), leaf("identifier", "ok")),
	)
	without := comp("block",
		comp("return_statement", tok("return"), leaf("identifier", "ok")),
	)
	if !bytes.Equal(Canon(withComment, 1), Canon(without, 1)) {
		t.Fatal("a comment-only difference must not change canon (reformat-invariance)")
	}
}

func TestRenameChangesCanon(t *testing.T) {
	a := comp("call_expression", leaf("identifier", "min"), tok("("), tok(")"))
	b := comp("call_expression", leaf("identifier", "max"), tok("("), tok(")"))
	if bytes.Equal(Canon(a, 1), Canon(b, 1)) {
		t.Fatal("renaming an identifier must change canon")
	}
}

func TestOperatorSensitivity(t *testing.T) {
	lt := comp("binary_expression", leaf("identifier", "a"), tok("<"), leaf("identifier", "b"))
	le := comp("binary_expression", leaf("identifier", "a"), tok("<="), leaf("identifier", "b"))
	if bytes.Equal(Canon(lt, 1), Canon(le, 1)) {
		t.Fatal("'<' vs '<=' must change canon")
	}
}

func TestLiteralSensitivity(t *testing.T) {
	a := comp("assignment", leaf("identifier", "timeout"), tok("="), leaf("int_literal", "30"))
	b := comp("assignment", leaf("identifier", "timeout"), tok("="), leaf("int_literal", "300"))
	if bytes.Equal(Canon(a, 1), Canon(b, 1)) {
		t.Fatal("changing a literal (30 vs 300) must change canon")
	}
}

func TestImportOrderInvariance(t *testing.T) {
	specA := comp("import_spec_list",
		comp("import_spec", leaf("interpreted_string_literal", `"fmt"`)),
		comp("import_spec", leaf("interpreted_string_literal", `"os"`)),
	)
	specB := comp("import_spec_list",
		comp("import_spec", leaf("interpreted_string_literal", `"os"`)),
		comp("import_spec", leaf("interpreted_string_literal", `"fmt"`)),
	)
	if !bytes.Equal(Canon(specA, 1), Canon(specB, 1)) {
		t.Fatal("import reordering must not change canon")
	}
}

func TestStructureMatters(t *testing.T) {
	// a + b * c  vs  (a + b) * c : same tokens, different structure.
	abc := comp("binary_expression",
		leaf("identifier", "a"), tok("+"),
		comp("binary_expression", leaf("identifier", "b"), tok("*"), leaf("identifier", "c")),
	)
	grouped := comp("binary_expression",
		comp("binary_expression", leaf("identifier", "a"), tok("+"), leaf("identifier", "b")),
		tok("*"), leaf("identifier", "c"),
	)
	if bytes.Equal(Canon(abc, 1), Canon(grouped, 1)) {
		t.Fatal("different structure with the same tokens must change canon")
	}
}

func TestDocTextSeparateFromCanon(t *testing.T) {
	tree := comp("function_declaration",
		comment("// Charge bills the card."),
		leaf("identifier", "Charge"),
	)
	noDoc := comp("function_declaration", leaf("identifier", "Charge"))
	if !bytes.Equal(Canon(tree, 1), Canon(noDoc, 1)) {
		t.Fatal("doc comment must be excluded from canon")
	}
	if !bytes.Contains(DocText(tree), []byte("Charge bills the card")) {
		t.Fatal("DocText must capture the comment text")
	}
}

func TestCanonDeterministic(t *testing.T) {
	tree := comp("block", comp("return_statement", tok("return"), leaf("identifier", "x")))
	if !bytes.Equal(Canon(tree, 1), Canon(tree, 1)) {
		t.Fatal("canon must be deterministic")
	}
}

// canon drives symbol identity: same canon => same symbol_hash; body change => different;
// comment-only edit => stable. This is the moat's identity guarantee end to end.
func TestCanonDrivesSymbolHash(t *testing.T) {
	body := func(callee string, withComment bool) *fakeNode {
		call := comp("call_expression", leaf("identifier", callee), tok("("), tok(")"))
		ret := comp("return_statement", tok("return"), call)
		if withComment {
			return comp("block", comment("// note"), ret)
		}
		return comp("block", ret)
	}
	id := func(b *fakeNode) SymbolIdentity {
		return SymbolIdentity{Repo: "pay", Lang: "go", Kind: "function", Signature: "Charge(c Card)", CanonBody: Canon(b, 1)}
	}
	if SymbolHash(1, id(body("validateCard", false))) == SymbolHash(1, id(body("validateCardV2", false))) {
		t.Fatal("a real body change must change symbol_hash")
	}
	if SymbolHash(1, id(body("validateCard", false))) != SymbolHash(1, id(body("validateCard", true))) {
		t.Fatal("a comment-only edit must not change symbol_hash")
	}
}
