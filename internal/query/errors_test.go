package query

import (
	"strings"
	"testing"
)

// graphStore adds a call graph to filesStore: every span is a symbol
// (pkg.name) and edges are given explicitly.
type graphStore struct {
	filesStore
	edges map[string][]string // caller qid -> callee qids
}

func (g graphStore) SymbolsAt(repo, ref string) map[string]SymbolRef {
	out := map[string]SymbolRef{}
	for _, f := range g.files[repo] {
		for _, sp := range f.Symbols {
			out[pkgFromPath(f.Path)+"."+sp.Name] = SymbolRef{Present: true, IsTest: IsTestPath(f.Path)}
		}
	}
	return out
}
func (g graphStore) SymbolAt(repo, sym, ref string) (SymbolRef, bool) {
	r, ok := g.SymbolsAt(repo, ref)[sym]
	return r, ok
}
func (g graphStore) Snapshot(repo, ref string) RefSnapshot {
	snap := RefSnapshot{Symbols: map[string]SymbolFacts{}, Callees: map[string][]Callee{}}
	for k := range g.SymbolsAt(repo, ref) {
		snap.Symbols[k] = SymbolFacts{}
		for _, c := range g.edges[k] {
			snap.Callees[k] = append(snap.Callees[k], Callee{Name: c})
		}
	}
	return snap
}

func errFixture() graphStore {
	py := `from rest_framework import status
from rest_framework.response import Response


class OrderError(Exception):
    pass


class OrderNotFound(OrderError):
    status_code = 404
    default_detail = "That order is gone"


def load_order(order_id):
    if order_id is None:
        raise ValueError(f"order id required, got {order_id}")
    raise OrderNotFound()


def view(request, order_id):
    try:
        return load_order(order_id)
    except OrderError as e:
        return Response({"detail": f"Order {order_id} not found"}, status=status.HTTP_404_NOT_FOUND)


def custom_exception_handler(exc, context):
    return None
`
	settings := `REST_FRAMEWORK = {
    "EXCEPTION_HANDLER": "shop.errors.custom_exception_handler",
}
`
	ts := `export class CartError extends Error {}
export async function addToCart(sku: string) {
  if (!sku) {
    throw new CartError(` + "`" + `sku ${sku} is invalid` + "`" + `);
  }
}
export function CartPage() {
  try {
    addToCart("");
  } catch (e) {
    if (e instanceof CartError) { show(e); }
  }
  api.get("/api/orders").catch((err) => {
    if (err.response.status === 404) { notFound(); }
  });
}
`
	goSrc := `package shop

import ("errors"; "fmt"; "net/http")

var ErrNoStock = errors.New("no stock left")

func Reserve(n int) error {
	if n < 0 {
		return fmt.Errorf("cannot reserve %d items", n)
	}
	return ErrNoStock
}

func Handle(w http.ResponseWriter, r *http.Request) {
	if err := Reserve(1); errors.Is(err, ErrNoStock) {
		http.Error(w, "out of stock", http.StatusConflict)
	}
}
`
	cpp := `void Planner::plan() {
  try {
    throw std::runtime_error("no path to goal");
  } catch (const std::runtime_error& e) {
    ROS_ERROR("planning failed: %s", e.what());
  }
}
`
	return graphStore{edges: map[string][]string{"shop.view": {"shop.load_order"}, "shop.Handle": {"shop.Reserve"}, "src.CartPage": {"src.addToCart"}}, filesStore: filesStore{files: map[string][]File{
		"api": {
			{Path: "shop/orders.py", Content: py, Symbols: []SymbolSpan{
				{Name: "OrderError", StartLine: 5, EndLine: 6}, {Name: "OrderNotFound", StartLine: 9, EndLine: 11},
				{Name: "load_order", StartLine: 14, EndLine: 17}, {Name: "view", StartLine: 20, EndLine: 24},
				{Name: "custom_exception_handler", StartLine: 27, EndLine: 28}}},
			{Path: "shop/settings.py", Content: settings},
		},
		"ui":      {{Path: "src/cart.ts", Content: ts, Symbols: []SymbolSpan{{Name: "addToCart", StartLine: 2, EndLine: 6}, {Name: "CartPage", StartLine: 7, EndLine: 16}}}},
		"svc":     {{Path: "shop/stock.go", Content: goSrc, Symbols: []SymbolSpan{{Name: "Reserve", StartLine: 7, EndLine: 12}, {Name: "Handle", StartLine: 14, EndLine: 18}}}},
		"planner": {{Path: "src/planner.cpp", Content: cpp, Symbols: []SymbolSpan{{Name: "plan", StartLine: 1, EndLine: 7}}}},
	}}}
}

