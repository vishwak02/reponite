//go:build neural

// neural.go implements query.SemanticRanker over a config-driven embeddings
// HTTP endpoint speaking the OpenAI-compatible /v1/embeddings shape (Ollama,
// OpenAI, LiteLLM, vLLM, …): POST {model, input:[...]} → {data:[{embedding}]}.
// Stdlib-only (net/http + encoding/json) but behind the `neural` build tag: a
// network call in the query path is nondeterministic, so it stays an adapter
// the pure core never depends on (ADR-018). Failure semantics are the seam's
// contract: any error returns to SemanticSearch, which falls back to the pure
// term ranker and RECORDS the failure — results are never silently degraded.
package semantic

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/vishwak02/reponite/internal/query"
)

// Config locates the embeddings endpoint. Endpoint + Model are required.
type Config struct {
	Endpoint string // e.g. http://localhost:11434/v1/embeddings
	Model    string // e.g. nomic-embed-text
	APIKey   string // optional bearer token
}

// FromEnv builds a NeuralRanker from REPONITE_EMBED_ENDPOINT +
// REPONITE_EMBED_MODEL (+ optional REPONITE_EMBED_API_KEY). ok=false when not
// configured — callers then leave the seam nil and the pure default ranks.
func FromEnv() (*NeuralRanker, bool) {
	c := Config{
		Endpoint: os.Getenv("REPONITE_EMBED_ENDPOINT"),
		Model:    os.Getenv("REPONITE_EMBED_MODEL"),
		APIKey:   os.Getenv("REPONITE_EMBED_API_KEY"),
	}
	if c.Endpoint == "" || c.Model == "" {
		return nil, false
	}
	return New(c), true
}

// New builds a NeuralRanker for cfg.
func New(cfg Config) *NeuralRanker {
	return &NeuralRanker{cfg: cfg, client: &http.Client{Timeout: 30 * time.Second}}
}

// NeuralRanker ranks docs by cosine similarity of dense embeddings fetched
// from the configured endpoint. Embeddings are cached by content hash for the
// process lifetime: a long-lived `serve`/`mcp` mount would otherwise re-embed
// the entire corpus on every query, and a symbol's text is unchanged between
// queries unless it was reindexed. The cache is keyed by (model, sha256(text))
// so switching models can never serve another model's vectors.
type NeuralRanker struct {
	cfg    Config
	client *http.Client

	mu    sync.Mutex
	cache map[string][]float64
}

// maxCacheEntries bounds the cache so a huge fleet can't grow it without limit;
// on overflow it is cleared wholesale (simple and predictable — the next query
// repopulates only what it needs).
const maxCacheEntries = 50000

func (n *NeuralRanker) cacheKey(text string) string {
	sum := sha256.Sum256([]byte(n.cfg.Model + "\x00" + text))
	return string(sum[:])
}

// cached returns the embeddings already known for inputs (nil entries where a
// fetch is still needed) plus the list of texts to fetch.
func (n *NeuralRanker) cached(inputs []string) (have [][]float64, missing []string) {
	have = make([][]float64, len(inputs))
	n.mu.Lock()
	defer n.mu.Unlock()
	need := map[string]bool{}
	for i, text := range inputs {
		if v, ok := n.cache[n.cacheKey(text)]; ok {
			have[i] = v
			continue
		}
		if !need[text] {
			need[text] = true
			missing = append(missing, text)
		}
	}
	return have, missing
}

// store records freshly fetched embeddings.
func (n *NeuralRanker) store(texts []string, vecs [][]float64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.cache == nil || len(n.cache) > maxCacheEntries {
		n.cache = make(map[string][]float64, len(texts))
	}
	for i, t := range texts {
		if i < len(vecs) {
			n.cache[n.cacheKey(t)] = vecs[i]
		}
	}
}

// RankerName labels results with the model that ranked them (invariant 5:
// an agent deciding how much to trust a ranking must know what produced it).
func (n *NeuralRanker) RankerName() string { return "neural:" + n.cfg.Model }

// Per-request bounds: batch inputs so one huge corpus doesn't build one huge
// request, and truncate each doc to keep within typical embedding-model
// context windows. Truncation only trims the tail of a symbol body — the name
// and opening lines carry most of the signal.
const (
	batchSize   = 64
	maxDocBytes = 2000
)

