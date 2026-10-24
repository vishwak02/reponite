package interfaces_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// v2Store is shopStore plus what the discovery endpoints read: a logged
// message, an error value and its handler, a config read, and a derived
// class.
func v2Store() *storage.Mem {
	m := shopStore()
	src := "package billing\n\nvar ErrDeclined = errors.New(\"card declined\")\n\nfunc Charge() error {\n\tlog.Printf(\"charging card %s\", id)\n\tif os.Getenv(\"BILLING_LIMIT\") == \"\" {\n\t\treturn ErrDeclined\n\t}\n\treturn save()\n}\n"
	m.PutFile("billing", "HEAD", query.File{Path: "billing/charge.go", Content: src, Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 5, EndLine: 11}}})
	m.PutFile("billing", "HEAD", query.File{Path: "billing/handler_test.go", Content: "package billing\n\nfunc TestDeclined(t *testing.T) {\n\tif err := Charge(); err != ErrDeclined {\n\t\tt.Fatal(err)\n\t}\n}\n",
		Symbols: []query.SymbolSpan{{Name: "TestDeclined", StartLine: 3, EndLine: 7}}})
	m.PutFile("billing", "HEAD", query.File{Path: "plugins/card.h", Content: "class Gateway {\n public:\n  virtual void pay();\n};\nclass CardGateway : public Gateway {\n public:\n  void pay() override;\n};\n"})
	return m
}

// The v2 endpoints, each with the MCP tool and arguments that give the same
// answer from the same query function and formatter.
var v2Cases = []struct {
	path  string
	tool  string
	args  map[string]string
	keys  []string // top-level keys the dashboard reads
	has   string
	etag  bool // answers only from the index
	isSug bool // an unknown symbol: the "did you mean" envelope
}{
	{"/api/v2/explain?target=billing.Charge&repo=billing", "reponite_explain", map[string]string{"target": "billing.Charge", "repo": "billing"}, []string{"target", "kind", "symbol"}, "billing.validateCard", true, false},
	{"/api/v2/explain?target=billing/charge.go", "reponite_explain", map[string]string{"target": "billing/charge.go"}, []string{"kind", "file"}, `"file"`, true, false},
	{"/api/v2/context?symbol=billing.Charge&repo=billing", "reponite_context", map[string]string{"symbol": "billing.Charge", "repo": "billing"}, []string{"symbol", "callee_edges"}, "go-types", true, false},
	{"/api/v2/context?symbol=Chrage&repo=billing", "reponite_context", map[string]string{"symbol": "Chrage", "repo": "billing"}, []string{"found"}, "billing.Charge", true, true},
	{"/api/v2/impls?base=Gateway", "reponite_impls", map[string]string{"base": "Gateway"}, []string{"base", "impls"}, "CardGateway", true, false},
	{"/api/v2/tests?symbol=billing.Charge", "reponite_tests", map[string]string{"symbol": "billing.Charge"}, []string{"symbol", "tests", "commands"}, "TestCharge", true, false},
	{"/api/v2/tests?symbol=Chrage", "reponite_tests", map[string]string{"symbol": "Chrage"}, []string{"found"}, "", true, true},
	{"/api/v2/why?symbol=billing.Charge", "reponite_why", map[string]string{"symbol": "billing.Charge"}, []string{"symbol", "commits", "compat_history"}, `"v1"`, false, false},
	{"/api/v2/errors?q=card+declined", "reponite_errors", map[string]string{"query": "card declined"}, []string{"mode", "producers"}, "ErrDeclined", true, false},
	{"/api/v2/logsite?line=charging+card+4242", "reponite_logsite", map[string]string{"line": "charging card 4242"}, []string{"line", "sites"}, "charging card %s", true, false},
	{"/api/v2/semsearch?q=charge+card", "reponite_semsearch", map[string]string{"query": "charge card"}, []string{"ranker", "hits"}, "term-idf", true, false},
	{"/api/v2/investigate?q=how+is+a+card+charged", "reponite_investigate", map[string]string{"question": "how is a card charged"}, []string{"question", "findings"}, "Charge", true, false},
	{"/api/v2/config?key=BILLING_LIMIT", "reponite_config", map[string]string{"key": "BILLING_LIMIT"}, []string{"keys", "total"}, "BILLING_LIMIT", true, false},
}

// Every v2 endpoint answers 200 with the shape its view reads, and the same
// answer the MCP tool gives: one query function, one formatter.
func TestWebV2Endpoints(t *testing.T) {
	m := v2Store()
	srv := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "billing"}).Routes())
	defer srv.Close()
	ts := &interfaces.ToolServer{Store: m, Repo: "billing"}
	for _, c := range v2Cases {
		r := call(t, srv, "GET", c.path, nil)
		if r.code != 200 {
			t.Errorf("%s -> %d %s", c.path, r.code, r.body)
			continue
		}
		if ct := r.hdr.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", c.path, ct)
		}
		var obj map[string]any
		if err := json.Unmarshal(r.body, &obj); err != nil {
			t.Errorf("%s: not a JSON object: %v\n%s", c.path, err, r.body)
			continue
		}
		for _, k := range c.keys {
			if _, ok := obj[k]; !ok {
				t.Errorf("%s: no %q in %s", c.path, k, r.body)
			}
		}
		if c.has != "" && !strings.Contains(string(r.body), c.has) {
			t.Errorf("%s: %q missing from %s", c.path, c.has, r.body)
		}
		want, err := ts.Call(c.tool, c.args)
		if err != nil {
			t.Errorf("%s: MCP %s: %v", c.path, c.tool, err)
			continue
		}
		if string(r.body) != want {
			t.Errorf("%s differs from MCP %s:\nweb: %s\nmcp: %s", c.path, c.tool, r.body, want)
		}
	}
}

