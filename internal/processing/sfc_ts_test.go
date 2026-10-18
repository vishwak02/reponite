//go:build treesitter

package processing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// shopFixture is a small storefront: a React+TS app (router, pages, child
// components, a form whose Save button posts with axios), a Vue SFC with
// defineProps and a pinia store, a Svelte page, and the Django/FastAPI
// backend they call.
var shopFixture = map[string]string{
	"web/src/App.tsx": `import { BrowserRouter, Routes, Route, Outlet } from 'react-router-dom';
import React, { lazy } from 'react';
import CartPage from './pages/CartPage';
import { OrdersPage } from './pages/OrdersPage';
const Settings = lazy(() => import('./pages/Settings'));

export default function App() {
  return (
    <BrowserRouter>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route path="cart" element={<CartPage />} />
          <Route path="orders/:id" element={<OrdersPage />} />
        </Route>
        <Route path="/settings" element={<Settings />} />
        {/* <Route path="/old" element={<OldPage />} /> */}
      </Routes>
    </BrowserRouter>
  );
}

function Layout() {
  return <main><Outlet /></main>;
}
`,
	"web/src/pages/CartPage.tsx": `import React, { useState, useEffect } from 'react';
import { CartItem } from '../components/CartItem';
import OrderForm from '../components/OrderForm';
import { fetchCart } from '../api/cart';

interface CartPageProps {
  title?: string;
  userId: number;
}

const CartPage: React.FC<CartPageProps> = ({ title, userId }) => {
  const [items, setItems] = useState<string[]>([]);
  useEffect(() => {
    fetchCart(userId).then(setItems);
  }, [userId]);
  return (
    <div>
      <h1>{title}</h1>
      {items.map((i) => <CartItem key={i} name={i} />)}
      <OrderForm onDone={() => setItems([])} />
    </div>
  );
};

export default CartPage;
`,
	"web/src/components/CartItem.tsx": `export const CartItem = ({ name }: { name: string }) => <li>{name}</li>;
`,
	"web/src/components/OrderForm.tsx": `import React, { useState } from 'react';
import axios from 'axios';

type Props = { onDone: () => void };

export default function OrderForm({ onDone }: Props) {
  const [qty, setQty] = useState(1);
  const handleSave = async () => {
    await axios.post('/api/orders', { qty });
    onDone();
  };
  return (
    <form>
      <input value={qty} onChange={(e) => setQty(Number(e.target.value))} />
      <button type="button" onClick={handleSave}>Save</button>
      <button type="button" onClick={onDone}>Cancel</button>
    </form>
  );
}
`,
	"web/src/api/cart.ts": `import axios from 'axios';

export async function fetchCart(userId: number) {
  const res = await axios.get(` + "`/api/carts/${userId}`" + `);
  return res.data;
}
`,
	"web/src/pages/OrdersPage.tsx": `export function OrdersPage() {
  return <section>orders</section>;
}
`,
	"web/src/pages/Settings.tsx": `export default function Settings() {
  return <section>settings</section>;
}
`,
	"server/shop/urls.py": `from django.urls import path
urlpatterns = [
    path("api/orders", OrdersView.as_view()),
    path("api/carts/<int:pk>", CartView.as_view()),
]
`,
	"server/api/cart.py": `from fastapi import FastAPI
app = FastAPI()

@app.post("/api/cart/add")
def add_to_cart():
    return {}

@app.post("/api/checkout")
def checkout_cart():
    return {}
`,
	"web2/src/router.ts": `import { createRouter, createWebHistory } from 'vue-router';
import CartSummary from './components/CartSummary.vue';

export default createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/cart', component: CartSummary },
    { path: '/account', component: () => import('./views/Account.vue'), children: [
      { path: 'profile', component: () => import('./views/Profile.vue') },
    ] },
  ],
});
`,
	"web2/src/components/CartSummary.vue": `<template>
  <div>
    <!-- <legacy-line /> -->
    <cart-line v-for="l in store.lines" :key="l.id" :line="l" />
    <button @click="checkout">Checkout</button>
  </div>
</template>

<script setup lang="ts">
import { useCartStore } from '../stores/cart';
import CartLine from './CartLine.vue';

const props = defineProps<{ title: string; compact?: boolean }>();
const store = useCartStore();

function checkout() {
  store.checkout();
}
</script>
`,
	"web2/src/components/CartLine.vue": `<script setup>
defineProps({ line: { type: Object, required: true } });
</script>
<template><li>{{ line.name }}</li></template>
`,
	"web2/src/views/Account.vue": "<template><router-view /></template>\n",
	"web2/src/views/Profile.vue": "<template><p>profile</p></template>\n",
	"web2/src/stores/cart.ts": `import { defineStore } from 'pinia';
import axios from 'axios';

export const useCartStore = defineStore('cart', {
  state: () => ({ lines: [] }),
  actions: {
    async checkout() {
      await axios.post('/api/checkout', { lines: this.lines });
    },
  },
});
`,
	"web3/src/routes/cart/+page.svelte": `<script lang="ts">
  import Counter from '$lib/Counter.svelte';
  export let data: { total: number };
  let count = 0;
  function add() {
    count += 1;
    fetch('/api/cart/add', { method: 'POST' });
  }
</script>

<h1>Cart {data.total}</h1>
<Counter value={count} on:change={add} />
<button on:click={add}>Add</button>
`,
	"web3/src/lib/Counter.svelte": `<script>
  export let value = 0;
</script>
<span>{value}</span>
`,
}

