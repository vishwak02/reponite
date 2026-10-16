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
	"unicode/utf8"
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
	return SemanticSearchCached(s, repo, ref, query, limit, r, nil)
}

// KeywordRankingNote is said with every term-ranked result: it is keyword
// matching over identifiers and bodies, not semantic similarity, so a reader
// does not take "no hit" for "no such behavior" when the code uses other words.
const KeywordRankingNote = "keyword ranking (term-idf: identifier-aware term frequency x inverse document frequency, cosine-compared), NOT semantic similarity — code that does the thing under other words is not found; follow up with grep/search for synonyms"

// SemanticSearchCached is SemanticSearch with a per-repo term-index cache for
// the default term ranker: tokenizing every symbol body of a fleet costs tens
// of seconds, while the vectors depend only on a ref's content, so a warm
// cache answers in about a second. Scores are identical to TermIDFRanker.Rank.
func SemanticSearchCached(s Store, repo, ref, query string, limit int, r SemanticRanker, cache TermIndexCache) SemanticResult {
	if limit <= 0 {
		limit = 10
	}
	if r == nil {
		r = TermIDFRanker{}
	}
	if tr, ok := r.(TermIDFRanker); ok && tr.Emb == nil {
		return termSearch(s, repo, ref, query, limit, cache)
	}
	var docs []SemanticDoc
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			lines := strings.Split(f.Content, "\n")
			for _, sp := range f.Symbols {
				docs = append(docs, SemanticDoc{
					Repo: rp, Path: f.Path, Symbol: sp.Name, Line: sp.StartLine,
					Text: spanText(lines, sp.StartLine, sp.EndLine),
				})
			}
		}
	}
	res := SemanticResult{Ranker: r.RankerName(), Considered: len(docs), Meta: NewMeta(s, repo, ref)}
	hits, err := r.Rank(query, docs, limit)
	if err != nil {
		fallback := TermIDFRanker{}
		res.Note = "ranker " + r.RankerName() + " failed (" + err.Error() + "); results ranked by " + fallback.RankerName() + " instead — " + KeywordRankingNote
		res.Ranker = fallback.RankerName()
		hits, _ = fallback.Rank(query, docs, limit)
	}
	res.Hits = hits
	return res
}

