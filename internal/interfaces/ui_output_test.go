package interfaces

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// reponite_ui over a pure store: pages, a tree in JSON and text, and uses.
func TestToolServerUI(t *testing.T) {
	m := storage.NewMem()
	app := `import { Route, Routes } from 'react-router-dom';
import CartPage from './CartPage';
export function App() {
  return <Routes><Route path="/cart" element={<CartPage />} /></Routes>;
}
`
	page := `import axios from 'axios';
export default function CartPage() {
  const save = () => axios.post('/api/orders', {});
  return <button onClick={save}>Save</button>;
}
`
	urls := `urlpatterns = [
    path("api/orders", OrdersView.as_view()),
]
`
	m.PutFile("shop", "HEAD", query.File{Path: "src/App.tsx", Content: app, Symbols: []query.SymbolSpan{{Name: "App", StartLine: 3, EndLine: 5}}})
	m.PutFile("shop", "HEAD", query.File{Path: "src/CartPage.tsx", Content: page, Symbols: []query.SymbolSpan{{Name: "CartPage", StartLine: 2, EndLine: 5}}})
	m.PutFile("api", "HEAD", query.File{Path: "shop/urls.py", Content: urls})
	ts := &ToolServer{Store: m, Repo: "shop"}

	out, err := ts.Call("reponite_ui", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"route": "/cart"`) || !strings.Contains(out, `"handle": "src.CartPage"`) {
		t.Fatalf("pages: %s", out)
	}
	out, _ = ts.Call("reponite_ui", map[string]string{"target": "/cart"})
	if !strings.Contains(out, `"handler": "save"`) || !strings.Contains(out, `"url": "/api/orders"`) || !strings.Contains(out, `"pattern": "api/orders"`) {
		t.Fatalf("tree: %s", out)
	}
	out, _ = ts.Call("reponite_ui", map[string]string{"target": "CartPage", "format": "text"})
	if !strings.Contains(out, `button "Save" click → save`) || !strings.Contains(out, "POST /api/orders → OrdersView.as_view (api:shop/urls.py:2)") {
		t.Fatalf("text: %s", out)
	}
	out, _ = ts.Call("reponite_ui", map[string]string{"target": "CartPage", "uses": "true"})
	if !strings.Contains(out, `"kind": "render"`) || !strings.Contains(out, `"in": "App"`) {
		t.Fatalf("uses: %s", out)
	}
}
