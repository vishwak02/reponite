//go:build sqlite

// sqlite.go is the production query.Store backed by pure-Go SQLite. It persists
// the records the in-memory store holds and serves the query layer unchanged
// (the pure logic in content/processing/query is backend-agnostic, ADR-018).
//
// Storage format v2 is content-addressed end to end, so indexing many refs of
// a repo costs storage in proportion to what actually differs between them
// (§4.3/§9). Consecutive release tags change a few percent of files, yet v1
// stored every symbol's hashes, every callee edge and every file's spans once
// PER REF (~10 MB a ref on a mid-size C++ repo). v2 interns every name to an
// integer and stores each distinct symbol record, callee-edge set, span set
// and external reference once; a ref is then a list of small integer rows
// pointing at them. Each CALLS edge keeps its resolution_method (invariant 5).
// A v1 index is migrated in place on Open.
package sqlite

import (
	"bytes"
	"compress/flate"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

var _ query.Store = (*Store)(nil)

// FormatVersion is the on-disk layout version (store_meta.format).
const FormatVersion = 2

// Store is a SQLite-backed query.Store.
type Store struct {
	db    *sql.DB
	path  string           // on-disk path (":memory:" for tests), for the dashboard DB view
	names map[string]int64 // interned-name cache
	refs  map[[2]string]int64
}

const schema = `
PRAGMA journal_mode = WAL;
PRAGMA synchronous = NORMAL;

CREATE TABLE IF NOT EXISTS store_meta (k TEXT PRIMARY KEY, v TEXT NOT NULL);

-- Every indexed (repo, ref). id is what every per-ref row points at.
CREATE TABLE IF NOT EXISTS refs_v2 (
  id INTEGER PRIMARY KEY,
  repo TEXT NOT NULL, ref TEXT NOT NULL,
  commit_hash TEXT NOT NULL DEFAULT '', manifest_hash TEXT NOT NULL DEFAULT '',
  index_ver INTEGER NOT NULL DEFAULT 0,
  UNIQUE (repo, ref)
);
-- Interned strings: symbol ids, callee names, file paths.
CREATE TABLE IF NOT EXISTS names (id INTEGER PRIMARY KEY, s TEXT NOT NULL UNIQUE);

-- A callee-edge set, stored once however many symbols/refs share it.
CREATE TABLE IF NOT EXISTS edge_sets (id INTEGER PRIMARY KEY, h BLOB NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS edges (
  set_id INTEGER NOT NULL, callee_id INTEGER NOT NULL,
  method TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 1,
  PRIMARY KEY (set_id, callee_id)
) WITHOUT ROWID;
-- A symbol record (its three hashes, confidences, language, test flag and its
-- edge set): identical records across refs are one row.
CREATE TABLE IF NOT EXISTS syms (
  id INTEGER PRIMARY KEY, h BLOB NOT NULL UNIQUE,
  symbol_hash TEXT NOT NULL, signature_hash TEXT NOT NULL, behavior_hash TEXT NOT NULL,
  behavior_conf REAL NOT NULL, direct_conf REAL NOT NULL,
  lang TEXT NOT NULL DEFAULT '', is_test INTEGER NOT NULL DEFAULT 0,
  edge_set INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_syms_edge_set ON syms(edge_set);
CREATE TABLE IF NOT EXISTS ref_syms (
  ref_id INTEGER NOT NULL, name_id INTEGER NOT NULL, sym_id INTEGER NOT NULL,
  PRIMARY KEY (ref_id, name_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_ref_syms_sym ON ref_syms(sym_id);

-- File content, stored once per distinct content (content.BlobHash), deflated:
-- source text is most of an index's bytes and compresses ~4x.
CREATE TABLE IF NOT EXISTS blobs (id INTEGER PRIMARY KEY, hash TEXT NOT NULL UNIQUE, z BLOB NOT NULL);
-- A file's symbol spans: they depend only on its content and path, so a file
-- unchanged across refs shares one set.
CREATE TABLE IF NOT EXISTS span_sets (id INTEGER PRIMARY KEY, h BLOB NOT NULL UNIQUE);
CREATE TABLE IF NOT EXISTS spans (
  set_id INTEGER NOT NULL, name_id INTEGER NOT NULL, start_line INTEGER NOT NULL, end_line INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_spans_set ON spans(set_id);
CREATE TABLE IF NOT EXISTS ref_files_v2 (
  ref_id INTEGER NOT NULL, path_id INTEGER NOT NULL, blob_id INTEGER NOT NULL, span_set INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (ref_id, path_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_ref_files_blob ON ref_files_v2(blob_id);
CREATE INDEX IF NOT EXISTS idx_ref_files_span ON ref_files_v2(span_set);

-- Cross-repo dependency edges (§8B/§9A.2), each distinct reference stored once.
CREATE TABLE IF NOT EXISTS ext_refs (
  id INTEGER PRIMARY KEY, h BLOB NOT NULL UNIQUE,
  from_id INTEGER NOT NULL, target_module TEXT NOT NULL, target_name TEXT NOT NULL,
  method TEXT NOT NULL DEFAULT '', confidence REAL NOT NULL DEFAULT 0.6,
  target_signature_hash TEXT NOT NULL DEFAULT '', target_symbol TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_ext_target ON ext_refs(target_name, target_module);
CREATE INDEX IF NOT EXISTS idx_ext_symbol ON ext_refs(target_symbol);
CREATE TABLE IF NOT EXISTS ref_ext_refs (
  ref_id INTEGER NOT NULL, ext_id INTEGER NOT NULL, PRIMARY KEY (ref_id, ext_id)
) WITHOUT ROWID;
CREATE INDEX IF NOT EXISTS idx_ref_ext_ext ON ref_ext_refs(ext_id);

-- SCIP monikers (§8B.4): a symbol's globally unique identity at a ref.
CREATE TABLE IF NOT EXISTS ref_monikers (
  ref_id INTEGER NOT NULL, name_id INTEGER NOT NULL, moniker TEXT NOT NULL,
  PRIMARY KEY (ref_id, name_id)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS ref_manifest (
  ref_id INTEGER NOT NULL, blob TEXT NOT NULL, PRIMARY KEY (ref_id, blob)
) WITHOUT ROWID;
-- Per-repo module/package identity (§8B.2).
CREATE TABLE IF NOT EXISTS repo_modules (repo TEXT PRIMARY KEY, module_path TEXT NOT NULL);
`

// Open opens (creating if needed) a SQLite-backed store at path (use
// ":memory:" for tests). A v1 index is migrated to v2 in place.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection: every statement sees the same (possibly :memory:) db,
	// and writes never contend with each other.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	s := &Store{db: db, path: path, names: map[string]int64{}, refs: map[[2]string]int64{}}
	if s.tableExists("ref_history") || s.tableExists("external_refs") || s.tableExists("refs") {
		if err := s.migrateV1(); err != nil {
			db.Close()
			return nil, fmt.Errorf("migrate v1 index: %w", err)
		}
	}
	if _, err := db.Exec(`INSERT INTO store_meta(k, v) VALUES('format', ?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, fmt.Sprint(FormatVersion)); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// DBStats reports the index database's file path and per-table row counts, for
// the dashboard's index/database view.
func (s *Store) DBStats() (string, map[string]int64) {
	tables := []string{"refs_v2", "names", "syms", "ref_syms", "edge_sets", "edges", "blobs", "ref_files_v2",
		"span_sets", "spans", "ext_refs", "ref_ext_refs", "ref_monikers", "ref_manifest", "repo_modules"}
	counts := make(map[string]int64, len(tables))
	for _, t := range tables {
		var n int64
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + t).Scan(&n); err == nil {
			counts[t] = n
		}
	}
	return s.path, counts
}

func (s *Store) tableExists(t string) bool {
	var n int
	_ = s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?`, t).Scan(&n)
	return n > 0
}

