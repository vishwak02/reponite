//go:build sqlite && treesitter

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// End to end on real source: an api repo (module github.com/acme/api) exports
// GetUser; a web repo imports that module and calls api.GetUser. After indexing
// both through tree-sitter + SQLite, module-resolved ximpact stitches the web
// caller to the api definition precisely — matched on module path, not bare
// name — and marks it import-resolved. A MultiStore fuses the two repos' DBs.
func TestEndToEndModuleResolvedXImpact(t *testing.T) {
	apiDir := t.TempDir()
	webDir := t.TempDir()

	// api repo: a Go module that defines GetUser.
	mustWrite(t, filepath.Join(apiDir, "go.mod"), "module github.com/acme/api\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(apiDir, "user.go"),
		"package api\n\nfunc GetUser(id int) string { return \"u\" }\n")

	// web repo: a Go module that imports api and calls api.GetUser.
	mustWrite(t, filepath.Join(webDir, "go.mod"), "module github.com/acme/web\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(webDir, "handler.go"),
		"package web\n\nimport api \"github.com/acme/api\"\n\nfunc Handle() string { return api.GetUser(1) }\n")

	apiStore, err := sqlite.Open(filepath.Join(apiDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer apiStore.Close()
	webStore, err := sqlite.Open(filepath.Join(webDir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer webStore.Close()

	if err := processing.IndexDir(apiStore, "api", "HEAD", apiDir, version.NormVer); err != nil {
		t.Fatal(err)
	}
	if err := processing.IndexDir(webStore, "web", "HEAD", webDir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	// Module paths were detected from the go.mod files.
	if m := apiStore.ModulePath("api"); m != "github.com/acme/api" {
		t.Fatalf("api module_path = %q; want github.com/acme/api", m)
	}
	// web resolved its api.GetUser call to a module-precise external reference.
	hits := webStore.ExternalRefsTo("github.com/acme/api", "GetUser")
	if len(hits) != 1 || hits[0].ResolutionMethod != processing.MethodImport {
		t.Fatalf("web external ref to api.GetUser = %+v", hits)
	}

	// Fleet view: both repos in one MultiStore, ximpact across the boundary.
	fleet := storage.NewMultiStore(apiStore, webStore)
	res := query.XImpact(fleet, "GetUser", "")
	if len(res.Modules) != 1 || res.Modules[0] != "github.com/acme/api" {
		t.Fatalf("target modules = %v; want [github.com/acme/api]", res.Modules)
	}
	found := false
	for _, c := range res.Callers {
		if c.Repo == "web" && c.ResolutionMethod == query.ImportResolution && c.Module == "github.com/acme/api" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a module-resolved web caller of api.GetUser; got %+v", res.Callers)
	}
	if len(res.Definitions) != 1 || res.Definitions[0].Repo != "api" || res.Definitions[0].Module != "github.com/acme/api" {
		t.Fatalf("expected the api definition site with its module; got %+v", res.Definitions)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