func indexShop(t *testing.T) *storage.Mem {
	t.Helper()
	dir := t.TempDir()
	for rel, content := range shopFixture {
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := storage.NewMem()
	if err := IndexDir(m, "shop", "HEAD", dir, 1); err != nil {
		t.Fatal(err)
	}
	return m
}

func calleesOf(m *storage.Mem, qid string) []string {
	var out []string
	for _, c := range m.Snapshot("shop", "HEAD").Callees[qid] {
		out = append(out, c.Name)
	}
	return out
}

func hasStr(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

// JSX elements are call edges: CartPage renders CartItem and OrderForm, so
// the call graph (context/usages/brief) sees them; <div> is no edge.
func TestJSXRenderEdges(t *testing.T) {
	m := indexShop(t)
	got := calleesOf(m, "web/src/pages.CartPage")
	for _, want := range []string{"web/src/components.CartItem", "web/src/components.OrderForm", "web/src/api.cart.fetchCart"} {
		if !hasStr(got, want) {
			t.Errorf("CartPage callees %v: missing %s", got, want)
		}
	}
	for _, c := range got {
		if strings.HasSuffix(c, ".div") || strings.HasSuffix(c, ".h1") {
			t.Errorf("intrinsic element became an edge: %s", c)
		}
	}
}

// A .vue file is indexed: its script's functions with their real lines, and
// the component itself as a symbol rendering its template's components.
func TestSFCIndexed(t *testing.T) {
	m := indexShop(t)
	syms := m.SymbolsAt("shop", "HEAD")
	if _, ok := syms["web2/src/components.CartSummary"]; !ok {
		t.Fatalf("CartSummary component symbol missing; have %v", keysOfSyms(syms))
	}
	if _, ok := syms["web2/src/components.CartSummary.checkout"]; !ok {
		t.Fatalf("script function checkout missing")
	}
	got := calleesOf(m, "web2/src/components.CartSummary")
	if !hasStr(got, "web2/src/components.CartLine") || !hasStr(got, "web2/src/components.CartSummary.checkout") {
		t.Errorf("CartSummary callees = %v, want CartLine (template) and checkout (handler)", got)
	}
	var lines []query.SymbolSpan
	for _, f := range m.Files("shop", "HEAD") {
		if f.Path == "web2/src/components/CartSummary.vue" {
			lines = f.Symbols
		}
	}
	found := false
	for _, sp := range lines {
		if sp.Name == "checkout" {
			found = true
			if sp.StartLine != 16 {
				t.Errorf("checkout span starts at line %d, want 16 (its line in the .vue file)", sp.StartLine)
			}
		}
	}
	if !found {
		t.Errorf("no span for checkout in %v", lines)
	}
	if _, ok := syms["web3/src/lib.Counter"]; !ok {
		t.Errorf("svelte component Counter not indexed")
	}
	if !hasStr(calleesOf(m, "web3/src/routes/cart.+page"), "web3/src/lib.Counter") {
		t.Errorf("+page.svelte should render Counter: %v", calleesOf(m, "web3/src/routes/cart.+page"))
	}
}

func keysOfSyms(m map[string]query.SymbolRef) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	return out
}

func TestUIPages(t *testing.T) {
	m := indexShop(t)
	res := query.UI(m, query.FleetRepo, "HEAD", "", query.UIOptions{})
	got := map[string]string{}
	for _, p := range res.Pages {
		def := ""
		if p.Def != nil {
			def = p.Def.Path
		}
		got[p.Framework+" "+p.Route] = p.Component + " @ " + def
	}
	want := map[string]string{
		"react-router /":              "Layout @ web/src/App.tsx",
		"react-router /cart":          "CartPage @ web/src/pages/CartPage.tsx",
		"react-router /orders/:id":    "OrdersPage @ web/src/pages/OrdersPage.tsx",
		"react-router /settings":      "Settings @ web/src/pages/Settings.tsx",
		"vue-router /cart":            "CartSummary @ web2/src/components/CartSummary.vue",
		"vue-router /account":         "Account @ web2/src/views/Account.vue",
		"vue-router /account/profile": "Profile @ web2/src/views/Profile.vue",
		"sveltekit /cart":             "+page @ web3/src/routes/cart/+page.svelte",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("page %q = %q, want %q", k, got[k], v)
		}
	}
	if _, ok := got["react-router /old"]; ok {
		t.Errorf("a commented-out route was listed")
	}
}