// --- interning -----------------------------------------------------------

type execer interface {
	Exec(string, ...interface{}) (sql.Result, error)
	QueryRow(string, ...interface{}) *sql.Row
}

func (s *Store) nameID(x execer, name string) (int64, error) {
	if id, ok := s.names[name]; ok {
		return id, nil
	}
	if _, err := x.Exec(`INSERT OR IGNORE INTO names(s) VALUES(?)`, name); err != nil {
		return 0, err
	}
	var id int64
	if err := x.QueryRow(`SELECT id FROM names WHERE s=?`, name).Scan(&id); err != nil {
		return 0, err
	}
	s.names[name] = id
	return id, nil
}

// lookupName returns an existing name's id without creating it.
func (s *Store) lookupName(name string) (int64, bool) {
	if id, ok := s.names[name]; ok {
		return id, true
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM names WHERE s=?`, name).Scan(&id); err != nil {
		return 0, false
	}
	s.names[name] = id
	return id, true
}

func (s *Store) refID(x execer, repo, ref string) (int64, error) {
	k := [2]string{repo, ref}
	if id, ok := s.refs[k]; ok {
		return id, nil
	}
	if _, err := x.Exec(`INSERT OR IGNORE INTO refs_v2(repo, ref) VALUES(?, ?)`, repo, ref); err != nil {
		return 0, err
	}
	var id int64
	if err := x.QueryRow(`SELECT id FROM refs_v2 WHERE repo=? AND ref=?`, repo, ref).Scan(&id); err != nil {
		return 0, err
	}
	s.refs[k] = id
	return id, nil
}

// lookupRef returns an existing ref's id without creating it.
func (s *Store) lookupRef(repo, ref string) (int64, bool) {
	k := [2]string{repo, ref}
	if id, ok := s.refs[k]; ok {
		return id, true
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM refs_v2 WHERE repo=? AND ref=?`, repo, ref).Scan(&id); err != nil {
		return 0, false
	}
	s.refs[k] = id
	return id, true
}

// digest is a 128-bit content address over length-prefixed fields.
type digest struct{ b []byte }

func (d *digest) str(v string) *digest {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(len(v)))
	d.b = append(append(d.b, n[:]...), v...)
	return d
}
func (d *digest) num(v int64) *digest {
	var n [8]byte
	binary.LittleEndian.PutUint64(n[:], uint64(v))
	d.b = append(d.b, n[:]...)
	return d
}
func (d *digest) flt(v float64) *digest { return d.num(int64(math.Float64bits(v))) }
func (d *digest) sum() []byte {
	h := sha256.Sum256(d.b)
	return h[:16]
}

