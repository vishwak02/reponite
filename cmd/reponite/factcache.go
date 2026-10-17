//go:build sqlite && treesitter

package main

import (
	"encoding/gob"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/version"
)

// fileFactCache keeps the fleet-wide results query computes (communication
// graph, route map, fleet map) in $REPONITE_STORE_DIR/.facts/ (else the user
// cache dir). query keys each entry by the exact indexed content it was
// derived from; this adds the binary's identity — a rebuilt reponite may
// derive differently, so its entries live apart — and the test view. Entries
// of other builds are pruned after a week. Every failure is a miss.
type fileFactCache struct {
	dir    string
	pruned bool
}

func newFileFactCache() *fileFactCache {
	root := os.Getenv(storeDirEnv)
	if root == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return nil
		}
		root = filepath.Join(d, "reponite")
	}
	return &fileFactCache{dir: filepath.Join(root, ".facts", buildID())}
}

// buildID identifies the code that derived a cached fact: a release binary
// by its version (reinstalling the same release keeps its cache), a
// development build by its size and modification time (every rebuild starts
// fresh, since its derivations may have changed).
func buildID() string {
	if v := version.Version; v != "" && v != "0.0.0-dev" {
		return "v" + v
	}
	exe, err := os.Executable()
	if err != nil {
		return "unknown"
	}
	st, err := os.Stat(exe)
	if err != nil {
		return "unknown"
	}
	return fmt.Sprintf("%x-%x", st.Size(), st.ModTime().UnixNano())
}

func (c *fileFactCache) path(key string) string {
	if hideTests() {
		key += ".notests"
	}
	return filepath.Join(c.dir, key+".gob")
}

func (c *fileFactCache) Load(key string, v any) bool {
	f, err := os.Open(c.path(key))
	if err != nil {
		return false
	}
	defer f.Close()
	return gob.NewDecoder(f).Decode(v) == nil
}

func (c *fileFactCache) Save(key string, v any) {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(c.dir, ".fact-*")
	if err != nil {
		return
	}
	if err := gob.NewEncoder(tmp).Encode(v); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	if os.Rename(tmp.Name(), c.path(key)) != nil {
		os.Remove(tmp.Name())
	}
	c.prune()
}

// prune removes other builds' entries untouched for a week.
func (c *fileFactCache) prune() {
	if c.pruned {
		return
	}
	c.pruned = true
	parent := filepath.Dir(c.dir)
	ents, err := os.ReadDir(parent)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() || e.Name() == filepath.Base(c.dir) {
			continue
		}
		if info, err := e.Info(); err == nil && time.Since(info.ModTime()) > 7*24*time.Hour {
			os.RemoveAll(filepath.Join(parent, e.Name()))
		}
	}
}

func init() { installFacts = installFactCache }

func installFactCache() {
	if os.Getenv("REPONITE_NO_FACT_CACHE") != "" {
		return
	}
	if c := newFileFactCache(); c != nil {
		query.SetFactCache(c)
	}
}
