package pyright

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/precise"
)

// tree writes files under a temp directory and returns an Input reading
// them from disk.
func tree(t *testing.T, files map[string]string) Input {
	t.Helper()
	dir := t.TempDir()
	in := Input{Files: map[string]string{}, Dir: dir}
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		in.Files[rel] = precise.BlobID([]byte(src))
	}
	in.Read = func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))) }
	return in
}

func edgeLines(es []Edge) []string {
	var out []string
	for _, e := range es {
		s := e.CallerPath + ":" + itoa(e.CallerLine) + " " + e.Name + " -> "
		switch {
		case e.Unbound:
			s += "?"
		case e.External:
			s += "ext"
		default:
			s += e.CalleePath + ":" + itoa(e.CalleeLine) + " " + e.CalleeName
			if e.Ctor {
				s += " (class)"
			}
		}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for ; n > 0; n /= 10 {
		s = string(rune('0'+n%10)) + s
	}
	return s
}

// TestRealPyright runs the real language server ($REPONITE_PYRIGHT, or one
// on $PATH; skipped without one).
func TestRealPyright(t *testing.T) {
	if _, err := Find(os.Getenv("REPONITE_PYRIGHT"), os.Getenv("REPONITE_NODE"), ""); err != nil {
		t.Skip(err)
	}
	in := tree(t, map[string]string{
		"shop/__init__.py": "",
		"shop/orders.py": `from shop.util import helper as h
import shop.util


class Order:
    def __init__(self, n):
        self.n = n

    def total(self) -> int:
        return h(self.n)


class Rush(Order):
    def total(self) -> int:
        return super().total() + 1


def make(kind) -> Order:
    return Rush(1) if kind else Order(2)


def run(kind, cb):
    o = make(kind)
    shop.util.helper(o.total())
    cb()
    x = len([1])
    return f"{o.total()!r:>{x}}"
`,
		"shop/other.py": `from shop.util import run2


def go():
    run2(1)
`,
		"shop/util.py": `def helper(v):
    return v


def other(v):
    return v


STEPS = [helper, other]


class Hook:
    def __call__(self):
        return 1


def drive(token, cb=None):
    for step in STEPS:
        step(1)
    f = helper
    f(2)
    hook = Hook()
    hook()
    match token:
        case Hook():
            return 0
    data = dict()
    g = data.get
    return g(3)


run2 = helper
`,
	})
	out, err := Run(context.Background(), in, Config{Pyright: os.Getenv("REPONITE_PYRIGHT"), Node: os.Getenv("REPONITE_NODE"),
		Store: precise.Open(t.TempDir())})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(edgeLines(out.Edges), "\n")
	t.Logf("%s (%d sites)\n%s", out.Version, out.Sites, got)
	for _, want := range []string{
		"shop/orders.py:10 h -> shop/util.py:1 helper",
		"shop/orders.py:15 total -> shop/orders.py:9 total",
		"shop/orders.py:19 Rush -> shop/orders.py:13 Rush (class)",
		"shop/orders.py:19 Order -> shop/orders.py:5 Order (class)",
		"shop/orders.py:23 make -> shop/orders.py:18 make",
		"shop/orders.py:24 helper -> shop/util.py:1 helper",
		"shop/orders.py:24 total -> shop/orders.py:9 total",
		"shop/orders.py:25 cb -> ?",
		"shop/orders.py:26 len -> ext",
		"shop/orders.py:27 total -> shop/orders.py:9 total",
		"shop/util.py:19 step -> ?",
		"shop/other.py:5 run2 -> shop/util.py:1 helper",
		"shop/util.py:21 f -> shop/util.py:1 helper",
		"shop/util.py:22 Hook -> shop/util.py:12 Hook (class)",
		"shop/util.py:23 hook -> ?",
		"shop/util.py:27 dict -> ext",
		"shop/util.py:29 g -> ext",
	} {
		if !strings.Contains(got+"\n", want+"\n") {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(got, "util.py:25") {
		t.Errorf("a case pattern read as a call")
	}
}
