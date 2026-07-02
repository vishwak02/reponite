package processing

import "testing"

func TestExtractPython(t *testing.T) {
	root := comp("module",
		comp("function_definition", tok("def"), leaf("identifier", "charge"),
			comp("parameters", tok("("), tok(")")), tok(":"),
			comp("block", comp("return_statement", tok("return"),
				comp("call", leaf("identifier", "validate"), comp("argument_list", tok("("), tok(")")))))),
		comp("class_definition", tok("class"), leaf("identifier", "Svc"), tok(":"),
			comp("block",
				comp("function_definition", tok("def"), leaf("identifier", "run"),
					comp("parameters", tok("("), leaf("identifier", "self"), tok(")")), tok(":"),
					comp("block", tok("pass"))))),
	)
	syms := Extract(root, PythonRules, 1)
	if c := find(syms, "charge"); c == nil || c.Kind != "function" || len(c.Callees) != 1 || c.Callees[0] != "validate" {
		t.Fatalf("python charge wrong: %+v", c)
	}
	if find(syms, "Svc") == nil {
		t.Fatal("python class Svc missing")
	}
	if find(syms, "run") == nil {
		t.Fatal("nested method run must be found by descending into the class")
	}
}

func TestExtractJavaScript(t *testing.T) {
	root := comp("program",
		comp("class_declaration", tok("class"), leaf("identifier", "Svc"),
			comp("class_body", tok("{"),
				comp("method_definition", leaf("property_identifier", "charge"),
					comp("formal_parameters", tok("("), tok(")")),
					comp("statement_block", tok("{"),
						comp("return_statement", tok("return"),
							comp("call_expression", leaf("identifier", "validate"), comp("arguments", tok("("), tok(")")))),
						tok("}"))),
				tok("}"))),
	)
	syms := Extract(root, JavaScriptRules, 1)
	if find(syms, "Svc") == nil {
		t.Fatal("js class Svc missing")
	}
	if m := find(syms, "charge"); m == nil || m.Kind != "method" || len(m.Callees) != 1 || m.Callees[0] != "validate" {
		t.Fatalf("js method charge wrong: %+v", m)
	}
}

func TestRulesForExt(t *testing.T) {
	for ext, want := range map[string]string{".go": "go", ".py": "python", ".ts": "typescript", ".java": "java", ".jsx": "javascript"} {
		if r, ok := RulesForExt(ext); !ok || r.Name != want {
			t.Fatalf("%s -> %q (ok=%v), want %s", ext, r.Name, ok, want)
		}
	}
	if _, ok := RulesForExt(".rb"); ok {
		t.Fatal(".rb should be unknown")
	}
}
