package interfaces

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestSnakeKey(t *testing.T) {
	for in, want := range map[string]string{"StartLine": "start_line", "ResolutionMethod": "resolution_method", "Meta": "_meta",
		"EntryPoints": "entry_points", "IsTest": "is_test", "In": "in", "URL": "url", "MoreCallers": "more_callers"} {
		if got := snakeKey(in); got != want {
			t.Errorf("snakeKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMapExplainGuideTools(t *testing.T) {
	m := storage.NewMem()
	m.Put("shop", "HEAD", "orders.Load", storage.SymbolRecord{Lang: "go"})
	m.Put("shop", "HEAD", "orders.Save", storage.SymbolRecord{Lang: "go", Callees: []query.Callee{{Name: "orders.Load", ResolutionMethod: "name-resolved", Confidence: 0.9}}})
	m.PutFile("shop", "HEAD", query.File{Path: "orders/orders.go", Content: "package orders\n\n// Load reads the book.\nfunc Load() {}\n\nfunc Save() { Load() }\n",
		Symbols: []query.SymbolSpan{{Name: "Load", StartLine: 4, EndLine: 4}, {Name: "Save", StartLine: 6, EndLine: 6}}})
	m.PutFile("shop", "HEAD", query.File{Path: "README.md", Content: "# Shop\n\nA demo store.\n"})
	ts := &ToolServer{Store: m, Repo: "shop"}

	out, err := ts.Call("reponite_map", map[string]string{})
	if err != nil || !strings.Contains(out, `"summary": "A demo store."`) || !strings.Contains(out, `"hubs"`) || !strings.Contains(out, `"truncated": false`) {
		t.Fatalf("map (a one-repo store maps that repo): %v %s", err, out)
	}
	out, _ = ts.Call("reponite_map", map[string]string{"format": "text"})
	if !strings.HasPrefix(out, "# shop @ HEAD") {
		t.Fatalf("map text: %s", out)
	}
	out, err = ts.Call("reponite_explain", map[string]string{"target": "Load"})
	if err != nil || !strings.Contains(out, `"doc": "Load reads the book."`) || !strings.Contains(out, `"symbol": "orders.Save"`) {
		t.Fatalf("explain: %v %s", err, out)
	}
	if _, err := ts.Call("reponite_explain", map[string]string{}); err == nil {
		t.Fatal("explain without a target must say so")
	}
	out, _ = ts.Call("reponite_guide", nil)
	for _, want := range []string{"reponite_map", "reponite_explain", "reponite_grep"} {
		if !strings.Contains(out, want) {
			t.Errorf("guide lacks %s", want)
		}
	}
}
