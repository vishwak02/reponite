package interfaces_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
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

// shopStore is a small repo at two refs: validateCard's body changed between
// v1 and HEAD, so Charge (which calls it) is behavior_changed.
func shopStore() *storage.Mem {
	m := storage.NewMem()
	sym := func(ref, name, behavior string, test bool, callees ...string) {
		var cs []query.Callee
		for _, c := range callees {
			cs = append(cs, query.Callee{Name: c, ResolutionMethod: "go-types", Confidence: 1})
		}
		m.Put("billing", ref, name, storage.SymbolRecord{
			Lang: "go", IsTest: test, SymbolHash: content.Hash(name + behavior), SignatureHash: content.Hash("sig:" + name),
			BehaviorHash: content.Hash(behavior), BehaviorConf: 1, DirectConf: 1, Callees: cs,
		})
	}
	src := "package billing\n\nfunc Charge() error {\n\tif err := validateCard(); err != nil {\n\t\treturn err\n\t}\n\treturn save()\n}\n\nfunc validateCard() error { return nil }\n\nfunc save() error { return nil }\n"
	for _, ref := range []string{"v1", "HEAD"} {
		sym(ref, "billing.Charge", "charge-"+ref, false, "billing.validateCard", "billing.save")
		sym(ref, "billing.validateCard", "validate-"+ref, false)
		sym(ref, "billing.save", "save", false)
		sym(ref, "billing.TestCharge", "test", true, "billing.Charge")
		m.PutFile("billing", ref, query.File{Path: "billing/charge.go", Content: src, Symbols: []query.SymbolSpan{
			{Name: "Charge", StartLine: 3, EndLine: 8}, {Name: "validateCard", StartLine: 10, EndLine: 10}, {Name: "save", StartLine: 12, EndLine: 12},
		}})
		m.PutFile("billing", ref, query.File{Path: "billing/charge_test.go", Content: "package billing\n\nfunc TestCharge(t *testing.T) { Charge() }\n",
			Symbols: []query.SymbolSpan{{Name: "TestCharge", StartLine: 3, EndLine: 3}}})
		m.PutManifest("billing", ref, content.Manifest{Ref: ref, Commit: "c0ffee-" + ref, Blobs: []content.Hash{content.Hash("blob-" + ref)}})
	}
	return m
}

type apiResp struct {
	code int
	hdr  http.Header
	body []byte
}

