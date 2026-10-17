//go:build sqlite && treesitter

package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// shopFixture is a small multi-language repo: a Go server, a FastAPI service
// with an argparse CLI, a React UI, and the project files around them.
var shopFixture = map[string]string{
	"README.md": "# Shop\n\n[![ci](https://example.invalid/badge.svg)](https://example.invalid)\n\nShop is a demo store that sells widgets. It has three parts.\n",
	"go.mod":    "module example.com/shop\n\ngo 1.22\n\nrequire (\n\tgithub.com/go-chi/chi/v5 v5.0.0\n\tgolang.org/x/text v0.3.0 // indirect\n)\n",
	"cmd/shopd/main.go": `package main

import "net/http"

func main() {
	http.HandleFunc("/api/orders", listOrders)
	http.ListenAndServe(":8080", nil)
}

func listOrders(w http.ResponseWriter, r *http.Request) {
	orders.Load()
}
`,
	"orders/doc.go": "// Package orders keeps the order book.\npackage orders\n",
	"orders/orders.go": `package orders

// Load reads every order from the book.
// It never fails.
func Load() []string {
	return nil
}

func Save() {
	Load()
}
`,
	"orders/orders_test.go": "package orders\n\nfunc TestLoad(t *T) {\n\tLoad()\n}\n",
	"api/__init__.py":       "\"\"\"HTTP API for the shop.\"\"\"\n",
	"api/app.py": `from fastapi import APIRouter

router = APIRouter(prefix="/api")


@router.get("/orders")
def list_orders():
    """Return every order."""
    return load_orders()


def load_orders():
    return []
`,
	"api/cli.py": `import argparse


def main():
    p = argparse.ArgumentParser()
    sub = p.add_subparsers()
    sub.add_parser("serve")
    sub.add_parser("migrate")


if __name__ == "__main__":
    main()
`,
	"requirements.txt":  "fastapi==0.110.0\nrequests>=2\n# a comment\n",
	"web/package.json":  `{"name": "shop-web", "description": "Storefront UI.", "scripts": {"build": "tsc", "start": "vite"}, "dependencies": {"react": "^18.0.0"}, "devDependencies": {"jest": "^29.0.0"}}`,
	"web/src/index.tsx": "import { createRoot } from 'react-dom/client';\ncreateRoot(document.getElementById('root')).render(<App />);\n",
	"web/src/App.tsx": `import { Route } from 'react-router-dom';

export function App() {
  return <Route path="/cart" element={<CartPage />} />;
}

export function CartPage() {
  return <button onClick={submitOrder}>Buy</button>;
}

export function submitOrder() {
  return fetch('/api/orders');
}
`,
	"Dockerfile":               "FROM golang:1.22 AS build\nRUN go build ./...\nFROM debian:12\nCMD [\"shopd\", \\\n     \"--port\", \"8080\"]\n",
	"Makefile":                 ".PHONY: build test\nbuild:\n\tgo build ./...\ntest: build\n\tgo test ./...\nVAR := x\n",
	"docker-compose.yml":       "services:\n  api:\n    image: shop-api\n  db:\n    image: postgres\n",
	".github/workflows/ci.yml": "name: CI\non: [push, pull_request]\njobs:\n  test:\n    runs-on: ubuntu-latest\n  lint:\n    runs-on: ubuntu-latest\n",
	".github/CODEOWNERS":       "* @someone\n",
}

func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, c := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		write(t, p, c)
	}
}

