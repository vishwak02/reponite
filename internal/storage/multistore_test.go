package storage_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func def(sym, sig string) storage.SymbolRecord {
	return storage.SymbolRecord{
		SymbolHash: content.Hash(sym), SignatureHash: content.Hash(sig),
		BehaviorHash: content.Hash("b"), BehaviorConf: 1, DirectConf: 1,
	}
}

func extCall(sym, target string) storage.SymbolRecord {
	r := def(sym, "s")
	r.Callees = []query.Callee{{Name: target, ResolutionMethod: query.ExternalResolution, Confidence: 0.6}}
	return r
}

// Two separate backing stores (as if two repos' SQLite DBs) aggregate into one
// fleet view, and a cross-repo ximpact stitches a caller in one to the target's
// definition in the other.
func TestMultiStoreFleetXImpact(t *testing.T) {
	a := storage.NewMem()
	a.Put("svc-a", "HEAD", "svc-a.handler", extCall("h", "getUserV2"))
	b := storage.NewMem()
	b.Put("api", "HEAD", "api.getUserV2", def("g", "sigV1"))
	ms := storage.NewMultiStore(a, b)

	if repos := ms.Repos(); len(repos) != 2 || repos[0] != "api" || repos[1] != "svc-a" {
		t.Fatalf("repos = %v (want sorted [api svc-a])", repos)
	}
	// Per-repo calls route to the owning backing store.
	if got := ms.Refs("api"); len(got) != 1 || got[0] != "HEAD" {
		t.Fatalf("api refs = %v", got)
	}
	if _, ok := ms.SymbolAt("api", "api.getUserV2", "HEAD"); !ok {
		t.Fatal("SymbolAt must route to the store owning 'api'")
	}
	// Cross-repo ximpact over the fleet: caller in svc-a, definition in api.
	res := query.XImpact(ms, "getUserV2", "")
	if len(res.Callers) != 1 || res.Callers[0].Repo != "svc-a" {
		t.Fatalf("fleet caller = %+v", res.Callers)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].Repo != "api" {
		t.Fatalf("fleet definition = %+v", res.Definitions)
	}
}

func TestMultiStoreUnknownRepo(t *testing.T) {
	ms := storage.NewMultiStore(storage.NewMem())
	if ms.Refs("nope") != nil {
		t.Fatal("unknown repo Refs must be nil")
	}
	if _, ok := ms.SymbolAt("nope", "x", "HEAD"); ok {
		t.Fatal("unknown repo SymbolAt must be false")
	}
}
