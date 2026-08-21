// semantic.go is the retrieval ladder's semantic rung (architecture ext §10A.2:
// "where is the thing that does X"). It ranks a ref's symbols by similarity to a
// natural-language query. The ranking strategy is pluggable via the
// SemanticRanker seam (ADR-020): the default TermIDFRanker is pure stdlib —
// identifier-aware bag-of-terms (camelCase / snake_case split) weighted by
// inverse document frequency over the in-scope corpus (a corpus property, which
// is why the seam sits at the RANKER, not per-text embedding) and compared with
// cosine similarity. That needs no model or network, so the default layer is
// pure and tested in-sandbox (ADR-018); the build-tagged neural adapter
// (internal/semantic, `-tags neural`) drops in behind the same seam, ranking by
// dense embeddings from a config-driven endpoint. Every result names the ranker
// that actually produced it, and an adapter failure falls back to the term
// ranker with the failure recorded — provenance is never silent (invariant 5).
package query

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Embedder turns text into a sparse term→weight vector; it is the tokenizer
// seam INSIDE the default term ranker (not the neural seam — that is
// SemanticRanker, because IDF weighting needs the whole corpus).
type Embedder interface {
	Embed(text string) map[string]float64
}

// TermEmbedder is the dependency-free default: identifier-aware bag-of-terms
// with term-frequency weights.
type TermEmbedder struct{}

func (TermEmbedder) Embed(text string) map[string]float64 {
	v := map[string]float64{}
	for _, tok := range tokenizeIdentifiers(text) {
		v[tok]++
	}
	return v
}

// SemanticHit is one ranked symbol.
type SemanticHit struct {
	Repo   string
	Path   string
	Symbol string
	Line   int
	Score  float64
}

// SemanticDoc is one rankable symbol: its identity plus the text a ranker
// scores (symbol name + body span).
type SemanticDoc struct {
	Repo   string
	Path   string
	Symbol string
	Line   int
	Text   string
}

// SemanticRanker is the semantic rung's strategy seam (ADR-020): rank docs by
// relevance to a natural-language query and return the top limit hits, best
// first. Implementations must be deterministic for a fixed input corpus.
// RankerName labels every result with the strategy that produced it — an agent
// deciding how much to trust a ranking needs to know whether it came from
// bag-of-terms or a neural model (invariant 5: never overclaim).
type SemanticRanker interface {
	RankerName() string
	Rank(query string, docs []SemanticDoc, limit int) ([]SemanticHit, error)
}

// TermIDFRanker is the pure default SemanticRanker: TF (via Emb, default
// TermEmbedder) × smoothed IDF over the doc corpus, cosine-compared. No model,
// no network, fully deterministic.
type TermIDFRanker struct {
	Emb Embedder // nil = TermEmbedder
}

func (TermIDFRanker) RankerName() string { return "term-idf" }