func TestUIReactTree(t *testing.T) {
	m := indexShop(t)
	res := query.UI(m, query.FleetRepo, "HEAD", "/orders/42", query.UIOptions{})
	if res.Mode != "tree" || res.Root == nil || res.Root.Name != "OrdersPage" || res.Page == nil {
		t.Fatalf("route lookup: mode %s root %+v note %s", res.Mode, res.Root, res.Note)
	}
	res = query.UI(m, query.FleetRepo, "HEAD", "CartPage", query.UIOptions{Budget: 8000})
	root := res.Root
	if root == nil || root.Name != "CartPage" || root.Handle != "web/src/pages.CartPage" {
		t.Fatalf("root = %+v", root)
	}
	props := map[string]query.UIProp{}
	for _, p := range root.Props {
		props[p.Name] = p
	}
	if p := props["title"]; p.Type != "string" || !p.Optional {
		t.Errorf("title prop = %+v", p)
	}
	if p := props["userId"]; p.Type != "number" || p.Optional {
		t.Errorf("userId prop = %+v", p)
	}
	if len(root.State) == 0 || root.State[0].Name != "items" || root.State[0].Kind != "useState" {
		t.Errorf("state = %+v", root.State)
	}
	if len(root.Effects) != 1 || root.Effects[0].Trigger != "deps" || len(root.Effects[0].Deps) != 1 || root.Effects[0].Deps[0] != "userId" {
		t.Fatalf("effects = %+v", root.Effects)
	}
	api := root.Effects[0].API
	if len(api) != 1 || api[0].Method != "GET" || api[0].URL != "/api/carts/{}" {
		t.Fatalf("effect API = %+v", api)
	}
	if len(api[0].Routes) != 1 || api[0].Routes[0].Path != "server/shop/urls.py" || !strings.HasPrefix(api[0].Routes[0].Handler, "CartView") {
		t.Errorf("effect API route = %+v", api[0].Routes)
	}
	if strings.Join(api[0].Via, ">") != "fetchCart" {
		t.Errorf("via = %v", api[0].Via)
	}
	var form *query.UIComponent
	for _, c := range root.Children {
		if c.Name == "OrderForm" {
			form = c
		}
	}
	if form == nil {
		t.Fatalf("OrderForm not expanded: renders %+v", root.Renders)
	}
	var save, cancel *query.UIHandler
	for i, h := range form.Handlers {
		switch h.Label {
		case "Save":
			save = &form.Handlers[i]
		case "Cancel":
			cancel = &form.Handlers[i]
		}
	}
	if save == nil || save.Element != "button" || save.Event != "click" || save.Handler != "handleSave" {
		t.Fatalf("Save handler = %+v (all %+v)", save, form.Handlers)
	}
	if len(save.API) != 1 || save.API[0].Method != "POST" || save.API[0].URL != "/api/orders" || len(save.API[0].Routes) != 1 || !strings.HasPrefix(save.API[0].Routes[0].Handler, "OrdersView") {
		t.Errorf("Save API = %+v", save.API)
	}
	if cancel == nil || !cancel.Prop {
		t.Errorf("Cancel binds the onDone prop: %+v", cancel)
	}
	if strings.Contains(strings.Join(root.Lib, ","), "CartItem") {
		t.Errorf("CartItem resolved in-repo, not a lib: %v", root.Lib)
	}
}