func call(t *testing.T, srv *httptest.Server, method, path string, hdr map[string]string) apiResp {
	t.Helper()
	req, err := http.NewRequest(method, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// a plain transport: no transparent gzip, so the test sees the wire
	resp, err := (&http.Transport{DisableCompression: true}).RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return apiResp{resp.StatusCode, resp.Header, b}
}

// Every endpoint the dashboard calls answers 200 with the JSON shape its page
// reads (web/src/lib/api.ts).
func TestWebAPIEndpoints(t *testing.T) {
	h := &interfaces.WebHandler{Store: shopStore(), Repo: "billing",
		ParseSymbols: func(path, src string) []query.EditedSymbol {
			if strings.Contains(src, "func Charge(") {
				return []query.EditedSymbol{{Name: "Charge", SignatureHash: content.Hash("sig:billing.Charge")}}
			}
			return nil
		}}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()

	cases := []struct {
		path string
		keys []string // top-level keys the page reads; nil for a JSON array
		has  string   // a value the answer must contain
	}{
		{"/api/repos", []string{"repos"}, "billing"},
		{"/api/overview", []string{"repos"}, `"symbols"`},
		{"/api/refs?repo=billing", []string{"repo", "refs"}, `"v1"`},
		{"/api/search?q=Charge&repo=billing&ref=HEAD", nil, "billing.Charge"},
		{"/api/search?q=Chrage&repo=billing&ref=HEAD", []string{"found"}, ""},
		{"/api/brief?repo=billing&ref=HEAD&symbol=billing.Charge", []string{"symbol", "target", "callees"}, "func Charge()"},
		{"/api/usages?repo=billing&ref=HEAD&symbol=Charge", []string{"symbol", "total", "usages"}, "charge_test.go"},
		{"/api/compat?repo=billing&ref=HEAD&symbol=billing.Charge", []string{"symbol", "origin", "verdicts"}, "behavior_changed"},
		{"/api/file?repo=billing&ref=HEAD&path=billing/charge.go", []string{"path", "content"}, "validateCard"},
		{"/api/graph?repo=billing&ref=HEAD&symbol=billing.Charge&depth=2&tests=false&external=false&limit=120", []string{"focus", "nodes", "edges"}, "billing.save"},
		{"/api/links?repo=billing&ref=HEAD", nil, ""},
		{"/api/topics?ref=HEAD", nil, ""},
		{"/api/coverage?ref=HEAD", []string{"repos"}, "billing"},
		{"/api/fleetmap?ref=HEAD", []string{"repos"}, "billing"},
		{"/api/grep?repo=billing&ref=HEAD&pattern=validateCard&limit=100&offset=0", []string{"matches", "total"}, "charge.go"},
		{"/api/diff?repo=billing&from=v1&to=HEAD&changed_only=true", []string{"changes"}, "billing.validateCard"},
		{"/api/ximpact?symbol=Charge&ref=HEAD", nil, ""},
		// handlers the pages do not call yet (P1)
		{"/api/context?repo=billing&ref=HEAD&symbol=billing.Charge", []string{"symbol", "callees"}, "billing.validateCard"},
		{"/api/rootcause?repo=billing&symbol=billing.Charge&from=v1&to=HEAD", []string{"target", "changed"}, "billing.validateCard"},
		{"/api/blast_radius?repo=billing&ref=HEAD&symbol=billing.Charge", nil, "billing.Charge"},
		{"/api/investigate?q=how+is+a+card+charged&ref=HEAD", []string{"question"}, ""},
	}
	for _, c := range cases {
		r := call(t, srv, "GET", c.path, nil)
		if r.code != 200 {
			t.Errorf("%s -> %d %s", c.path, r.code, r.body)
			continue
		}
		if ct := r.hdr.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: Content-Type %q", c.path, ct)
		}
		var v any
		if err := json.Unmarshal(r.body, &v); err != nil {
			t.Errorf("%s: not JSON: %v\n%s", c.path, err, r.body)
			continue
		}
		if c.keys == nil {
			if _, ok := v.(map[string]any); !ok {
				if _, ok := v.([]any); !ok {
					t.Errorf("%s: neither an object nor an array: %s", c.path, r.body)
				}
			}
		} else if obj, ok := v.(map[string]any); !ok {
			t.Errorf("%s: want an object, got %s", c.path, r.body)
		} else {
			for _, k := range c.keys {
				if _, ok := obj[k]; !ok {
					t.Errorf("%s: no %q in %s", c.path, k, r.body)
				}
			}
		}
		if c.has != "" && !strings.Contains(string(r.body), c.has) {
			t.Errorf("%s: %q missing from %s", c.path, c.has, r.body)
		}
	}
	if r := call(t, srv, "GET", "/api/file?repo=billing&ref=HEAD&path=nope.go", nil); r.code != 404 {
		t.Errorf("/api/file for a missing path: want 404, got %d", r.code)
	}

	// verify_edit: the edited content's symbols against the stored ones
	resp, err := http.Post(srv.URL+"/api/verify_edit?repo=billing&ref=HEAD&path=billing/charge.go",
		"application/x-www-form-urlencoded", strings.NewReader("content="+url.QueryEscape("package billing\n")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(b), `"removed"`) || !strings.Contains(string(b), "Charge") {
		t.Fatalf("/api/verify_edit: %d %s", resp.StatusCode, b)
	}
}

// The API is read-only: GET (and HEAD) everywhere, POST only for verify_edit.
func TestWebAPIMethods(t *testing.T) {
	srv := httptest.NewServer((&interfaces.WebHandler{Store: shopStore(), Repo: "billing"}).Routes())
	defer srv.Close()
	for _, m := range []string{"POST", "PUT", "DELETE", "PATCH"} {
		r := call(t, srv, m, "/api/repos", nil)
		if r.code != http.StatusMethodNotAllowed || !strings.Contains(r.hdr.Get("Allow"), "GET") {
			t.Errorf("%s /api/repos: want 405 with Allow, got %d %q", m, r.code, r.hdr.Get("Allow"))
		}
	}
	if r := call(t, srv, "DELETE", "/api/verify_edit", nil); r.code != http.StatusMethodNotAllowed || !strings.Contains(r.hdr.Get("Allow"), "POST") {
		t.Errorf("DELETE /api/verify_edit: want 405 allowing POST, got %d %q", r.code, r.hdr.Get("Allow"))
	}
	if r := call(t, srv, "HEAD", "/api/repos", nil); r.code != 200 || len(r.body) != 0 {
		t.Errorf("HEAD /api/repos: %d, %d body bytes", r.code, len(r.body))
	}
}

// ETag/If-None-Match: a repeated question is answered 304 without a body
// until the index changes; answers that read outside the index, and errors,
// carry no ETag.
func TestWebAPIETag(t *testing.T) {
	version := "1"
	h := &interfaces.WebHandler{Store: shopStore(), Repo: "billing", DataVersion: func() string { return version }}
	srv := httptest.NewServer(h.Routes())
	defer srv.Close()
	const p = "/api/graph?repo=billing&ref=HEAD&symbol=billing.Charge"
	first := call(t, srv, "GET", p, nil)
	tag := first.hdr.Get("ETag")
	if first.code != 200 || !strings.HasPrefix(tag, `W/"`) || first.hdr.Get("Cache-Control") != "no-cache" {
		t.Fatalf("first answer: %d ETag %q Cache-Control %q", first.code, tag, first.hdr.Get("Cache-Control"))
	}
	again := call(t, srv, "GET", p, map[string]string{"If-None-Match": tag})
	if again.code != http.StatusNotModified || len(again.body) != 0 || again.hdr.Get("ETag") != tag {
		t.Fatalf("revalidation: want 304 with the same ETag and no body, got %d %q %d bytes", again.code, again.hdr.Get("ETag"), len(again.body))
	}
	if r := call(t, srv, "GET", p, map[string]string{"If-None-Match": `W/"other", ` + strings.TrimPrefix(tag, "W/")}); r.code != http.StatusNotModified {
		t.Fatalf("a list holding the tag (strong form) must match: %d", r.code)
	}
	if r := call(t, srv, "GET", "/api/graph?repo=billing&ref=v1&symbol=billing.Charge", nil); r.hdr.Get("ETag") == tag {
		t.Fatal("another ref must have another ETag")
	}
	// an answer is computed once per ETag: until the data version moves, the
	// server answers from its memo (here the store changes behind its back)
	m := h.Store.(*storage.Mem)
	m.Put("billing", "HEAD", "billing.audit", storage.SymbolRecord{SymbolHash: content.Hash("a"), SignatureHash: content.Hash("s"), BehaviorHash: content.Hash("a"), BehaviorConf: 1, DirectConf: 1})
	m.Put("billing", "HEAD", "billing.Charge", storage.SymbolRecord{SymbolHash: content.Hash("c2"), SignatureHash: content.Hash("sig:billing.Charge"), BehaviorHash: content.Hash("c2"), BehaviorConf: 1, DirectConf: 1,
		Callees: []query.Callee{{Name: "billing.audit", ResolutionMethod: "go-types", Confidence: 1}}})
	if r := call(t, srv, "GET", p, nil); r.code != 200 || !bytes.Equal(r.body, first.body) {
		t.Fatalf("same data version: want the memoized answer, got %d %s", r.code, r.body)
	}
	version = "2" // a re-index
	r2 := call(t, srv, "GET", p, map[string]string{"If-None-Match": tag})
	if r2.code != 200 || r2.hdr.Get("ETag") == tag || !strings.Contains(string(r2.body), "billing.audit") {
		t.Fatalf("after a write: want 200 with a new ETag and the new answer, got %d %q %s", r2.code, r2.hdr.Get("ETag"), r2.body)
	}
	if r := call(t, srv, "GET", "/api/file?repo=billing&ref=HEAD&path=nope.go", nil); r.code != 404 || r.hdr.Get("ETag") != "" {
		t.Fatalf("an error must carry no ETag: %d %q", r.code, r.hdr.Get("ETag"))
	}
	if r := call(t, srv, "GET", "/api/brief?repo=billing&ref=HEAD&symbol=billing.Charge", nil); r.code != 200 || r.hdr.Get("ETag") != "" {
		t.Fatalf("brief reads git, not only the index: want no ETag, got %d %q", r.code, r.hdr.Get("ETag"))
	}
	// no data version: no ETag at all (it could not be invalidated)
	srv2 := httptest.NewServer((&interfaces.WebHandler{Store: shopStore(), Repo: "billing"}).Routes())
	defer srv2.Close()
	if r := call(t, srv2, "GET", p, nil); r.hdr.Get("ETag") != "" {
		t.Fatalf("without DataVersion: want no ETag, got %q", r.hdr.Get("ETag"))
	}
}

// Large answers are gzipped for a client that accepts it; small ones and
// clients that do not are answered as is. Compact answers are the indented
// answer with the whitespace removed, nothing else.
func TestWebAPIGzipAndCompact(t *testing.T) {
	m := shopStore()
	// enough source that the grep answer is worth compressing
	var big strings.Builder
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&big, "// validateCard is called from line %d\n", i)
	}
	m.PutFile("billing", "HEAD", query.File{Path: "billing/notes.go", Content: big.String()})
	plain := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "billing"}).Routes())
	defer plain.Close()
	compact := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "billing", Compact: true}).Routes())
	defer compact.Close()

	const p = "/api/grep?repo=billing&ref=HEAD&pattern=validateCard&limit=500"
	ref := call(t, plain, "GET", p, nil)
	if ref.code != 200 || ref.hdr.Get("Content-Encoding") != "" || !strings.Contains(ref.hdr.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("identity answer: %d %q Vary %q", ref.code, ref.hdr.Get("Content-Encoding"), ref.hdr.Get("Vary"))
	}
	gz := call(t, plain, "GET", p, map[string]string{"Accept-Encoding": "gzip, br"})
	if gz.hdr.Get("Content-Encoding") != "gzip" || len(gz.body) >= len(ref.body) {
		t.Fatalf("gzip answer: encoding %q, %d bytes vs %d", gz.hdr.Get("Content-Encoding"), len(gz.body), len(ref.body))
	}
	zr, err := gzip.NewReader(bytes.NewReader(gz.body))
	if err != nil {
		t.Fatal(err)
	}
	unz, _ := io.ReadAll(zr)
	if !bytes.Equal(unz, ref.body) {
		t.Fatal("the gzipped answer is not the identity answer")
	}
	if r := call(t, plain, "GET", p, map[string]string{"Accept-Encoding": "gzip;q=0"}); r.hdr.Get("Content-Encoding") != "" {
		t.Fatal("gzip;q=0 refuses gzip")
	}
	if r := call(t, plain, "GET", "/api/refs?repo=billing", map[string]string{"Accept-Encoding": "gzip"}); r.hdr.Get("Content-Encoding") != "" {
		t.Fatal("a small answer is not worth compressing")
	}

	c := call(t, compact, "GET", p, nil)
	var want bytes.Buffer
	if err := json.Compact(&want, ref.body); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(c.body, want.Bytes()) || bytes.Contains(c.body, []byte("\n  ")) {
		t.Fatalf("compact answer differs from the compacted indented one:\n%s", c.body)
	}
}

