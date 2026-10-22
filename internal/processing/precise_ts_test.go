//go:build treesitter

package processing

import "testing"

func TestPreciseCallsReplaceInferredOnes(t *testing.T) {
	files := map[string]string{
		"nav/include/nav/map.hpp": `class Map {
public:
    int getElevator(const Location& l) const;
    int getElevator(const NodePtr& n) const;
};
`,
		"nav/src/map.cpp": `#include "nav/map.hpp"
int Map::getElevator(const Location& l) const { return 0; }
int Map::getElevator(const NodePtr& n) const { return 1; }
`,
		"nav/src/cost.cpp": `#include "nav/map.hpp"
int cost(const Map& m, const NodePtr& next) {
    return m.getElevator(next) + helper(next) + mystery(next);
}
int helper(const NodePtr& n) { return 0; }
int mystery(const NodePtr& n) { return 0; }
`,
	}
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		// m.getElevator(next): the NodePtr overload (map.cpp line 3)
		{CallerPath: "nav/src/cost.cpp", CallerLine: 2, Name: "getElevator", CalleePath: "nav/src/map.cpp", CalleeLine: 3},
		// helper(next): a library's helper, not the repo's
		{CallerPath: "nav/src/cost.cpp", CallerLine: 2, Name: "helper", External: true},
		// mystery(next): the compiler could not bind it
		{CallerPath: "nav/src/cost.cpp", CallerLine: 2, Name: "mystery", Unbound: true},
	}}}
	edges := indexAndEdgesWith(t, files, units)
	got := edges["nav/src.cost"]
	if got["nav/src.Map.getElevator"] != "clang-compdb" {
		t.Errorf("the compiler's overload is the edge, labeled with its provenance: %v", got)
	}
	if _, ok := got["nav/src.helper"]; ok {
		t.Errorf("a call the compiler bound outside the repo is not the repo's same-named function: %v", got)
	}
	if got["helper"] != MethodExternal {
		t.Errorf("helper reads as external: %v", got)
	}
	if got["nav/src.mystery"] == "" {
		t.Errorf("a call the compiler could not bind keeps the inferred edge: %v", got)
	}
	// without proven calls the index is what it was
	plain := indexAndEdges(t, files)["nav/src.cost"]
	if _, ok := plain["nav/src.helper"]; !ok {
		t.Errorf("inferred resolution links helper by name: %v", plain)
	}
}

func TestPreciseCallToLocalClassIsNotRecursion(t *testing.T) {
	files := map[string]string{
		"t/src/agent_test.cpp": `void runTest() {
    class Result {
    public:
        int get() { return 1; }
    };
    Result r;
    r.get();
}
`,
	}
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		// r.get(): the local class's method, defined on line 4 inside runTest
		{CallerPath: "t/src/agent_test.cpp", CallerLine: 1, Name: "get", CalleePath: "t/src/agent_test.cpp", CalleeLine: 4},
	}}}
	edges := indexAndEdgesWith(t, files, units)
	if _, ok := edges["t/src.runTest"]["t/src.runTest"]; ok {
		t.Errorf("a call of a local class's method is not a call of the enclosing function: %v", edges["t/src.runTest"])
	}
}

// A second inline overload in a class body maps to its class's method id,
// never to the class itself, even when another class has a method of the
// same name in the file.
func TestPreciseOverloadInClassBodyMapsToMethod(t *testing.T) {
	files := map[string]string{
		"nav/include/nav/shapes.hpp": `class Box {
public:
    int size() const { return 1; }
    int size(int k) const { return k; }
};
class Bag {
public:
    int size() const { return 2; }
};
`,
		"nav/src/use.cpp": `#include "nav/shapes.hpp"
int use(const Box& b) {
    return b.size(3);
}
`,
	}
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		{CallerPath: "nav/src/use.cpp", CallerLine: 2, Name: "size", CalleePath: "nav/include/nav/shapes.hpp", CalleeLine: 4},
	}}}
	got := indexAndEdgesWith(t, files, units)["nav/src.use"]
	if _, ok := got["nav/include/nav.Box"]; ok {
		t.Errorf("a call never links to a class: %v", got)
	}
	if got["nav/include/nav.Box.size"] != "clang-compdb" {
		t.Errorf("the overload maps to Box.size: %v", got)
	}
}

