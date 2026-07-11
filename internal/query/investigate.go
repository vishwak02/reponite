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
	Meta     Meta
}

// Investigate answers a natural-language question about the code: it ranks
// symbols fleet-wide by semantic similarity (repo may be FleetRepo "*"), then
// for each — in relevance order until the budget is spent — attaches a body
// preview and its callers/callees, and renders a cited markdown dossier.
func Investigate(s Store, repo, ref, question string, budget int) InvestigateResult {
	if budget <= 0 {
		budget = DefaultInvestigateBudget
	}
	if repo == "" {
		repo = FleetRepo
	}
	res := InvestigateResult{Question: question, Meta: Meta{Repo: repo, Ref: ref}}
	hits := SemanticSearch(s, repo, ref, question, investigateCandidates, nil)
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

	spent := 0
	for _, h := range hits {
		ctx := Context(s, h.Repo, ref, h.Symbol, false)
		qid := ctx.Symbol
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
	res.Dossier = renderDossier(question, res.Findings, res.Omitted)
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
func renderDossier(question string, fs []InvestigateFinding, omitted int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Investigation: %s\n\n", question)
	repos := map[string]bool{}
	for _, f := range fs {
		repos[f.Repo] = true
	}
	fmt.Fprintf(&b, "%d relevant symbol(s) across %d repo(s), most relevant first.\n", len(fs), len(repos))
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
