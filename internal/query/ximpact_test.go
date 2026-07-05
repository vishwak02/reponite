package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// ext builds a record with one unresolved-external callee edge to target.
func ext(sym, target string) storage.SymbolRecord {
	r := rc(sym, "s", "b", 1)
	r.Callees = []query.Callee{{Name: target, ResolutionMethod: query.ExternalResolution, Confidence: 0.6}}
	return r
}

func TestXImpactAcrossRepos(t *testing.T) {
	m := storage.NewMem()
	// Two repos call an external getUserV2; one calls something else.
	m.Put("svc-a", "HEAD", "svc-a.handler", ext("h", "getUserV2"))
	m.Put("svc-b", "HEAD", "svc-b.worker", ext("w", "getUserV2"))
	m.Put("svc-b", "HEAD", "svc-b.other", ext("o", "unrelated"))
	// An in-repo (name-resolved) edge to getUserV2 must NOT count as external.
	local := rc("l", "s", "b", 1)
	local.Callees = []query.Callee{{Name: "getUserV2", ResolutionMethod: "name-resolved", Confidence: 0.9}}
	m.Put("svc-a", "HEAD", "svc-a.internal", local)

	res := query.XImpact(m, "getUserV2", "")
	if len(res.Callers) != 2 {
		t.Fatalf("expected 2 external callers of getUserV2, got %+v", res.Callers)
	}
	// Sorted by repo: svc-a.handler then svc-b.worker.
	if res.Callers[0].Repo != "svc-a" || res.Callers[0].Caller != "svc-a.handler" {
		t.Fatalf("caller[0] = %+v", res.Callers[0])
	}
	if res.Callers[1].Repo != "svc-b" || res.Callers[1].Caller != "svc-b.worker" {
		t.Fatalf("caller[1] = %+v", res.Callers[1])
	}
}

// The target's own contract state is fused in: definition sites + whether the
// signature moved across refs (the deploy-safety signal, §8B.3).
func TestXImpactContractFusion(t *testing.T) {
	m := storage.NewMem()
	// api defines getUserV2; its signature CHANGES between v1 and v2.
	m.Put("api", "v1", "api.getUserV2", rc("g", "sigV1", "b", 1))
	m.Put("api", "v2", "api.getUserV2", rc("g", "sigV2", "b", 1))
	// svc-a calls it externally (a cross-repo dependency).
	m.Put("svc-a", "HEAD", "svc-a.handler", ext("h", "getUserV2"))

	res := query.XImpact(m, "getUserV2", "")
	if !res.ContractChanged {
		t.Fatalf("signature moved across api refs → ContractChanged must be true; defs=%+v", res.Definitions)
	}
	if len(res.Definitions) != 2 {
		t.Fatalf("expected 2 definition sites (api@v1, api@v2), got %+v", res.Definitions)
	}
	if len(res.Callers) != 1 || res.Callers[0].Repo != "svc-a" {
		t.Fatalf("expected 1 external caller in svc-a, got %+v", res.Callers)
	}

	// A stable target (single signature) reports ContractChanged=false.
	m2 := storage.NewMem()
	m2.Put("api", "v1", "api.stable", rc("s", "sig", "b", 1))
	m2.Put("api", "v2", "api.stable", rc("s", "sig", "b", 1))
	if query.XImpact(m2, "stable", "").ContractChanged {
		t.Fatal("identical signature across refs must be ContractChanged=false")
	}
}
