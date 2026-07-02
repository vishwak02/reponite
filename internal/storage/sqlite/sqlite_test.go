//go:build sqlite

package sqlite

import (
	"testing"

	"github.com/reponite/reponite/internal/content"
	"github.com/reponite/reponite/internal/query"
	"github.com/reponite/reponite/internal/storage"
)

func rec(sym, sig, beh string, conf float64, callees ...string) storage.SymbolRecord {
	cs := make([]query.Callee, len(callees))
	for i, c := range callees {
		cs[i] = query.Callee{Name: c, Confidence: 1}
	}
	return storage.SymbolRecord{
		SymbolHash: content.Hash(sym), SignatureHash: content.Hash(sig),
		BehaviorHash: content.Hash(beh), BehaviorConf: conf, Callees: cs,
	}
}

func TestSQLiteStoreRoundTripAndOracle(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Put("billing", "HEAD", "Charge", rec("c", "sig", "behNEW", 1, "validateCard")); err != nil {
		t.Fatal(err)
	}
	if err := st.Put("billing", "prod", "Charge", rec("c", "sig", "behOLD", 1, "validateCard")); err != nil {
		t.Fatal(err)
	}

	if repos := st.Repos(); len(repos) != 1 || repos[0] != "billing" {
		t.Fatalf("repos %v", repos)
	}
	if refs := st.Refs("billing"); len(refs) != 2 || refs[0] != "HEAD" || refs[1] != "prod" {
		t.Fatalf("refs %v", refs)
	}

	origin, ok := st.SymbolAt("billing", "Charge", "HEAD")
	if !ok || !origin.Present {
		t.Fatal("origin not found")
	}
	prod, _ := st.SymbolAt("billing", "Charge", "prod")
	if query.Compat(origin, prod).Verdict != query.BehaviorChanged {
		t.Fatal("prod must be behavior_changed via SQLite store")
	}
	if _, ok := st.SymbolAt("billing", "Charge", "v1"); ok {
		t.Fatal("absent ref must report not found")
	}

	snap := st.Snapshot("billing", "HEAD")
	if len(snap.Callees["Charge"]) != 1 || snap.Callees["Charge"][0].Name != "validateCard" {
		t.Fatalf("snapshot callees %+v", snap.Callees)
	}

	if err := st.PutFile("billing", "HEAD", query.File{
		Path: "charge.go", Content: "func Charge(){ validateCard() }",
		Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 1, EndLine: 1}},
	}); err != nil {
		t.Fatal(err)
	}
	res, err := query.GrepRepo(st, "billing", "HEAD", "validateCard", query.GrepOptions{Fixed: true})
	if err != nil || len(res.Matches) != 1 || res.Matches[0].Symbol != "Charge" {
		t.Fatalf("grep via SQLite: %+v err=%v", res.Matches, err)
	}
}