// spanText is lines[start-1:end] joined, from a file split once.
func spanText(lines []string, start, end int) string {
	if start <= 0 || end < start || start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

// TermIndexVersion changes whenever tokenization or the index layout does,
// so a cached index from another version is rebuilt, never misread.
const TermIndexVersion = 1

// TermIndex is the term ranker's corpus for one repo at one ref: each symbol's
// identity and its term-frequency vector over a local vocabulary.
type TermIndex struct {
	Version int
	Key     string // what the cache validated it against (set by the cache)
	Terms   []string
	Paths   []string
	Docs    []TermDoc
}

// TermDoc is one symbol: Path indexes TermIndex.Paths; IDs/TFs are its terms.
type TermDoc struct {
	Path   int32
	Symbol string
	Line   int32
	IDs    []uint32
	TFs    []uint16
}

// TermIndexCache stores built term indexes. Load returns ok=false for a
// missing or stale entry; the cache owns validation (it knows the content
// fingerprint of repo@ref).
type TermIndexCache interface {
	Load(repo, ref string) (*TermIndex, bool)
	Save(repo, ref string, ix *TermIndex)
}

// BuildTermIndex tokenizes every symbol (name + body span) of files.
func BuildTermIndex(files []File) *TermIndex {
	ix := &TermIndex{Version: TermIndexVersion}
	vocab := map[string]uint32{}
	counts := map[uint32]uint16{}
	for _, f := range files {
		if len(f.Symbols) == 0 {
			continue
		}
		lines := strings.Split(f.Content, "\n")
		pi := int32(len(ix.Paths))
		ix.Paths = append(ix.Paths, f.Path)
		for _, sp := range f.Symbols {
			for k := range counts {
				delete(counts, k)
			}
			add := func(text string) {
				forEachTerm(text, func(t string) {
					id, ok := vocab[t]
					if !ok {
						id = uint32(len(ix.Terms))
						vocab[t] = id
						ix.Terms = append(ix.Terms, t)
					}
					if counts[id] < 65535 {
						counts[id]++
					}
				})
			}
			add(sp.Name)
			add(spanText(lines, sp.StartLine, sp.EndLine))
			d := TermDoc{Path: pi, Symbol: sp.Name, Line: int32(sp.StartLine)}
			if len(counts) > 0 {
				d.IDs = make([]uint32, 0, len(counts))
				for id := range counts {
					d.IDs = append(d.IDs, id)
				}
				sort.Slice(d.IDs, func(i, j int) bool { return d.IDs[i] < d.IDs[j] })
				d.TFs = make([]uint16, len(d.IDs))
				for i, id := range d.IDs {
					d.TFs[i] = counts[id]
				}
			}
			ix.Docs = append(ix.Docs, d)
		}
	}
	return ix
}

// termSearch is TermIDFRanker over cached per-repo term indexes: df is summed
// across the in-scope repos (IDF is a corpus property), and only documents
// containing a query term are scored — the rest score 0 and are never hits.
func termSearch(s Store, repo, ref, q string, limit int, cache TermIndexCache) SemanticResult {
	res := SemanticResult{Ranker: TermIDFRanker{}.RankerName(), Meta: NewMeta(s, repo, ref), Note: KeywordRankingNote}
	type repoIx struct {
		repo string
		ix   *TermIndex
	}
	var ixs []repoIx
	for _, rp := range reposFor(s, repo) {
		var ix *TermIndex
		if cache != nil {
			if c, ok := cache.Load(rp, ref); ok && c.Version == TermIndexVersion {
				ix = c
			}
		}
		if ix == nil {
			ix = BuildTermIndex(s.Files(rp, ref))
			if cache != nil {
				cache.Save(rp, ref, ix)
			}
		}
		ixs = append(ixs, repoIx{rp, ix})
	}
	res.Considered = 0
	df := map[string]int{}
	n := 0
	for _, r := range ixs {
		res.Considered += len(r.ix.Docs)
		local := make([]int, len(r.ix.Terms))
		for _, d := range r.ix.Docs {
			if len(d.IDs) == 0 {
				continue
			}
			n++
			for _, id := range d.IDs {
				local[id]++
			}
		}
		for id, c := range local {
			if c > 0 {
				df[r.ix.Terms[id]] += c
			}
		}
	}
	qv := TermEmbedder{}.Embed(q)
	if len(qv) == 0 || n == 0 {
		return res
	}
	idf := func(term string) float64 {
		d := df[term]
		if d == 0 {
			return 0
		}
		return math.Log(1 + float64(n)/float64(d))
	}
	qw := map[string]float64{}
	for t, tf := range qv {
		if w := tf * idf(t); w > 0 {
			qw[t] = w
		}
	}
	if len(qw) == 0 {
		return res
	}
	qnorm := norm(qw)
	var hits []SemanticHit
	for _, r := range ixs {
		// Query weight by local term id; idf by local id, computed lazily.
		qloc := map[uint32]float64{}
		for id, t := range r.ix.Terms {
			if w, ok := qw[t]; ok {
				qloc[uint32(id)] = w
			}
		}
		if len(qloc) == 0 {
			continue
		}
		idfLocal := make([]float64, len(r.ix.Terms))
		for i := range idfLocal {
			idfLocal[i] = -1
		}
		for _, d := range r.ix.Docs {
			var dot float64
			for i, id := range d.IDs {
				if w, ok := qloc[id]; ok {
					if idfLocal[id] < 0 {
						idfLocal[id] = idf(r.ix.Terms[id])
					}
					dot += w * float64(d.TFs[i]) * idfLocal[id]
				}
			}
			if dot == 0 {
				continue
			}
			var ss float64
			for i, id := range d.IDs {
				if idfLocal[id] < 0 {
					idfLocal[id] = idf(r.ix.Terms[id])
				}
				x := float64(d.TFs[i]) * idfLocal[id]
				ss += x * x
			}
			dn := 1.0
			if ss != 0 {
				dn = math.Sqrt(ss)
			}
			hits = append(hits, SemanticHit{Repo: r.repo, Path: r.ix.Paths[d.Path], Symbol: d.Symbol, Line: int(d.Line),
				Score: dot / (qnorm * dn)})
		}
	}
	SortSemanticHits(hits)
	if len(hits) > limit {
		hits = hits[:limit]
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
	forEachTerm(text, func(t string) { out = append(out, t) })
	return out
}

// forEachTerm is the tokenizer: ASCII text (nearly all code) is cut by byte
// index without per-rune allocation; anything else takes the rune path. Both
// produce exactly the same terms.
func forEachTerm(text string, emit func(string)) {
	start := -1 // start of the current term (byte index)
	var prev byte
	ascii := true
	flush := func(end int) {
		if start >= 0 {
			t := text[start:end]
			if ascii && len(t) >= 2 {
				emit(lowerASCII(t))
			} else if !ascii && utf8.RuneCountInString(t) >= 2 {
				emit(strings.ToLower(t))
			}
		}
		start = -1
		ascii = true
	}
	for i := 0; i < len(text); {
		c := text[i]
		if c < utf8.RuneSelf {
			isLower, isUpper, isDigit := c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9'
			if isLower || isUpper || isDigit {
				if start >= 0 && isUpper && (prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9') {
					flush(i)
				}
				if start < 0 {
					start = i
				}
			} else {
				flush(i)
			}
			prev = c
			i++
			continue
		}
		r, size := utf8.DecodeRuneInString(text[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if start >= 0 && unicode.IsUpper(r) && (prev >= 'a' && prev <= 'z' || prev >= '0' && prev <= '9') {
				flush(i)
			}
			if start < 0 {
				start = i
			}
			ascii = false
			prev = 0 // a non-ASCII letter never starts a camelCase split
			if unicode.IsLower(r) {
				prev = 'a'
			} else if unicode.IsDigit(r) {
				prev = '0'
			}
		} else {
			flush(i)
			prev = 0
		}
		i += size
	}
	flush(len(text))
}

func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c >= 'A' && c <= 'Z' {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if b[j] >= 'A' && b[j] <= 'Z' {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
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
