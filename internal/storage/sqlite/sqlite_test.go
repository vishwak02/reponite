//go:build sqlite

package sqlite

import (
	"database/sql"
	"path/filepath"
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

// DBStats exposes the physical index for the dashboard's database view: the
// file path and per-table row counts.
func TestSQLiteDBStats(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Put("r", "HEAD", "r.A", rec("a", "s", "b", 1, "B"))
	st.Put("r", "HEAD", "r.B", rec("b", "s", "b", 1))
	path, tables := st.DBStats()
	if path != ":memory:" {
		t.Fatalf("DBStats path = %q, want :memory:", path)
	}
	if tables["ref_history"] != 2 {
		t.Fatalf("ref_history rows = %d, want 2 (%v)", tables["ref_history"], tables)
	}
	if _, ok := tables["external_refs"]; !ok {
		t.Fatal("DBStats must report every index table, including external_refs")
	}
}

// Opening a database created by an OLDER reponite must migrate cleanly. This
// caught a real upgrade break: an index over a column that migrate() adds was
// declared in the base schema, where CREATE TABLE IF NOT EXISTS is a no-op on
// an existing table — so the column did not exist yet and Open failed for
// every previously indexed repo.
func TestSQLiteOpensLegacyDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "old.db")

	// A pre-Phase-6b external_refs table: no target_symbol, no symbol_monikers.
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	// A pre-is_test ref_history: the CREATE TABLE IF NOT EXISTS in the base
	// schema is a no-op against it, so every column added later must come from
	// migrate() or Open fails for this database (invariant 8).
	_, err = legacy.Exec(`
CREATE TABLE refs (repo TEXT NOT NULL, ref TEXT NOT NULL, commit_hash TEXT, indexed_at TEXT, PRIMARY KEY (repo, ref));
CREATE TABLE ref_history (
  repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1,
  symbol_hash TEXT, signature_hash TEXT, behavior_hash TEXT, behavior_conf REAL,
  PRIMARY KEY (repo, ref, name)
);
INSERT INTO ref_history(repo, ref, name, present, symbol_hash, signature_hash, behavior_hash, behavior_conf)
VALUES('web','HEAD','svc.Fetch',1,'sh','sig','bh',1.0);
CREATE TABLE external_refs (
  repo TEXT NOT NULL, ref TEXT NOT NULL, from_name TEXT NOT NULL,
  target_module TEXT NOT NULL, target_name TEXT NOT NULL,
  resolution_method TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 0.6,
  PRIMARY KEY (repo, ref, from_name, target_module, target_name)
);
INSERT INTO external_refs(repo, ref, from_name, target_module, target_name, resolution_method, confidence)
VALUES('web','HEAD','web.fetch','github.com/acme/api','getUser','import-resolved',0.75);`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatalf("opening a legacy index must migrate, not fail: %v", err)
	}
	defer st.Close()

	// The pre-existing row survives and reads through the new columns.
	hits := st.ExternalRefsTo("github.com/acme/api", "getUser")
	if len(hits) != 1 || hits[0].Caller != "web.fetch" {
		t.Fatalf("legacy rows must survive migration: %+v", hits)
	}
	if hits[0].TargetSymbol != "" || hits[0].TargetSignatureHash != "" {
		t.Fatalf("migrated columns must default to empty (unknown), got %+v", hits[0])
	}
	// The pre-existing symbol still reads, with the migrated columns defaulting
	// to their zero values rather than erroring.
	sym, ok := st.SymbolAt("web", "svc.Fetch", "HEAD")
	if !ok || !sym.Present {
		t.Fatal("a symbol stored before the migration must still read back")
	}
	if sym.IsTest {
		t.Error("a migrated is_test column must default to false, not garbage")
	}
	if len(st.SymbolsAt("web", "HEAD")) != 1 {
		t.Fatalf("SymbolsAt must read the migrated table: %v", st.SymbolsAt("web", "HEAD"))
	}

	// And the new tables/queries work on the migrated database.
	if err := st.PutMonikers("web", "HEAD", map[string]string{"web.fetch": "moniker"}); err != nil {
		t.Fatalf("symbol_monikers must exist after migration: %v", err)
	}
	if len(st.ExternalRefsToSymbol("moniker")) != 0 {
		t.Fatal("no reference targets that moniker yet")
	}
}

