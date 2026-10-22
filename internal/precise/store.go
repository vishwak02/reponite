// Package precise keeps what a compiler proved about one unit of code (a C++
// translation unit) in a content-addressed store, so a unit compiled once is
// never compiled again for another ref, branch or repo whose sources for it
// are the same. Each entry is one file named by the hash of its parts; what
// a driver reads to tell whether an entry still holds (the files a unit read)
// is an entry of its own. Every failure reads as a miss. Stdlib only.
package precise

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Store is a directory of entries, one per key, by kind (cpp, ts, …).
type Store struct {
	dir string
}

// Open is the store under root (created on first write).
func Open(root string) *Store {
	return &Store{dir: root}
}

// Has reports whether the store holds any entry of kind.
func (s *Store) Has(kind string) bool {
	if s == nil {
		return false
	}
	fi, err := os.Stat(filepath.Join(s.dir, kind))
	return err == nil && fi.IsDir()
}

// Dir is the store's directory.
func (s *Store) Dir() string { return s.dir }

// Key hashes the parts of a unit's identity into a key.
func Key(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		var n [8]byte
		l := uint64(len(p))
		for i := 0; i < 8; i++ {
			n[i] = byte(l >> (8 * i))
		}
		h.Write(n[:]) // length-prefixed: ("ab","c") is not ("a","bc")
		h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *Store) path(kind, key string) string {
	return filepath.Join(s.dir, kind, key[:2], key+".gob.gz")
}

// Get decodes the entry of key into v; false on a miss or any failure.
func (s *Store) Get(kind, key string, v any) bool {
	if s == nil || len(key) < 2 {
		return false
	}
	p := s.path(kind, key)
	f, err := os.Open(p)
	if err != nil {
		return false
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return false
	}
	if err := gob.NewDecoder(zr).Decode(v); err != nil {
		return false
	}
	now := time.Now()
	_ = os.Chtimes(p, now, now) // last use, for Prune
	return true
}

// Put stores v under key, atomically (written aside, then renamed).
func (s *Store) Put(kind, key string, v any) error {
	if s == nil || len(key) < 2 {
		return nil
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if err := gob.NewEncoder(zw).Encode(v); err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	p := s.path(kind, key)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".put-*")
	if err != nil {
		return err
	}
	if _, err := io.Copy(tmp, &buf); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), p)
}

// Size is the store's total bytes and entry count.
func (s *Store) Size() (bytes int64, entries int) {
	_ = filepath.WalkDir(s.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".gz" {
			if fi, e := d.Info(); e == nil {
				bytes += fi.Size()
				entries++
			}
		}
		return nil
	})
	return bytes, entries
}

// Prune removes the least recently used entries until the store is at most
// max bytes; it returns how many it removed.
func (s *Store) Prune(max int64) int {
	type entry struct {
		path string
		size int64
		used time.Time
	}
	var all []entry
	var total int64
	_ = filepath.WalkDir(s.dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(p) == ".gz" {
			if fi, e := d.Info(); e == nil {
				all = append(all, entry{p, fi.Size(), fi.ModTime()})
				total += fi.Size()
			}
		}
		return nil
	})
	if total <= max {
		return 0
	}
	sort.Slice(all, func(i, j int) bool { return all[i].used.Before(all[j].used) }) // oldest first
	n := 0
	for _, e := range all {
		if total <= max {
			break
		}
		if os.Remove(e.path) == nil {
			total -= e.size
			n++
		}
	}
	return n
}
