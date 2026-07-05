//go:build sqlite

package sqlite

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
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

// Each callee edge's resolution_method survives a store round-trip (invariant 5).
func TestSQLiteResolutionMethodRoundTrip(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.Put("r", "HEAD", "Charge", storage.SymbolRecord{
		SymbolHash: "c", SignatureHash: "s", BehaviorHash: "b", BehaviorConf: 0.6,
		Callees: []query.Callee{
			{Name: "validateCard", ResolutionMethod: "name-resolved", Confidence: 0.9},
			{Name: "log", ResolutionMethod: "unresolved-external", Confidence: 0.6},
		},
	}); err != nil {
		t.Fatal(err)
	}
	got := map[string]query.Callee{}
	for _, c := range st.Snapshot("r", "HEAD").Callees["Charge"] {
		got[c.Name] = c
	}
	if c := got["validateCard"]; c.ResolutionMethod != "name-resolved" || c.Confidence != 0.9 {
		t.Fatalf("resolved callee round-trip wrong: %+v", c)
	}
	if c := got["log"]; c.ResolutionMethod != "unresolved-external" || c.Confidence != 0.6 {
		t.Fatalf("external callee round-trip wrong: %+v", c)
	}
}

// ClearRef drops a ref's symbols/callees/files so a reindex replaces them.
func TestSQLiteClearRef(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Put("r", "HEAD", "A", rec("a", "s", "b", 1, "B"))
	st.PutFile("r", "HEAD", query.File{Path: "a.go", Content: "x"})
	if err := st.ClearRef("r", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.SymbolAt("r", "A", "HEAD"); ok {
		t.Fatal("ClearRef must remove symbols")
	}
	if len(st.Snapshot("r", "HEAD").Callees) != 0 {
		t.Fatal("ClearRef must remove callee edges")
	}
	if len(st.Files("r", "HEAD")) != 0 {
		t.Fatal("ClearRef must remove ref file references")
	}
}

// Files are content-addressed: identical content across refs stores one blob,
// distinct content stores another — storage ∝ unique content (§4.3/§9).
func TestSQLiteFileContentAddressedDedup(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	same := "package a\nfunc F(){}\n"
	diff := "package a\nfunc G(){}\n"
	for _, f := range []struct{ ref, content string }{
		{"v1", same}, {"v2", same}, {"v3", diff},
	} {
		if err := st.PutFile("r", f.ref, query.File{Path: "a.go", Content: f.content}); err != nil {
			t.Fatal(err)
		}
	}

	var blobs int
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM file_blobs`).Scan(&blobs); err != nil {
		t.Fatal(err)
	}
	if blobs != 2 {
		t.Fatalf("identical content across v1/v2 must dedup: want 2 blobs (same+diff), got %d", blobs)
	}
	// content is still readable per ref through the blob join
	if fs := st.Files("r", "v1"); len(fs) != 1 || fs[0].Content != same {
		t.Fatalf("v1 files: %+v", fs)
	}
	if fs := st.Files("r", "v3"); len(fs) != 1 || fs[0].Content != diff {
		t.Fatalf("v3 files: %+v", fs)
	}
}