func TestErrorsFromAMessage(t *testing.T) {
	s := errFixture()
	cases := []struct {
		msg, path string
		line      int
		kind      string
	}{
		{`{"detail": "Order 991 not found"}`, "shop/orders.py", 24, "http_response"},
		{"ValueError: order id required, got None", "shop/orders.py", 16, "raise"},
		{"sku  is invalid", "src/cart.ts", 4, "throw"},
		{"cannot reserve -3 items", "shop/stock.go", 9, "error_value"},
		{"what():  no path to goal", "src/planner.cpp", 3, "throw"},
		{"That order is gone", "shop/orders.py", 11, "error_class"},
	}
	for _, c := range cases {
		r := Errors(s, FleetRepo, "v1", c.msg, 0)
		if len(r.Sites) == 0 {
			t.Errorf("%q: no producer (%s)", c.msg, r.Note)
			continue
		}
		got := r.Sites[0]
		if got.Path != c.path || got.Line != c.line || got.Kind != c.kind {
			t.Errorf("%q: top = %s:%d %s, want %s:%d %s", c.msg, got.Path, got.Line, got.Kind, c.path, c.line, c.kind)
		}
	}
	// A default message reaches the bare raise of its class too.
	r := Errors(s, FleetRepo, "v1", "That order is gone", 0)
	found := false
	for _, x := range r.Sites {
		if x.Line == 17 && x.Match == "default_message" {
			found = true
		}
	}
	if !found {
		t.Errorf("raise OrderNotFound() not linked to its default_detail: %+v", r.Sites)
	}
}

func handlerAt(hs []ErrorHandler, path string, line int) *ErrorHandler {
	for i := range hs {
		if hs[i].Path == path && hs[i].Line == line {
			return &hs[i]
		}
	}
	return nil
}

func TestErrorsHandlers(t *testing.T) {
	s := errFixture()
	// A subclass raise is caught by `except OrderError` in the caller, and by
	// the DRF EXCEPTION_HANDLER the settings name.
	r := Errors(s, FleetRepo, "v1", "OrderNotFound", 0)
	if r.Mode != "type" || len(r.Sites) == 0 || r.Sites[0].Status != 404 {
		t.Fatalf("type mode: %+v", r)
	}
	h := handlerAt(r.Handlers, "shop/orders.py", 23)
	if h == nil || h.Match != "base:OrderError" {
		t.Errorf("except OrderError not matched as base: %+v", r.Handlers)
	}
	if d := handlerAt(r.Handlers, "shop/orders.py", 27); d == nil || d.Kind != "drf_exception_handler" {
		t.Errorf("DRF handler missing: %+v", r.Handlers)
	}

	r = Errors(s, FleetRepo, "v1", "sku x is invalid", 0)
	if h := handlerAt(r.Handlers, "src/cart.ts", 10); h == nil || h.Match != "exact" {
		t.Errorf("instanceof-narrowed catch: %+v", r.Handlers)
	}

	r = Errors(s, FleetRepo, "v1", "no stock left", 0)
	if h := handlerAt(r.Handlers, "shop/stock.go", 15); h == nil || h.Kind != "errors_is" || h.Match != "exact" || !h.OnPath {
		t.Errorf("errors.Is on path: %+v", r.Handlers)
	}

	r = Errors(s, FleetRepo, "v1", "404", 0)
	if r.Mode != "status" || len(r.Sites) < 2 {
		t.Errorf("status mode: %+v", r.Sites)
	}
	if h := handlerAt(r.Handlers, "src/cart.ts", 14); h == nil || h.Kind != "status_check" {
		t.Errorf("client-side 404 check: %+v", r.Handlers)
	}

	r = Errors(s, FleetRepo, "v1", "no path to goal", 0)
	if h := handlerAt(r.Handlers, "src/planner.cpp", 4); h == nil || h.Match != "exact" || !h.OnPath {
		t.Errorf("C++ catch: %+v", r.Handlers)
	}
}

func TestErrorsCatalogueAndBudget(t *testing.T) {
	s := errFixture()
	r := Errors(s, FleetRepo, "v1", "", 0)
	if r.Mode != "catalogue" {
		t.Fatalf("mode %s", r.Mode)
	}
	var names []string
	for _, x := range r.Types {
		names = append(names, x.Repo+":"+x.Type)
	}
	joined := strings.Join(names, " ")
	for _, want := range []string{"api:OrderNotFound", "api:OrderError", "ui:CartError", "svc:ErrNoStock"} {
		if !strings.Contains(joined, want) {
			t.Errorf("catalogue lacks %s: %s", want, joined)
		}
	}
	small := Errors(s, FleetRepo, "v1", "", 60)
	if !small.Truncated || small.Next == "" {
		t.Errorf("budget not applied: %+v", small)
	}
	if none := Errors(s, FleetRepo, "v1", "a message no code has", 0); len(none.Sites) != 0 || !strings.Contains(none.Note, "no raise") {
		t.Errorf("miss: %+v", none)
	}
}
