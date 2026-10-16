//go:build sqlite && treesitter

package main

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"regexp"

	"github.com/vishwak02/reponite/internal/query"
)

// termCache persists semsearch's per-repo term indexes next to each repo's
// index ($REPONITE_STORE_DIR/<repo>/semindex/, else the user cache dir). An
// entry is valid only for the exact content it was built from — the ref's
// commit, manifest hash and indexing ruleset — plus the tokenizer version and the --no-tests
// view, so a re-indexed ref or a new tokenizer rebuilds instead of serving
// stale vectors. Cache failures are never errors: the search just builds.
type termCache struct {
	store   query.Store
	noTests bool
}

var unsafeRef = regexp.MustCompile(`[^A-Za-z0-9._@-]`)

func (c *termCache) file(repo, ref string) (string, string, bool) {
	eff := query.EffectiveRef(c.store, repo, ref)
	man, ok := c.store.Manifest(repo, eff)
	if !ok || man.Commit == "" && len(man.Blobs) == 0 {
		return "", "", false // no content identity to validate against
	}
	iv := 0
	if v, ok := c.store.(query.IndexVersioner); ok {
		iv, _ = v.IndexVersion(repo, eff)
	}
	root := os.Getenv(storeDirEnv)
	if root == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return "", "", false
		}
		root = filepath.Join(d, "reponite")
	}
	name := unsafeRef.ReplaceAllString(eff, "_")
	if c.noTests {
		name += ".notests"
	}
	key := fmt.Sprintf("v%d|iv%d|%s|%s|notests=%v", query.TermIndexVersion, iv, man.Commit, man.Hash(), c.noTests)
	return filepath.Join(root, repo, "semindex", name+".gob"), key, true
}

func (c *termCache) Load(repo, ref string) (*query.TermIndex, bool) {
	path, key, ok := c.file(repo, ref)
	if !ok {
		return nil, false
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()
	var ix query.TermIndex
	if err := gob.NewDecoder(f).Decode(&ix); err != nil || ix.Key != key {
		return nil, false
	}
	return &ix, true
}

func (c *termCache) Save(repo, ref string, ix *query.TermIndex) {
	path, key, ok := c.file(repo, ref)
	if !ok {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	ix.Key = key
	tmp, err := os.CreateTemp(filepath.Dir(path), ".semindex-*")
	if err != nil {
		return
	}
	if err := gob.NewEncoder(tmp).Encode(ix); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
	}
}