func (r TermIDFRanker) Rank(query string, docs []SemanticDoc, limit int) ([]SemanticHit, error) {
	emb := r.Emb
	if emb == nil {
		emb = TermEmbedder{}
	}
	qv := emb.Embed(query)
	if len(qv) == 0 {
		return nil, nil
	}
	// IDF weighting: a term shared by most symbols (e.g. "repo", "get", "error")
	// carries little signal, while a rare one ("ximpact", "picking") is highly
	// discriminative. Without this, a query like "cross-repo impact" ranks every
	// *repo*-named helper above the actual impact code.
	type scored struct {
		hit SemanticHit
		vec map[string]float64
	}
	var svs []scored
	df := map[string]int{}
	for _, d := range docs {
		vec := emb.Embed(d.Symbol + " " + d.Text)
		if len(vec) == 0 {
			continue
		}
		for term := range vec {
			df[term]++
		}
		svs = append(svs, scored{SemanticHit{Repo: d.Repo, Path: d.Path, Symbol: d.Symbol, Line: d.Line}, vec})
	}
	n := float64(len(svs))
	idf := func(term string) float64 {
		d := df[term]
		if d == 0 {
			return 0
		}
		// Smoothed: a rare term weighs far more than a ubiquitous one, but even a
		// term in every symbol keeps a small positive weight (so a single-symbol
		// corpus, or an all-common query, still ranks instead of collapsing to 0).
		return math.Log(1 + n/float64(d))
	}
	weighted := func(v map[string]float64) map[string]float64 {
		w := make(map[string]float64, len(v))
		for t, tf := range v {
			if idfv := idf(t); idfv > 0 {
				w[t] = tf * idfv
			}
		}
		return w
	}
	qw := weighted(qv)
	var hits []SemanticHit
	for _, sv := range svs {
		score := cosine(qw, weighted(sv.vec))
		if score > 0 {
			h := sv.hit
			h.Score = score
			hits = append(hits, h)
		}
	}
	SortSemanticHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

// SemanticResult is the semantic rung's result: the ranked hits plus the name
// of the ranker that ACTUALLY produced them and, when an adapter failed and the
// search fell back, a note saying so — the ranking's provenance is part of the
// answer, never implied.
type SemanticResult struct {
	Hits   []SemanticHit
	Ranker string
	// Considered is how many symbols were ranked. A caller can see that a
	// "top 10" came from a corpus of 30 rather than 30,000 — the difference
	// between a ranking and a shrug.
	Considered int
	Note       string
	Meta       Meta
}

// SemanticSearch ranks symbols by similarity of (name + body) to query,
// returning the top limit (default 10). repo may be FleetRepo ("*") to rank
// across every repo in the store. r defaults to TermIDFRanker; if a non-default
// ranker fails (a neural endpoint down, mid-flight error), the search FALLS
// BACK to the term ranker and records both the failure and the ranker that
// produced the returned hits. Doc collection is pure over the Store's files
// (the same source spans grep/brief use).
func SemanticSearch(s Store, repo, ref, query string, limit int, r SemanticRanker) SemanticResult {
	if r == nil {
		r = TermIDFRanker{}
	}
	if limit <= 0 {
		limit = 10
	}
	var docs []SemanticDoc
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			for _, sp := range f.Symbols {
				docs = append(docs, SemanticDoc{
					Repo: rp, Path: f.Path, Symbol: sp.Name, Line: sp.StartLine,
					Text: sliceLines(f.Content, sp.StartLine, sp.EndLine),
				})
			}
		}
	}
	res := SemanticResult{Ranker: r.RankerName(), Considered: len(docs), Meta: Meta{Repo: repo, Ref: ref}}
	hits, err := r.Rank(query, docs, limit)
	if err != nil {
		fallback := TermIDFRanker{}
		res.Note = "ranker " + r.RankerName() + " failed (" + err.Error() + "); results ranked by " + fallback.RankerName() + " instead"
		res.Ranker = fallback.RankerName()
		hits, _ = fallback.Rank(query, docs, limit)
	}
	res.Hits = hits
	return res
}

// SortSemanticHits orders by score desc, then (repo, path, symbol) for
// determinism — the canonical hit order every SemanticRanker implementation
// (including build-tagged adapters) must produce.
func SortSemanticHits(hits []SemanticHit) {
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].Score != hits[j].Score {
			return hits[i].Score > hits[j].Score
		}
		if hits[i].Repo != hits[j].Repo {
			return hits[i].Repo < hits[j].Repo
		}
		if hits[i].Path != hits[j].Path {
			return hits[i].Path < hits[j].Path
		}
		return hits[i].Symbol < hits[j].Symbol
	})
}

// tokenizeIdentifiers splits text into lowercased terms, breaking identifiers on
// case and non-alphanumeric boundaries (validateCardNumber -> validate card
// number; fetch_user -> fetch user). Terms shorter than 2 runes are dropped.
func tokenizeIdentifiers(text string) []string {
	var out []string
	var cur []rune
	flush := func() {
		if len(cur) >= 2 {
			out = append(out, strings.ToLower(string(cur)))
		}
		cur = cur[:0]
	}
	var prev rune
	for _, r := range text {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			// camelCase boundary: lower/digit -> Upper starts a new term.
			if len(cur) > 0 && unicode.IsUpper(r) && (unicode.IsLower(prev) || unicode.IsDigit(prev)) {
				flush()
			}
			cur = append(cur, r)
		default:
			flush()
		}
		prev = r
	}
	flush()
	return out
}

func cosine(a, b map[string]float64) float64 {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	// iterate the smaller map for the dot product.
	small, large := a, b
	if len(b) < len(a) {
		small, large = b, a
	}
	var dot float64
	for k, va := range small {
		if vb, ok := large[k]; ok {
			dot += va * vb
		}
	}
	if dot == 0 {
		return 0
	}
	return dot / (norm(a) * norm(b))
}

func norm(v map[string]float64) float64 {
	var s float64
	for _, x := range v {
		s += x * x
	}
	if s == 0 {
		return 1
	}
	return math.Sqrt(s)
}
