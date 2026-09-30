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
	if tables["ref_syms"] != 2 {
		t.Fatalf("ref_syms rows = %d, want 2 (%v)", tables["ref_syms"], tables)
	}
	if _, ok := tables["ext_refs"]; !ok {
		t.Fatal("DBStats must report every index table, including ext_refs")
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
	if err := st.db.QueryRow(`SELECT COUNT(*) FROM blobs`).Scan(&blobs); err != nil {
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

// v2's point: a symbol unchanged between two refs is ONE stored record and
// one edge set, however many refs list it; a changed one adds exactly one.
func TestSQLiteSymbolRecordsDedupAcrossRefs(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	for _, ref := range []string{"v1", "v2", "v3"} {
		st.Put("r", ref, "pkg.A", rec("a", "s", "b", 1, "pkg.B"))
		st.Put("r", ref, "pkg.B", rec("b", "s", "b2", 1))
	}
	st.Put("r", "v3", "pkg.A", rec("a2", "s", "b3", 1, "pkg.B")) // A changed at v3
	count := func(tbl string) (n int) {
		st.db.QueryRow(`SELECT COUNT(*) FROM ` + tbl).Scan(&n)
		return
	}
	if n := count("syms"); n != 3 {
		t.Fatalf("syms = %d, want 3 (A, B, A@v3)", n)
	}
	if n := count("edge_sets"); n != 1 {
		t.Fatalf("edge_sets = %d, want 1 (A's callee set is the same in every ref)", n)
	}
	if n := count("ref_syms"); n != 6 {
		t.Fatalf("ref_syms = %d, want 6", n)
	}
	if got := st.Snapshot("r", "v3").Callees["pkg.A"]; len(got) != 1 || got[0].Name != "pkg.B" {
		t.Fatalf("v3 callees: %+v", got)
	}
	// Reindexing a ref drops what only it referenced, and nothing shared.
	if err := st.ClearRef("r", "v3"); err != nil {
		t.Fatal(err)
	}
	if n := count("syms"); n != 2 {
		t.Fatalf("after ClearRef v3: syms = %d, want 2 (A@v3 reclaimed, shared rows kept)", n)
	}
	if _, ok := st.SymbolAt("r", "pkg.A", "v1"); !ok {
		t.Fatal("clearing v3 must not touch v1")
	}
}

// A full v1 index (symbols, callees, files with spans, external refs,
// monikers, ref commit + ruleset) reads back identically after migration.
func TestSQLiteMigratesFullV1Index(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = legacy.Exec(`
CREATE TABLE refs (repo TEXT NOT NULL, ref TEXT NOT NULL, commit_hash TEXT, manifest_hash TEXT, index_ver INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (repo, ref));
CREATE TABLE ref_history (repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL, present INTEGER NOT NULL DEFAULT 1,
  symbol_hash TEXT, signature_hash TEXT, behavior_hash TEXT, behavior_conf REAL, direct_conf REAL NOT NULL DEFAULT 1,
  lang TEXT NOT NULL DEFAULT '', is_test INTEGER NOT NULL DEFAULT 0, PRIMARY KEY (repo, ref, name));
CREATE TABLE callees (repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL, callee TEXT NOT NULL,
  resolution_method TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 1, PRIMARY KEY (repo, ref, name, callee));
CREATE TABLE file_blobs (hash TEXT PRIMARY KEY, content TEXT NOT NULL);
CREATE TABLE ref_files (repo TEXT NOT NULL, ref TEXT NOT NULL, path TEXT NOT NULL, blob_hash TEXT NOT NULL, PRIMARY KEY (repo, ref, path));
CREATE TABLE file_symbols (repo TEXT NOT NULL, ref TEXT NOT NULL, path TEXT NOT NULL, name TEXT NOT NULL, start_line INTEGER, end_line INTEGER);
CREATE TABLE symbol_monikers (repo TEXT NOT NULL, ref TEXT NOT NULL, symbol TEXT NOT NULL, moniker TEXT NOT NULL, PRIMARY KEY (repo, ref, symbol));
INSERT INTO refs VALUES('r','3.7.2','abc123','',2);
INSERT INTO ref_history VALUES('r','3.7.2','pkg.A',1,'sh','sig','bh',0.9,0.9,'cpp',0);
INSERT INTO ref_history VALUES('r','3.7.2','pkg.B',1,'sh2','sig2','bh2',1,1,'cpp',1);
INSERT INTO callees VALUES('r','3.7.2','pkg.A','pkg.B','name-resolved',0.9);
INSERT INTO callees VALUES('r','3.7.2','pkg.A','std::sort','unresolved-external',0.6);
INSERT INTO file_blobs VALUES('h1','void A() {}');
INSERT INTO ref_files VALUES('r','3.7.2','pkg/a.cpp','h1');
INSERT INTO file_symbols VALUES('r','3.7.2','pkg/a.cpp','A',1,1);
INSERT INTO symbol_monikers VALUES('r','3.7.2','pkg.A','m-A');`)
	if err != nil {
		t.Fatal(err)
	}
	legacy.Close()

	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if st.tableExists("ref_history") {
		t.Fatal("v1 tables must be dropped after migration")
	}
	a, ok := st.SymbolAt("r", "pkg.A", "3.7.2")
	if !ok || a.BehaviorHash != "bh" || a.Lang != "cpp" || a.BehaviorConf != 0.9 {
		t.Fatalf("pkg.A: %+v %v", a, ok)
	}
	if b, _ := st.SymbolAt("r", "pkg.B", "3.7.2"); !b.IsTest {
		t.Fatal("is_test must survive")
	}
	cs := st.Snapshot("r", "3.7.2").Callees["pkg.A"]
	if len(cs) != 2 {
		t.Fatalf("callees: %+v", cs)
	}
	fs := st.Files("r", "3.7.2")
	if len(fs) != 1 || fs[0].Content != "void A() {}" || len(fs[0].Symbols) != 1 || fs[0].Symbols[0].Name != "A" {
		t.Fatalf("files: %+v", fs)
	}
	if st.MonikersAt("r", "3.7.2")["pkg.A"] != "m-A" {
		t.Fatal("monikers must survive")
	}
	if man, ok := st.Manifest("r", "3.7.2"); !ok || man.Commit != "abc123" {
		t.Fatalf("commit: %+v", man)
	}
	if v, ok := st.IndexVersion("r", "3.7.2"); !ok || v != 2 {
		t.Fatalf("index_ver: %d %v", v, ok)
	}
	// Reopening a migrated store is a no-op.
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if len(st2.SymbolsAt("r", "3.7.2")) != 2 {
		t.Fatal("reopen must keep the migrated data")
	}
}
