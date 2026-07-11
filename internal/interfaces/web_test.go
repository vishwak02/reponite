package interfaces_test

import (
	"io"
	"net/http"
	"net/http/httptest"
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

	if !strings.Contains(get("/"), "<title>reponite") {
		t.Fatal("dashboard HTML not served at /")
	}
	// The embedded CSS/JS assets are served (proves //go:embed wired correctly).
	if !strings.Contains(get("/style.css"), "--accent") {
		t.Fatal("/style.css not served from embedded asset")
	}
	if !strings.Contains(get("/app.js"), "async function brief") {
		t.Fatal("/app.js not served from embedded asset")
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
	// Overview: per-repo/ref index stats (the Overview/database view's data).
	ov := get("/api/overview")
	if !strings.Contains(ov, "billing") || !strings.Contains(ov, "symbols") {
		t.Fatalf("/api/overview incomplete: %s", ov)
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
