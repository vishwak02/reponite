//go:build treesitter

package processing

import "testing"

// C++: a call through an abstract class (one with a pure virtual) runs
// the one implementation every concrete class of the repo deriving from it
// shares; the declared method keeps its edge. A concrete base's own virtual
// may run (an object of the base type), so its one override stays a
// possible target; two implementations stay a list; a test's mock never
// stands in for a production caller's target.
func TestCppSingleOverride(t *testing.T) {
	files := map[string]string{
		"pkg/shape.hpp": `#pragma once
class Shape {
public:
  virtual ~Shape() = default;
  virtual double area() const = 0;
  virtual double scale() { return 1; }
  double twice() { return 2 * area(); }
};
class Circle : public Shape {
public:
  double area() const override;
  double scale() override { return 2; }
};
class Engine {
public:
  virtual void start() { }
  virtual void stop() = 0;
};
class Diesel : public Engine {
public:
  void start() override { }
  void stop() override { }
};
class Electric : public Engine {
public:
  void stop() override { }
};
class Plain {
public:
  virtual void run() { }
};
class Fast : public Plain {
public:
  void run() override { }
};
`,
		"pkg/shape.cpp": `#include "pkg/shape.hpp"
double Circle::area() const { return 3; }
`,
		"pkg/use.cpp": `#include "pkg/shape.hpp"
double total(Shape& s, Engine* e, Plain* p) {
  e->start();
  e->stop();
  p->run();
  return s.area() + s.scale();
}
`,
		"test/mock_shape.hpp": `#pragma once
#include "pkg/shape.hpp"
class MockShape : public Shape {
public:
  double area() const override { return 0; }
};
`,
		"test/test_use.cpp": `#include "test/mock_shape.hpp"
double check(Shape& s) { return s.area(); }
`,
		"pkg/repo.hpp": `#pragma once
class Repo {
public:
  virtual bool online() const = 0;
  virtual int items() const { return 0; }
};
class SqlRepo : public Repo {
public:
  bool online() const override { return true; }
  int items() const override { return 1; }
};
`,
		"pkg/mock_repo.hpp": `#pragma once
#include "pkg/repo.hpp"
class MockRepo : public Repo {
public:
  bool online() const override { return false; }
};
`,
		"pkg/count.cpp": `#include "pkg/repo.hpp"
int count(Repo* r) { return r->items(); }
`,
	}
	edges := indexAndEdges(t, files)
	total := edges["pkg.total"]
	wantMethod(t, total, "pkg.Circle.area", MethodVirtualSingle)
	wantMethod(t, total, "pkg.Shape.area", MethodTyped)
	// Shape is abstract: its own scale never runs as an object's own
	wantMethod(t, total, "pkg.Circle.scale", MethodVirtualSingle)
	// Engine is abstract, Diesel and Electric run different start()s
	wantMethod(t, total, "pkg.Diesel.start", MethodOverride)
	wantMethod(t, total, "pkg.Diesel.stop", MethodOverride)
	wantMethod(t, total, "pkg.Electric.stop", MethodOverride)
	// Plain is concrete: Plain::run may run
	wantMethod(t, total, "pkg.Fast.run", MethodOverride)
	wantMethod(t, total, "pkg.Plain.run", MethodTyped)
	// this->area() in the abstract class: a pure method, one implementation
	wantMethod(t, edges["pkg.Shape.twice"], "pkg.Circle.area", MethodVirtualSingle)
	// a mock beside production code (production may build it) runs
	// Repo::items: two targets
	if m := edges["pkg.count"]["pkg.SqlRepo.items"]; m == MethodVirtualSingle {
		t.Errorf("a production file's mock is a possible object: %v", edges["pkg.count"])
	}
	check := edges["test.check"]
	if check["pkg.Circle.area"] == MethodVirtualSingle {
		t.Errorf("a test caller may run its mock: %v", check)
	}
}

// Java: interface and abstract methods resolve like C++'s pure virtuals; an
// interface a lambda may implement (one abstract method, lambdas in the
// repo) stays a list.
func TestJavaSingleImplementation(t *testing.T) {
	files := map[string]string{
		"src/app/Use.java": `package app;
interface Store { String load(String k); void save(String k); }
abstract class BaseStore implements Store { public void save(String k) {} }
class SqlStore extends BaseStore { public String load(String k) { return k; } }
interface Handler { void handle(String m); }
class LogHandler implements Handler { public void handle(String m) {} }
class Use {
  Store s;
  Handler h;
  void run() { s.load("a"); s.save("b"); h.handle("c"); Runnable r = () -> {}; }
}
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["src/app.Use.run"]
	wantMethod(t, run, "src/app.SqlStore.load", MethodVirtualSingle)
	wantMethod(t, run, "src/app.BaseStore.save", MethodVirtualSingle)
	wantMethod(t, run, "src/app.Store.load", MethodTyped)
	wantMethod(t, run, "src/app.LogHandler.handle", MethodOverride)
}

// TypeScript: an abstract class's abstract method is a declaration whose
// one implementation is the target; an interface is structural (any object
// of its shape implements it), so its implementing classes stay a list; a
// const annotated with the base and made of a subclass runs the subclass's.
func TestTSAbstractDispatch(t *testing.T) {
	files := map[string]string{
		"src/store.ts": `export interface Store {
  load(k: string): string;
}
export abstract class Base {
  abstract area(): number;
  describe(): number { return this.area(); }
}
export class Circle extends Base {
  area(): number { return 3; }
}
export abstract class Tool {
  abstract use(): number;
}
export class Saw extends Tool {
  use(): number { return 1; }
}
export class Drill extends Tool {
  use(): number { return 2; }
}
export class SqlStore implements Store {
  load(k: string): string { return k; }
}
`,
		"src/use.ts": `import { Store, Base, Tool, Saw } from "./store";
export function run(s: Store, b: Base, t: Tool) {
  s.load("a");
  b.area();
  t.use();
}
export function made() {
  const t: Tool = new Saw();
  t.use();
}
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["src.use.run"]
	wantMethod(t, run, "src.store.Circle.area", MethodAbstractSingle)
	wantMethod(t, run, "src.store.Base.area", MethodAbstractDecl)
	wantMethod(t, run, "src.store.Saw.use", MethodAbstractImpl)
	wantMethod(t, run, "src.store.Drill.use", MethodAbstractImpl)
	wantMethod(t, run, "src.store.SqlStore.load", MethodAbstractImpl)
	wantMethod(t, run, "src.store.Store.load", MethodAbstractDecl)
	wantMethod(t, edges["src.store.Base.describe"], "src.store.Circle.area", MethodAbstractSingle)
	made := edges["src.use.made"]
	wantMethod(t, made, "src.store.Saw.use", MethodTyped)
	wantMethod(t, made, "src.store.Tool.use", MethodAbstractDecl)
	if _, ok := made["src.store.Drill.use"]; ok {
		t.Errorf("a const made of Saw never runs Drill.use: %v", made)
	}
}