// External refs + module_path survive a store round-trip and drive the
// module-resolved half of ximpact (§8B). ClearRef drops a ref's external refs.
func TestSQLiteExternalRefsAndModulePath(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetModulePath("web", "github.com/acme/web"); err != nil {
		t.Fatal(err)
	}
	if got := st.ModulePath("web"); got != "github.com/acme/web" {
		t.Fatalf("ModulePath round-trip = %q", got)
	}
	if st.ModulePath("nope") != "" {
		t.Fatal("unknown repo module must be empty")
	}

	refs := []query.ExternalRef{
		{From: "web.fetch", Module: "github.com/acme/api", Name: "getUser", ResolutionMethod: "import-resolved", Confidence: 0.75, TargetSignatureHash: "sigV1"},
		{From: "web.fetch", Module: "github.com/acme/api", Name: "listUsers", ResolutionMethod: "import-resolved", Confidence: 0.75},
	}
	if err := st.PutExternalRefs("web", "HEAD", refs); err != nil {
		t.Fatal(err)
	}
	hits := st.ExternalRefsTo("github.com/acme/api", "getUser")
	if len(hits) != 1 || hits[0].Repo != "web" || hits[0].Caller != "web.fetch" || hits[0].Confidence != 0.75 {
		t.Fatalf("ExternalRefsTo round-trip: %+v", hits)
	}
	// §8B.3 per-caller skew: the captured target contract round-trips; an
	// uncaptured one stays "" (unknown), never invented.
	if hits[0].TargetSignatureHash != "sigV1" {
		t.Fatalf("target_signature_hash round-trip: %+v", hits[0])
	}
	if h := st.ExternalRefsTo("github.com/acme/api", "listUsers"); len(h) != 1 || h[0].TargetSignatureHash != "" {
		t.Fatalf("uncaptured target signature must stay empty: %+v", h)
	}
	if len(st.ExternalRefsTo("github.com/acme/api", "listUsers")) != 1 {
		t.Fatal("second external ref must round-trip independently")
	}
	// Reindex replaces: PutExternalRefs for the ref drops the prior set.
	if err := st.PutExternalRefs("web", "HEAD", nil); err != nil {
		t.Fatal(err)
	}
	if len(st.ExternalRefsTo("github.com/acme/api", "getUser")) != 0 {
		t.Fatal("empty PutExternalRefs must clear the ref's external refs")
	}
	// Phase 6b: SCIP monikers round-trip, are matched exactly, and ClearRef
	// drops them with the rest of the ref.
	if err := st.PutMonikers("web", "HEAD", map[string]string{"web.fetch": "scip-go gomod github.com/acme/web v1 `svc`/fetch()."}); err != nil {
		t.Fatal(err)
	}
	if got := st.MonikersAt("web", "HEAD"); got["web.fetch"] == "" {
		t.Fatalf("moniker round-trip: %v", got)
	}
	scipRef := query.ExternalRef{From: "web.fetch", TargetSymbol: "scip-go gomod github.com/acme/api v1 `pkg/user`/GetUser().",
		ResolutionMethod: "scip-resolved", Confidence: 0.95}
	if err := st.PutExternalRefs("web", "prod", []query.ExternalRef{scipRef}); err != nil {
		t.Fatal(err)
	}
	if h := st.ExternalRefsToSymbol(scipRef.TargetSymbol); len(h) != 1 || h[0].TargetSymbol != scipRef.TargetSymbol {
		t.Fatalf("ExternalRefsToSymbol round-trip: %+v", h)
	}
	if h := st.ExternalRefsToSymbol("scip-go gomod other v1 `x`/Nope()."); len(h) != 0 {
		t.Fatalf("a different moniker must not match: %+v", h)
	}
	if err := st.ClearRef("web", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if len(st.MonikersAt("web", "HEAD")) != 0 {
		t.Fatal("ClearRef must remove monikers")
	}

	// ClearRef also removes external refs.
	st.PutExternalRefs("web", "HEAD", refs)
	if err := st.ClearRef("web", "HEAD"); err != nil {
		t.Fatal(err)
	}
	if len(st.ExternalRefsTo("github.com/acme/api", "getUser")) != 0 {
		t.Fatal("ClearRef must remove external refs")
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