// internSet stores a content-addressed row (or returns the existing one); fill
// writes its children only when the row is new.
func internSet(x execer, table string, h []byte, fill func(id int64) error) (int64, error) {
	var id int64
	if err := x.QueryRow(`SELECT id FROM `+table+` WHERE h=?`, h).Scan(&id); err == nil {
		return id, nil
	}
	res, err := x.Exec(`INSERT INTO `+table+`(h) VALUES(?)`, h)
	if err != nil {
		return 0, err
	}
	id, _ = res.LastInsertId()
	if fill != nil {
		if err := fill(id); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func deflate(s string) ([]byte, error) {
	var b bytes.Buffer
	w, err := flate.NewWriter(&b, flate.BestSpeed)
	if err != nil {
		return nil, err
	}
	if _, err := io.WriteString(w, s); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func inflate(z []byte) (string, error) {
	b, err := io.ReadAll(flate.NewReader(bytes.NewReader(z)))
	return string(b), err
}

// --- writes (used by the indexer) -----------------------------------------

func (s *Store) withTx(fn func(tx *sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		s.names, s.refs = map[string]int64{}, map[[2]string]int64{} // ids of a rolled-back tx
		return err
	}
	return tx.Commit()
}

// AddRef records/updates a ref's commit and manifest hash.
func (s *Store) AddRef(repo, ref, commit, manifestHash string) error {
	return s.withTx(func(tx *sql.Tx) error {
		id, err := s.refID(tx, repo, ref)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE refs_v2 SET commit_hash=?, manifest_hash=? WHERE id=?`, commit, manifestHash, id)
		return err
	})
}

// SetIndexVersion stamps the ruleset a ref was indexed under.
func (s *Store) SetIndexVersion(repo, ref string, v int) error {
	return s.withTx(func(tx *sql.Tx) error {
		id, err := s.refID(tx, repo, ref)
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE refs_v2 SET index_ver=? WHERE id=?`, v, id)
		return err
	})
}

// IndexVersion reports the ruleset a ref was indexed under (query.IndexVersioner).
func (s *Store) IndexVersion(repo, ref string) (int, bool) {
	var v int
	if err := s.db.QueryRow(`SELECT index_ver FROM refs_v2 WHERE repo=? AND ref=?`, repo, ref).Scan(&v); err != nil {
		return 0, false
	}
	return v, true
}

// RefByCommit finds a ref of repo already indexed at commit under ruleset
// indexVer — the same code, so indexing it again would store nothing new.
func (s *Store) RefByCommit(repo, commit string, indexVer int) (string, bool) {
	if commit == "" {
		return "", false
	}
	var ref string
	if err := s.db.QueryRow(`SELECT ref FROM refs_v2 WHERE repo=? AND commit_hash=? AND index_ver=? ORDER BY id LIMIT 1`,
		repo, commit, indexVer).Scan(&ref); err != nil {
		return "", false
	}
	return ref, true
}

// AliasRef makes `to` another label for the already-indexed ref `from` (same
// commit): its per-ref rows are copied — integers pointing at shared records —
// so the new label costs a few kilobytes and no re-parse.
func (s *Store) AliasRef(repo, from, to string) error {
	src, ok := s.lookupRef(repo, from)
	if !ok {
		return fmt.Errorf("%s@%s is not indexed", repo, from)
	}
	if err := s.ClearRef(repo, to); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		dst, err := s.refID(tx, repo, to)
		if err != nil {
			return err
		}
		for _, q := range []string{
			`UPDATE refs_v2 SET commit_hash=(SELECT commit_hash FROM refs_v2 WHERE id=?1), manifest_hash=(SELECT manifest_hash FROM refs_v2 WHERE id=?1),
			   index_ver=(SELECT index_ver FROM refs_v2 WHERE id=?1) WHERE id=?2`,
			`INSERT OR REPLACE INTO ref_syms(ref_id, name_id, sym_id) SELECT ?2, name_id, sym_id FROM ref_syms WHERE ref_id=?1`,
			`INSERT OR REPLACE INTO ref_files_v2(ref_id, path_id, blob_id, span_set) SELECT ?2, path_id, blob_id, span_set FROM ref_files_v2 WHERE ref_id=?1`,
			`INSERT OR IGNORE INTO ref_ext_refs(ref_id, ext_id) SELECT ?2, ext_id FROM ref_ext_refs WHERE ref_id=?1`,
			`INSERT OR REPLACE INTO ref_monikers(ref_id, name_id, moniker) SELECT ?2, name_id, moniker FROM ref_monikers WHERE ref_id=?1`,
			`INSERT OR IGNORE INTO ref_manifest(ref_id, blob) SELECT ?2, blob FROM ref_manifest WHERE ref_id=?1`,
		} {
			if _, err := tx.Exec(q, src, dst); err != nil {
				return err
			}
		}
		return nil
	})
}

