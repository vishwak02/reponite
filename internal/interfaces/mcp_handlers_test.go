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
