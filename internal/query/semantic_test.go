package query_test

import (
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestTokenizeIdentifiersAndSearch(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path: "billing.go",
		Content: "func validateCardNumber() bool { return luhnCheck() }\n" +
			"func renderTemplate() string { return html }\n" +
			"func parseConfig() Config { return cfg }\n",
		Symbols: []query.SymbolSpan{
			{Name: "validateCardNumber", StartLine: 1, EndLine: 1},
			{Name: "renderTemplate", StartLine: 2, EndLine: 2},
			{Name: "parseConfig", StartLine: 3, EndLine: 3},
		},
	})

	res := query.SemanticSearch(m, "r", "HEAD", "validate a credit card", 3, nil)
	if len(res.Hits) == 0 {
		t.Fatal("expected semantic hits")
	}
	// The card-validation function must rank first (camelCase split -> card, validate).
	if res.Hits[0].Symbol != "validateCardNumber" {
		t.Fatalf("top hit = %q (want validateCardNumber); hits=%+v", res.Hits[0].Symbol, res.Hits)
	}
	// The result names the strategy that ranked it (provenance, ADR-020).
	// It must also say plainly that it is keyword ranking, not semantic.
	if res.Ranker != "term-idf" || !strings.Contains(res.Note, "NOT semantic") {
		t.Fatalf("default ranking must be labeled term-idf and keyword-only, got %q / %q", res.Ranker, res.Note)
	}

	// A query with no shared terms yields nothing.
	if r := query.SemanticSearch(m, "r", "HEAD", "quantum entanglement", 3, nil); len(r.Hits) != 0 {
		t.Fatalf("unrelated query should score zero, got %+v", r.Hits)
	}
}

// fakeRanker exercises the SemanticRanker seam: it sees the collected docs and
// returns a fixed ranking (or an error, to test the fallback contract).
type fakeRanker struct {
	name string
	err  error
	docs []query.SemanticDoc // captured for assertions
}

func (f *fakeRanker) RankerName() string { return f.name }
func (f *fakeRanker) Rank(q string, docs []query.SemanticDoc, limit int) ([]query.SemanticHit, error) {
	f.docs = docs
	if f.err != nil {
		return nil, f.err
	}
	// Deliberately rank the LAST doc first, to prove the custom strategy (not
	// the term default) produced the ordering.
	var hits []query.SemanticHit
	for i := len(docs) - 1; i >= 0 && len(hits) < limit; i-- {
		d := docs[i]
		hits = append(hits, query.SemanticHit{Repo: d.Repo, Path: d.Path, Symbol: d.Symbol, Line: d.Line, Score: 1 - float64(len(hits))*0.1})
	}
	return hits, nil
}

func semanticCorpus() *storage.Mem {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "a.go",
		Content: "func alpha() {}\nfunc beta() {}\n",
		Symbols: []query.SymbolSpan{{Name: "alpha", StartLine: 1, EndLine: 1}, {Name: "beta", StartLine: 2, EndLine: 2}},
	})
	return m
}

// A custom ranker plugs in behind the seam: it receives every in-scope doc and
// its ordering is returned verbatim, labeled with its own name.
func TestSemanticRankerSeam(t *testing.T) {
	m := semanticCorpus()
	fr := &fakeRanker{name: "neural:test-model"}
	res := query.SemanticSearch(m, "r", "HEAD", "anything", 10, fr)
	if len(fr.docs) != 2 {
		t.Fatalf("ranker must receive every in-scope doc, got %d", len(fr.docs))
	}
	if res.Ranker != "neural:test-model" {
		t.Fatalf("result must be labeled with the ranker that produced it, got %q", res.Ranker)
	}
	if len(res.Hits) != 2 || res.Hits[0].Symbol != "beta" {
		t.Fatalf("custom ranking must be returned verbatim (beta first), got %+v", res.Hits)
	}
}

