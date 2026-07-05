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
}
