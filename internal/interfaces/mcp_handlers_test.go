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

	out, _ = ts.Call("reponite_refs", nil)
	if !strings.Contains(out, `"HEAD"`) || !strings.Contains(out, `"prod"`) {
		t.Fatalf("refs: %s", out)
	}

	if _, err := ts.Call("reponite_bogus", nil); err == nil {
		t.Fatal("unknown tool must error")
	}
}
