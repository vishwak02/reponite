//go:build sqlite

// sqlite.go is the production query.Store backed by pure-Go SQLite. It persists
// the records the in-memory store holds and serves the query layer unchanged
// (the pure logic in content/processing/query is backend-agnostic, ADR-018).
// Symbol identity uses a query-serving schema (ref_history/callees/manifest_blobs);
// file content is content-addressed (file_blobs keyed by content.BlobHash, with
// per-ref ref_files rows), so indexing N refs costs storage ∝ unique file
// content (§4.3/§9). Each CALLS edge stores its resolution_method (invariant 5).
package sqlite

import (
	"database/sql"
	"fmt"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

var _ query.Store = (*Store)(nil)

// Store is a SQLite-backed query.Store.
type Store struct {
	db   *sql.DB
	path string // on-disk path (":memory:" for tests), for the dashboard DB view
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS refs (
  repo TEXT NOT NULL, ref TEXT NOT NULL,
  commit_hash TEXT, manifest_hash TEXT,
  PRIMARY KEY (repo, ref)
);
CREATE TABLE IF NOT EXISTS ref_history (
  repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL,
  present INTEGER NOT NULL DEFAULT 1,
  symbol_hash TEXT, signature_hash TEXT, behavior_hash TEXT, behavior_conf REAL,
  direct_conf REAL NOT NULL DEFAULT 1,
  lang TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (repo, ref, name)
);
CREATE INDEX IF NOT EXISTS idx_hist_symbol ON ref_history(repo, name);
CREATE TABLE IF NOT EXISTS callees (
  repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL,
  callee TEXT NOT NULL, resolution_method TEXT NOT NULL DEFAULT '',
  confidence REAL NOT NULL DEFAULT 1,
  PRIMARY KEY (repo, ref, name, callee)
);
-- Files are content-addressed: identical content is stored once in file_blobs
-- (keyed by content.BlobHash) and referenced per (repo,ref,path) from ref_files,
-- so indexing N refs of a repo costs storage ∝ unique file content (§4.3/§9).
CREATE TABLE IF NOT EXISTS file_blobs (
  hash TEXT PRIMARY KEY,
  content TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ref_files (
  repo TEXT NOT NULL, ref TEXT NOT NULL, path TEXT NOT NULL,
  blob_hash TEXT NOT NULL REFERENCES file_blobs(hash),
  PRIMARY KEY (repo, ref, path)
);
CREATE TABLE IF NOT EXISTS file_symbols (
  repo TEXT NOT NULL, ref TEXT NOT NULL, path TEXT NOT NULL,
  name TEXT NOT NULL, start_line INTEGER, end_line INTEGER
);
CREATE TABLE IF NOT EXISTS manifest_blobs (
  repo TEXT NOT NULL, ref TEXT NOT NULL, blob TEXT NOT NULL,
  PRIMARY KEY (repo, ref, blob)
);
-- Cross-repo dependency edges (§8B/§9A.2): a caller symbol's reference to a
-- symbol outside its own repo, resolved through the caller's import bindings.
-- Indexed by (target_module, target_name) so ximpact matches fleet-wide callers.
CREATE TABLE IF NOT EXISTS external_refs (
  repo TEXT NOT NULL, ref TEXT NOT NULL, from_name TEXT NOT NULL,
  target_module TEXT NOT NULL, target_name TEXT NOT NULL,
  resolution_method TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 0.6,
  PRIMARY KEY (repo, ref, from_name, target_module, target_name)
);
CREATE INDEX IF NOT EXISTS idx_extref_target ON external_refs(target_module, target_name);
-- Per-repo module/package identity (§8B.2): a symbol's cross-repo identity is
-- (module_path, name), so a target's module resolves its precise fleet callers.
CREATE TABLE IF NOT EXISTS repo_modules (
  repo TEXT PRIMARY KEY, module_path TEXT NOT NULL
);
`

// Open opens (creating if needed) a SQLite-backed store at path (use
// ":memory:" for tests). WAL is enabled for crash-safe concurrent reads.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return s, nil
}

// DBStats reports the index database's file path and per-table row counts, for
// the dashboard's index/database view — making the stored model tangible. The
// counts are the physical persistence behind the logical query.Overview.
func (s *Store) DBStats() (string, map[string]int64) {
	tables := []string{"refs", "ref_history", "callees", "external_refs", "repo_modules", "file_blobs", "ref_files", "file_symbols", "manifest_blobs"}
	counts := make(map[string]int64, len(tables))
	for _, t := range tables {
		var n int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n); err == nil {
			counts[t] = n
		}
	}
	return s.path, counts
}

// migrate applies additive schema changes introduced after the initial schema,
// so a store opened on an older index.db gains new columns without a rebuild.
// Each statement is idempotent: adding a column that already exists is ignored.
func (s *Store) migrate() error {
	for _, stmt := range []string{
		`ALTER TABLE callees ADD COLUMN resolution_method TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ref_history ADD COLUMN direct_conf REAL NOT NULL DEFAULT 1`,
		`ALTER TABLE ref_history ADD COLUMN lang TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := s.db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column name") {
			return err
		}
	}
	return nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// --- writes (used by the indexer) ---

func (s *Store) upsertRef(repo, ref string) error {
	_, err := s.db.Exec(
		`INSERT INTO refs(repo, ref) VALUES(?, ?) ON CONFLICT(repo, ref) DO NOTHING`, repo, ref)
	return err
}

// AddRef records/updates a ref's commit and manifest hash.
func (s *Store) AddRef(repo, ref, commit, manifestHash string) error {
	_, err := s.db.Exec(
		`INSERT INTO refs(repo, ref, commit_hash, manifest_hash) VALUES(?,?,?,?)
		 ON CONFLICT(repo, ref) DO UPDATE SET commit_hash=excluded.commit_hash, manifest_hash=excluded.manifest_hash`,
		repo, ref, commit, manifestHash)
	return err
}

// ClearRef drops a ref's symbols, callee edges, and file references so a reindex
// replaces rather than accumulates. Content-addressed file_blobs are left intact
// (shared across refs; reclaimed by GC), as is the refs row (reindex updates it).
func (s *Store) ClearRef(repo, ref string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	for _, q := range []string{
		`DELETE FROM ref_history WHERE repo=? AND ref=?`,
		`DELETE FROM callees WHERE repo=? AND ref=?`,
		`DELETE FROM ref_files WHERE repo=? AND ref=?`,
		`DELETE FROM file_symbols WHERE repo=? AND ref=?`,
		`DELETE FROM external_refs WHERE repo=? AND ref=?`,
	} {
		if _, err := tx.Exec(q, repo, ref); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// Put stores a symbol at a ref (ref_history + its callees).
func (s *Store) Put(repo, ref, name string, rec storage.SymbolRecord) error {
	if err := s.upsertRef(repo, ref); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO ref_history(repo, ref, name, present, symbol_hash, signature_hash, behavior_hash, behavior_conf, direct_conf, lang)
		 VALUES(?,?,?,1,?,?,?,?,?,?)
		 ON CONFLICT(repo, ref, name) DO UPDATE SET
		   present=1, symbol_hash=excluded.symbol_hash, signature_hash=excluded.signature_hash,
		   behavior_hash=excluded.behavior_hash, behavior_conf=excluded.behavior_conf, direct_conf=excluded.direct_conf, lang=excluded.lang`,
		repo, ref, name, string(rec.SymbolHash), string(rec.SignatureHash), string(rec.BehaviorHash), rec.BehaviorConf, rec.DirectConf, rec.Lang); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM callees WHERE repo=? AND ref=? AND name=?`, repo, ref, name); err != nil {
		tx.Rollback()
		return err
	}
	for _, c := range rec.Callees {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO callees(repo, ref, name, callee, resolution_method, confidence) VALUES(?,?,?,?,?,?)`,
			repo, ref, name, c.Name, c.ResolutionMethod, c.Confidence); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// PutFile stores a file and its symbol spans at a ref.
func (s *Store) PutFile(repo, ref string, f query.File) error {
	if err := s.upsertRef(repo, ref); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	blob := string(content.BlobHash([]byte(f.Content)))
	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO file_blobs(hash, content) VALUES(?,?)`, blob, f.Content); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO ref_files(repo, ref, path, blob_hash) VALUES(?,?,?,?)
		 ON CONFLICT(repo, ref, path) DO UPDATE SET blob_hash=excluded.blob_hash`,
		repo, ref, f.Path, blob); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM file_symbols WHERE repo=? AND ref=? AND path=?`, repo, ref, f.Path); err != nil {
		tx.Rollback()
		return err
	}
	for _, sp := range f.Symbols {
		if _, err := tx.Exec(
			`INSERT INTO file_symbols(repo, ref, path, name, start_line, end_line) VALUES(?,?,?,?,?,?)`,
			repo, ref, f.Path, sp.Name, sp.StartLine, sp.EndLine); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// PutManifest stores a ref's manifest (blobs + identity).
func (s *Store) PutManifest(repo, ref string, man content.Manifest) error {
	if err := s.AddRef(repo, ref, man.Commit, string(man.Hash())); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM manifest_blobs WHERE repo=? AND ref=?`, repo, ref); err != nil {
		tx.Rollback()
		return err
	}
	for _, b := range man.Blobs {
		if _, err := tx.Exec(
			`INSERT OR IGNORE INTO manifest_blobs(repo, ref, blob) VALUES(?,?,?)`, repo, ref, string(b)); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// PutExternalRefs replaces a ref's cross-repo dependency edges (§8B).
func (s *Store) PutExternalRefs(repo, ref string, refs []query.ExternalRef) error {
	if err := s.upsertRef(repo, ref); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM external_refs WHERE repo=? AND ref=?`, repo, ref); err != nil {
		tx.Rollback()
		return err
	}
	for _, r := range refs {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO external_refs(repo, ref, from_name, target_module, target_name, resolution_method, confidence)
			 VALUES(?,?,?,?,?,?,?)`,
			repo, ref, r.From, r.Module, r.Name, r.ResolutionMethod, r.Confidence); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

// SetModulePath records repo's module/package identity (§8B.2).
func (s *Store) SetModulePath(repo, modulePath string) error {
	if modulePath == "" {
		return nil
	}
	_, err := s.db.Exec(
		`INSERT INTO repo_modules(repo, module_path) VALUES(?,?)
		 ON CONFLICT(repo) DO UPDATE SET module_path=excluded.module_path`,
		repo, modulePath)
	return err
}

// --- reads (query.Store) ---

func (s *Store) Repos() []string {
	return s.scanStrings(`SELECT DISTINCT repo FROM refs ORDER BY repo`)
}

func (s *Store) ModulePath(repo string) string {
	var mod sql.NullString
	if err := s.db.QueryRow(`SELECT module_path FROM repo_modules WHERE repo=?`, repo).Scan(&mod); err != nil {
		return ""
	}
	return mod.String
}

func (s *Store) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	rows, err := s.db.Query(
		`SELECT repo, ref, from_name, target_module, target_name, resolution_method, confidence
		 FROM external_refs WHERE target_module=? AND target_name=?
		 ORDER BY repo, ref, from_name`, module, name)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []query.ExternalRefHit
	for rows.Next() {
		var h query.ExternalRefHit
		if rows.Scan(&h.Repo, &h.Ref, &h.Caller, &h.Module, &h.Name, &h.ResolutionMethod, &h.Confidence) == nil {
			out = append(out, h)
		}
	}
	return out
}

func (s *Store) Refs(repo string) []string {
	return s.scanStrings(`SELECT ref FROM refs WHERE repo=? ORDER BY ref`, repo)
}

func (s *Store) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	var present int
	var sig, beh, lang sql.NullString
	var conf, dconf sql.NullFloat64
	err := s.db.QueryRow(
		`SELECT present, signature_hash, behavior_hash, behavior_conf, direct_conf, lang FROM ref_history WHERE repo=? AND ref=? AND name=?`,
		repo, ref, symbol).Scan(&present, &sig, &beh, &conf, &dconf, &lang)
	if err != nil {
		return query.SymbolRef{Present: false}, false
	}
	return query.SymbolRef{
		Present:       present == 1,
		Lang:          lang.String,
		SignatureHash: content.Hash(sig.String),
		BehaviorHash:  content.Hash(beh.String),
		BehaviorConf:  conf.Float64,
		DirectConf:    dconf.Float64,
	}, true
}

func (s *Store) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	out := map[string]query.SymbolRef{}
	rows, err := s.db.Query(
		`SELECT name, present, signature_hash, behavior_hash, behavior_conf, direct_conf, lang FROM ref_history WHERE repo=? AND ref=?`,
		repo, ref)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var present int
		var sig, beh, lang sql.NullString
		var conf, dconf sql.NullFloat64
		if rows.Scan(&name, &present, &sig, &beh, &conf, &dconf, &lang) != nil {
			continue
		}
		if present != 1 {
			continue
		}
		out[name] = query.SymbolRef{
			Present: true, Lang: lang.String, SignatureHash: content.Hash(sig.String),
			BehaviorHash: content.Hash(beh.String), BehaviorConf: conf.Float64, DirectConf: dconf.Float64,
		}
	}
	return out
}

func (s *Store) Snapshot(repo, ref string) query.RefSnapshot {
	snap := query.RefSnapshot{
		Symbols: map[string]query.SymbolFacts{},
		Callees: map[string][]query.Callee{},
	}
	rows, err := s.db.Query(
		`SELECT name, symbol_hash, signature_hash, behavior_hash FROM ref_history WHERE repo=? AND ref=?`, repo, ref)
	if err == nil {
		for rows.Next() {
			var name string
			var sh, sig, beh sql.NullString
			if rows.Scan(&name, &sh, &sig, &beh) == nil {
				snap.Symbols[name] = query.SymbolFacts{
					SymbolHash: content.Hash(sh.String), SignatureHash: content.Hash(sig.String), BehaviorHash: content.Hash(beh.String),
				}
			}
		}
		rows.Close()
	}
	crows, err := s.db.Query(`SELECT name, callee, resolution_method, confidence FROM callees WHERE repo=? AND ref=?`, repo, ref)
	if err == nil {
		for crows.Next() {
			var name, callee, method string
			var conf float64
			if crows.Scan(&name, &callee, &method, &conf) == nil {
				snap.Callees[name] = append(snap.Callees[name], query.Callee{Name: callee, ResolutionMethod: method, Confidence: conf})
			}
		}
		crows.Close()
	}
	return snap
}

func (s *Store) Files(repo, ref string) []query.File {
	byPath := map[string]*query.File{}
	var order []string
	rows, err := s.db.Query(
		`SELECT rf.path, fb.content FROM ref_files rf
		 JOIN file_blobs fb ON fb.hash = rf.blob_hash
		 WHERE rf.repo=? AND rf.ref=? ORDER BY rf.path`, repo, ref)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var path, cont string
		if rows.Scan(&path, &cont) == nil {
			byPath[path] = &query.File{Path: path, Content: cont}
			order = append(order, path)
		}
	}
	rows.Close()
	srows, err := s.db.Query(`SELECT path, name, start_line, end_line FROM file_symbols WHERE repo=? AND ref=?`, repo, ref)
	if err == nil {
		for srows.Next() {
			var path, name string
			var start, end int
			if srows.Scan(&path, &name, &start, &end) == nil {
				if f, ok := byPath[path]; ok {
					f.Symbols = append(f.Symbols, query.SymbolSpan{Name: name, StartLine: start, EndLine: end})
				}
			}
		}
		srows.Close()
	}
	out := make([]query.File, 0, len(order))
	for _, p := range order {
		out = append(out, *byPath[p])
	}
	return out
}

func (s *Store) Manifest(repo, ref string) (content.Manifest, bool) {
	var commit sql.NullString
	if err := s.db.QueryRow(`SELECT commit_hash FROM refs WHERE repo=? AND ref=?`, repo, ref).Scan(&commit); err != nil {
		return content.Manifest{}, false
	}
	man := content.Manifest{Ref: ref, Commit: commit.String}
	rows, err := s.db.Query(`SELECT blob FROM manifest_blobs WHERE repo=? AND ref=? ORDER BY blob`, repo, ref)
	if err == nil {
		for rows.Next() {
			var b string
			if rows.Scan(&b) == nil {
				man.Blobs = append(man.Blobs, content.Hash(b))
			}
		}
		rows.Close()
	}
	return man, true
}

func (s *Store) scanStrings(qy string, args ...interface{}) []string {
	rows, err := s.db.Query(qy, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if rows.Scan(&v) == nil {
			out = append(out, v)
		}
	}
	return out
}
