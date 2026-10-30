//go:build treesitter

package processing

import (
	"strings"
	"testing"
)

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

// Members a macro generates share one line: a call there names the
// definition making it, and only a name that singles one out credits it with
// the compiler's edge.
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
	units[0].Calls[0].CallerName = "load"
	pe = preciseEdges(units, li, nil)
	if pc := pe["rec/src.Rec.load"]; pc == nil || len(pe) != 1 {
		t.Errorf("the caller's name picks load among the line's members: %+v", pe)
	}
	units[0].Calls[0].CallerName = "rec" // a name none of them has
	if pe := preciseEdges(units, li, nil); len(pe) != 0 {
		t.Errorf("a name no member has proves nothing: %+v", pe)
	}
	// the callee side: a call bound to one of two members on one line
	li["rec/src/rec.cpp"] = append(li["rec/src/rec.cpp"], locSpan{qid: "rec/src.use", start: 9, end: 9})
	units[0].Calls[0] = PreciseCall{CallerPath: "rec/src/rec.cpp", CallerLine: 9, CallerName: "use", Name: "save", CalleePath: "rec/src/rec.cpp", CalleeLine: 3}
	pe = preciseEdges(units, li, nil)
	if pc := pe["rec/src.use"]; pc == nil || len(pc.Bound["save"]) != 1 || pc.Bound["save"][0] != "rec/src.Rec.save" {
		t.Errorf("the called name picks save: %+v", pe["rec/src.use"])
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

// A virtual call the compiler bound to a pure virtual whose class the
// repo implements once runs that implementation: it is resolved beside the
// bound declaration.
func TestPreciseVirtualCallSingleImplementation(t *testing.T) {
	files := map[string]string{
		"core/src/shapes.cpp": `class Shape {
public:
    virtual int area() const = 0;
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
	if got["core/src.Shape.area"] != "clang-compdb" || got["core/src.Square.area"] != MethodVirtualSingle {
		t.Errorf("the bound declaration and its one implementation: %v", got)
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

// A driver that locates a call by its own line and the bound definition by
// its name (Python, TypeScript): a class is the edge only when the call
// constructs it, and a callee location inside another definition (a local
// variable's line) proves nothing.
func TestPreciseCallsLocatedByName(t *testing.T) {
	files := map[string]string{
		"shop/orders.py": `from shop.util import helper


class Order:
    def total(self):
        return helper(1)


def run(kind):
    o = Order()
    x = o.total()
    f = make()
    return f(x)


def make():
    g = 1
    return g
`,
		"shop/util.py": `def helper(v):
    return v


def total(v):
    return v
`,
	}
	units := []PreciseUnit{{Method: "pyright", Conf: 0.97, Calls: []PreciseCall{
		{CallerPath: "shop/orders.py", CallerLine: 6, Name: "helper", CalleePath: "shop/util.py", CalleeLine: 1, CalleeName: "helper"},
		{CallerPath: "shop/orders.py", CallerLine: 10, Name: "Order", CalleePath: "shop/orders.py", CalleeLine: 4, CalleeName: "Order", Ctor: true},
		{CallerPath: "shop/orders.py", CallerLine: 11, Name: "total", CalleePath: "shop/orders.py", CalleeLine: 5, CalleeName: "total"},
		// a name located at a line of another definition's body: not proven
		{CallerPath: "shop/orders.py", CallerLine: 13, Name: "f", CalleePath: "shop/orders.py", CalleeLine: 17, CalleeName: "g"},
	}}}
	edges := indexAndEdgesWith(t, files, units)
	got := edges["shop.orders.run"]
	if got["shop.orders.Order"] != "pyright" {
		t.Errorf("Order() constructs the class: %v", got)
	}
	if got["shop.orders.Order.total"] != "pyright" {
		t.Errorf("o.total() is the method pyright bound: %v", got)
	}
	if _, ok := got["shop.util.total"]; ok {
		t.Errorf("the same-named function of another module is not the bound method: %v", got)
	}
	if _, ok := got["shop.orders.make"]; !ok {
		t.Errorf("make() keeps its inferred edge: %v", got)
	}
	if got := edges["shop.orders.Order.total"]; got["shop.util.helper"] != "pyright" {
		t.Errorf("helper(1) is the imported function: %v", got)
	}
	// a class located where a function is called (no Ctor) proves nothing
	units[0].Calls[1].Ctor = false
	if got := indexAndEdgesWith(t, files, units)["shop.orders.run"]; got["shop.orders.Order"] == "pyright" {
		t.Errorf("a class bound by a call that does not construct it: %v", got)
	}
}

// A Python name defined twice in a class (a property's getter, then its
// setter) is its last definition: a call in the body before it is not
// that method's.
func TestPreciseShadowedDefinitionBody(t *testing.T) {
	files := map[string]string{
		"acl/model.py": `class Checker:
    pass


class Acl:
    @property
    def secret(self):
        return Checker()

    @secret.setter
    def secret(self, v):
        self.v = v
`,
	}
	units := []PreciseUnit{{Method: "pyright", Conf: 0.97, Calls: []PreciseCall{
		{CallerPath: "acl/model.py", CallerLine: 8, Name: "Checker", CalleePath: "acl/model.py", CalleeLine: 1, CalleeName: "Checker", Ctor: true},
	}}}
	got := indexAndEdgesWith(t, files, units)["acl.model.Acl.secret"]
	if got["acl.model.Checker"] == "pyright" {
		t.Errorf("a call in a shadowed definition's body is not the last definition's: %v", got)
	}
}

// A Python method called on an object is virtual: the method pyright bound
// is the edge, and the overrides of the classes deriving from its class
// stay linked as possible targets — and when the bound method is abstract
// and the repo implements it once, that implementation is the resolved
// target and the bound method a declaration edge, as for inferred calls.
func TestPreciseNameLocatedVirtualCall(t *testing.T) {
	files := map[string]string{
		"shop/backend.py": `class Backend:
    def send(self, p):
        raise NotImplementedError


class Printer(Backend):
    def send(self, p):
        return p


def dispatch(b: Backend):
    return b.send(1)
`,
	}
	call := PreciseCall{CallerPath: "shop/backend.py", CallerLine: 12, Name: "send", CalleePath: "shop/backend.py", CalleeLine: 2, CalleeName: "send"}
	units := []PreciseUnit{{Method: "pyright", Conf: 0.97, Calls: []PreciseCall{call}}}
	if got := indexAndEdgesWith(t, files, units)["shop.backend.dispatch"]; got["shop.backend.Printer.send"] != "" {
		t.Errorf("a call bound without Virtual links no override: %v", got)
	}
	units[0].Calls[0].Virtual = true
	got := indexAndEdgesWith(t, files, units)["shop.backend.dispatch"]
	if got["shop.backend.Backend.send"] != MethodAbstractDecl || got["shop.backend.Printer.send"] != MethodAbstractSingle {
		t.Errorf("the abstract bound method and its one implementation: %v", got)
	}
	// a second implementation: the running program picks, a list
	files["shop/fax.py"] = `from shop.backend import Backend


class Fax(Backend):
    def send(self, p):
        return p
`
	got = indexAndEdgesWith(t, files, units)["shop.backend.dispatch"]
	if got["shop.backend.Backend.send"] != MethodAbstractDecl || got["shop.backend.Printer.send"] != MethodAbstractImpl ||
		got["shop.fax.Fax.send"] != MethodAbstractImpl {
		t.Errorf("the abstract bound method and its implementations: %v", got)
	}
	// a concrete bound method keeps its proof, overrides are possible
	files["shop/backend.py"] = strings.Replace(files["shop/backend.py"], "raise NotImplementedError", "return None", 1)
	got = indexAndEdgesWith(t, files, units)["shop.backend.dispatch"]
	if got["shop.backend.Backend.send"] != "pyright" || got["shop.backend.Printer.send"] != MethodOverride {
		t.Errorf("the bound method and the override: %v", got)
	}
}
