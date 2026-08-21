// investigate.go implements reponite_investigate — the agent's "understand X"
// superpower (blueprint §2 macro tools). One natural-language question returns a
// single dense, cited dossier: the symbols across the fleet that do the thing,
// what each is, where it lives, and how it connects (callers/callees) — replacing
// the semsearch → brief → context → repeat loop an agent runs by hand. Pure
// composition over SemanticSearch + Context + the brief source helpers, filled
// to a token budget in relevance order, so it is tested in-sandbox (ADR-018).
package query

import (
	"fmt"
	"strings"
)

// DefaultInvestigateBudget is the token budget when the caller passes <= 0. A bit
// larger than a single brief, since this is a multi-symbol synthesis.
const DefaultInvestigateBudget = 4000

const (
	investigateCandidates  = 25 // semantic hits to consider before budget-filling
	investigatePreviewLine = 6  // body lines shown per finding
	investigateNeighbors   = 4  // callers/callees shown per finding
)

// InvestigateFinding is one relevant symbol with just enough to understand its
// role, plus a handle to drill in.
type InvestigateFinding struct {
	Repo      string
	Path      string
	Symbol    string // qualified id (also the drill-in handle for brief/context)
	Line      int
	Score     float64
	Preview   string   // first lines of the body (includes the signature)
	Callers   []string // who uses it (blast radius), truncated
	Callees   []string // what it uses, truncated
	MoreUsers int      // callers beyond those shown
}

// InvestigateResult is the assembled dossier.
type InvestigateResult struct {
	Question string
	Findings []InvestigateFinding
	Dossier  string // rendered markdown — the primary agent-facing payload
	Omitted  int    // relevant matches dropped for budget
	// Considered is how many symbols were ranked to produce these findings, and
	// TopScore the best similarity achieved. Together they let a reader judge
	// whether a ranking means anything: 25 findings out of 30 indexed symbols,
	// or a top score near zero, is a shrug dressed as an answer.
	Considered int
	TopScore   float64
	Meta       Meta
}

// weakMatchScore is the similarity below which a ranking is reported as "no
// strong match". Term-IDF cosine on a genuinely relevant symbol clears this
// comfortably; scores under it are usually incidental word overlap.
const weakMatchScore = 0.12

// Investigate answers a natural-language question about the code: it ranks
// symbols fleet-wide by semantic similarity (repo may be FleetRepo "*"), then
// for each — in relevance order until the budget is spent — attaches a body
// preview and its callers/callees, and renders a cited markdown dossier.
func Investigate(s Store, repo, ref, question string, budget int) InvestigateResult {
	return InvestigateWith(s, repo, ref, question, budget, nil)
}

// InvestigateWith is Investigate with an explicit SemanticRanker for the
// candidate-discovery rung (nil = the pure term-idf default) — the same seam
// semsearch exposes (ADR-020), so a configured neural ranker improves the
// dossier's recall too.
func InvestigateWith(s Store, repo, ref, question string, budget int, r SemanticRanker) InvestigateResult {
	if budget <= 0 {
		budget = DefaultInvestigateBudget
	}
	if repo == "" {
		repo = FleetRepo
	}
	res := InvestigateResult{Question: question, Meta: Meta{Repo: repo, Ref: ref}}
	sem := SemanticSearch(s, repo, ref, question, investigateCandidates, r)
	// A degraded discovery rung must reach the caller: if the configured ranker
	// failed and the search fell back, the dossier says so (never-lie) — its
	// findings were selected by a weaker strategy than the agent asked for.
	if sem.Note != "" {
		res.Meta.Warnings = append(res.Meta.Warnings, sem.Note)
	}
	hits := sem.Hits
	res.Considered = sem.Considered
	if len(hits) > 0 {
		res.TopScore = hits[0].Score
	}
	// Say when the ranking is weak. Every other surface reports what it could
	// not resolve; investigate used to rank incidental word overlap with the
	// same confidence as a real hit, and it is the tool an agent reaches for
	// FIRST on an unfamiliar repo.
	if len(hits) > 0 && hits[0].Score < weakMatchScore {
		res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf(
			"no strong match: best similarity %.3f over %d indexed symbols — these findings may be incidental word overlap, and the answer may lie in files reponite does not index (check `reponite repos`)",
			hits[0].Score, sem.Considered))
	}
	if len(hits) == 0 {
		res.Dossier = "# Investigation: " + question + "\n\n_No symbols matched. Try different words, or `reponite_repos` to see what's indexed._"
		return res
	}

	filesByRepo := map[string][]File{} // cache Files per repo (fleet hits span repos)
	files := func(r string) []File {
		if f, ok := filesByRepo[r]; ok {
			return f
		}
		f := s.Files(r, ref)
		filesByRepo[r] = f
		return f
	}

	// The ranker scores one entry per symbol SPAN, so a symbol with several
	// spans (a C++ class declared in a header and defined in a .cpp, say)
	// yields several hits that all resolve to the SAME qualified id. Ranking
	// the same symbol twice made the dossier look like it had found more than
	// it had; keep the best-scoring entry per (repo, qid).
	seen := map[[2]string]bool{}
	spent := 0
	for _, h := range hits {
		ctx := Context(s, h.Repo, ref, h.Symbol, false)
		qid := ctx.Symbol
		if key := [2]string{h.Repo, qid}; seen[key] {
			continue
		} else {
			seen[key] = true
		}
		path, span, body, ok := symbolSource(files(h.Repo), qid)
		if !ok {
			path, span = h.Path, SymbolSpan{StartLine: h.Line}
		}
		f := InvestigateFinding{
			Repo: h.Repo, Path: path, Symbol: qid, Line: span.StartLine, Score: h.Score,
			Preview:   firstLines(body, investigatePreviewLine),
			Callees:   nonExternalNames(ctx.Callees, investigateNeighbors),
			Callers:   truncNames(ctx.Callers, investigateNeighbors),
			MoreUsers: max0(len(ctx.Callers) - investigateNeighbors),
		}
		cost := estFinding(f)
		if spent+cost > budget {
			res.Omitted++
			continue
		}
		spent += cost
		res.Findings = append(res.Findings, f)
	}
	res.Dossier = renderDossier(question, res.Findings, res.Omitted, res.Considered, res.TopScore, res.Meta.Warnings)
	return res
}