// Members a macro generates share one line: a call there does not say which
// of them makes it, so none is credited with the compiler's edge.
func TestPreciseMacroMembersOnOneLine(t *testing.T) {
	files := map[string]string{
		"rec/src/rec.cpp": `class Rec {
public:
    void save(int& s) const { write(s); }
    void load(int& s) { read(s); }
};
void write(int& s) {}
void read(int& s) {}
`,
	}
	// as libclang reports two members a macro expanded on one line (here
	// both claimed at line 3, their caller line)
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		{CallerPath: "rec/src/rec.cpp", CallerLine: 3, Name: "read", CalleePath: "rec/src/rec.cpp", CalleeLine: 7},
	}}}
	li := locIndex{"rec/src/rec.cpp": {{qid: "rec/src.Rec.save", start: 3, end: 3}, {qid: "rec/src.Rec.load", start: 3, end: 3}}}
	pe := preciseEdges(units, li, nil)
	if len(pe) != 0 {
		t.Errorf("a line two definitions share proves nothing about either: %+v", pe)
	}
	_ = files
}

// A virtual call the compiler bound to the base method may run any
// override: they stay beside it even when inference could not type the
// receiver.
func TestPreciseVirtualCallKeepsOverrides(t *testing.T) {
	files := map[string]string{
		"core/src/shapes.cpp": `class Shape {
public:
    virtual int area() const { return 0; }
};
class Square : public Shape {
public:
    int area() const override { return 4; }
};
Shape* make();
int total() {
    return make()->area();
}
`,
	}
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		{CallerPath: "core/src/shapes.cpp", CallerLine: 10, Name: "area", CalleePath: "core/src/shapes.cpp", CalleeLine: 3, Virtual: true},
	}}}
	got := indexAndEdgesWith(t, files, units)["core/src.total"]
	if got["core/src.Shape.area"] != "clang-compdb" {
		t.Errorf("the method the compiler bound: %v", got)
	}
	if got["core/src.Square.area"] != MethodOverride {
		t.Errorf("Square's override may run: %v", got)
	}
}

// One caller proven by a unit the build's commands compiled and by a
// synthesized one: each edge carries the provenance of the unit that bound it.
func TestPreciseProvenancePerEdge(t *testing.T) {
	li := locIndex{"a.cpp": {{qid: "p.f", start: 1, end: 5}, {qid: "p.g", start: 7, end: 8}, {qid: "p.h", start: 9, end: 10}}}
	units := []PreciseUnit{
		{Method: MethodClangSynth, Conf: ConfClangSynth, Calls: []PreciseCall{
			{CallerPath: "a.cpp", CallerLine: 1, Name: "h", CalleePath: "a.cpp", CalleeLine: 9},
			{CallerPath: "a.cpp", CallerLine: 1, Name: "g", CalleePath: "a.cpp", CalleeLine: 7},
		}},
		{Method: MethodClang, Conf: ConfClang, Calls: []PreciseCall{
			{CallerPath: "a.cpp", CallerLine: 1, Name: "g", CalleePath: "a.cpp", CalleeLine: 7},
		}},
	}
	pc := preciseEdges(units, li, nil)["p.f"]
	if pc == nil || pc.Via["p.g"].Method != MethodClang || pc.Via["p.h"].Method != MethodClangSynth {
		t.Errorf("g is proven by the build's command, h by the synthesized context: %+v", pc)
	}
}

// A virtual call on an object of a class that does not override the method
// runs that class's subclasses' overrides only, never a sibling's.
func TestPreciseVirtualCallReceiverClass(t *testing.T) {
	files := map[string]string{
		"core/src/shapes.cpp": `class Base {
public:
    virtual int f() const { return 0; }
};
class A : public Base {
public:
    int f() const override { return 1; }
};
class B : public Base {
};
int use(B* b) {
    return b->f();
}
`,
	}
	units := []PreciseUnit{{Method: "clang-compdb", Conf: 1.0, Calls: []PreciseCall{
		{CallerPath: "core/src/shapes.cpp", CallerLine: 11, Name: "f", CalleePath: "core/src/shapes.cpp", CalleeLine: 3,
			Virtual: true, RecvPath: "core/src/shapes.cpp", RecvLine: 9},
	}}}
	got := indexAndEdgesWith(t, files, units)["core/src.use"]
	if got["core/src.Base.f"] != "clang-compdb" {
		t.Errorf("the method the compiler bound: %v", got)
	}
	if _, ok := got["core/src.A.f"]; ok {
		t.Errorf("A is not a B: its override cannot run: %v", got)
	}
}
