// factcache.go remembers the fleet-wide results that are expensive to derive
// — the communication graph, the HTTP route map, the fleet map — between
// commands. Each is a pure function of the indexed content it reads, so it is
// keyed by exactly that: for every repo in scope, the ref read (after pins),
// its commit, its manifest hash and its indexing ruleset, plus the call's own
// arguments and the cache owner's build identity. Any re-index, a different
// pin, a new binary or a different test view is a different key, so a stale
// answer is never served. Without a cache (tests, the pure core) every call
// computes, exactly as before.
package query

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
)

// FactCache stores derived results by key. Load reports false on a miss or an
// unreadable entry; Save failures are silent (the next call recomputes).
type FactCache interface {
	Load(key string, v any) bool
	Save(key string, v any)
}

var (
	factMu    sync.RWMutex
	factCache FactCache
)

// SetFactCache installs the process-wide cache (nil disables caching).
func SetFactCache(c FactCache) {
	factMu.Lock()
	factCache = c
	factMu.Unlock()
}

func currentFactCache() FactCache {
	factMu.RLock()
	defer factMu.RUnlock()
	return factCache
}

// contentKey identifies the content of every repo in scope at ref; ok is
// false when a repo has no content identity (nothing to validate against).
func contentKey(s Store, repo, ref string) (string, bool) {
	repos := append([]string(nil), reposFor(s, repo)...)
	sort.Strings(repos)
	var b strings.Builder
	for _, rp := range repos {
		eff := EffectiveRef(s, rp, ref)
		man, ok := s.Manifest(rp, eff)
		if !ok || man.Commit == "" && len(man.Blobs) == 0 {
			if !containsStr(s.Refs(rp), eff) {
				fmt.Fprintf(&b, "%s@%s:absent;", rp, eff) // not indexed at that ref: contributes nothing
				continue
			}
			return "", false // indexed but with no content identity to validate against: never cache
		}
		iv := 0
		if v, ok := s.(IndexVersioner); ok {
			iv, _ = v.IndexVersion(rp, eff)
		}
		fmt.Fprintf(&b, "%s@%s:%s:%s:%d;", rp, eff, man.Commit, man.Hash(), iv)
	}
	return b.String(), true
}

// cachedFact returns compute()'s result for (kind, args) over the content of
// repo@ref, from the cache when an entry for exactly that content exists.
func cachedFact[T any](s Store, repo, ref, kind string, args string, compute func() T) T {
	c := currentFactCache()
	if c == nil {
		return compute()
	}
	ck, ok := contentKey(s, repo, ref)
	if !ok {
		return compute()
	}
	h := sha256.Sum256([]byte(kind + "\x00" + args + "\x00" + repo + "\x00" + ref + "\x00" + ck))
	key := kind + "-" + hex.EncodeToString(h[:12])
	var v T
	if c.Load(key, &v) {
		return v
	}
	v = compute()
	c.Save(key, v)
	return v
}

// PathFilesReader is optional on a Store: the files at a ref whose path keep
// accepts, without decoding the others.
type PathFilesReader interface {
	FilesMatching(repo, ref string, keep func(path string) bool) []File
}

// filesMatching reads only the files keep accepts when the store can, else
// filters a full read.
func filesMatching(s Store, repo, ref string, keep func(string) bool) []File {
	if r, ok := s.(PathFilesReader); ok {
		return r.FilesMatching(repo, ref, keep)
	}
	var out []File
	for _, f := range s.Files(repo, ref) {
		if keep(f.Path) {
			out = append(out, f)
		}
	}
	return out
}
