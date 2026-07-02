//go:build sqlite

// sqlite.go is the production query.Store backed by pure-Go SQLite. It persists
// the records the in-memory store holds and serves the query layer unchanged
// (the pure logic in content/processing/query is backend-agnostic, ADR-018).
// This first adapter uses a query-serving schema (ref_history/callees/files/
// manifest_blobs); the full content-addressed dedup tables from §9 are a later
// refinement driven by the already-built content.Dedup/Manifest logic.
package sqlite

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

var _ query.Store = (*Store)(nil)

// Store is a SQLite-backed query.Store.
type Store struct{ db *sql.DB }

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
  PRIMARY KEY (repo, ref, name)
);
CREATE INDEX IF NOT EXISTS idx_hist_symbol ON ref_history(repo, name);
CREATE TABLE IF NOT EXISTS callees (
  repo TEXT NOT NULL, ref TEXT NOT NULL, name TEXT NOT NULL,
  callee TEXT NOT NULL, confidence REAL NOT NULL DEFAULT 1,
  PRIMARY KEY (repo, ref, name, callee)
);
CREATE TABLE IF NOT EXISTS files (
  repo TEXT NOT NULL, ref TEXT NOT NULL, path TEXT NOT NULL,
  content TEXT NOT NULL,
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
	return &Store{db: db}, nil
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
		`INSERT INTO ref_history(repo, ref, name, present, symbol_hash, signature_hash, behavior_hash, behavior_conf)
		 VALUES(?,?,?,1,?,?,?,?)
		 ON CONFLICT(repo, ref, name) DO UPDATE SET
		   present=1, symbol_hash=excluded.symbol_hash, signature_hash=excluded.signature_hash,
		   behavior_hash=excluded.behavior_hash, behavior_conf=excluded.behavior_conf`,
		repo, ref, name, string(rec.SymbolHash), string(rec.SignatureHash), string(rec.BehaviorHash), rec.BehaviorConf); err != nil {
		tx.Rollback()
		return err
	}
	if _, err := tx.Exec(`DELETE FROM callees WHERE repo=? AND ref=? AND name=?`, repo, ref, name); err != nil {
		tx.Rollback()
		return err
	}
	for _, c := range rec.Callees {
		if _, err := tx.Exec(
			`INSERT OR REPLACE INTO callees(repo, ref, name, callee, confidence) VALUES(?,?,?,?,?)`,
			repo, ref, name, c.Name, c.Confidence); err != nil {
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
	if _, err := tx.Exec(
		`INSERT INTO files(repo, ref, path, content) VALUES(?,?,?,?)
		 ON CONFLICT(repo, ref, path) DO UPDATE SET content=excluded.content`,
		repo, ref, f.Path, f.Content); err != nil {
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

// --- reads (query.Store) ---

func (s *Store) Repos() []string {
	return s.scanStrings(`SELECT DISTINCT repo FROM refs ORDER BY repo`)
}

func (s *Store) Refs(repo string) []string {
	return s.scanStrings(`SELECT ref FROM refs WHERE repo=? ORDER BY ref`, repo)
}

func (s *Store) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	var present int
	var sig, beh sql.NullString
	var conf sql.NullFloat64
	err := s.db.QueryRow(
		`SELECT present, signature_hash, behavior_hash, behavior_conf FROM ref_history WHERE repo=? AND ref=? AND name=?`,
		repo, ref, symbol).Scan(&present, &sig, &beh, &conf)
	if err != nil {
		return query.SymbolRef{Present: false}, false
	}
	return query.SymbolRef{
		Present:       present == 1,
		SignatureHash: content.Hash(sig.String),
		BehaviorHash:  content.Hash(beh.String),
		BehaviorConf:  conf.Float64,
	}, true
}

func (s *Store) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	out := map[string]query.SymbolRef{}
	rows, err := s.db.Query(
		`SELECT name, present, signature_hash, behavior_hash, behavior_conf FROM ref_history WHERE repo=? AND ref=?`,
		repo, ref)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var present int
		var sig, beh sql.NullString
		var conf sql.NullFloat64
		if rows.Scan(&name, &present, &sig, &beh, &conf) != nil {
			continue
		}
		if present != 1 {
			continue
		}
		out[name] = query.SymbolRef{
			Present: true, SignatureHash: content.Hash(sig.String),
			BehaviorHash: content.Hash(beh.String), BehaviorConf: conf.Float64,
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
	crows, err := s.db.Query(`SELECT name, callee, confidence FROM callees WHERE repo=? AND ref=?`, repo, ref)
	if err == nil {
		for crows.Next() {
			var name, callee string
			var conf float64
			if crows.Scan(&name, &callee, &conf) == nil {
				snap.Callees[name] = append(snap.Callees[name], query.Callee{Name: callee, Confidence: conf})
			}
		}
		crows.Close()
	}
	return snap
}

func (s *Store) Files(repo, ref string) []query.File {
	byPath := map[string]*query.File{}
	var order []string
	rows, err := s.db.Query(`SELECT path, content FROM files WHERE repo=? AND ref=? ORDER BY path`, repo, ref)
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