// The fallback contract (never lie): a failing adapter falls back to the pure
// term ranker, and the result RECORDS both the failure and the ranker that
// actually produced the hits.
func TestSemanticRankerFallbackOnError(t *testing.T) {
	m := semanticCorpus()
	fr := &fakeRanker{name: "neural:down-model", err: errors.New("connection refused")}
	res := query.SemanticSearch(m, "r", "HEAD", "alpha", 10, fr)
	if res.Ranker != "term-idf" {
		t.Fatalf("fallback hits must be labeled term-idf, got %q", res.Ranker)
	}
	if !strings.Contains(res.Note, "neural:down-model") || !strings.Contains(res.Note, "connection refused") {
		t.Fatalf("the failure must be recorded in the note, got %q", res.Note)
	}
	if len(res.Hits) != 1 || res.Hits[0].Symbol != "alpha" {
		t.Fatalf("fallback must still rank (term match on alpha), got %+v", res.Hits)
	}
}

// memCache is an in-memory TermIndexCache that counts builds.
type memCache struct {
	m     map[string]*query.TermIndex
	saves int
}

func (c *memCache) Load(repo, ref string) (*query.TermIndex, bool) {
	ix, ok := c.m[repo+"@"+ref]
	return ix, ok
}
func (c *memCache) Save(repo, ref string, ix *query.TermIndex) {
	c.m[repo+"@"+ref] = ix
	c.saves++
}

// The indexed term search scores exactly like TermIDFRanker.Rank over the same
// docs (IDF over the whole multi-repo corpus), and a warm cache rebuilds nothing.
func TestTermSearchMatchesRankerAndCaches(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{Path: "planner/a.cpp",
		Content: "void Planner::publishStatus() { status_pub_.publish(robot_status_); }\nvoid Planner::plan() { computePath(goal_); }\nint idle();\n",
		Symbols: []query.SymbolSpan{{Name: "Planner.publishStatus", StartLine: 1, EndLine: 1}, {Name: "Planner.plan", StartLine: 2, EndLine: 2}, {Name: "idle", StartLine: 3, EndLine: 3}}})
	m.PutFile("monitor", "HEAD", query.File{Path: "monitor/w.py",
		Content: "def on_status(msg):\n    log_robot_status(msg)\ndef éclair_ÉtatRobot():\n    pass\n",
		Symbols: []query.SymbolSpan{{Name: "on_status", StartLine: 1, EndLine: 2}, {Name: "éclair_ÉtatRobot", StartLine: 3, EndLine: 4}}})
	var docs []query.SemanticDoc
	for _, rp := range []string{"monitor", "platform"} {
		for _, f := range m.Files(rp, "HEAD") {
			lines := strings.Split(f.Content, "\n")
			for _, sp := range f.Symbols {
				docs = append(docs, query.SemanticDoc{Repo: rp, Path: f.Path, Symbol: sp.Name, Line: sp.StartLine,
					Text: strings.Join(lines[sp.StartLine-1:sp.EndLine], "\n")})
			}
		}
	}
	for _, q := range []string{"publish robot status", "plan path", "état robot"} {
		want, _ := query.TermIDFRanker{}.Rank(q, docs, 10)
		c := &memCache{m: map[string]*query.TermIndex{}}
		got := query.SemanticSearchCached(m, query.FleetRepo, "HEAD", q, 10, nil, c)
		if len(got.Hits) != len(want) {
			t.Fatalf("%q: %d hits, want %d (%+v vs %+v)", q, len(got.Hits), len(want), got.Hits, want)
		}
		for i := range want {
			if got.Hits[i].Symbol != want[i].Symbol || math.Abs(got.Hits[i].Score-want[i].Score) > 1e-12 {
				t.Fatalf("%q hit %d: %+v want %+v", q, i, got.Hits[i], want[i])
			}
		}
		if c.saves != 2 {
			t.Fatalf("cold run builds each repo once, saves=%d", c.saves)
		}
		again := query.SemanticSearchCached(m, query.FleetRepo, "HEAD", q, 10, nil, c)
		if c.saves != 2 || len(again.Hits) != len(want) {
			t.Fatalf("warm run must not rebuild: saves=%d", c.saves)
		}
	}
}