func TestUIVueAndSvelte(t *testing.T) {
	m := indexShop(t)
	res := query.UI(m, query.FleetRepo, "HEAD", "CartSummary", query.UIOptions{})
	root := res.Root
	if root == nil || root.Framework != "vue" {
		t.Fatalf("root = %+v (%s)", root, res.Note)
	}
	if len(root.Props) != 2 || root.Props[0].Name != "title" || root.Props[1].Name != "compact" || !root.Props[1].Optional {
		t.Errorf("props = %+v", root.Props)
	}
	if len(root.State) == 0 || root.State[0].Name != "store" || root.State[0].Kind != "store" || root.State[0].From != "useCartStore" {
		t.Errorf("state = %+v", root.State)
	}
	if len(root.Handlers) != 1 || root.Handlers[0].Handler != "checkout" || root.Handlers[0].Label != "Checkout" {
		t.Fatalf("handlers = %+v", root.Handlers)
	}
	api := root.Handlers[0].API
	if len(api) == 0 || api[0].URL != "/api/checkout" || len(api[0].Routes) == 0 || api[0].Routes[0].Handler != "checkout_cart" {
		t.Errorf("checkout API = %+v", api)
	}
	if len(root.Renders) != 1 || root.Renders[0].Name != "CartLine" {
		t.Errorf("renders = %+v (the commented-out <legacy-line> must not count)", root.Renders)
	}
	if len(root.Children) != 1 || len(root.Children[0].Props) != 1 || root.Children[0].Props[0].Name != "line" || root.Children[0].Props[0].Optional {
		t.Errorf("CartLine child = %+v", root.Children)
	}

	sv := query.UI(m, query.FleetRepo, "HEAD", "web3/src/routes/cart/+page.svelte", query.UIOptions{}).Root
	if sv == nil || sv.Framework != "svelte" {
		t.Fatalf("svelte root = %+v", sv)
	}
	if len(sv.Props) != 1 || sv.Props[0].Name != "data" {
		t.Errorf("svelte props = %+v", sv.Props)
	}
	var events []string
	for _, h := range sv.Handlers {
		events = append(events, h.Element+":"+h.Event+"->"+h.Handler)
		if len(h.API) != 1 || h.API[0].Method != "POST" || len(h.API[0].Routes) != 1 || h.API[0].Routes[0].Handler != "add_to_cart" {
			t.Errorf("svelte handler %s API = %+v", h.Handler, h.API)
		}
	}
	if strings.Join(events, ",") != "Counter:change->add,button:click->add" {
		t.Errorf("svelte handlers = %v", events)
	}
	if len(sv.Renders) != 1 || sv.Renders[0].Def == nil || sv.Renders[0].Def.Path != "web3/src/lib/Counter.svelte" {
		t.Errorf("svelte renders = %+v", sv.Renders)
	}
}

func TestUIUsesBudgetAndEdges(t *testing.T) {
	m := indexShop(t)
	uses := query.UI(m, query.FleetRepo, "HEAD", "CartItem", query.UIOptions{Uses: true}).Uses
	if len(uses) != 1 || uses[0].Path != "web/src/pages/CartPage.tsx" || uses[0].In != "CartPage" || uses[0].Resolution != "import" {
		t.Errorf("uses = %+v", uses)
	}
	routed := query.UI(m, query.FleetRepo, "HEAD", "Settings", query.UIOptions{Uses: true}).Uses
	kinds := ""
	for _, u := range routed {
		kinds += u.Kind + ","
	}
	if !strings.Contains(kinds, "route") {
		t.Errorf("Settings should be found as a route target: %+v", routed)
	}

	small := query.UI(m, query.FleetRepo, "HEAD", "CartPage", query.UIOptions{Budget: 150})
	if !small.Truncated || !strings.Contains(small.Next, "reponite ui ") || small.Root == nil || len(small.Root.Children) != 0 {
		t.Errorf("budget 150: truncated=%v next=%q root=%+v", small.Truncated, small.Next, small.Root)
	}

	var api, renders int
	for _, e := range query.UIEdges(m, query.FleetRepo, "HEAD") {
		switch {
		case e.Kind == "calls_api" && e.From == "web/src/components.OrderForm#handleSave" && e.To == "POST /api/orders":
			api++
		case e.Kind == "renders" && e.From == "web/src/pages.CartPage" && e.To == "web/src/components.OrderForm":
			renders++
		}
	}
	if api != 1 || renders != 1 {
		t.Errorf("UIEdges: handler->api %d, renders %d", api, renders)
	}
}