// ClearRef drops a ref's symbols, files, external refs and monikers so a
// reindex replaces rather than accumulates, then reclaims shared rows no ref
// points at any more (content-addressed rows are kept while any ref uses them).
func (s *Store) ClearRef(repo, ref string) error {
	id, ok := s.lookupRef(repo, ref)
	if !ok {
		return nil
	}
	return s.withTx(func(tx *sql.Tx) error {
		for _, t := range []string{"ref_syms", "ref_files_v2", "ref_ext_refs", "ref_monikers"} {
			if _, err := tx.Exec(`DELETE FROM `+t+` WHERE ref_id=?`, id); err != nil {
				return err
			}
		}
		return gc(tx)
	})
}

// gc deletes content-addressed rows that no ref references.
func gc(tx *sql.Tx) error {
	for _, q := range []string{
		`DELETE FROM syms WHERE NOT EXISTS (SELECT 1 FROM ref_syms r WHERE r.sym_id = syms.id)`,
		`DELETE FROM edges WHERE NOT EXISTS (SELECT 1 FROM syms s WHERE s.edge_set = edges.set_id)`,
		`DELETE FROM edge_sets WHERE NOT EXISTS (SELECT 1 FROM syms s WHERE s.edge_set = edge_sets.id)`,
		`DELETE FROM spans WHERE NOT EXISTS (SELECT 1 FROM ref_files_v2 f WHERE f.span_set = spans.set_id)`,
		`DELETE FROM span_sets WHERE NOT EXISTS (SELECT 1 FROM ref_files_v2 f WHERE f.span_set = span_sets.id)`,
		`DELETE FROM blobs WHERE NOT EXISTS (SELECT 1 FROM ref_files_v2 f WHERE f.blob_id = blobs.id)`,
		`DELETE FROM ext_refs WHERE NOT EXISTS (SELECT 1 FROM ref_ext_refs r WHERE r.ext_id = ext_refs.id)`,
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

// Put stores a symbol at a ref (its record + its callee-edge set).
func (s *Store) Put(repo, ref, name string, rec storage.SymbolRecord) error {
	return s.withTx(func(tx *sql.Tx) error { return s.put(tx, repo, ref, name, rec) })
}

func (s *Store) put(tx *sql.Tx, repo, ref, name string, rec storage.SymbolRecord) error {
	rid, err := s.refID(tx, repo, ref)
	if err != nil {
		return err
	}
	nid, err := s.nameID(tx, name)
	if err != nil {
		return err
	}
	edgeSet, err := s.edgeSet(tx, rec.Callees)
	if err != nil {
		return err
	}
	isTest := int64(0)
	if rec.IsTest {
		isTest = 1
	}
	h := (&digest{}).str(string(rec.SymbolHash)).str(string(rec.SignatureHash)).str(string(rec.BehaviorHash)).
		flt(rec.BehaviorConf).flt(rec.DirectConf).str(rec.Lang).num(isTest).num(edgeSet).sum()
	var sid int64
	if err := tx.QueryRow(`SELECT id FROM syms WHERE h=?`, h).Scan(&sid); err != nil {
		res, err := tx.Exec(`INSERT INTO syms(h, symbol_hash, signature_hash, behavior_hash, behavior_conf, direct_conf, lang, is_test, edge_set)
			VALUES(?,?,?,?,?,?,?,?,?)`, h, string(rec.SymbolHash), string(rec.SignatureHash), string(rec.BehaviorHash),
			rec.BehaviorConf, rec.DirectConf, rec.Lang, isTest, edgeSet)
		if err != nil {
			return err
		}
		sid, _ = res.LastInsertId()
	}
	_, err = tx.Exec(`INSERT INTO ref_syms(ref_id, name_id, sym_id) VALUES(?,?,?)
		ON CONFLICT(ref_id, name_id) DO UPDATE SET sym_id=excluded.sym_id`, rid, nid, sid)
	return err
}

// edgeSet interns a callee list (order-independent) and returns its id; 0 = none.
func (s *Store) edgeSet(tx *sql.Tx, callees []query.Callee) (int64, error) {
	if len(callees) == 0 {
		return 0, nil
	}
	cs := append([]query.Callee(nil), callees...)
	sort.Slice(cs, func(i, j int) bool { return cs[i].Name < cs[j].Name })
	d := &digest{}
	for _, c := range cs {
		d.str(c.Name).str(c.ResolutionMethod).flt(c.Confidence)
	}
	return internSet(tx, "edge_sets", d.sum(), func(id int64) error {
		for _, c := range cs {
			cid, err := s.nameID(tx, c.Name)
			if err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR REPLACE INTO edges(set_id, callee_id, method, confidence) VALUES(?,?,?,?)`,
				id, cid, c.ResolutionMethod, c.Confidence); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutFile stores a file and its symbol spans at a ref.
func (s *Store) PutFile(repo, ref string, f query.File) error {
	return s.withTx(func(tx *sql.Tx) error { return s.putFile(tx, repo, ref, f) })
}

func (s *Store) putFile(tx *sql.Tx, repo, ref string, f query.File) error {
	rid, err := s.refID(tx, repo, ref)
	if err != nil {
		return err
	}
	pid, err := s.nameID(tx, f.Path)
	if err != nil {
		return err
	}
	hash := string(content.BlobHash([]byte(f.Content)))
	var bid int64
	if err := tx.QueryRow(`SELECT id FROM blobs WHERE hash=?`, hash).Scan(&bid); err != nil {
		z, err := deflate(f.Content)
		if err != nil {
			return err
		}
		res, err := tx.Exec(`INSERT INTO blobs(hash, z) VALUES(?,?)`, hash, z)
		if err != nil {
			return err
		}
		bid, _ = res.LastInsertId()
	}
	var spanSet int64
	if len(f.Symbols) > 0 {
		sp := append([]query.SymbolSpan(nil), f.Symbols...)
		d := (&digest{}).str(f.Path).str(hash)
		for _, x := range sp {
			d.str(x.Name).num(int64(x.StartLine)).num(int64(x.EndLine))
		}
		if spanSet, err = internSet(tx, "span_sets", d.sum(), func(id int64) error {
			for _, x := range sp {
				nid, err := s.nameID(tx, x.Name)
				if err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO spans(set_id, name_id, start_line, end_line) VALUES(?,?,?,?)`,
					id, nid, x.StartLine, x.EndLine); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	_, err = tx.Exec(`INSERT INTO ref_files_v2(ref_id, path_id, blob_id, span_set) VALUES(?,?,?,?)
		ON CONFLICT(ref_id, path_id) DO UPDATE SET blob_id=excluded.blob_id, span_set=excluded.span_set`, rid, pid, bid, spanSet)
	return err
}

// PutManifest stores a ref's manifest (blobs + identity).
func (s *Store) PutManifest(repo, ref string, man content.Manifest) error {
	if err := s.AddRef(repo, ref, man.Commit, string(man.Hash())); err != nil {
		return err
	}
	return s.withTx(func(tx *sql.Tx) error {
		rid, err := s.refID(tx, repo, ref)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM ref_manifest WHERE ref_id=?`, rid); err != nil {
			return err
		}
		for _, b := range man.Blobs {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO ref_manifest(ref_id, blob) VALUES(?,?)`, rid, string(b)); err != nil {
				return err
			}
		}
		return nil
	})
}

// PutExternalRefs replaces a ref's cross-repo dependency edges (§8B).
func (s *Store) PutExternalRefs(repo, ref string, refs []query.ExternalRef) error {
	return s.withTx(func(tx *sql.Tx) error { return s.putExternalRefs(tx, repo, ref, refs) })
}

func (s *Store) putExternalRefs(tx *sql.Tx, repo, ref string, refs []query.ExternalRef) error {
	rid, err := s.refID(tx, repo, ref)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ref_ext_refs WHERE ref_id=?`, rid); err != nil {
		return err
	}
	for _, r := range refs {
		fid, err := s.nameID(tx, r.From)
		if err != nil {
			return err
		}
		h := (&digest{}).str(r.From).str(r.Module).str(r.Name).str(r.ResolutionMethod).flt(r.Confidence).
			str(r.TargetSignatureHash).str(r.TargetSymbol).sum()
		var eid int64
		if err := tx.QueryRow(`SELECT id FROM ext_refs WHERE h=?`, h).Scan(&eid); err != nil {
			res, err := tx.Exec(`INSERT INTO ext_refs(h, from_id, target_module, target_name, method, confidence, target_signature_hash, target_symbol)
				VALUES(?,?,?,?,?,?,?,?)`, h, fid, r.Module, r.Name, r.ResolutionMethod, r.Confidence, r.TargetSignatureHash, r.TargetSymbol)
			if err != nil {
				return err
			}
			eid, _ = res.LastInsertId()
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO ref_ext_refs(ref_id, ext_id) VALUES(?,?)`, rid, eid); err != nil {
			return err
		}
	}
	return nil
}

