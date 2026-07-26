package query_test

import (
	"strings"
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

// The module-resolved tier must treat the target's module_path as a module
// ROOT. A real import path is the module path PLUS a package path
// ("github.com/acme/api/pkg/user"), so exact-equality matching silently
// demoted every multi-package repo's callers to the name-based tier — the
// "module-path precise" tier never fired outside single-package toy repos.
func TestXImpactMatchesModuleRootNotExactPath(t *testing.T) {
	m := storage.NewMem()
	m.Put("api", "HEAD", "pkg/user.GetUser", rc("g", "sig", "b", 1))
	if err := m.SetModulePath("api", "github.com/acme/api"); err != nil {
		t.Fatal(err)
	}
	// What a real Go indexer captures: the full import path of the package.
	m.PutExternalRefs("web", "HEAD", []query.ExternalRef{{
		From: "svc.Handle", Module: "github.com/acme/api/pkg/user", Name: "GetUser",
		ResolutionMethod: query.ImportResolution, Confidence: 0.75,
	}})
	// A SIBLING module that merely shares a prefix must never match.
	m.PutExternalRefs("other", "HEAD", []query.ExternalRef{{
		From: "x.Call", Module: "github.com/acme/apiv2/pkg/user", Name: "GetUser",
		ResolutionMethod: query.ImportResolution, Confidence: 0.75,
	}})

	res := query.XImpact(m, "GetUser", "")
	var precise []query.XImpactCaller
	for _, c := range res.Callers {
		if c.ResolutionMethod == query.ImportResolution {
			precise = append(precise, c)
		}
	}
	if len(precise) != 1 || precise[0].Caller != "svc.Handle" {
		t.Fatalf("the subpackage import must match its module root exactly once: %+v", res.Callers)
	}
	for _, c := range res.Callers {
		if c.Repo == "other" && c.ResolutionMethod == query.ImportResolution {
			t.Fatalf("sibling module apiv2 must not match root api: %+v", c)
		}
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

// Phase 6b tier 0: with SCIP indexes on both sides, a caller is matched to the
// target by MONIKER — a globally unique symbol identity — so the link is
// symbol-resolved (0.95) rather than a (module, name) match (0.75) or a bare
// name guess (0.6), and it sorts above both.
func TestXImpactSCIPResolvedTier(t *testing.T) {
	const moniker = "scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser()."
	m := storage.NewMem()
	m.Put("api", "HEAD", "user.GetUser", rc("g", "sig", "b", 1))
	if err := m.SetModulePath("api", "github.com/acme/api"); err != nil {
		t.Fatal(err)
	}
	// The api repo has a SCIP index, so its definition owns a moniker.
	if err := m.PutMonikers("api", "HEAD", map[string]string{"user.GetUser": moniker}); err != nil {
		t.Fatal(err)
	}
	// web was SCIP-indexed too: its reference carries the exact moniker.
	m.PutExternalRefs("web", "HEAD", []query.ExternalRef{
		{From: "svc.Fetch", TargetSymbol: moniker, ResolutionMethod: query.SCIPResolution, Confidence: 0.95},
	})
	// legacy had no SCIP index: only the import-resolved (module, name) tier.
	m.PutExternalRefs("legacy", "HEAD", []query.ExternalRef{
		{From: "old.Fetch", Module: "github.com/acme/api", Name: "GetUser",
			ResolutionMethod: query.ImportResolution, Confidence: 0.75},
	})

	res := query.XImpact(m, "GetUser", "")
	if len(res.Callers) != 2 {
		t.Fatalf("want both callers, got %+v", res.Callers)
	}
	// The SCIP tier sorts first and keeps its own method/confidence.
	top := res.Callers[0]
	if top.Caller != "svc.Fetch" || top.ResolutionMethod != query.SCIPResolution || top.Confidence != 0.95 {
		t.Fatalf("SCIP-resolved caller must lead, labeled and at its own confidence: %+v", top)
	}
	if res.Callers[1].ResolutionMethod != query.ImportResolution {
		t.Fatalf("import-resolved caller must follow the SCIP tier: %+v", res.Callers[1])
	}
	// The definition exposes its moniker, and the note says how many were
	// symbol-resolved (never implied).
	if len(res.Definitions) != 1 || res.Definitions[0].Moniker != moniker {
		t.Fatalf("definition must carry its moniker: %+v", res.Definitions)
	}
	if !strings.Contains(res.Note, "1 caller(s) SCIP-resolved") {
		t.Fatalf("note must state the SCIP-resolved count, got %q", res.Note)
	}
}

// A moniker is globally unique, so a same-named symbol in another module is
// never matched by the SCIP tier — and a repo without SCIP simply keeps the
// old tiers (no regression, nothing invented).
func TestXImpactSCIPNoFalsePositivesAndNoRegression(t *testing.T) {
	m := storage.NewMem()
	m.Put("api", "HEAD", "user.GetUser", rc("g", "sig", "b", 1))
	m.SetModulePath("api", "github.com/acme/api")
	m.PutMonikers("api", "HEAD", map[string]string{
		"user.GetUser": "scip-go gomod github.com/acme/api v1 `pkg/user`/GetUser().",
	})
	// A DIFFERENT project's GetUser moniker — same bare name, different symbol.
	m.PutExternalRefs("other", "HEAD", []query.ExternalRef{
		{From: "x.Call", TargetSymbol: "scip-go gomod github.com/other/lib v1 `pkg/user`/GetUser().",
			ResolutionMethod: query.SCIPResolution, Confidence: 0.95},
	})
	res := query.XImpact(m, "GetUser", "")
	for _, c := range res.Callers {
		if c.ResolutionMethod == query.SCIPResolution {
			t.Fatalf("a different module's moniker must never match: %+v", c)
		}
	}
	if strings.Contains(res.Note, "SCIP-resolved") {
		t.Fatalf("no SCIP matches, so the note must not claim any: %q", res.Note)
	}

	// No monikers at all → the pre-Phase-6b behavior, unchanged.
	m2 := storage.NewMem()
	m2.Put("api", "HEAD", "user.GetUser", rc("g", "sig", "b", 1))
	m2.SetModulePath("api", "github.com/acme/api")
	m2.PutExternalRefs("web", "HEAD", []query.ExternalRef{
		{From: "svc.Fetch", Module: "github.com/acme/api", Name: "GetUser",
			ResolutionMethod: query.ImportResolution, Confidence: 0.75},
	})
	plain := query.XImpact(m2, "GetUser", "")
	if len(plain.Callers) != 1 || plain.Callers[0].ResolutionMethod != query.ImportResolution {
		t.Fatalf("without SCIP the existing tiers must be untouched: %+v", plain.Callers)
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
