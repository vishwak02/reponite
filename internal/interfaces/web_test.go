package interfaces_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestWebHandler(t *testing.T) {
	m := storage.NewMem()
	m.Put("billing", "HEAD", "billing.Charge", storage.SymbolRecord{
		SymbolHash: content.Hash("c"), SignatureHash: content.Hash("s"),
		BehaviorHash: content.Hash("b"), BehaviorConf: 1, DirectConf: 1,
	})
	m.PutFile("billing", "HEAD", query.File{
		Path:    "billing/charge.go",
		Content: "package billing\nfunc Charge() error { return nil }\n",
		Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 2, EndLine: 2}},
	})

	srv := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "billing"}).Routes())
	defer srv.Close()

	get := func(path string) string {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s -> %d", path, resp.StatusCode)
		}
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}

	app := get("/")
	if !strings.Contains(app, "<title>reponite") || !strings.Contains(app, `id="root"`) {
		t.Fatal("dashboard app not served at /")
	}
	// the app's built assets are embedded and served under /assets/
	js0 := regexp.MustCompile(`/assets/[^"]+\.js`).FindString(app)
	if js0 == "" || !strings.Contains(get(js0), "api/") {
		t.Fatalf("app script %q not served from the embedded build", js0)
	}
	if resp, err := http.Get(srv.URL + "/app.js"); err != nil || resp.StatusCode != 404 {
		t.Fatalf("the old dashboard's /app.js is still served: %v %v", resp, err)
	}
	if !strings.Contains(get("/api/refs"), "HEAD") {
		t.Fatal("/api/refs missing HEAD")
	}
	if !strings.Contains(get("/api/search?q=Charge&ref=HEAD"), "billing.Charge") {
		t.Fatal("/api/search did not find Charge")
	}
	brief := get("/api/brief?symbol=Charge&ref=HEAD")
	if !strings.Contains(brief, "billing.Charge") || !strings.Contains(brief, "func Charge()") {
		t.Fatalf("/api/brief incomplete: %s", brief)
	}
	// /api/file serves one stored file; an unknown path is a 404
	if f := get("/api/file?path=billing/charge.go&ref=HEAD"); !strings.Contains(f, `"content"`) {
		t.Fatalf("/api/file incomplete: %s", f)
	}
	if resp, err := http.Get(srv.URL + "/api/file?path=nope.go&ref=HEAD"); err != nil || resp.StatusCode != 404 {
		t.Fatalf("/api/file for a missing path: want 404, got %v %v", resp, err)
	}
	// Overview: per-repo/ref index stats (the Overview/database view's data).
	ov := get("/api/overview")
	if !strings.Contains(ov, "billing") || !strings.Contains(ov, "symbols") {
		t.Fatalf("/api/overview incomplete: %s", ov)
	}

	// The app's pages call the Usages and Verify endpoints.
	var bundle strings.Builder
	chunks, _ := filepath.Glob("webui/dist/assets/*.js")
	for _, c := range chunks {
		bundle.WriteString(get("/assets/" + filepath.Base(c)))
	}
	for _, api := range []string{`"usages"`, "verify_edit"} {
		if !strings.Contains(bundle.String(), api) {
			t.Fatalf("no app script calls /api/%s", api)
		}
	}

	// /api/usages backs the Usages view: the call site with line + confirmation.
	usages := get("/api/usages?symbol=Charge&ref=HEAD")
	if !strings.Contains(usages, `"total"`) || !strings.Contains(usages, `"usages"`) {
		t.Fatalf("/api/usages incomplete: %s", usages)
	}

	// The Verify view POSTs the proposed content as a form body (large files
	// don't fit in a query string); FormValue must read it.
	resp, err := http.Post(srv.URL+"/api/verify_edit?path=billing/charge.go&ref=HEAD",
		"application/x-www-form-urlencoded",
		strings.NewReader("content="+`package billing`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// No ParseSymbols injected in the pure test -> 501, but the route must
	// exist and answer (the treesitter build wires the parser).
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("/api/verify_edit POST: want 501 without a parser, got %d", resp.StatusCode)
	}
}

// A team server over a MultiStore lists every repo and routes the ?repo= param.
func TestWebHandlerTeam(t *testing.T) {
	a := storage.NewMem()
	a.Put("api", "HEAD", "api.GetUser", storage.SymbolRecord{SymbolHash: content.Hash("g"), SignatureHash: content.Hash("s"), BehaviorHash: content.Hash("b"), BehaviorConf: 1, DirectConf: 1})
	b := storage.NewMem()
	b.Put("svc", "HEAD", "svc.Handler", storage.SymbolRecord{SymbolHash: content.Hash("h"), SignatureHash: content.Hash("s"), BehaviorHash: content.Hash("b"), BehaviorConf: 1, DirectConf: 1})
	ms := storage.NewMultiStore(a, b)

	srv := httptest.NewServer((&interfaces.WebHandler{Store: ms, Repo: "api"}).Routes())
	defer srv.Close()
	get := func(path string) string {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return string(body)
	}

	if repos := get("/api/repos"); !strings.Contains(repos, "api") || !strings.Contains(repos, "svc") {
		t.Fatalf("/api/repos must list both repos: %s", repos)
	}
	// ?repo= routes search to the other repo in the fleet.
	if s := get("/api/search?repo=svc&q=Handler"); !strings.Contains(s, "svc.Handler") {
		t.Fatalf("/api/search?repo=svc did not reach the svc repo: %s", s)
	}
	// default (no repo param) uses the handler's repo.
	if s := get("/api/search?q=GetUser"); !strings.Contains(s, "api.GetUser") {
		t.Fatalf("default repo search failed: %s", s)
	}
}
