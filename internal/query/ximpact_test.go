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
