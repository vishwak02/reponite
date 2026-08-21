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
	if s := find(syms, "Save"); s == nil || s.Kind != "method" || s.Recv != "Store" {
		t.Fatalf("Save missing/kind/recv (want recv=Store): %+v", s)
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

// P0 regression (pure mirror of the real-grammar test): an in-class C++ method
// definition names via field_identifier inside its declarator; when the
// declarator yields no name the callable is anonymous — a name is NEVER
// invented from a parameter type or body identifier.
func TestDeclaratorNameStopsAtParameters(t *testing.T) {
	names := func(syms []Symbol) []string {
		var out []string
		for _, s := range syms {
			out = append(out, s.Kind+":"+s.Name)
		}
		return out
	}
	method := func(nameNode *fakeNode) *fakeNode {
		return comp("function_definition",
			leaf("primitive_type", "bool"),
			comp("function_declarator",
				nameNode,
				comp("parameter_list", tok("("),
					comp("parameter_declaration",
						comp("qualified_identifier", leaf("namespace_identifier", "ros"), leaf("type_identifier", "NodeHandle")),
						leaf("identifier", "nh")),
					tok(")"))),
			comp("compound_statement", tok("{"),
				comp("assignment_expression", leaf("identifier", "scan_pub_"), call("advertise")),
				tok("}")))
	}
	root := comp("translation_unit",
		comp("class_specifier", tok("class"), leaf("type_identifier", "Lidar"),
			comp("field_declaration_list", method(leaf("field_identifier", "init")))))
	syms := Extract(root, CppRules, 1)
	init := find(syms, "init")
	if init == nil {
		t.Fatalf("in-class method must be named by its field_identifier; got %v", names(syms))
	}
	if init.Recv != "Lidar" {
		t.Errorf("in-class method must qualify by its class, got recv %q", init.Recv)
	}
	if s := find(syms, "NodeHandle"); s != nil {
		t.Errorf("a parameter TYPE must never become a symbol name: %+v", *s)
	}
	if s := find(syms, "scan_pub_"); s != nil {
		t.Errorf("a body identifier must never become a symbol name: %+v", *s)
	}

	// Declarator present but nameless -> anonymous -> no symbol (never invented).
	root = comp("translation_unit",
		comp("class_specifier", tok("class"), leaf("type_identifier", "Lidar"),
			comp("field_declaration_list", method(tok("(")))))
	for _, s := range Extract(root, CppRules, 1) {
		if s.Kind != "type" {
			t.Errorf("anonymous callable must be dropped, got %+v", s)
		}
	}
}

// A bare type reference (`struct Foo x;`, a forward declaration) is not a
// definition: no symbol.
func TestTypeReferenceNotExtracted(t *testing.T) {
	root := comp("translation_unit",
		comp("declaration", comp("struct_specifier", tok("struct"), leaf("type_identifier", "Foo")), leaf("identifier", "x")),
		comp("struct_specifier", tok("struct"), leaf("type_identifier", "Bar"),
			comp("field_declaration_list", tok("{"), tok("}"))),
	)
	syms := Extract(root, CppRules, 1)
	if find(syms, "Foo") != nil {
		t.Errorf("type reference must not be extracted: %+v", syms)
	}
	if bar := find(syms, "Bar"); bar == nil || bar.Kind != "type" {
		t.Errorf("type definition must still be extracted: %+v", syms)
	}
}

// Shell: the callee is the `command_name` node, never a bare `word`. A shell
// command's ARGUMENTS are `word` nodes exactly like its name, so matching
// `word` would make the last argument look like the callee — this pure test
// pins that rule without needing the grammar.
func TestShellCalleeIsCommandNameNotArgument(t *testing.T) {
	// connect() { configure_ssh "$host"; exec ssh -A user@host; }
	cmd := func(name string, args ...string) *fakeNode {
		// A real tree-sitter node reports the source text it spans, so
		// command_name carries the command's text as well as its word child.
		cn := &fakeNode{typ: "command_name", text: name, kids: []content.AST{leaf("word", name)}}
		kids := []content.AST{content.AST(cn)}
		for _, a := range args {
			kids = append(kids, leaf("word", a))
		}
		return comp("command", kids...)
	}
	root := comp("program",
		comp("function_definition",
			leaf("word", "connect"),
			tok("("), tok(")"),
			comp("compound_statement", tok("{"),
				cmd("configure_ssh", "$host"),
				cmd("update_hosts"),
				cmd("echo", "done"), // a builtin: filtered
				tok("}"))))

	syms := Extract(root, ShellRules, 1)
	fn := find(syms, "connect")
	if fn == nil {
		t.Fatalf("shell function not extracted: %+v", syms)
	}
	got := map[string]bool{}
	for _, c := range fn.Callees {
		got[c] = true
	}
	if !got["configure_ssh"] || !got["update_hosts"] {
		t.Errorf("callees must be the command names: %v", fn.Callees)
	}
	if got["$host"] || got["done"] {
		t.Errorf("an argument must never be read as the callee: %v", fn.Callees)
	}
	if got["echo"] {
		t.Errorf("builtins must be filtered, else every function calls echo: %v", fn.Callees)
	}
}

// Shebang detection: a CLI entry point carries no extension, and is usually
// the most valuable file in a tree to index.
func TestRulesForShebang(t *testing.T) {
	for line, want := range map[string]string{
		"#!/bin/bash":            "shell",
		"#!/bin/sh":              "shell",
		"#!/usr/bin/env bash":    "shell",
		"#!/usr/bin/env zsh":     "shell",
		"#!/usr/bin/env python3": "python",
		"#!/usr/bin/node":        "javascript",
	} {
		r, ok := RulesForShebang(line)
		if !ok || r.Name != want {
			t.Errorf("RulesForShebang(%q) = %q/%v, want %s", line, r.Name, ok, want)
		}
	}
	for _, line := range []string{"", "# not a shebang", "package main", "#!/usr/bin/perl"} {
		if _, ok := RulesForShebang(line); ok {
			t.Errorf("RulesForShebang(%q) must not match", line)
		}
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