// PutMonikers replaces a ref's symbol -> SCIP moniker map (§8B.4).
func (s *Store) PutMonikers(repo, ref string, mons map[string]string) error {
	return s.withTx(func(tx *sql.Tx) error { return s.putMonikers(tx, repo, ref, mons) })
}

func (s *Store) putMonikers(tx *sql.Tx, repo, ref string, mons map[string]string) error {
	rid, err := s.refID(tx, repo, ref)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ref_monikers WHERE ref_id=?`, rid); err != nil {
		return err
	}
	for sym, mon := range mons {
		if mon == "" {
			continue
		}
		nid, err := s.nameID(tx, sym)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO ref_monikers(ref_id, name_id, moniker) VALUES(?,?,?)`, rid, nid, mon); err != nil {
			return err
		}
	}
	return nil
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

// --- reads (query.Store) ----------------------------------------------------

func (s *Store) Repos() []string {
	return s.scanStrings(`SELECT DISTINCT repo FROM refs_v2 ORDER BY repo`)
}

func (s *Store) ModulePath(repo string) string {
	var mod sql.NullString
	if err := s.db.QueryRow(`SELECT module_path FROM repo_modules WHERE repo=?`, repo).Scan(&mod); err != nil {
		return ""
	}
	return mod.String
}

func (s *Store) Refs(repo string) []string {
	return s.scanStrings(`SELECT ref FROM refs_v2 WHERE repo=? ORDER BY ref`, repo)
}

