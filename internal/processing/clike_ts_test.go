//go:build treesitter

package processing

import "testing"

// Real-grammar extraction for C, C++, and Rust (CI, -tags treesitter). Verifies
// the LangRules node-type assumptions and the declarator/scope name resolution
// against actual parse trees.
func TestCCppRustExtraction(t *testing.T) {
	t.Run("c", func(t *testing.T) {
		src := `#include <stdio.h>
struct Point { int x; int y; };
typedef struct { int a; } Alias;
int add(int a, int b) { return a + b; }
struct Point make() { return add(0,0); }
void run() { add(1, 2); printf("hi"); }
`
		syms := extractSrc(t, ".c", src)
		// declarator name resolution: even the struct-returning `make` is named "make".
		wantFn(t, syms, "add")
		wantFn(t, syms, "make")
		wantFn(t, syms, "run")
		wantType(t, syms, "Point")
		wantType(t, syms, "Alias")
		if !hasCallee(find(syms, "run").Callees, "add") {
			t.Errorf("run should call add; callees=%v", find(syms, "run").Callees)
		}
	})

	t.Run("cpp", func(t *testing.T) {
		src := `#include <vector>
namespace ns { class Widget { public: void draw(); }; }
void ns::Widget::draw() { compute(3); }
int freeFn() { return 1; }
`
		syms := extractSrc(t, ".cpp", src)
		// qualified name ns::Widget::draw resolves to the last segment "draw".
		wantFn(t, syms, "draw")
		wantFn(t, syms, "freeFn")
		wantType(t, syms, "Widget")
	})

	t.Run("rust", func(t *testing.T) {
		src := `pub struct User { pub id: u32 }
pub trait Greet { fn hello(&self); }
impl User { pub fn new() -> Self { User{id:0} } fn helper(&self) {} }
pub fn compute(n: i32) -> i32 { helper(); n }
`
		syms := extractSrc(t, ".rs", src)
		wantType(t, syms, "User")
		wantType(t, syms, "Greet")
		wantFn(t, syms, "compute")
		// impl methods are qualified by the impl'd type (Recv=User).
		newM := findRecv(syms, "new", "User")
		if newM == nil {
			t.Fatalf("impl method User.new not found (recv-qualified); syms=%v", symNames(syms))
		}
		if !hasCallee(find(syms, "compute").Callees, "helper") {
			t.Errorf("compute should call helper; callees=%v", find(syms, "compute").Callees)
		}
	})
}

func extractSrc(t *testing.T, ext, src string) []Symbol {
	t.Helper()
	rules, ok := RulesForExt(ext)
	if !ok {
		t.Fatalf("no rules for %s", ext)
	}
	root, _, err := parseFileRules([]byte(src), ext, rules)
	if err != nil {
		t.Fatal(err)
	}
	return Extract(root, rules, 1)
}

func wantFn(t *testing.T, syms []Symbol, name string) {
	t.Helper()
	if s := find(syms, name); s == nil {
		t.Errorf("function %q not extracted; got %v", name, symNames(syms))
	}
}

func wantType(t *testing.T, syms []Symbol, name string) {
	t.Helper()
	s := find(syms, name)
	if s == nil || s.Kind != "type" {
		t.Errorf("type %q not extracted; got %v", name, symNames(syms))
	}
}

func findRecv(syms []Symbol, name, recv string) *Symbol {
	for i := range syms {
		if syms[i].Name == name && syms[i].Recv == recv {
			return &syms[i]
		}
	}
	return nil
}