func shopStore(t *testing.T) *sqlite.Store {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, shopFixture)
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := processing.IndexDir(st, "shop", "HEAD", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestMapMultiLanguageRepo(t *testing.T) {
	st := shopStore(t)
	m := query.Map(st, "shop", "HEAD", 6000)
	if m.Summary != "Shop is a demo store that sells widgets." || m.SummarySource != "README.md:5" {
		t.Errorf("summary = %q [%s]", m.Summary, m.SummarySource)
	}
	langs := map[string]bool{}
	for _, l := range m.Languages {
		langs[l.Lang] = l.Files > 0 && l.Symbols > 0
	}
	for _, l := range []string{"go", "python", "typescript"} {
		if !langs[l] {
			t.Errorf("language %s missing or empty: %+v", l, m.Languages)
		}
	}
	kinds := map[string][]query.EntryPoint{}
	for _, e := range m.EntryPoints {
		kinds[e.Kind] = append(kinds[e.Kind], e)
	}
	for _, k := range []string{query.EntryMain, query.EntryCLI, query.EntryHTTP, query.EntryUIRoot, query.EntryUIRoutes,
		query.EntryDocker, query.EntryCompose, query.EntryScripts, query.EntryMake, query.EntryCI} {
		if len(kinds[k]) == 0 {
			t.Errorf("no %s entry point; got %+v", k, m.EntryPoints)
		}
	}
	has := func(k, sub string) bool {
		for _, e := range kinds[k] {
			if strings.Contains(e.Name+" "+e.Framework+" "+e.Detail+" "+strings.Join(e.Items, ","), sub) {
				return true
			}
		}
		return false
	}
	for _, c := range []struct{ kind, want string }{
		{query.EntryCLI, "serve,migrate"}, {query.EntryHTTP, "fastapi"}, {query.EntryHTTP, "net/http"},
		{query.EntryUIRoutes, "/cart → CartPage"}, {query.EntryDocker, `CMD ["shopd", "--port", "8080"]`},
		{query.EntryCompose, "api,db"}, {query.EntryMake, "build,test"}, {query.EntryCI, "test,lint"},
		{query.EntryCI, "on: push, pull_request"}, {query.EntryScripts, "start: vite"}, {query.EntryMain, "shopd"},
	} {
		if !has(c.kind, c.want) {
			t.Errorf("%s entry lacks %q: %+v", c.kind, c.want, kinds[c.kind])
		}
	}
	purposes := map[string]string{}
	for _, md := range m.Modules {
		purposes[md.Path] = md.Purpose
	}
	// web/src inherits the purpose of web/, which holds only its package.json.
	for p, want := range map[string]string{"orders": "Package orders keeps the order book.", "api": "HTTP API for the shop.", "web/src": "Storefront UI."} {
		if purposes[p] != want {
			t.Errorf("module %s purpose = %q, want %q (modules %+v)", p, purposes[p], want, m.Modules)
		}
	}
	if purposes["cmd/shopd"] != "" {
		t.Errorf("a module with no doc must have no purpose, got %q", purposes["cmd/shopd"])
	}
	deps := map[string]query.Dependency{}
	for _, d := range m.Dependencies {
		deps[d.Ecosystem+":"+d.Name] = d
	}
	if d := deps["go:github.com/go-chi/chi/v5"]; d.Version != "v5.0.0" || d.Dev {
		t.Errorf("go dep = %+v", d)
	}
	if d := deps["go:golang.org/x/text"]; !d.Dev {
		t.Errorf("an indirect go dep is not a direct one: %+v", d)
	}
	if _, ok := deps["pypi:fastapi"]; !ok {
		t.Errorf("pypi deps missing: %+v", m.Dependencies)
	}
	if d := deps["npm:jest"]; !d.Dev {
		t.Errorf("npm devDependency = %+v", d)
	}
	if len(m.Hubs) == 0 || m.Hubs[0].Path == "" {
		t.Errorf("hubs = %+v", m.Hubs)
	}
	for _, h := range m.Hubs {
		if strings.Contains(h.Symbol, "TestLoad") {
			t.Errorf("a test is not a hub: %+v", h)
		}
	}
	// Everything under .github is the project's own text (CODEOWNERS too).
	owners := false
	for _, f := range st.Files("shop", "HEAD") {
		owners = owners || f.Path == ".github/CODEOWNERS"
	}
	if !owners {
		t.Error(".github/CODEOWNERS must be stored")
	}

	// A tight budget truncates explicitly and says what to do next.
	small := query.Map(st, "shop", "HEAD", 150)
	if !small.Truncated || len(small.Omitted) == 0 || len(small.Next) == 0 || !strings.Contains(small.Next[0], "--budget") {
		t.Errorf("small budget: truncated=%v omitted=%v next=%v", small.Truncated, small.Omitted, small.Next)
	}
	js, err := interfaces.MapJSON(m)
	if err != nil || !strings.Contains(js, `"entry_points"`) || !strings.Contains(js, `"_meta"`) || !strings.Contains(js, `"truncated": false`) {
		t.Errorf("map JSON wire format: %v\n%s", err, js)
	}
	if txt := interfaces.MapText(m); !strings.Contains(txt, "## entry points") || !strings.Contains(txt, "## modules") {
		t.Errorf("map text:\n%s", txt)
	}
}

func TestExplainSymbolFileDirRoute(t *testing.T) {
	st := shopStore(t)
	opt := query.ExplainOptions{Budget: 4000}

	r := query.Explain(st, "HEAD", "orders.Load", opt)
	s := r.Symbol
	if r.Kind != "symbol" || s == nil {
		t.Fatalf("explain Load: %+v", r)
	}
	if s.Kind != "function" || s.Signature != "func Load() []string" || s.Doc != "Load reads every order from the book.\nIt never fails." || s.Path != "orders/orders.go" || s.StartLine != 5 {
		t.Errorf("symbol facts = kind %q sig %q doc %q at %s:%d", s.Kind, s.Signature, s.Doc, s.Path, s.StartLine)
	}
	callers := map[string]bool{}
	for _, c := range s.Callers {
		callers[c.Symbol] = true
		if c.ResolutionMethod == "" || c.Confidence == 0 {
			t.Errorf("caller without provenance: %+v", c)
		}
	}
	if !callers["orders.Save"] {
		t.Errorf("callers = %+v", s.Callers)
	}
	if len(s.Tests) == 0 || !strings.Contains(s.Tests[0].In, "TestLoad") || !s.Tests[0].Calls {
		t.Errorf("tests = %+v", s.Tests)
	}
	if !strings.HasPrefix(s.Edit, "reponite brief orders.Load") {
		t.Errorf("edit pointer = %q", s.Edit)
	}

	r = query.Explain(st, "HEAD", "list_orders", opt)
	if r.Symbol == nil || r.Symbol.Doc != "Return every order." || len(r.Symbol.Decorators) == 0 {
		t.Fatalf("explain list_orders: %+v", r.Symbol)
	}
	route := false
	for _, re := range r.Symbol.ReachedBy {
		if re.Kind == "route" && strings.Contains(re.Detail, "GET /api/orders") {
			route = true
		}
	}
	if !route {
		t.Errorf("list_orders reached_by = %+v", r.Symbol.ReachedBy)
	}

	r = query.Explain(st, "HEAD", "submitOrder", opt)
	ui := false
	for _, re := range r.Symbol.ReachedBy {
		if re.Kind == "ui-event" && strings.Contains(re.Detail, "onClick") {
			ui = true
		}
	}
	if !ui {
		t.Errorf("submitOrder reached_by = %+v", r.Symbol.ReachedBy)
	}

	r = query.Explain(st, "HEAD", "orders/orders.go", opt)
	if r.Kind != "file" || len(r.File.Symbols) != 2 || len(r.File.Inbound) == 0 {
		t.Errorf("explain file: %+v", r.File)
	}
	r = query.Explain(st, "HEAD", "orders/orders.go:10", opt)
	if r.Kind != "symbol" || r.Symbol.Symbol != "orders.Save" {
		t.Errorf("explain path:line → %s %+v", r.Kind, r.Symbol)
	}
	r = query.Explain(st, "HEAD", "web/src/App.tsx", opt)
	if r.Kind != "file" || len(r.File.Imports) == 0 || r.File.Imports[0] != "react-router-dom" {
		t.Errorf("explain tsx file imports: %+v", r.File)
	}
	r = query.Explain(st, "HEAD", "orders", opt)
	if r.Kind != "directory" || r.Dir.Purpose != "Package orders keeps the order book." || len(r.Dir.Files) == 0 {
		t.Errorf("explain dir: %+v", r.Dir)
	}

	r = query.Explain(st, "HEAD", "GET /api/orders", opt)
	if r.Kind != "route" || len(r.Routes) == 0 {
		t.Fatalf("explain route: %+v", r)
	}
	var handlers []string
	clients := 0
	for _, er := range r.Routes {
		if er.Handler != nil {
			handlers = append(handlers, er.Handler.Symbol)
		}
		clients += len(er.Clients)
	}
	if !containsSub(handlers, "list_orders") || clients == 0 {
		t.Errorf("route handlers %v, clients %d: %+v", handlers, clients, r.Routes)
	}

	if r = query.Explain(st, "HEAD", "noSuchThing", opt); r.Kind != "not_found" {
		t.Errorf("miss: %+v", r)
	}
	if txt := interfaces.ExplainText(query.Explain(st, "HEAD", "orders.Load", opt)); !strings.Contains(txt, "# function orders.Load") || !strings.Contains(txt, "## callers") {
		t.Errorf("explain text:\n%s", txt)
	}
}

func containsSub(ss []string, sub string) bool {
	for _, s := range ss {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Project files come from a git ref too, including the CI workflow under
// .github (the only dot-directory read).
func TestMapFromGitRef(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, shopFixture)
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("init", &git.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@e", When: time.Unix(0, 0).UTC()}}); err != nil {
		t.Fatal(err)
	}
	st, err := sqlite.Open(filepath.Join(t.TempDir(), "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := processing.IndexGitRef(st, "shop", "v1", dir, "HEAD", version.NormVer); err != nil {
		t.Fatal(err)
	}
	m := query.Map(st, "shop", "v1", 6000)
	var ci, docker, readme bool
	for _, e := range m.EntryPoints {
		ci = ci || e.Kind == query.EntryCI
		docker = docker || e.Kind == query.EntryDocker
	}
	readme = m.Summary != ""
	if !ci || !docker || !readme {
		t.Errorf("git ref map: ci=%v docker=%v readme=%v entries=%+v", ci, docker, readme, m.EntryPoints)
	}
}