const symCols = `s.signature_hash, s.behavior_hash, s.behavior_conf, s.direct_conf, s.lang, s.is_test`

func scanSymRef(sc interface{ Scan(...interface{}) error }, extra ...interface{}) (query.SymbolRef, error) {
	var sig, beh, lang string
	var conf, dconf float64
	var isTest int
	if err := sc.Scan(append(extra, &sig, &beh, &conf, &dconf, &lang, &isTest)...); err != nil {
		return query.SymbolRef{}, err
	}
	return query.SymbolRef{
		Present: true, Lang: lang, IsTest: isTest != 0,
		SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(beh),
		BehaviorConf: conf, DirectConf: dconf,
	}, nil
}

func (s *Store) SymbolAt(repo, symbol, ref string) (query.SymbolRef, bool) {
	rid, ok := s.lookupRef(repo, ref)
	if !ok {
		return query.SymbolRef{Present: false}, false
	}
	nid, ok := s.lookupName(symbol)
	if !ok {
		return query.SymbolRef{Present: false}, false
	}
	row := s.db.QueryRow(`SELECT `+symCols+` FROM ref_syms r JOIN syms s ON s.id = r.sym_id
		WHERE r.ref_id=? AND r.name_id=?`, rid, nid)
	sr, err := scanSymRef(row)
	if err != nil {
		return query.SymbolRef{Present: false}, false
	}
	return sr, true
}

func (s *Store) SymbolsAt(repo, ref string) map[string]query.SymbolRef {
	out := map[string]query.SymbolRef{}
	rid, ok := s.lookupRef(repo, ref)
	if !ok {
		return out
	}
	rows, err := s.db.Query(`SELECT n.s, `+symCols+` FROM ref_syms r
		JOIN syms s ON s.id = r.sym_id JOIN names n ON n.id = r.name_id WHERE r.ref_id=?`, rid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		sr, err := scanSymRef(rows, &name)
		if err == nil {
			out[name] = sr
		}
	}
	return out
}

