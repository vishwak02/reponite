package content

import (
	"bytes"
	"testing"
)

// Broadened Go canonicalization corpus (S0.4). Reuses the fake-AST helpers from
// canon_test.go. Verifies that meaningful Go constructs are part of identity and
// that the conservative defaults hold.

func TestMethodVsFunctionDiffer(t *testing.T) {
	fn := comp("function_declaration", tok("func"), leaf("identifier", "Save"),
		comp("parameter_list", tok("("), tok(")")), comp("block"))
	md := comp("method_declaration", tok("func"),
		comp("parameter_list", tok("("),
			comp("parameter_declaration", leaf("identifier", "s"), leaf("type_identifier", "*Store")), tok(")")),
		leaf("field_identifier", "Save"), comp("parameter_list", tok("("), tok(")")), comp("block"))
	if bytes.Equal(Canon(fn, 1), Canon(md, 1)) {
		t.Fatal("a method (with receiver) and a like-named function must differ")
	}
}

func TestReceiverTypeMatters(t *testing.T) {
	mk := func(recv string) *fakeNode {
		return comp("method_declaration", tok("func"),
			comp("parameter_list", tok("("),
				comp("parameter_declaration", leaf("identifier", "s"), leaf("type_identifier", recv)), tok(")")),
			leaf("field_identifier", "Save"), comp("block"))
	}
	if bytes.Equal(Canon(mk("*Store"), 1), Canon(mk("*Cache"), 1)) {
		t.Fatal("receiver type is part of identity")
	}
}

func TestGenericsTypeParamsMatter(t *testing.T) {
	mk := func(constraint string) *fakeNode {
		return comp("function_declaration", tok("func"), leaf("identifier", "Map"),
			comp("type_parameter_list", tok("["),
				comp("type_parameter", leaf("identifier", "T"), leaf("type_identifier", constraint)), tok("]")),
			comp("block"))
	}
	if bytes.Equal(Canon(mk("any"), 1), Canon(mk("comparable"), 1)) {
		t.Fatal("a type-constraint change must change canon")
	}
}

func TestStructTagMatters(t *testing.T) {
	mk := func(tag string) *fakeNode {
		return comp("field_declaration", leaf("field_identifier", "ID"),
			leaf("type_identifier", "string"), leaf("raw_string_literal", tag))
	}
	if bytes.Equal(Canon(mk("`json:\"a\"`"), 1), Canon(mk("`json:\"b\"`"), 1)) {
		t.Fatal("a struct tag change must change canon (tags affect behavior)")
	}
}

// Conservative choice: keyed composite-literal field order is NOT normalized
// (invariant 2 — normalization is opt-in and off until proven safe).
func TestCompositeLiteralFieldOrderConservative(t *testing.T) {
	ab := comp("composite_literal", tok("{"),
		comp("keyed_element", leaf("field_identifier", "A"), tok(":"), leaf("int_literal", "1")),
		comp("keyed_element", leaf("field_identifier", "B"), tok(":"), leaf("int_literal", "2")), tok("}"))
	ba := comp("composite_literal", tok("{"),
		comp("keyed_element", leaf("field_identifier", "B"), tok(":"), leaf("int_literal", "2")),
		comp("keyed_element", leaf("field_identifier", "A"), tok(":"), leaf("int_literal", "1")), tok("}"))
	if bytes.Equal(Canon(ab, 1), Canon(ba, 1)) {
		t.Fatal("composite-literal field order is kept (conservative), so these must differ")
	}
}

func TestSliceStructureMatters(t *testing.T) {
	two := comp("slice_expression", leaf("identifier", "a"), tok("["),
		leaf("int_literal", "1"), tok(":"), leaf("int_literal", "2"), tok("]"))
	three := comp("slice_expression", leaf("identifier", "a"), tok("["),
		leaf("int_literal", "1"), tok(":"), leaf("int_literal", "2"), tok(":"), leaf("int_literal", "3"), tok("]"))
	if bytes.Equal(Canon(two, 1), Canon(three, 1)) {
		t.Fatal("a[1:2] and a[1:2:3] must differ")
	}
}

func TestVarVsConstKeywordMatters(t *testing.T) {
	v := comp("declaration", tok("var"), leaf("identifier", "x"), tok("="), leaf("int_literal", "1"))
	c := comp("declaration", tok("const"), leaf("identifier", "x"), tok("="), leaf("int_literal", "1"))
	if bytes.Equal(Canon(v, 1), Canon(c, 1)) {
		t.Fatal("var vs const keyword must change canon")
	}
}

func TestReformatInvarianceMultipleCommentPositions(t *testing.T) {
	withComments := comp("function_declaration",
		comment("// leading"), tok("func"), leaf("identifier", "F"),
		comp("block", comment("// inside"),
			comp("return_statement", tok("return"), leaf("int_literal", "1")), comment("// trailing")))
	clean := comp("function_declaration", tok("func"), leaf("identifier", "F"),
		comp("block", comp("return_statement", tok("return"), leaf("int_literal", "1"))))
	if !bytes.Equal(Canon(withComments, 1), Canon(clean, 1)) {
		t.Fatal("comments in any position must not change canon")
	}
}

func TestFileHashReformatInvariance(t *testing.T) {
	file := func(lit string, withComment bool) *fakeNode {
		body := comp("block", comp("return_statement", tok("return"), leaf("int_literal", lit)))
		if withComment {
			return comp("source_file", comp("function_declaration", comment("// doc"), leaf("identifier", "F"), body))
		}
		return comp("source_file", comp("function_declaration", leaf("identifier", "F"), body))
	}
	if FileHash(1, Canon(file("1", true), 1)) != FileHash(1, Canon(file("1", false), 1)) {
		t.Fatal("file_hash must be reformat-invariant")
	}
	if FileHash(1, Canon(file("1", false), 1)) == FileHash(1, Canon(file("2", false), 1)) {
		t.Fatal("a literal change must change file_hash")
	}
}
