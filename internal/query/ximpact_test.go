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

// Two DIFFERENT symbols sharing a bare name (storage.Mem.Put vs sqlite.Store.Put)
// each with a stable signature must NOT be reported as a contract change — the
// old code keyed the signature set on the bare name and cried wolf.
func TestXImpactContractNoNameConflation(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "storage.Mem.Put", rc("m", "memSig", "b", 1))
	m.Put("r", "prev", "storage.Mem.Put", rc("m", "memSig", "b", 1)) // stable
	m.Put("r", "HEAD", "storage/sqlite.Store.Put", rc("s", "sqliteSig", "b", 1))
	m.Put("r", "prev", "storage/sqlite.Store.Put", rc("s", "sqliteSig", "b", 1)) // stable
	res := query.XImpact(m, "Put", "")
	if res.ContractChanged {
		t.Fatalf("distinct same-named methods (Mem.Put vs Store.Put) must not be a contract change; defs=%+v", res.Definitions)
	}
	if len(res.Definitions) != 4 {
		t.Fatalf("expected 4 definition sites, got %d", len(res.Definitions))
	}
}

// The precision upgrade: when the target's repo has a known module_path, callers
// that resolved the dependency through their imports (import-resolved external
// refs) are matched on (module, name) — precise, higher-confidence, listed first
// — and fused with the name-based fallback, deduped by caller.
func TestXImpactModuleResolvedFusion(t *testing.T) {
	m := storage.NewMem()
	// api defines getUser and declares its module identity.
	m.Put("api", "HEAD", "api.getUser", rc("g", "sig", "b", 1))
	if err := m.SetModulePath("api", "github.com/acme/api"); err != nil {
		t.Fatal(err)
	}
	imp := func(from string) query.ExternalRef {
		return query.ExternalRef{From: from, Module: "github.com/acme/api", Name: "getUser", ResolutionMethod: query.ImportResolution, Confidence: 0.75}
	}
	// web depends on it precisely (import-resolved).
	m.PutExternalRefs("web", "HEAD", []query.ExternalRef{imp("web.fetch")})
	// worker depends on it BOTH precisely and via a name-based edge → dedup once.
	m.PutExternalRefs("worker", "HEAD", []query.ExternalRef{imp("worker.run")})
	m.Put("worker", "HEAD", "worker.run", ext("wr", "getUser"))
	// legacy depends only via a name-based unresolved-external edge (no imports captured).
	m.Put("legacy", "HEAD", "legacy.old", ext("lo", "getUser"))

	res := query.XImpact(m, "getUser", "")

	if len(res.Modules) != 1 || res.Modules[0] != "github.com/acme/api" {
		t.Fatalf("target module = %v; want [github.com/acme/api]", res.Modules)
	}
	if len(res.Callers) != 3 {
		t.Fatalf("want 3 deduped callers (web, worker, legacy), got %+v", res.Callers)
	}
	// Precise tier first: web.fetch, worker.run (both import-resolved).
	for i, c := range res.Callers[:2] {
		if c.ResolutionMethod != query.ImportResolution || c.Module != "github.com/acme/api" {
			t.Fatalf("caller[%d]=%+v; want import-resolved to the api module", i, c)
		}
	}
	if res.Callers[2].Caller != "legacy.old" || res.Callers[2].ResolutionMethod != query.ExternalResolution {
		t.Fatalf("caller[2]=%+v; want legacy.old as name-based fallback", res.Callers[2])
	}
	// worker.run appears exactly once (precise wins over its name-based edge).
	workerCount := 0
	for _, c := range res.Callers {
		if c.Caller == "worker.run" {
			workerCount++
		}
	}
	if workerCount != 1 {
		t.Fatalf("worker.run must be deduped to one entry, got %d", workerCount)
	}
}

// §8B.3 per-caller signature skew: a caller whose CAPTURED target contract no
// longer matches the target's current signature reads "stale" ("still expects
// the old shape"); a matching one reads "current"; a caller indexed without a
// captured contract stays unknown ("") and is never counted stale.
func TestXImpactPerCallerSignatureSkew(t *testing.T) {
	m := storage.NewMem()
	// The target's contract moved: v1 had sigOLD, HEAD has sigNEW.
	m.Put("api", "v1", "api.getUser", rc("g1", "sigOLD", "b1", 1))
	m.Put("api", "HEAD", "api.getUser", rc("g2", "sigNEW", "b2", 1))
	if err := m.SetModulePath("api", "github.com/acme/api"); err != nil {
		t.Fatal(err)
	}
	dep := func(from, capturedSig string) query.ExternalRef {
		return query.ExternalRef{From: from, Module: "github.com/acme/api", Name: "getUser",
			ResolutionMethod: query.ImportResolution, Confidence: 0.75, TargetSignatureHash: capturedSig}
	}
	m.PutExternalRefs("legacy", "HEAD", []query.ExternalRef{dep("legacy.fetch", "sigOLD")}) // captured before the change
	m.PutExternalRefs("fresh", "HEAD", []query.ExternalRef{dep("fresh.fetch", "sigNEW")})   // captured after
	m.PutExternalRefs("blind", "HEAD", []query.ExternalRef{dep("blind.fetch", "")})         // never captured

	res := query.XImpact(m, "getUser", "")
	skews := map[string]string{}
	for _, c := range res.Callers {
		skews[c.Caller] = c.ExpectedSignature
	}
	if skews["legacy.fetch"] != query.SkewStale {
		t.Fatalf("legacy captured sigOLD vs current sigNEW must be stale, got %q", skews["legacy.fetch"])
	}
	if skews["fresh.fetch"] != query.SkewCurrent {
		t.Fatalf("fresh captured the current contract, got %q", skews["fresh.fetch"])
	}
	if skews["blind.fetch"] != "" {
		t.Fatalf("uncaptured contract must stay unknown, got %q", skews["blind.fetch"])
	}
	if res.StaleCallers != 1 {
		t.Fatalf("StaleCallers = %d; want 1 (only legacy; unknown never counted)", res.StaleCallers)
	}
	if !res.ContractChanged {
		t.Fatal("the target's own contract moved across refs; ContractChanged must hold")
	}
}

// The false-positive guard the OLD name-based ximpact couldn't give: a caller in
// an unrelated module that imports a DIFFERENT package's getUser is matched only
// when its own module matches the target's — a distinct module never collides.
func TestXImpactModuleDistinguishesCollisions(t *testing.T) {
	m := storage.NewMem()
	m.Put("api", "HEAD", "api.getUser", rc("g", "sig", "b", 1))
	m.SetModulePath("api", "github.com/acme/api")
	// This caller precisely depends on a *different* module's getUser.
	m.PutExternalRefs("unrelated", "HEAD", []query.ExternalRef{
		{From: "unrelated.x", Module: "github.com/other/pkg", Name: "getUser", ResolutionMethod: query.ImportResolution, Confidence: 0.75},
	})

	res := query.XImpact(m, "getUser", "")
	// The precise scan for the api module must NOT return the other-module caller.
	for _, c := range res.Callers {
		if c.Caller == "unrelated.x" && c.ResolutionMethod == query.ImportResolution {
			t.Fatalf("a precise dependency on a different module must not match api.getUser: %+v", c)
		}
	}
}
