package interfaces

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestToolServerCall(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "Charge", storage.SymbolRecord{SignatureHash: "sig", BehaviorHash: "bNEW", BehaviorConf: 1, Callees: []query.Callee{{Name: "validateCard", Confidence: 0.6}}})
	m.Put("r", "prod", "Charge", storage.SymbolRecord{SignatureHash: "sig", BehaviorHash: "bOLD", BehaviorConf: 1})
	m.Put("r", "HEAD", "validateCard", storage.SymbolRecord{SignatureHash: "vsig", BehaviorHash: "v1"})
	m.PutFile("r", "HEAD", query.File{Path: "c.go", Content: "func Charge(){ validateCard() }", Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 1, EndLine: 1}}})
	ts := &ToolServer{Store: m, Repo: "r"}

	out, err := ts.Call("reponite_compat", map[string]string{"symbol": "Charge"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"verdict": "behavior_changed"`) {
		t.Fatalf("compat: %s", out)
	}

	out, _ = ts.Call("reponite_context", map[string]string{"symbol": "Charge"})
	if !strings.Contains(out, `"validateCard"`) || !strings.Contains(out, `"callees"`) {
		t.Fatalf("context: %s", out)
	}

	out, _ = ts.Call("reponite_grep", map[string]string{"pattern": "validateCard"})
	if !strings.Contains(out, `"symbol": "Charge"`) {
		t.Fatalf("grep: %s", out)
	}

	// The pattern is a regex by DEFAULT (P0 fix): an alternation matches the
	// union of its branches instead of being searched as one literal string
	// (which silently returned zero).
	out, _ = ts.Call("reponite_grep", map[string]string{"pattern": "validateCard|noSuchName"})
	if !strings.Contains(out, `"total": 1`) {
		t.Fatalf("grep alternation must match its branches by default: %s", out)
	}
	// fixed=true still opts into literal semantics.
	out, _ = ts.Call("reponite_grep", map[string]string{"pattern": "validateCard|noSuchName", "fixed": "true"})
	if !strings.Contains(out, `"total": 0`) {
		t.Fatalf("grep fixed=true must treat the pattern literally: %s", out)
	}

	out, _ = ts.Call("reponite_refs", nil)
	if !strings.Contains(out, `"HEAD"`) || !strings.Contains(out, `"prod"`) {
		t.Fatalf("refs: %s", out)
	}

	if _, err := ts.Call("reponite_bogus", nil); err == nil {
		t.Fatal("unknown tool must error")
	}

	// Self-healing: a miss returns "did you mean" with the nearest indexed name,
	// not an empty result.
	out, _ = ts.Call("reponite_brief", map[string]string{"symbol": "Charg"})
	if !strings.Contains(out, `"found": false`) || !strings.Contains(out, "Charge") {
		t.Fatalf("brief miss should suggest Charge: %s", out)
	}
	out, _ = ts.Call("reponite_search", map[string]string{"query": "zzzznope"})
	if !strings.Contains(out, `"did_you_mean"`) {
		t.Fatalf("search miss should return a did_you_mean envelope: %s", out)
	}

	// Fleet orientation lists the repo.
	out, _ = ts.Call("reponite_repos", nil)
	if !strings.Contains(out, `"repo": "r"`) {
		t.Fatalf("repos: %s", out)
	}
}

// Fleet-wide search (no repo arg → discovery defaults fleet-wide) spans every
// repo in the store, each hit tagged with its source repo.
func TestToolServerFleetSearch(t *testing.T) {
	m := storage.NewMem()
	m.Put("svc-a", "HEAD", "svc-a.PickItem", storage.SymbolRecord{SignatureHash: "s", BehaviorHash: "b"})
	m.Put("svc-b", "HEAD", "svc-b.PickOrder", storage.SymbolRecord{SignatureHash: "s", BehaviorHash: "b"})
	ts := &ToolServer{Store: m, Repo: "svc-a"}
	out, _ := ts.Call("reponite_search", map[string]string{"query": "Pick"})
	if !strings.Contains(out, "svc-a.PickItem") || !strings.Contains(out, "svc-b.PickOrder") {
		t.Fatalf("fleet search should span both repos: %s", out)
	}
	if !strings.Contains(out, `"repo": "svc-b"`) {
		t.Fatalf("fleet hits should carry their repo: %s", out)
	}
}

func TestToolServerWhyErrorsTests(t *testing.T) {
	m := storage.NewMem()
	src := "def charge(order):\n    if not order:\n        raise ValueError(f\"order {order} is empty\")\n"
	m.Put("r", "HEAD", "shop.charge", storage.SymbolRecord{SignatureHash: "s"})
	m.Put("r", "HEAD", "shop/tests.test_charge", storage.SymbolRecord{IsTest: true, Callees: []query.Callee{{Name: "shop.charge"}}})
	m.PutFile("r", "HEAD", query.File{Path: "shop/pay.py", Content: src, Symbols: []query.SymbolSpan{{Name: "charge", StartLine: 1, EndLine: 3}}})
	m.PutFile("r", "HEAD", query.File{Path: "shop/tests/test_pay.py", Content: "import pytest\ndef test_charge():\n    charge(None)\n",
		Symbols: []query.SymbolSpan{{Name: "test_charge", StartLine: 2, EndLine: 3}}})
	ts := &ToolServer{Store: m, Repo: "r"}

	out, err := ts.Call("reponite_why", map[string]string{"symbol": "charge"})
	if err != nil || !strings.Contains(out, `"compat_history"`) || !strings.Contains(out, "no git history") {
		t.Fatalf("why: %v %s", err, out)
	}
	out, _ = ts.Call("reponite_errors", map[string]string{"query": "order 42 is empty"})
	if !strings.Contains(out, `"path": "shop/pay.py"`) || !strings.Contains(out, `"line": 3`) {
		t.Fatalf("errors: %s", out)
	}
	out, _ = ts.Call("reponite_errors", map[string]string{"format": "text"})
	if !strings.HasPrefix(out, "error catalogue") {
		t.Fatalf("errors catalogue text: %s", out)
	}
	out, _ = ts.Call("reponite_tests", map[string]string{"symbol": "charge"})
	if !strings.Contains(out, "pytest shop/tests/test_pay.py::test_charge") {
		t.Fatalf("tests: %s", out)
	}
}

// reponite_trace returns the nested JSON tree by default and the text tree
// with format=text, each with the coverage note.
func TestToolServerTrace(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "Checkout", storage.SymbolRecord{Callees: []query.Callee{{Name: "SaveOrder", ResolutionMethod: "name-resolved", Confidence: 0.9}}})
	m.Put("r", "HEAD", "SaveOrder", storage.SymbolRecord{})
	m.PutFile("r", "HEAD", query.File{Path: "c.go", Content: "func Checkout(){ SaveOrder() }\nfunc SaveOrder(){ db.Exec(\"INSERT INTO orders\") }\n",
		Symbols: []query.SymbolSpan{{Name: "Checkout", StartLine: 1, EndLine: 1}, {Name: "SaveOrder", StartLine: 2, EndLine: 2}}})
	ts := &ToolServer{Store: m, Repo: "r"}
	out, err := ts.Call("reponite_trace", map[string]string{"entry": "Checkout"})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"handle": "SaveOrder"`, `"resolution_method": "name-resolved"`, `"kind": "db"`, `"op": "sql"`, `read [r@HEAD]`} {
		if !strings.Contains(out, want) {
			t.Errorf("trace json lacks %s: %s", want, out)
		}
	}
	out, _ = ts.Call("reponite_trace", map[string]string{"entry": "SaveOrder", "direction": "up", "format": "text"})
	if !strings.Contains(out, "- Checkout  ⟵caller name-resolved 0.90") {
		t.Errorf("trace text: %s", out)
	}
}
