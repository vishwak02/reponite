//go:build neural

package semantic

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// fakeEmbeddings serves the OpenAI-compatible /v1/embeddings shape with
// deterministic 3-dim vectors: axis 0 for "card"-flavored text, axis 1 for
// "template"-flavored, axis 2 otherwise — so ranking is exact, not fuzzy.
func fakeEmbeddings(t *testing.T, requests *int, batchSizes *[]int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Model string   `json:"model"`
			Input []string `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		*requests++
		*batchSizes = append(*batchSizes, len(req.Input))
		type item struct {
			Index     int       `json:"index"`
			Embedding []float64 `json:"embedding"`
		}
		var data []item
		for i, text := range req.Input {
			vec := []float64{0, 0, 1}
			if strings.Contains(text, "card") || strings.Contains(text, "Card") {
				vec = []float64{1, 0.1, 0}
			} else if strings.Contains(text, "emplate") {
				vec = []float64{0, 1, 0.1}
			}
			data = append(data, item{Index: i, Embedding: vec})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
}

func TestNeuralRankerRanksByEmbedding(t *testing.T) {
	requests, batches := 0, []int{}
	srv := fakeEmbeddings(t, &requests, &batches)
	defer srv.Close()

	n := New(Config{Endpoint: srv.URL, Model: "test-model"})
	if n.RankerName() != "neural:test-model" {
		t.Fatalf("ranker name = %q", n.RankerName())
	}
	docs := []query.SemanticDoc{
		{Repo: "r", Path: "a.go", Symbol: "renderTemplate", Line: 1, Text: "func renderTemplate() {}"},
		{Repo: "r", Path: "b.go", Symbol: "validateCard", Line: 1, Text: "func validateCard() {}"},
		{Repo: "r", Path: "c.go", Symbol: "unrelated", Line: 1, Text: "func unrelated() {}"},
	}
	hits, err := n.Rank("check a credit card", docs, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 2 || hits[0].Symbol != "validateCard" {
		t.Fatalf("embedding cosine must rank validateCard first, got %+v", hits)
	}
}

// >batchSize docs split into multiple requests, order preserved end to end.
func TestNeuralRankerBatches(t *testing.T) {
	requests, batches := 0, []int{}
	srv := fakeEmbeddings(t, &requests, &batches)
	defer srv.Close()

	n := New(Config{Endpoint: srv.URL, Model: "m"})
	var docs []query.SemanticDoc
	for i := 0; i < 100; i++ {
		// Distinct text per doc: identical texts are deduped by the cache, so
		// batching is only exercised by genuinely distinct inputs.
		sym := fmt.Sprintf("filler%d", i)
		if i == 90 {
			sym = "validateCard" // lives in the SECOND batch
		}
		docs = append(docs, query.SemanticDoc{Repo: "r", Path: "p.go", Symbol: sym, Line: i + 1, Text: sym})
	}
	hits, err := n.Rank("card lookup", docs, 1)
	if err != nil {
		t.Fatal(err)
	}
	if requests < 2 {
		t.Fatalf("101 inputs must split into >1 batch, got %d request(s) %v", requests, batches)
	}
	for _, b := range batches {
		if b > batchSize {
			t.Fatalf("a batch exceeded the bound: %v", batches)
		}
	}
	if len(hits) != 1 || hits[0].Symbol != "validateCard" || hits[0].Line != 91 {
		t.Fatalf("cross-batch ordering must hold (validateCard@91 first), got %+v", hits)
	}
}

// The full honesty loop through SemanticSearch: a dead endpoint falls back to
// the pure term ranker, labeled, with the failure recorded.
func TestNeuralRankerFallsBackThroughSemanticSearch(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "billing.go",
		Content: "func validateCard() {}\n",
		Symbols: []query.SymbolSpan{{Name: "validateCard", StartLine: 1, EndLine: 1}},
	})
	n := New(Config{Endpoint: "http://127.0.0.1:1/v1/embeddings", Model: "dead"}) // nothing listens
	res := query.SemanticSearch(m, "r", "HEAD", "validate card", 5, n)
	if res.Ranker != "term-idf" {
		t.Fatalf("fallback ranking must be labeled term-idf, got %q", res.Ranker)
	}
	if !strings.Contains(res.Note, "neural:dead") {
		t.Fatalf("the endpoint failure must be recorded, got %q", res.Note)
	}
	if len(res.Hits) != 1 || res.Hits[0].Symbol != "validateCard" {
		t.Fatalf("term fallback must still answer, got %+v", res.Hits)
	}
}

// A truncated/miscounted endpoint response is an error, not a silent misrank.
func TestNeuralRankerRejectsCountMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]any{{"index": 0, "embedding": []float64{1}}}})
	}))
	defer srv.Close()
	n := New(Config{Endpoint: srv.URL, Model: "m"})
	_, err := n.Rank("q", []query.SemanticDoc{{Symbol: "a", Text: "a"}, {Symbol: "b", Text: "b"}}, 5)
	if err == nil {
		t.Fatal("an embedding-count mismatch must surface as an error")
	}
}

// An endpoint answering with inconsistent embedding dimensions (a proxy
// misroute, a mid-rollout model switch) must ERROR, not silently drop the
// mismatched docs: a partial or empty ranking still labeled "neural:<model>"
// would be a lie about what was searched. Erroring routes it to the honest
// term-idf fallback instead.
func TestNeuralRankerRejectsDimensionSkew(t *testing.T) {
	// The query embeds to 3 dims; the doc named "odd" comes back with 2.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var data []map[string]any
		for i, s := range req.Input {
			vec := []float64{1, 0, 0}
			if strings.Contains(s, "odd") {
				vec = []float64{1, 0}
			}
			data = append(data, map[string]any{"index": i, "embedding": vec})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	n := New(Config{Endpoint: srv.URL, Model: "skewed"})
	docs := []query.SemanticDoc{
		{Repo: "r", Path: "a.go", Symbol: "normal", Line: 1, Text: "normal"},
		{Repo: "r", Path: "b.go", Symbol: "odd", Line: 1, Text: "odd"},
	}
	hits, err := n.Rank("q", docs, 5)
	if err == nil {
		t.Fatalf("dimension skew must surface as an error, got hits=%+v", hits)
	}
	if !strings.Contains(err.Error(), "dimension") {
		t.Fatalf("error should name the cause, got %v", err)
	}

	// And through SemanticSearch it becomes a labeled term-idf fallback,
	// never an empty result wearing the neural label. The query ("renderer")
	// misses the skew marker while the doc text carries it, so query and doc
	// embeddings disagree on dimension — exactly the misroute case.
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "b.go",
		Content: "func oddRenderer() {}\n",
		Symbols: []query.SymbolSpan{{Name: "oddRenderer", StartLine: 1, EndLine: 1}},
	})
	res := query.SemanticSearch(m, "r", "HEAD", "renderer", 5, n)
	if res.Ranker != "term-idf" || !strings.Contains(res.Note, "dimension") {
		t.Fatalf("skew must degrade to a labeled term-idf fallback, got ranker=%q note=%q", res.Ranker, res.Note)
	}
	if len(res.Hits) != 1 {
		t.Fatalf("the fallback must still answer, got %+v", res.Hits)
	}
}

// Embeddings are cached by (model, content hash): a long-lived serve/mcp mount
// must not re-embed the whole corpus on every query, and identical texts are
// embedded once. Only the changing query text is fetched on a repeat call.
func TestNeuralRankerCachesEmbeddings(t *testing.T) {
	requests, batches := 0, []int{}
	srv := fakeEmbeddings(t, &requests, &batches)
	defer srv.Close()

	n := New(Config{Endpoint: srv.URL, Model: "m"})
	docs := []query.SemanticDoc{
		{Repo: "r", Path: "a.go", Symbol: "validateCard", Line: 1, Text: "func validateCard() {}"},
		{Repo: "r", Path: "b.go", Symbol: "renderTemplate", Line: 1, Text: "func renderTemplate() {}"},
		// A genuinely duplicated symbol (same name and body in another file):
		// its embedded text is identical, so it is embedded once.
		{Repo: "r", Path: "c.go", Symbol: "validateCard", Line: 1, Text: "func validateCard() {}"},
	}
	if _, err := n.Rank("first query", docs, 5); err != nil {
		t.Fatal(err)
	}
	// 4 inputs (query + 3 docs) but only 3 unique texts to embed.
	if batches[0] != 3 {
		t.Fatalf("identical doc texts must be embedded once: batch sizes %v", batches)
	}

	before := requests
	hits, err := n.Rank("second query", docs, 5)
	if err != nil {
		t.Fatal(err)
	}
	if requests != before+1 {
		t.Fatalf("a repeat rank should need one request, got %d", requests-before)
	}
	if got := batches[len(batches)-1]; got != 1 {
		t.Fatalf("only the new query text should be fetched, got a batch of %d", got)
	}
	// The property that matters: caching must not change the answer. A fresh
	// ranker with an empty cache produces the identical ranking.
	fresh, err := New(Config{Endpoint: srv.URL, Model: "m"}).Rank("second query", docs, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != len(fresh) {
		t.Fatalf("cached ranking differs in length: cached %+v vs fresh %+v", hits, fresh)
	}
	for i := range hits {
		if hits[i].Symbol != fresh[i].Symbol || hits[i].Path != fresh[i].Path || hits[i].Score != fresh[i].Score {
			t.Fatalf("cached ranking differs at %d: %+v vs %+v", i, hits[i], fresh[i])
		}
	}
}

// Switching models must never serve the previous model's vectors.
func TestNeuralRankerCacheIsPerModel(t *testing.T) {
	requests, batches := 0, []int{}
	srv := fakeEmbeddings(t, &requests, &batches)
	defer srv.Close()
	docs := []query.SemanticDoc{{Repo: "r", Path: "a.go", Symbol: "x", Line: 1, Text: "same text"}}

	a := New(Config{Endpoint: srv.URL, Model: "model-a"})
	b := New(Config{Endpoint: srv.URL, Model: "model-b"})
	a.Rank("q", docs, 1)
	before := requests
	b.Rank("q", docs, 1)
	if requests == before {
		t.Fatal("a different model must not reuse the first model's cached vectors")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("REPONITE_EMBED_ENDPOINT", "")
	t.Setenv("REPONITE_EMBED_MODEL", "")
	if _, ok := FromEnv(); ok {
		t.Fatal("unconfigured env must yield no ranker")
	}
	t.Setenv("REPONITE_EMBED_ENDPOINT", "http://localhost:11434/v1/embeddings")
	t.Setenv("REPONITE_EMBED_MODEL", "nomic-embed-text")
	r, ok := FromEnv()
	if !ok || r.RankerName() != "neural:nomic-embed-text" {
		t.Fatalf("configured env must yield the neural ranker, got %v %v", r, ok)
	}
}
