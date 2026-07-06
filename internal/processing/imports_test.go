package processing

import (
	"reflect"
	"testing"
)

// resolveExternalRefs is the cross-repo resolution policy — the correctness core
// of external_refs. It's tested directly against import maps (no AST needed).
func TestResolveExternalRefs(t *testing.T) {
	imports := []ImportBinding{
		{Local: "bar", Module: "github.com/x/bar"},                  // whole-module (Go/JS namespace)
		{Local: "np", Module: "numpy"},                              // whole-module (Python alias)
		{Local: "baz", Module: "foo.bar", Symbol: "baz"},            // from-import
		{Local: "q", Module: "foo.bar", Symbol: "qux"},              // aliased from-import (real name qux)
		{Local: "Bar", Module: "pkg.a", Symbol: "Bar"},              // imported symbol (class) used as receiver
		{Local: "helper", Module: "com.foo.Util", Symbol: "helper"}, // Java static import
	}
	byLocal := importsByLocal(imports)

	qcalls := []QualifiedCall{
		{Qualifier: "bar", Name: "Do"},     // -> github.com/x/bar . Do
		{Qualifier: "np", Name: "array"},   // -> numpy . array
		{Name: "baz"},                      // unqualified from-import -> foo.bar . baz
		{Name: "q"},                        // aliased from-import -> foo.bar . qux (un-aliased)
		{Qualifier: "Bar", Name: "method"}, // imported class as receiver -> pkg.a . Bar
		{Name: "helper"},                   // static import -> com.foo.Util . helper
		{Qualifier: "self", Name: "x"},     // receiver not an import -> dropped
		{Name: "localFunc"},                // not imported -> dropped
	}

	got := resolveExternalRefs("caller", qcalls, byLocal)
	want := []ExternalRefKey{
		{"github.com/x/bar", "Do"},
		{"numpy", "array"},
		{"foo.bar", "baz"},
		{"foo.bar", "qux"},
		{"pkg.a", "Bar"},
		{"com.foo.Util", "helper"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d external refs, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].Module != w.Module || got[i].Name != w.Name {
			t.Errorf("ref[%d] = (%q,%q); want (%q,%q)", i, got[i].Module, got[i].Name, w.Module, w.Name)
		}
		if got[i].From != "caller" || got[i].ResolutionMethod != MethodImport || got[i].Confidence != ConfImport {
			t.Errorf("ref[%d] provenance = %+v; want from=caller import-resolved@%v", i, got[i], ConfImport)
		}
	}
}

type ExternalRefKey struct{ Module, Name string }

// Duplicate calls to the same external symbol collapse to one external ref.
func TestResolveExternalRefsDedup(t *testing.T) {
	byLocal := importsByLocal([]ImportBinding{{Local: "bar", Module: "m/bar"}})
	got := resolveExternalRefs("c", []QualifiedCall{
		{Qualifier: "bar", Name: "Do"},
		{Qualifier: "bar", Name: "Do"},
		{Qualifier: "bar", Name: "Other"},
	}, byLocal)
	if len(got) != 2 {
		t.Fatalf("want 2 deduped refs (Do, Other), got %+v", got)
	}
}

// Imports walks a (fake) Go import_declaration: aliased, plain, and blank/dot
// imports. Blank ("_") and dot (".") imports bind no usable call-site name and
// are dropped; a plain import's local name is its path's last segment.
func TestGoImportsFakeAST(t *testing.T) {
	root := comp("source_file",
		comp("import_declaration", tok("import"),
			comp("import_spec_list", tok("("),
				comp("import_spec", leaf("interpreted_string_literal", `"fmt"`)),
				comp("import_spec", leaf("package_identifier", "bz"), leaf("interpreted_string_literal", `"github.com/x/baz"`)),
				comp("import_spec", leaf("blank_identifier", "_"), leaf("interpreted_string_literal", `"github.com/x/blank"`)),
				comp("import_spec", comp("dot", tok(".")), leaf("interpreted_string_literal", `"github.com/x/dot"`)),
				comp("import_spec", leaf("interpreted_string_literal", `"github.com/x/bar/v2"`)),
				tok(")"),
			),
		),
	)
	got := Imports(root, GoRules)
	want := []ImportBinding{
		{Local: "fmt", Module: "fmt"},
		{Local: "bz", Module: "github.com/x/baz"},
		{Local: "bar", Module: "github.com/x/bar/v2"}, // /v2 suffix skipped -> "bar"
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Go imports = %+v; want %+v", got, want)
	}
}

// Python from-import via fake AST: the real (un-aliased) symbol name is captured
// as Symbol, and the module is the from-target; relative imports are dropped.
func TestPyImportsFakeAST(t *testing.T) {
	// A dotted_name leaf's Text() is its full source span ("foo.bar"), as real
	// tree-sitter returns for any node's Content.
	root := comp("module",
		comp("import_statement", tok("import"),
			comp("aliased_import", leaf("dotted_name", "numpy"), tok("as"), leaf("identifier", "np"))),
		comp("import_from_statement", tok("from"),
			leaf("dotted_name", "foo"), tok("import"),
			leaf("dotted_name", "baz"), tok(","),
			comp("aliased_import", leaf("dotted_name", "qux"), tok("as"), leaf("identifier", "q"))),
		comp("import_from_statement", tok("from"),
			comp("relative_import", comp("import_prefix", tok("."))), tok("import"),
			leaf("dotted_name", "sibling")),
	)
	got := Imports(root, PythonRules)
	want := []ImportBinding{
		{Local: "np", Module: "numpy"},
		{Local: "baz", Module: "foo", Symbol: "baz"},
		{Local: "q", Module: "foo", Symbol: "qux"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Python imports = %+v; want %+v", got, want)
	}
}