// Answers that depend only on the index carry an ETag and revalidate to 304;
// why (git history) never does, nor do the ranked answers when an external
// ranker orders them.
func TestWebV2ETags(t *testing.T) {
	h := &interfaces.WebHandler{Store: v2Store(), Repo: "billing", DataVersion: func() string { return "1" }}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()
	for _, c := range v2Cases {
		r := call(t, srv, "GET", c.path, nil)
		tag := r.hdr.Get("ETag")
		if (tag != "") != c.etag {
			t.Errorf("%s: ETag %q, want one: %v", c.path, tag, c.etag)
			continue
		}
		if tag == "" {
			continue
		}
		if again := call(t, srv, "GET", c.path, map[string]string{"If-None-Match": tag}); again.code != http.StatusNotModified {
			t.Errorf("%s: revalidation: %d", c.path, again.code)
		}
	}
	ranked := httptest.NewServer((&interfaces.WebHandler{Store: v2Store(), Repo: "billing", Ranker: query.TermIDFRanker{}, DataVersion: func() string { return "1" }}).Routes())
	defer ranked.Close()
	for _, p := range []string{"/api/v2/semsearch?q=charge", "/api/v2/investigate?q=charge"} {
		if r := call(t, ranked, "GET", p, nil); r.code != 200 || r.hdr.Get("ETag") != "" {
			t.Errorf("%s with a ranker: want 200 and no ETag, got %d %q", p, r.code, r.hdr.Get("ETag"))
		}
	}
}

// v2 is read-only, compressed when large, compacted when Compact, and bounded.
func TestWebV2Methods(t *testing.T) {
	m := v2Store()
	srv := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "billing", Compact: true}).Routes())
	defer srv.Close()
	for _, c := range v2Cases {
		path := c.path[:strings.Index(c.path, "?")]
		if r := call(t, srv, "POST", c.path, nil); r.code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s: want 405, got %d", path, r.code)
		}
	}
	if r := call(t, srv, "GET", "/api/v2/explain", nil); r.code != http.StatusBadRequest || !strings.Contains(string(r.body), "target") {
		t.Errorf("explain without a target: want 400 naming it, got %d %s", r.code, r.body)
	}
	r := call(t, srv, "GET", "/api/v2/investigate?q=how+is+a+card+charged", nil)
	if strings.Contains(string(r.body), "\n  ") {
		t.Errorf("compact answer is indented: %s", r.body)
	}
	gz := call(t, srv, "GET", "/api/v2/investigate?q=how+is+a+card+charged", map[string]string{"Accept-Encoding": "gzip"})
	if len(r.body) >= 1024 && gz.hdr.Get("Content-Encoding") != "gzip" {
		t.Errorf("a %d-byte answer is not gzipped", len(r.body))
	}
	// a limit beyond the bound is the bound
	for _, p := range []string{"/api/v2/impls?base=Gateway&limit=99999999", "/api/v2/logsite?line=charging&limit=-1", "/api/v2/semsearch?q=charge&limit=99999999", "/api/v2/tests?symbol=billing.Charge&depth=99999"} {
		if r := call(t, srv, "GET", p, nil); r.code != 200 {
			t.Errorf("%s: %d %s", p, r.code, r.body)
		}
	}
}

// why reads history from the injected provider: the commits that shaped the
// symbol's lines come back with it.
func TestWebV2WhyHistory(t *testing.T) {
	m := v2Store()
	m.PutManifest("billing", "HEAD", content.Manifest{Ref: "HEAD", Commit: "c0ffee-HEAD", Blobs: []content.Hash{content.Hash("blob-HEAD")}})
	h := &interfaces.WebHandler{Store: m, Repo: "billing", History: fakeHistory{}}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()
	r := call(t, srv, "GET", "/api/v2/why?symbol=billing.Charge&repo=billing", nil)
	if r.code != 200 || !strings.Contains(string(r.body), "charge cards") {
		t.Fatalf("why with history: %d %s", r.code, r.body)
	}
}

type fakeHistory struct{}

func (fakeHistory) LineHistory(repo string, r query.LineRange, limit int) ([]query.Change, error) {
	return []query.Change{{Commit: "abc123", Time: 1700000000, Author: "dev", Subject: "charge cards", Paths: []string{r.Path}, Added: 3, Kind: query.ChangeBody}}, nil
}
func (fakeHistory) Pickaxe(repo, commit, needle string, paths []string, limit int) ([]query.Change, error) {
	return nil, nil
}
func (fakeHistory) Contains(repo string, commits []string, tips map[string]string) (map[string][]string, error) {
	return map[string][]string{}, nil
}
func (fakeHistory) CommitTimes(repo string, commits []string) (map[string]int64, error) {
	return map[string]int64{}, nil
}
