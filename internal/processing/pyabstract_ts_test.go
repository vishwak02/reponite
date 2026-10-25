//go:build treesitter

package processing

import "testing"

// A call through an abstract class runs one of its concrete subclasses'
// overrides, never the abstract body: those are the call's targets, and the
// declaration is kept below the resolved band. A method with code of its
// own keeps its edge; a Protocol's stub is abstract, an ABC's do-nothing
// hook (pass) runs.
func TestPythonAbstractMethodTargets(t *testing.T) {
	files := map[string]string{
		"shop/pricing.py": `from abc import ABC, abstractmethod
from typing import Protocol


class Adjuster(ABC):
    @abstractmethod
    def adjust(self, v):
        """Adjust v."""

    def stub(self):
        ...

    def hook(self):
        pass

    def run(self):
        self.adjust(1)
        self.hook()


class Plus(Adjuster):
    def adjust(self, v):
        return v + 1

    def stub(self):
        return 2

    def hook(self):
        return 3


class Minus(Adjuster):
    def adjust(self, v):
        return v - 1


class Sink(Protocol):
    def write(self, v):
        pass


class FileSink:
    def write(self, v):
        return v


class Plain:
    def go(self):
        return 1


class Fast(Plain):
    def go(self):
        return 2
`,
		"shop/use.py": `from shop.pricing import Adjuster, Plain


def apply(a: Adjuster, p: Plain):
    a.adjust(1)
    a.stub()
    a.hook()
    p.go()
`,
	}
	edges := indexAndEdges(t, files)
	apply := edges["shop.use.apply"]
	for _, q := range []string{"shop.pricing.Plus.adjust", "shop.pricing.Minus.adjust"} {
		if m := apply[q]; m != MethodAbstractImpl {
			t.Errorf("an abstract method's concrete override %s is a target: %q in %v", q, m, apply)
		}
	}
	if m := apply["shop.pricing.Adjuster.adjust"]; m != MethodAbstractDecl {
		t.Errorf("the abstract declaration is kept as a declaration: %q", m)
	}
	if m := apply["shop.pricing.Adjuster.stub"]; m != MethodAbstractDecl {
		t.Errorf("an ABC's ... body is abstract: %q", m)
	}
	if m := apply["shop.pricing.Plus.stub"]; m != MethodAbstractImpl {
		t.Errorf("the override of an ABC's stub: %q", m)
	}
	if m := apply["shop.pricing.Adjuster.hook"]; m != MethodTyped {
		t.Errorf("an ABC's pass hook runs for subclasses that keep it: %q", m)
	}
	if m := apply["shop.pricing.Plus.hook"]; m != MethodOverride {
		t.Errorf("a hook's override is a possible target: %q", m)
	}
	if m := apply["shop.pricing.Plain.go"]; m != MethodTyped {
		t.Errorf("a method with code keeps its edge: %q", m)
	}
	if m := apply["shop.pricing.Fast.go"]; m != MethodOverride {
		t.Errorf("a concrete method's override stays a possible target: %q", m)
	}
	run := edges["shop.pricing.Adjuster.run"]
	if m := run["shop.pricing.Adjuster.adjust"]; m != MethodAbstractDecl {
		t.Errorf("self.f() on an abstract f in its own class: %q in %v", m, run)
	}
	if m := run["shop.pricing.Minus.adjust"]; m != MethodAbstractImpl {
		t.Errorf("self.f() runs a concrete override: %q", m)
	}
}

func TestPyStubKinds(t *testing.T) {
	cases := []struct {
		src  string
		stub uint8
		cls  uint8
	}{
		{"class A(ABC):\n    @abc.abstractmethod\n    def f(self):\n        return 1\n", pyAbstractMethod, pyClassABC},
		{"class A(metaclass=ABCMeta):\n    def f(self):\n        \"\"\"doc\"\"\"\n        raise NotImplementedError(\"x\")\n", pyStubEllipsis, pyClassABC},
		{"class A(typing.Protocol):\n    def f(self): ...\n", pyStubEllipsis, pyClassProtocol},
		{"class A(Base):\n    def f(self):\n        pass\n", pyStubPass, 0},
		{"class A:\n    def f(self):\n        x = 1\n", 0, 0},
		{"class A:\n    def f(self):\n        raise ValueError()\n", 0, 0},
	}
	for _, c := range cases {
		n, err := parseRoot([]byte(c.src), grammarForExt(".py"))
		if err != nil {
			t.Fatal(err)
		}
		root := tsNode{n: n, src: []byte(c.src)}
		cls := firstDescendant(root, "class_definition")
		dec := firstDescendant(cls, "decorated_definition")
		fn := firstDescendant(cls, "function_definition")
		if got := pyStubKind(dec, fn); got != c.stub {
			t.Errorf("%q: stub %d, want %d", c.src, got, c.stub)
		}
		if got := pyClassKind(cls); got != c.cls {
			t.Errorf("%q: class %d, want %d", c.src, got, c.cls)
		}
	}
}
