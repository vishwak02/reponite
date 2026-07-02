// manifest.go implements the pure content-addressing set logic (architecture §4):
// a Manifest is a ref snapshot owning nothing but a set of blob hashes, so
// "index N refs" grows storage with *unique* content, not N; a manifest diff is
// a set operation; and GC's mark phase is set subtraction. This is pure and
// stdlib-only (ADR-018); the SQLite/on-disk persistence in package storage wraps
// these types, it does not reimplement the logic.
package content

import "sort"

// Manifest is a ref snapshot: metadata plus the set of blob hashes it references
// (architecture §4.3). Blobs is treated as a set; duplicates are ignored.
type Manifest struct {
	Ref    string
	Commit string
	Blobs  []Hash
}

// Hash is the identity of the snapshot: ManifestHash over its sorted blobs.
func (m Manifest) Hash() Hash { return ManifestHash(m.Blobs) }

func hashSet(hs []Hash) map[Hash]struct{} {
	s := make(map[Hash]struct{}, len(hs))
	for _, h := range hs {
		s[h] = struct{}{}
	}
	return s
}

func sortedHashes(m map[Hash]struct{}) []Hash {
	out := make([]Hash, 0, len(m))
	for h := range m {
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// ManifestDiff is the set difference between two ref snapshots (§4.1: a diff is a
// set operation, not a re-analysis).
type ManifestDiff struct {
	Added   []Hash // blobs in B not in A
	Removed []Hash // blobs in A not in B
	Shared  int    // count present in both
}

// DiffManifests returns the blob-level delta from a to b (outputs sorted).
func DiffManifests(a, b Manifest) ManifestDiff {
	as, bs := hashSet(a.Blobs), hashSet(b.Blobs)
	added, removed := map[Hash]struct{}{}, map[Hash]struct{}{}
	var d ManifestDiff
	for h := range bs {
		if _, ok := as[h]; ok {
			d.Shared++
		} else {
			added[h] = struct{}{}
		}
	}
	for h := range as {
		if _, ok := bs[h]; !ok {
			removed[h] = struct{}{}
		}
	}
	d.Added, d.Removed = sortedHashes(added), sortedHashes(removed)
	return d
}

// UniqueBlobs is the deduplicated union of blobs across manifests — what storage
// actually holds (each unique blob once, no matter how many refs reference it).
func UniqueBlobs(manifests []Manifest) []Hash {
	u := map[Hash]struct{}{}
	for _, m := range manifests {
		for _, h := range m.Blobs {
			u[h] = struct{}{}
		}
	}
	return sortedHashes(u)
}

// DedupStats quantifies content-addressed savings across a set of manifests.
type DedupStats struct {
	TotalReferences int // sum of blob references across all manifests
	UniqueBlobs     int // distinct blobs actually stored
}

// Dedup reports how storage scales with unique content rather than ref count.
func Dedup(manifests []Manifest) DedupStats {
	total := 0
	for _, m := range manifests {
		total += len(m.Blobs)
	}
	return DedupStats{TotalReferences: total, UniqueBlobs: len(UniqueBlobs(manifests))}
}

// UnreferencedBlobs returns stored blobs referenced by no live manifest — the
// sweep set of mark-sweep GC (architecture §11.4): mark = union of live manifest
// blobs; sweep = stored − marked. Output sorted.
func UnreferencedBlobs(live []Manifest, stored []Hash) []Hash {
	marked := hashSet(UniqueBlobs(live))
	orphan := map[Hash]struct{}{}
	for _, h := range stored {
		if _, ok := marked[h]; !ok {
			orphan[h] = struct{}{}
		}
	}
	return sortedHashes(orphan)
}