// Size parameters are bounded: a page cannot ask for an unbounded answer.
func TestWebAPILimits(t *testing.T) {
	m := storage.NewMem()
	var callees []query.Callee
	for i := 0; i < 1500; i++ {
		name := fmt.Sprintf("hub.leaf%04d", i)
		callees = append(callees, query.Callee{Name: name, ResolutionMethod: "go-types", Confidence: 1})
		m.Put("hub", "HEAD", name, storage.SymbolRecord{SymbolHash: content.Hash(name), SignatureHash: content.Hash("s"), BehaviorHash: content.Hash(name), BehaviorConf: 1, DirectConf: 1})
	}
	m.Put("hub", "HEAD", "hub.Root", storage.SymbolRecord{SymbolHash: content.Hash("root"), SignatureHash: content.Hash("s"), BehaviorHash: content.Hash("root"), BehaviorConf: 1, DirectConf: 1, Callees: callees})
	var src strings.Builder
	for i := 0; i < 1500; i++ {
		src.WriteString("needle\n")
	}
	m.PutFile("hub", "HEAD", query.File{Path: "hub/hay.txt", Content: src.String()})
	srv := httptest.NewServer((&interfaces.WebHandler{Store: m, Repo: "hub"}).Routes())
	defer srv.Close()

	var g struct {
		Nodes     []any `json:"nodes"`
		Truncated bool  `json:"truncated"`
	}
	r := call(t, srv, "GET", "/api/graph?repo=hub&symbol=hub.Root&limit=1000000", nil)
	if err := json.Unmarshal(r.body, &g); err != nil {
		t.Fatalf("%v: %s", err, r.body)
	}
	if len(g.Nodes) > 1001 || !g.Truncated {
		t.Fatalf("graph limit not bounded: %d nodes, truncated %v", len(g.Nodes), g.Truncated)
	}
	for _, lim := range []string{"-1", "999999"} {
		var gr struct {
			Matches   []any `json:"matches"`
			Total     int   `json:"total"`
			Truncated bool  `json:"truncated"`
		}
		r := call(t, srv, "GET", "/api/grep?repo=hub&pattern=needle&limit="+lim, nil)
		if err := json.Unmarshal(r.body, &gr); err != nil {
			t.Fatalf("%v: %s", err, r.body)
		}
		if len(gr.Matches) != 1000 || gr.Total != 1500 || !gr.Truncated {
			t.Fatalf("grep limit=%s: %d matches of %d (truncated %v), want 1000 of 1500", lim, len(gr.Matches), gr.Total, gr.Truncated)
		}
	}
	if r := call(t, srv, "GET", "/api/grep?repo=hub&pattern=needle&offset=-5", nil); r.code != 200 {
		t.Fatalf("a negative offset is read as 0: %d %s", r.code, r.body)
	}
}

