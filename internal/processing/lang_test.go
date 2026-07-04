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

func TestExtractTypeScript(t *testing.T) {
	root := comp("program",
		comp("interface_declaration", tok("interface"), leaf("type_identifier", "Repo"),
			comp("interface_body", tok("{"), tok("}"))),
		comp("class_declaration", tok("class"), leaf("type_identifier", "Svc"),
			comp("class_body", tok("{"),
				comp("method_definition", leaf("property_identifier", "charge"),
					comp("formal_parameters", tok("("), tok(")")),
					comp("statement_block", tok("{"),
						comp("expression_statement",
							comp("call_expression", leaf("identifier", "validate"), comp("arguments", tok("("), tok(")")))),
						tok("}"))),
				tok("}"))),
		comp("function_declaration", tok("function"), leaf("identifier", "helper"),
			comp("formal_parameters", tok("("), tok(")")),
			comp("statement_block", tok("{"), tok("}"))),
	)
	syms := Extract(root, TypeScriptRules, 1)
	if find(syms, "Repo") == nil {
		t.Fatal("ts interface Repo missing")
	}
	if find(syms, "Svc") == nil {
		t.Fatal("ts class Svc missing")
	}
	if h := find(syms, "helper"); h == nil || h.Kind != "function" {
		t.Fatalf("ts function helper wrong: %+v", h)
	}
	m := find(syms, "charge")
	if m == nil || m.Kind != "method" || len(m.Callees) != 1 || m.Callees[0] != "validate" {
		t.Fatalf("ts method charge wrong: %+v", m)
	}
	if m.Recv != "Svc" {
		t.Fatalf("ts method charge must qualify by enclosing class Svc, got recv=%q", m.Recv)
	}
}

func TestExtractJava(t *testing.T) {
	root := comp("program",
		comp("class_declaration", tok("class"), leaf("identifier", "Payment"),
			comp("class_body", tok("{"),
				comp("constructor_declaration", leaf("identifier", "Payment"),
					comp("formal_parameters", tok("("), tok(")")),
					comp("constructor_body", tok("{"), tok("}"))),
				comp("method_declaration", tok("void"), leaf("identifier", "charge"),
					comp("formal_parameters", tok("("), tok(")")),
					comp("block", tok("{"),
						comp("expression_statement",
							comp("method_invocation", leaf("identifier", "validate"), comp("argument_list", tok("("), tok(")")))),
						tok("}"))),
				tok("}"))),
	)
	syms := Extract(root, JavaRules, 1)
	if find(syms, "Payment") == nil {
		t.Fatal("java class Payment missing")
	}
	m := find(syms, "charge")
	if m == nil || m.Kind != "method" || len(m.Callees) != 1 || m.Callees[0] != "validate" {
		t.Fatalf("java method charge wrong: %+v", m)
	}
	if m.Recv != "Payment" {
		t.Fatalf("java method charge must qualify by enclosing class Payment, got recv=%q", m.Recv)
	}
	// constructor_declaration is a MethodDecl too — a method-kind symbol named after the class.
	ctor := false
	for i := range syms {
		if syms[i].Name == "Payment" && syms[i].Kind == "method" {
			ctor = true
		}
	}
	if !ctor {
		t.Fatal("java constructor should extract as a method-kind symbol")
	}
}

// Two classes with a same-named method must stay distinct after qualification.
func TestExtractEnclosingClassDisambiguates(t *testing.T) {
	mkClass := func(name string) *fakeNode {
		return comp("class_declaration", tok("class"), leaf("identifier", name),
			comp("class_body", tok("{"),
				comp("method_definition", leaf("property_identifier", "run"),
					comp("formal_parameters", tok("("), tok(")")),
					comp("statement_block", tok("{"), tok("}"))),
				tok("}")))
	}
	root := comp("program", mkClass("A"), mkClass("B"))
	syms := Extract(root, JavaScriptRules, 1)
	var recvs []string
	for i := range syms {
		if syms[i].Name == "run" {
			recvs = append(recvs, syms[i].Recv)
		}
	}
	if len(recvs) != 2 || recvs[0] == recvs[1] {
		t.Fatalf("same-named methods must carry distinct receivers, got %v", recvs)
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