func (n *NeuralRanker) Rank(q string, docs []query.SemanticDoc, limit int) ([]query.SemanticHit, error) {
	if len(docs) == 0 {
		return nil, nil
	}
	inputs := make([]string, 0, len(docs)+1)
	inputs = append(inputs, q)
	for _, d := range docs {
		inputs = append(inputs, truncateUTF8(d.Symbol+" "+d.Text, maxDocBytes))
	}
	vecs, err := n.embedCached(inputs)
	if err != nil {
		return nil, err
	}
	if len(vecs) != len(inputs) {
		return nil, fmt.Errorf("endpoint returned %d embeddings for %d inputs", len(vecs), len(inputs))
	}
	qv := vecs[0]
	var hits []query.SemanticHit
	for i, d := range docs {
		// A dimension mismatch means the endpoint answered from a different
		// model (proxy misroute, mid-rollout switch). Scoring it 0 would drop
		// the doc from an ostensibly complete ranking — or return an EMPTY
		// result still labeled "neural:<model>". Error out instead, so
		// SemanticSearch falls back to term-idf and says why (never-lie).
		if len(vecs[i+1]) != len(qv) {
			return nil, fmt.Errorf("inconsistent embedding dimensions: query %d, doc %q %d — endpoint may be serving mixed models",
				len(qv), d.Symbol, len(vecs[i+1]))
		}
		if score := denseCosine(qv, vecs[i+1]); score > 0 {
			hits = append(hits, query.SemanticHit{Repo: d.Repo, Path: d.Path, Symbol: d.Symbol, Line: d.Line, Score: score})
		}
	}
	query.SortSemanticHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// embedCached fetches only the inputs not already embedded (the query text
// changes every call; symbol bodies almost never do), then reassembles the
// full ordered vector list.
func (n *NeuralRanker) embedCached(inputs []string) ([][]float64, error) {
	have, missing := n.cached(inputs)
	if len(missing) == 0 {
		return have, nil
	}
	fetched, err := n.embed(missing)
	if err != nil {
		return nil, err
	}
	if len(fetched) != len(missing) {
		return nil, fmt.Errorf("endpoint returned %d embeddings for %d inputs", len(fetched), len(missing))
	}
	n.store(missing, fetched)
	byText := make(map[string][]float64, len(missing))
	for i, t := range missing {
		byText[t] = fetched[i]
	}
	for i, text := range inputs {
		if have[i] == nil {
			have[i] = byText[text]
		}
	}
	return have, nil
}

// embed fetches embeddings for inputs in bounded batches, preserving order.
func (n *NeuralRanker) embed(inputs []string) ([][]float64, error) {
	out := make([][]float64, 0, len(inputs))
	for start := 0; start < len(inputs); start += batchSize {
		end := start + batchSize
		if end > len(inputs) {
			end = len(inputs)
		}
		vecs, err := n.embedBatch(inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vecs...)
	}
	return out, nil
}

// OpenAI-compatible /v1/embeddings request/response shapes.
type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}
type embedResponse struct {
	Data []struct {
		Index     int       `json:"index"`
		Embedding []float64 `json:"embedding"`
	} `json:"data"`
}

func (n *NeuralRanker) embedBatch(inputs []string) ([][]float64, error) {
	body, err := json.Marshal(embedRequest{Model: n.cfg.Model, Input: inputs})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, n.cfg.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if n.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+n.cfg.APIKey)
	}
	resp, err := n.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embeddings endpoint %s: %s", n.cfg.Endpoint, resp.Status)
	}
	var er embedResponse
	if err := json.NewDecoder(resp.Body).Decode(&er); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if len(er.Data) != len(inputs) {
		return nil, fmt.Errorf("endpoint returned %d embeddings for %d inputs", len(er.Data), len(inputs))
	}
	vecs := make([][]float64, len(inputs))
	for _, d := range er.Data {
		if d.Index < 0 || d.Index >= len(vecs) {
			return nil, fmt.Errorf("embedding index %d out of range", d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return nil, fmt.Errorf("empty embedding for input %d", i)
		}
	}
	return vecs, nil
}

// truncateUTF8 trims s to at most max bytes without splitting a rune — a
// half-rune would be re-encoded as U+FFFD in the request body, silently
// corrupting the text the model embeds.
func truncateUTF8(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

// denseCosine is cosine similarity over dense vectors of equal length; a
// length mismatch scores 0 rather than panicking (the caller already verified
// counts; dimensions are the model's contract).
func denseCosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += a[i] * b[i]
		na += a[i] * a[i]
		nb += b[i] * b[i]
	}
	if dot == 0 || na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
