package query

import (
	"strings"
	"testing"
)

func testsFixture() graphStore {
	cart := `def add_item(cart, sku):
    cart.append(sku)


def checkout(cart):
    return add_item(cart, "fee")
`
	api := `from fastapi import FastAPI
app = FastAPI()


@app.get("/api/orders")
def list_orders():
    return add_item([], "x")
`
	pyTest := `import pytest
from shop.cart import add_item


def test_checkout():
    assert checkout([]) == ["fee"]


class TestCart:
    def test_add(self):
        add_item([], "a")


def test_orders(client):
    client.get("/api/orders")
`
	goSrc := "package shop\n\nfunc Total(xs []int) int { return len(xs) }\n"
	goTest := "package shop\n\nimport \"testing\"\n\nfunc TestTotal(t *testing.T) {\n\tif Total(nil) != 0 {\n\t\tt.Fatal()\n\t}\n}\n"
	ts := "export function formatPrice(n: number) { return `$${n}`; }\n"
	spec := `import { formatPrice } from '../price';
describe('price', () => {
  it('formats cents', () => {
    expect(formatPrice(5)).toBe('$5');
  });
  test('other', () => {});
});
`
	cpp := "double Planner::cost(int n) { return n; }\n"
	gtest := `#include <gtest/gtest.h>
TEST(PlannerTest, CostIsLinear) {
  Planner p;
  EXPECT_EQ(p.cost(2), 2);
}
`
	rospy := `import rostest
def test_talks():
    ping_node()
`
	launch := `<launch><test test-name="talker" pkg="chatter" type="test_talker.py"/></launch>`
	node := "def ping_node():\n    pass\n"
	return graphStore{
		edges: map[string][]string{
			"shop.checkout":                {"shop.add_item"},
			"shop.list_orders":             {"shop.add_item"},
			"shop/tests.test_checkout":     {"shop.checkout"},
			"shop/tests.TestCart.test_add": {"shop.add_item"},
			"pkg.TestTotal":                {"pkg.Total"},
			"chatter/test.test_talks":      {"chatter/src.ping_node"},
		},
		filesStore: filesStore{files: map[string][]File{"r": {
			{Path: "conftest.py", Content: "import pytest\n"},
			{Path: "shop/cart.py", Content: cart, Symbols: []SymbolSpan{{Name: "add_item", StartLine: 1, EndLine: 2}, {Name: "checkout", StartLine: 5, EndLine: 6}}},
			{Path: "shop/api.py", Content: api, Symbols: []SymbolSpan{{Name: "list_orders", StartLine: 6, EndLine: 7}}},
			{Path: "shop/tests/test_cart.py", Content: pyTest, Symbols: []SymbolSpan{{Name: "test_checkout", StartLine: 5, EndLine: 6},
				{Name: "TestCart", StartLine: 9, EndLine: 11}, {Name: "test_add", StartLine: 10, EndLine: 11}, {Name: "test_orders", StartLine: 14, EndLine: 15}}},
			{Path: "pkg/total.go", Content: goSrc, Symbols: []SymbolSpan{{Name: "Total", StartLine: 3, EndLine: 3}}},
			{Path: "pkg/total_test.go", Content: goTest, Symbols: []SymbolSpan{{Name: "TestTotal", StartLine: 5, EndLine: 9}}},
			{Path: "web/src/price.ts", Content: ts, Symbols: []SymbolSpan{{Name: "formatPrice", StartLine: 1, EndLine: 1}}},
			{Path: "web/src/__tests__/price.spec.ts", Content: spec},
			{Path: "planner/src/planner.cpp", Content: cpp, Symbols: []SymbolSpan{{Name: "cost", StartLine: 1, EndLine: 1}}},
			{Path: "planner/test/test_planner.cpp", Content: gtest},
			{Path: "chatter/src/node.py", Content: node, Symbols: []SymbolSpan{{Name: "ping_node", StartLine: 1, EndLine: 2}}},
			{Path: "chatter/test/test_talker.py", Content: rospy, Symbols: []SymbolSpan{{Name: "test_talks", StartLine: 2, EndLine: 3}}},
			{Path: "chatter/test/talker.test", Content: launch},
		}}},
	}
}

func testNames(r TestsResult) string {
	var out []string
	for _, h := range r.Tests {
		out = append(out, h.Name+"/"+h.Framework+"/"+h.How)
	}
	return strings.Join(out, " ")
}

func TestTestsPytestGraphHTTPAndCommand(t *testing.T) {
	r := Tests(testsFixture(), "r", "v1", "add_item", 0, 0)
	got := testNames(r)
	for _, want := range []string{"TestCart.test_add/pytest/call_graph", "test_checkout/pytest/call_graph", "test_orders/pytest/http"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	for _, h := range r.Tests {
		if h.Name == "test_checkout" && (h.Depth != 2 || len(h.Via) != 1 || h.Via[0] != "shop.checkout") {
			t.Errorf("depth/via: %+v", h)
		}
	}
	if len(r.Commands) != 1 || !strings.HasPrefix(r.Commands[0].Command, "pytest ") ||
		!strings.Contains(r.Commands[0].Command, "shop/tests/test_cart.py::TestCart::test_add") {
		t.Errorf("command: %+v", r.Commands)
	}
	if d1 := Tests(testsFixture(), "r", "v1", "add_item", 1, 0); strings.Contains(testNames(d1), "test_checkout/") {
		t.Errorf("depth 1 must not reach test_checkout: %s", testNames(d1))
	}
}

func TestTestsFrameworks(t *testing.T) {
	s := testsFixture()
	cases := []struct{ sym, name, fw, cmd string }{
		{"Total", "TestTotal", "go", "go test ./pkg -run '^(TestTotal)$'"},
		{"formatPrice", "formats cents", "jest", "npx jest web/src/__tests__/price.spec.ts -t 'formats cents'"},
		{"cost", "PlannerTest.CostIsLinear", "gtest", "--gtest_filter=PlannerTest.CostIsLinear"},
		{"ping_node", "test_talks", "rostest", "rostest chatter talker.test"},
	}
	for _, c := range cases {
		r := Tests(s, "r", "v1", c.sym, 0, 0)
		if len(r.Tests) != 1 || r.Tests[0].Name != c.name || r.Tests[0].Framework != c.fw {
			t.Errorf("%s: %s", c.sym, testNames(r))
			continue
		}
		if !strings.Contains(r.Tests[0].Command, c.cmd) {
			t.Errorf("%s: command %q lacks %q", c.sym, r.Tests[0].Command, c.cmd)
		}
	}
	if r := Tests(s, "r", "v1", "list_orders", 0, 0); r.Total == 0 {
		t.Errorf("list_orders is tested over HTTP: %s", r.Note)
	}
	if r := Tests(s, "r", "v1", "nope", 0, 0); !strings.Contains(r.Note, "not found") {
		t.Errorf("miss: %s", r.Note)
	}
	if r := Tests(s, "r", "v1", "add_item", 0, 50); !r.Truncated || len(r.Commands) != 1 || r.Commands[0].Tests != 3 {
		t.Errorf("budget: truncated=%v commands=%+v", r.Truncated, r.Commands)
	}
}
