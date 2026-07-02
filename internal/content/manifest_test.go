package content

import "testing"

func mani(ref string, blobs ...string) Manifest {
	hs := make([]Hash, len(blobs))
	for i, b := range blobs {
		hs[i] = Hash("sha256:" + b)
	}
	return Manifest{Ref: ref, Blobs: hs}
}

func TestManifestHashOrderIndependent(t *testing.T) {
	if mani("v1", "a", "b", "c").Hash() != mani("v1", "c", "a", "b").Hash() {
		t.Fatal("manifest hash must be order-independent")
	}
}

func TestDiffManifests(t *testing.T) {
	d := DiffManifests(mani("A", "a", "b", "c"), mani("B", "a", "b", "d"))
	if len(d.Added) != 1 || d.Added[0] != Hash("sha256:d") {
		t.Fatalf("added: %v", d.Added)
	}
	if len(d.Removed) != 1 || d.Removed[0] != Hash("sha256:c") {
		t.Fatalf("removed: %v", d.Removed)
	}
	if d.Shared != 2 {
		t.Fatalf("shared: %d", d.Shared)
	}
}

func TestDiffIdenticalContentIsEmpty(t *testing.T) {
	d := DiffManifests(mani("A", "a", "b"), mani("B", "b", "a"))
	if len(d.Added) != 0 || len(d.Removed) != 0 || d.Shared != 2 {
		t.Fatalf("identical content must diff empty: %+v", d)
	}
}

func TestDedupProportionalToUniqueContent(t *testing.T) {
	s := Dedup([]Manifest{mani("v2.3.0", "a", "b", "c"), mani("v2.3.1", "a", "b", "d")})
	if s.TotalReferences != 6 {
		t.Fatalf("total refs %d", s.TotalReferences)
	}
	if s.UniqueBlobs != 4 {
		t.Fatalf("unique blobs %d (a,b shared)", s.UniqueBlobs)
	}
}

func TestManyIdenticalRefsDoNotGrowStorage(t *testing.T) {
	base := []string{"a", "b", "c", "d", "e"}
	one := Dedup([]Manifest{mani("t0", base...)}).UniqueBlobs
	many := make([]Manifest, 50)
	for i := range many {
		many[i] = mani("t", base...)
	}
	if got := Dedup(many).UniqueBlobs; got != one {
		t.Fatalf("50 identical tags must not grow unique storage: one=%d many=%d", one, got)
	}
}

func TestUnreferencedBlobsGCMark(t *testing.T) {
	live := []Manifest{mani("HEAD", "a", "b", "c")}
	stored := []Hash{"sha256:a", "sha256:b", "sha256:c", "sha256:d", "sha256:e"}
	orphan := UnreferencedBlobs(live, stored)
	if len(orphan) != 2 || orphan[0] != Hash("sha256:d") || orphan[1] != Hash("sha256:e") {
		t.Fatalf("orphans must be d,e: %v", orphan)
	}
}

func TestUniqueBlobsSortedUnion(t *testing.T) {
	u := UniqueBlobs([]Manifest{mani("A", "b", "a"), mani("B", "a", "c")})
	if len(u) != 3 || u[0] != Hash("sha256:a") || u[1] != Hash("sha256:b") || u[2] != Hash("sha256:c") {
		t.Fatalf("want sorted union a,b,c: %v", u)
	}
}
