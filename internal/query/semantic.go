// semantic.go is the retrieval ladder's semantic rung (architecture ext §10A.2:
// "where is the thing that does X"). It ranks a ref's symbols by similarity to a
// natural-language query. The scoring is pluggable via an Embedder; the default
// TermEmbedder is pure stdlib — it tokenizes identifiers (camelCase / snake_case
// split, lowercased) into a term-frequency vector, which SemanticSearch then
// weights by inverse document frequency over the in-scope corpus (so rare,
// discriminative terms outrank ubiquitous ones) and compares with cosine
// similarity. That needs no model or network, so the whole layer is pure and
// tested in-sandbox (ADR-018); a real neural embedder (ollama/remote, keyed by
// content.EmbedHash) can drop in behind the same interface for higher recall.
package query

import (
	"math"
	"sort"
	"strings"
	"unicode"
)

// Embedder turns text into a sparse term→weight vector for similarity scoring.
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

// SemanticSearch ranks symbols by similarity of (name + body) to query,
// returning the top limit (default 10) with score > 0. repo may be FleetRepo
// ("*") to rank across every repo in the store. emb defaults to TermEmbedder.
// Pure over the Store's files (same source spans grep/brief use).
func SemanticSearch(s Store, repo, ref, query string, limit int, emb Embedder) []SemanticHit {
	if emb == nil {
		emb = TermEmbedder{}
	}
	if limit <= 0 {
		limit = 10
	}
	qv := emb.Embed(query)
	if len(qv) == 0 {
		return nil
	}
	// Two passes with IDF weighting: a term shared by most symbols (e.g. "repo",
	// "get", "error") carries little signal, while a rare one ("ximpact",
	// "picking") is highly discriminative. Without this, a query like "cross-repo
	// impact" ranks every *repo*-named helper above the actual impact code. IDF is
	// a corpus property, so it's computed here over the in-scope symbols rather
	// than in the (per-text) Embedder.
	type doc struct {
		hit SemanticHit
		vec map[string]float64
	}
	var docs []doc
	df := map[string]int{}
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			for _, sp := range f.Symbols {
				body := sliceLines(f.Content, sp.StartLine, sp.EndLine)
				vec := emb.Embed(sp.Name + " " + body)
				if len(vec) == 0 {
					continue
				}
				for term := range vec {
					df[term]++
				}
				docs = append(docs, doc{SemanticHit{Repo: rp, Path: f.Path, Symbol: sp.Name, Line: sp.StartLine}, vec})
			}
		}
	}
	n := float64(len(docs))
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
	for _, dc := range docs {
		score := cosine(qw, weighted(dc.vec))
		if score > 0 {
			h := dc.hit
			h.Score = score
			hits = append(hits, h)
		}
	}
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
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
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
