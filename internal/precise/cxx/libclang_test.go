//go:build cgo

package cxx

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestParseReportsDefinitionsAndCalls(t *testing.T) {
	if err := Load(os.Getenv("REPONITE_LIBCLANG")); err != nil {
		t.Skip(err)
	}
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	write := func(rel, src string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("nav/include/nav/map.hpp", `#include <missing/graph.hpp>
struct Location { int x; };
class Map {
public:
    int getElevator(const Location& l) const;
    int getElevator(const graph::NodePtr& n) const;
    int floor(int x) const { return helper(x); }
    static int helper(int x);
};
`)
	src := write("nav/src/map.cpp", `#include "nav/map.hpp"
#include <cstdlib>
int Map::getElevator(const Location& l) const { return std::abs(l.x); }
int Map::helper(int x) { return x; }
int use(const Map& m, const graph::NodePtr& n, const Location& l) {
    return m.getElevator(l) + m.getElevator(n) + m.floor(1);
}
`)
	r, err := Parse(root, src, []string{"-x", "c++", "-std=c++17", "-I", filepath.Join(root, "nav/include")})
	if err != nil {
		t.Fatal(err)
	}
	defs := map[string]Def{}
	for _, d := range r.Defs {
		defs[d.Path+":"+itoa(d.Line)] = d
	}
	if _, ok := defs["nav/src/map.cpp:3"]; !ok {
		t.Errorf("Map::getElevator's definition: %+v", r.Defs)
	}
	if _, ok := defs["nav/include/nav/map.hpp:7"]; !ok {
		t.Errorf("an inline method in the header is a definition too: %+v", r.Defs)
	}
	var sawAbs, sawBound, sawUnbound bool
	for _, c := range r.Calls {
		switch {
		case c.Name == "abs" && c.Kind == 'E':
			sawAbs = true
		case c.Name == "floor" && c.Kind == 'B' && c.OwnerLine == 5 && c.DeclPath == "nav/include/nav/map.hpp" && c.DeclLine == 7:
			sawBound = true
		case c.Name == "getElevator" && c.Kind == 'U':
			sawUnbound = true
		}
	}
	if !sawAbs {
		t.Errorf("std::abs is bound outside the repo: %+v", r.Calls)
	}
	if !sawUnbound {
		t.Errorf("the call whose argument's type comes from a missing header is not bound by guess: %+v", r.Calls)
	}
	if !sawBound {
		t.Errorf("m.floor(1) is bound to Map::floor by its declaration: %+v", r.Calls)
	}
	found := false
	for _, inc := range r.Includes {
		if inc == "nav/include/nav/map.hpp" {
			found = true
		}
	}
	if !found {
		t.Errorf("the repo header it included: %v", r.Includes)
	}
	if Version() == "" {
		t.Error("libclang reports a version")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// A call clang's error recovery drops (its argument's type comes from a
// missing header) is not proven to be its same-named sibling's target.
func TestDroppedCallLeavesNameUnbound(t *testing.T) {
	if err := Load(os.Getenv("REPONITE_LIBCLANG")); err != nil {
		t.Skip(err)
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	write := func(rel, src string) string {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	write("pa/a.hpp", "#include <missing/graph.hpp>\nnamespace a { inline int send(const net::Msg& m) { return 0; } }\n")
	write("pb/b.hpp", "namespace b { inline int send(int x) { return x; } }\n")
	src := write("src/use.cpp", "#include \"pa/a.hpp\"\n#include \"pb/b.hpp\"\nint use(const net::Msg& m) {\n    return a::send(m) + b::send(1);\n}\n")
	r, err := Parse(root, src, []string{"-x", "c++", "-std=c++17", "-I", root})
	if err != nil {
		t.Fatal(err)
	}
	unbound := false
	for _, e := range link([]Result{r}, []string{MethodSynth}, nil) {
		if e.CallerPath == "src/use.cpp" && e.Name == "send" && e.Unbound {
			unbound = true
		}
	}
	if !unbound {
		t.Errorf("the dropped a::send(m) keeps send undecided: calls %+v refs %+v", r.Calls, r.Refs)
	}
	if !readsItself(r, "src/use.cpp") {
		t.Errorf("a unit lists its own source among the repo files it read: %v", r.Includes)
	}
}

// Calls into the repo's own templates are proven against the template's
// definition; a helper an outside macro defines at its use is not the repo's.
func TestTemplatesAndOutsideMacroHelpers(t *testing.T) {
	if err := Load(os.Getenv("REPONITE_LIBCLANG")); err != nil {
		t.Skip(err)
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	root := filepath.Join(base, "repo")
	lib := filepath.Join(base, "lib")
	for p, src := range map[string]string{
		filepath.Join(lib, "fw.hpp"): "void fw_register(int);\n#define REGISTER(name) static int name##_reg() { fw_register(1); return 0; }\n",
		filepath.Join(root, "src/a.cpp"): `#include "fw.hpp"
template <typename T> T twice(T x) { return x + x; }
template <typename T> struct Box { T v; T get() const { return v; } };
int use() {
    Box<int> b{3};
    return twice(2) + b.get();
}
REGISTER(thing)
`,
	} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	r, err := Parse(root, filepath.Join(root, "src/a.cpp"), []string{"-x", "c++", "-std=c++17", "-I", lib})
	if err != nil {
		t.Fatal(err)
	}
	bound := map[string]Edge{}
	for _, e := range link([]Result{r}, []string{MethodSynth}, nil) {
		if e.CallerPath == "src/a.cpp" && e.CallerLine == 4 {
			bound[e.Name] = e
		}
		if e.Name == "fw_register" {
			t.Errorf("a helper the outside REGISTER macro defines is not the repo's caller: %+v", e)
		}
	}
	if e := bound["twice"]; e.CalleeLine != 2 || e.Unbound {
		t.Errorf("twice(2) is proven to the template at line 2: %+v", e)
	}
	if e := bound["get"]; e.CalleeLine != 3 || e.Unbound {
		t.Errorf("b.get() is proven to Box<T>::get at line 3: %+v", e)
	}
}