// nonExternalNames returns up to n callee base names, preferring in-repo edges
// (qualified names) over opaque externals so the "what it uses" list is useful.
func nonExternalNames(callees []string, n int) []string {
	var qualified, bare []string
	for _, c := range callees {
		if strings.Contains(c, ".") || strings.Contains(c, "/") {
			qualified = append(qualified, baseName(c))
		} else {
			bare = append(bare, c)
		}
	}
	return truncNames(append(qualified, bare...), n)
}

func truncNames(names []string, n int) []string {
	out := make([]string, 0, n)
	for _, x := range names {
		if len(out) >= n {
			break
		}
		out = append(out, baseName(x))
	}
	return out
}

func max0(x int) int {
	if x < 0 {
		return 0
	}
	return x
}

func estFinding(f InvestigateFinding) int {
	return estTokens(f.Preview) + estTokens(f.Symbol) + estTokens(f.Path) +
		estTokens(strings.Join(f.Callers, " ")) + estTokens(strings.Join(f.Callees, " ")) + 8
}

// renderDossier produces the dense, cited markdown an agent reads directly.
func renderDossier(question string, fs []InvestigateFinding, omitted, considered int, topScore float64, warnings []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Investigation: %s\n\n", question)
	repos := map[string]bool{}
	for _, f := range fs {
		repos[f.Repo] = true
	}
	fmt.Fprintf(&b, "%d symbol(s) across %d repo(s), most relevant first — ranked from %d indexed symbols (best similarity %.3f).\n",
		len(fs), len(repos), considered, topScore)
	// Caveats belong at the TOP: a reader who stops after the first screen must
	// still see that the ranking was weak or degraded.
	for _, w := range warnings {
		fmt.Fprintf(&b, "\n> **Caveat:** %s\n", w)
	}
	for i, f := range fs {
		fmt.Fprintf(&b, "\n## %d. %s\n", i+1, f.Symbol)
		fmt.Fprintf(&b, "`%s / %s:%d`\n", f.Repo, f.Path, f.Line)
		if f.Preview != "" {
			fmt.Fprintf(&b, "```\n%s\n```\n", f.Preview)
		}
		if len(f.Callees) > 0 {
			fmt.Fprintf(&b, "- **uses:** %s\n", strings.Join(f.Callees, ", "))
		}
		if len(f.Callers) > 0 {
			users := strings.Join(f.Callers, ", ")
			if f.MoreUsers > 0 {
				users += fmt.Sprintf(", +%d more", f.MoreUsers)
			}
			fmt.Fprintf(&b, "- **used by:** %s\n", users)
		}
	}
	if omitted > 0 {
		fmt.Fprintf(&b, "\n_%d lower-ranked match(es) omitted for budget. Drill in with `brief <symbol>` or raise the budget._\n", omitted)
	}
	return b.String()
}