// Built assets are served precompressed when the browser accepts it, with
// their own type and cacheable for good; the uncompressed file otherwise.
func TestWebAssets(t *testing.T) {
	srv := httptest.NewServer((&interfaces.WebHandler{Store: storage.NewMem()}).Routes())
	defer srv.Close()
	brs, _ := filepath.Glob("webui/dist/assets/*.js.br")
	if len(brs) == 0 {
		t.Fatal("the build wrote no .br assets")
	}
	name := "/assets/" + strings.TrimSuffix(filepath.Base(brs[0]), ".br")
	raw := call(t, srv, "GET", name, nil)
	if raw.code != 200 || raw.hdr.Get("Content-Encoding") != "" || !strings.Contains(raw.hdr.Get("Content-Type"), "javascript") ||
		raw.hdr.Get("Cache-Control") != "public, max-age=31536000, immutable" || !strings.Contains(raw.hdr.Get("Vary"), "Accept-Encoding") {
		t.Fatalf("identity asset: %d %v", raw.code, raw.hdr)
	}
	for enc, dec := range map[string]func([]byte) []byte{
		"br": nil, // the stdlib has no brotli reader: compare with the file on disk
		"gzip": func(b []byte) []byte {
			zr, _ := gzip.NewReader(bytes.NewReader(b))
			out, _ := io.ReadAll(zr)
			return out
		},
	} {
		r := call(t, srv, "GET", name, map[string]string{"Accept-Encoding": enc})
		if r.code != 200 || r.hdr.Get("Content-Encoding") != enc || r.hdr.Get("Content-Length") != strconv.Itoa(len(r.body)) {
			t.Fatalf("%s asset: %d %v", enc, r.code, r.hdr)
		}
		if dec != nil && !bytes.Equal(dec(r.body), raw.body) {
			t.Fatalf("%s asset does not decode to the file", enc)
		}
		if dec == nil {
			disk, _ := os.ReadFile(brs[0])
			if !bytes.Equal(disk, r.body) {
				t.Fatal("br asset is not the .br file")
			}
		}
	}
	if r := call(t, srv, "GET", name, map[string]string{"Accept-Encoding": "gzip, deflate, br"}); r.hdr.Get("Content-Encoding") != "br" {
		t.Fatalf("brotli is preferred when accepted: %q", r.hdr.Get("Content-Encoding"))
	}
	if r := call(t, srv, "GET", name, map[string]string{"Accept-Encoding": "br;q=0, gzip"}); r.hdr.Get("Content-Encoding") != "gzip" {
		t.Fatalf("br;q=0 refuses brotli: %q", r.hdr.Get("Content-Encoding"))
	}
	if r := call(t, srv, "HEAD", name, map[string]string{"Accept-Encoding": "br"}); r.code != 200 || len(r.body) != 0 || r.hdr.Get("Content-Length") == "" {
		t.Fatalf("HEAD asset: %d %d bytes %v", r.code, len(r.body), r.hdr)
	}
	woff, _ := filepath.Glob("webui/dist/assets/*.woff2")
	if len(woff) == 0 {
		t.Fatal("no font in the build")
	}
	if r := call(t, srv, "GET", "/assets/"+filepath.Base(woff[0]), map[string]string{"Accept-Encoding": "br"}); r.code != 200 || r.hdr.Get("Content-Encoding") != "" || r.hdr.Get("Content-Type") != "font/woff2" {
		t.Fatalf("a font is served as is: %d %v", r.code, r.hdr)
	}
	for _, p := range []string{"/assets/", "/assets/nope.js", "/assets/%2e%2e/index.html"} {
		if r := call(t, srv, "GET", p, nil); r.code != 404 {
			t.Errorf("%s: want 404, got %d", p, r.code)
		}
	}
}
