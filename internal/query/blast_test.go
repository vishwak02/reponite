package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// BlastRadius fuses in-repo callers, fleet callers, and covering tests for a
// symbol into one dossier.
func TestBlastRadius(t *testing.T) {
	m := storage.NewMem()
	// api defines GetUser and declares its module; a local caller + a test cover it.
	m.Put("api", "HEAD", "api.GetUser", rc("g", "sig", "b", 1))
	m.SetModulePath("api", "github.com/acme/api")
	m.Put("api", "HEAD", "api.handler", rc("h", "s", "b", 1, "api.GetUser"))     // in-repo caller (resolved qid)
	m.Put("api", "HEAD", "api.TestGetUser", rc("t", "s", "b", 1, "api.GetUser")) // covering test
	m.PutExternalRefs("web", "HEAD", []query.ExternalRef{{From: "web.fetch", Module: "github.com/acme/api", Name: "GetUser", ResolutionMethod: query.ImportResolution, Confidence: 0.75}})

	r := query.BlastRadius(m, "api", "HEAD", "GetUser")
	if r.Symbol != "api.GetUser" {
		t.Fatalf("symbol resolved to %q", r.Symbol)
	}
	// api.handler references GetUser (in-repo caller); TestGetUser is a covering test.
	if !hasStr(r.InRepoCallers, "api.handler") {
		t.Fatalf("in-repo callers = %v", r.InRepoCallers)
	}
	if !hasStr(r.CoveringTests, "api.TestGetUser") {
		t.Fatalf("covering tests = %v", r.CoveringTests)
	}
	// web.fetch is a module-resolved fleet caller.
	found := false
	for _, c := range r.FleetCallers {
		if c.Repo == "web" && c.Caller == "web.fetch" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fleet callers = %+v", r.FleetCallers)
	}
	if r.Summary == "" {
		t.Fatal("summary should be populated")
	}
}

func hasStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