func (s *Store) Snapshot(repo, ref string) query.RefSnapshot {
	snap := query.RefSnapshot{Symbols: map[string]query.SymbolFacts{}, Callees: map[string][]query.Callee{}}
	rid, ok := s.lookupRef(repo, ref)
	if !ok {
		return snap
	}
	rows, err := s.db.Query(`SELECT n.s, s.symbol_hash, s.signature_hash, s.behavior_hash FROM ref_syms r
		JOIN syms s ON s.id = r.sym_id JOIN names n ON n.id = r.name_id WHERE r.ref_id=?`, rid)
	if err == nil {
		for rows.Next() {
			var name, sh, sig, beh string
			if rows.Scan(&name, &sh, &sig, &beh) == nil {
				snap.Symbols[name] = query.SymbolFacts{SymbolHash: content.Hash(sh), SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(beh)}
			}
		}
		rows.Close()
	}
	crows, err := s.db.Query(`SELECT n.s, cn.s, e.method, e.confidence FROM ref_syms r
		JOIN syms s ON s.id = r.sym_id JOIN edges e ON e.set_id = s.edge_set
		JOIN names n ON n.id = r.name_id JOIN names cn ON cn.id = e.callee_id WHERE r.ref_id=?`, rid)
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
	rid, ok := s.lookupRef(repo, ref)
	if !ok {
		return nil
	}
	byPath := map[string]*query.File{}
	var order []string
	rows, err := s.db.Query(`SELECT n.s, b.z FROM ref_files_v2 f
		JOIN blobs b ON b.id = f.blob_id JOIN names n ON n.id = f.path_id WHERE f.ref_id=? ORDER BY n.s`, rid)
	if err != nil {
		return nil
	}
	for rows.Next() {
		var path string
		var z []byte
		if rows.Scan(&path, &z) == nil {
			cont, err := inflate(z)
			if err != nil {
				continue // a corrupt blob is skipped, never served as empty content
			}
			byPath[path] = &query.File{Path: path, Content: cont}
			order = append(order, path)
		}
	}
	rows.Close()
	srows, err := s.db.Query(`SELECT pn.s, sn.s, sp.start_line, sp.end_line FROM ref_files_v2 f
		JOIN spans sp ON sp.set_id = f.span_set JOIN names pn ON pn.id = f.path_id JOIN names sn ON sn.id = sp.name_id
		WHERE f.ref_id=?`, rid)
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
	var id int64
	var commit string
	if err := s.db.QueryRow(`SELECT id, commit_hash FROM refs_v2 WHERE repo=? AND ref=?`, repo, ref).Scan(&id, &commit); err != nil {
		return content.Manifest{}, false
	}
	man := content.Manifest{Ref: ref, Commit: commit}
	rows, err := s.db.Query(`SELECT blob FROM ref_manifest WHERE ref_id=? ORDER BY blob`, id)
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

const extSelect = `SELECT r.repo, r.ref, fn.s, e.target_module, e.target_name, e.method, e.confidence, e.target_signature_hash, e.target_symbol
	FROM ext_refs e JOIN ref_ext_refs re ON re.ext_id = e.id JOIN refs_v2 r ON r.id = re.ref_id JOIN names fn ON fn.id = e.from_id`

func (s *Store) scanExt(q string, args ...interface{}) []query.ExternalRefHit {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []query.ExternalRefHit
	for rows.Next() {
		var h query.ExternalRefHit
		if rows.Scan(&h.Repo, &h.Ref, &h.Caller, &h.Module, &h.Name, &h.ResolutionMethod, &h.Confidence, &h.TargetSignatureHash, &h.TargetSymbol) == nil {
			out = append(out, h)
		}
	}
	return out
}

func (s *Store) ExternalRefsTo(module, name string) []query.ExternalRefHit {
	// module is a module ROOT: an import path is the module path plus a package
	// path, so a reference to "<root>/pkg/user" must match root. substr() is
	// used rather than LIKE because module paths legitimately contain "_" and
	// "%", which LIKE would treat as wildcards.
	prefix := module + "/"
	return s.scanExt(extSelect+` WHERE e.target_name=? AND (e.target_module=? OR substr(e.target_module, 1, ?)=?)
		ORDER BY r.repo, r.ref, fn.s`, name, module, len(prefix), prefix)
}

// ExternalRefsToSymbol returns every reference targeting this exact SCIP
// moniker — the symbol-resolved cross-repo tier (Phase 6b).
func (s *Store) ExternalRefsToSymbol(moniker string) []query.ExternalRefHit {
	if moniker == "" {
		return nil
	}
	return s.scanExt(extSelect+` WHERE e.target_symbol=? ORDER BY r.repo, r.ref, fn.s`, moniker)
}

// MonikersAt returns the ref's symbol -> SCIP moniker map (empty without SCIP).
func (s *Store) MonikersAt(repo, ref string) map[string]string {
	out := map[string]string{}
	rid, ok := s.lookupRef(repo, ref)
	if !ok {
		return out
	}
	rows, err := s.db.Query(`SELECT n.s, m.moniker FROM ref_monikers m JOIN names n ON n.id = m.name_id WHERE m.ref_id=?`, rid)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sym, mon string
		if rows.Scan(&sym, &mon) == nil {
			out[sym] = mon
		}
	}
	return out
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

// --- v1 -> v2 migration ------------------------------------------------------

// migrateV1 rewrites a v1 index (one row per symbol / edge / span / external
// ref per ref) into v2 in one transaction, then drops the v1 tables. Every
// column added to v1 over time may be missing from an old file, so each is
// read only if present, defaulting to its zero value (invariant 8).
func (s *Store) migrateV1() error {
	cols := func(t string) map[string]bool {
		out := map[string]bool{}
		rows, err := s.db.Query(`PRAGMA table_info(` + t + `)`)
		if err != nil {
			return out
		}
		defer rows.Close()
		for rows.Next() {
			var cid, notnull, pk int
			var name, typ string
			var def sql.NullString
			if rows.Scan(&cid, &name, &typ, &notnull, &def, &pk) == nil {
				out[name] = true
			}
		}
		return out
	}
	col := func(have map[string]bool, name, def string) string {
		if have[name] {
			return name
		}
		return def
	}
	has := map[string]bool{}
	for _, t := range []string{"refs", "ref_history", "callees", "file_symbols", "ref_files", "file_blobs", "manifest_blobs", "external_refs", "symbol_monikers"} {
		has[t] = s.tableExists(t)
	}
	type rr struct{ repo, ref string }
	var order []rr
	seen := map[rr]bool{}
	for _, t := range []string{"refs", "ref_history", "ref_files", "external_refs"} {
		if !has[t] {
			continue
		}
		rows, err := s.db.Query(`SELECT DISTINCT repo, ref FROM ` + t)
		if err != nil {
			return err
		}
		for rows.Next() {
			var a, b string
			if rows.Scan(&a, &b) == nil && !seen[rr{a, b}] {
				seen[rr{a, b}] = true
				order = append(order, rr{a, b})
			}
		}
		rows.Close()
	}
	hc, cc, ec, rc := cols("ref_history"), cols("callees"), cols("external_refs"), cols("refs")
	err := s.withTx(func(tx *sql.Tx) error {
		for _, k := range order {
			rid, err := s.refID(tx, k.repo, k.ref)
			if err != nil {
				return err
			}
			if has["refs"] {
				var commit, man sql.NullString
				var iv sql.NullInt64
				q := `SELECT ` + col(rc, "commit_hash", "''") + `, ` + col(rc, "manifest_hash", "''") + `, ` +
					col(rc, "index_ver", "0") + ` FROM refs WHERE repo=? AND ref=?`
				if tx.QueryRow(q, k.repo, k.ref).Scan(&commit, &man, &iv) == nil {
					if _, err := tx.Exec(`UPDATE refs_v2 SET commit_hash=?, manifest_hash=?, index_ver=? WHERE id=?`,
						commit.String, man.String, iv.Int64, rid); err != nil {
						return err
					}
				}
			}
			callees := map[string][]query.Callee{}
			if has["callees"] {
				rows, err := tx.Query(`SELECT name, callee, `+col(cc, "resolution_method", "''")+`, `+col(cc, "confidence", "1")+
					` FROM callees WHERE repo=? AND ref=?`, k.repo, k.ref)
				if err != nil {
					return err
				}
				for rows.Next() {
					var n, c, m string
					var conf float64
					if rows.Scan(&n, &c, &m, &conf) == nil {
						callees[n] = append(callees[n], query.Callee{Name: c, ResolutionMethod: m, Confidence: conf})
					}
				}
				rows.Close()
			}
			if has["ref_history"] {
				rows, err := tx.Query(`SELECT name, COALESCE(symbol_hash,''), COALESCE(signature_hash,''), COALESCE(behavior_hash,''),
					COALESCE(behavior_conf,0), `+col(hc, "direct_conf", "1")+`, `+col(hc, "lang", "''")+`, `+col(hc, "is_test", "0")+`
					FROM ref_history WHERE repo=? AND ref=? AND present=1`, k.repo, k.ref)
				if err != nil {
					return err
				}
				type row struct {
					name string
					rec  storage.SymbolRecord
				}
				var recs []row
				for rows.Next() {
					var r row
					var sh, sig, beh, lang string
					var isTest int
					if rows.Scan(&r.name, &sh, &sig, &beh, &r.rec.BehaviorConf, &r.rec.DirectConf, &lang, &isTest) == nil {
						r.rec.SymbolHash, r.rec.SignatureHash, r.rec.BehaviorHash = content.Hash(sh), content.Hash(sig), content.Hash(beh)
						r.rec.Lang, r.rec.IsTest = lang, isTest != 0
						recs = append(recs, r)
					}
				}
				rows.Close()
				for _, r := range recs {
					r.rec.Callees = callees[r.name]
					if err := s.put(tx, k.repo, k.ref, r.name, r.rec); err != nil {
						return err
					}
				}
			}
			if has["ref_files"] && has["file_blobs"] {
				spans := map[string][]query.SymbolSpan{}
				if has["file_symbols"] {
					rows, err := tx.Query(`SELECT path, name, COALESCE(start_line,0), COALESCE(end_line,0) FROM file_symbols WHERE repo=? AND ref=?`, k.repo, k.ref)
					if err != nil {
						return err
					}
					for rows.Next() {
						var p, n string
						var a, b int
						if rows.Scan(&p, &n, &a, &b) == nil {
							spans[p] = append(spans[p], query.SymbolSpan{Name: n, StartLine: a, EndLine: b})
						}
					}
					rows.Close()
				}
				rows, err := tx.Query(`SELECT rf.path, fb.content FROM ref_files rf JOIN file_blobs fb ON fb.hash = rf.blob_hash
					WHERE rf.repo=? AND rf.ref=?`, k.repo, k.ref)
				if err != nil {
					return err
				}
				var files []query.File
				for rows.Next() {
					var f query.File
					if rows.Scan(&f.Path, &f.Content) == nil {
						files = append(files, f)
					}
				}
				rows.Close()
				for _, f := range files {
					f.Symbols = spans[f.Path]
					if err := s.putFile(tx, k.repo, k.ref, f); err != nil {
						return err
					}
				}
			}
			if has["external_refs"] {
				rows, err := tx.Query(`SELECT from_name, target_module, target_name, `+col(ec, "resolution_method", "''")+`, `+
					col(ec, "confidence", "0.6")+`, `+col(ec, "target_signature_hash", "''")+`, `+col(ec, "target_symbol", "''")+
					` FROM external_refs WHERE repo=? AND ref=?`, k.repo, k.ref)
				if err != nil {
					return err
				}
				var refs []query.ExternalRef
				for rows.Next() {
					var r query.ExternalRef
					if rows.Scan(&r.From, &r.Module, &r.Name, &r.ResolutionMethod, &r.Confidence, &r.TargetSignatureHash, &r.TargetSymbol) == nil {
						refs = append(refs, r)
					}
				}
				rows.Close()
				if err := s.putExternalRefs(tx, k.repo, k.ref, refs); err != nil {
					return err
				}
			}
			if has["symbol_monikers"] {
				rows, err := tx.Query(`SELECT symbol, moniker FROM symbol_monikers WHERE repo=? AND ref=?`, k.repo, k.ref)
				if err != nil {
					return err
				}
				mons := map[string]string{}
				for rows.Next() {
					var a, b string
					if rows.Scan(&a, &b) == nil {
						mons[a] = b
					}
				}
				rows.Close()
				if err := s.putMonikers(tx, k.repo, k.ref, mons); err != nil {
					return err
				}
			}
			if has["manifest_blobs"] {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO ref_manifest(ref_id, blob) SELECT ?, blob FROM manifest_blobs WHERE repo=? AND ref=?`,
					rid, k.repo, k.ref); err != nil {
					return err
				}
			}
		}
		for t, ok := range has {
			if ok {
				if _, err := tx.Exec(`DROP TABLE IF EXISTS ` + t); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if s.path != ":memory:" && !strings.HasPrefix(s.path, "file::memory:") {
		_, _ = s.db.Exec(`VACUUM`) // hand the freed v1 pages back to the filesystem
	}
	return nil
}
