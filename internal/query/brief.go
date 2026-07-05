// brief.go implements reponite_brief (architecture ext §8C / ADR-014): one
// token-budgeted bundle with everything an agent needs to edit a symbol —
// the target's full body, its callees (what it relies on) and callers (blast
// radius), covering tests, and the compat snapshot (is this API load-bearing?).
// It is pure assembly over the Store primitives (source location, Context, the
// Oracle), so it is tested in-sandbox against storage.Mem (ADR-018). Only the
// target ships a full body; every neighbor is a preview + a handle (its
// qualified id, reusable in a follow-up brief/context/compat call), which is
// what turns graph access into a token saving rather than a bigger payload.
package query

import (
	"sort"
	"strings"
)

// DefaultBriefBudget is the token budget when the caller passes <= 0 (§8C.3).
const DefaultBriefBudget = 3000

const calleePreviewLines = 3

// BriefTarget is the symbol being edited: full body + location + facts.
type BriefTarget struct {
	Name      string
	Path      string
	Exported  bool
	StartLine int
	EndLine   int
	Body      string
}

// BriefNeighbor is a callee or caller: a preview + a handle to fetch the full
// body on demand (the handle is the symbol's qualified id). Callees also carry
// the edge's resolution provenance (invariant 5).
type BriefNeighbor struct {
	Name             string
	Handle           string
	Path             string
	Preview          string
	ResolutionMethod string  `json:",omitempty"`
	Confidence       float64 `json:",omitempty"`
}

// BriefCompat is one ref's Oracle verdict for the target (deploy-safety signal).
type BriefCompat struct {
	Ref              string
	Verdict          string
	Confidence       float64
	DirectConfidence float64
}

// IntentRecord is the "why" for the target's last change (PR/ticket/commit),
// supplied by an optional linkage provider so brief stays pure (§8A.6/ADR-017).
type IntentRecord struct {
	Commit  string
	PRs     []string
	Tickets []string
	Summary string `json:",omitempty"`
}

// IntentProvider resolves a symbol's change provenance. The pure core takes it
// as a dependency (nil = intent unavailable) so the git-backed implementation
// lives in a thin adapter, not here.
type IntentProvider interface {
	IntentFor(repo, ref, qid, path string, startLine, endLine int) (IntentRecord, bool)
}

// BriefResult is the assembled bundle. Omitted names the sections dropped to fit
// the token budget (or unavailable), each with a handle to fetch on demand.
type BriefResult struct {
	Symbol  string
	Ref     string
	Target  BriefTarget
	Callees []BriefNeighbor
	Callers []BriefNeighbor
	Tests   []string
	Compat  []BriefCompat
	Intent  *IntentRecord `json:",omitempty"`
	Omitted []string
	Meta    Meta
}

// Brief assembles the editing bundle for symbol at a ref, filling sections by
// priority until tokenBudget is reached (target > callees > callers > tests >
// intent > compat, §8C.2). intent may be nil.
func Brief(s Store, repo, ref, symbol string, tokenBudget int, intent IntentProvider) BriefResult {
	if tokenBudget <= 0 {
		tokenBudget = DefaultBriefBudget
	}
	var warns []string
	names := ResolveSymbol(s, repo, ref, symbol)
	if len(names) == 0 {
		return BriefResult{Symbol: symbol, Ref: ref, Meta: Meta{Repo: repo, Ref: ref, Warnings: []string{"symbol not found at " + ref}}}
	}
	qid := names[0]
	if len(names) > 1 {
		warns = append(warns, "ambiguous "+symbol+"; using "+qid+" (also: "+strings.Join(names[1:], ", ")+")")
	}

	res := BriefResult{Symbol: qid, Ref: ref, Omitted: []string{}}
	files := s.Files(repo, ref)
	spent := 0
	add := func(cost int) bool { // reserve budget; false when it would overflow
		if spent+cost > tokenBudget {
			return false
		}
		spent += cost
		return true
	}

	// 1. Target: full body (always attempted; body truncated if it alone overflows).
	path, span, body, ok := symbolSource(files, qid)
	res.Target = BriefTarget{Name: baseName(qid), Path: path, Exported: isExported(baseName(qid)), StartLine: span.StartLine, EndLine: span.EndLine}
	if ok {
		if estTokens(body) > tokenBudget {
			body = truncateToTokens(body, tokenBudget)
			res.Omitted = append(res.Omitted, "target.body(truncated)")
		}
		res.Target.Body = body
		spent += estTokens(body)
	} else {
		res.Omitted = append(res.Omitted, "target.body(source not indexed)")
	}

	ctx := Context(s, repo, ref, qid, true) // include tests so covering tests are visible

	// 2. Callees (depth 1): preview + handle + edge provenance.
	calleesDropped := false
	for _, e := range ctx.CalleeEdges {
		if IsTestName(baseName(e.Name)) {
			continue
		}
		n := neighbor(files, e.Name)
		n.ResolutionMethod, n.Confidence = e.ResolutionMethod, e.Confidence
		if !add(estNeighbor(n)) {
			calleesDropped = true
			break
		}
		res.Callees = append(res.Callees, n)
	}
	if calleesDropped {
		res.Omitted = append(res.Omitted, "callees(budget)")
	}

	// 3. Callers (blast radius) and 5. covering tests, split by IsTestName.
	var tests []string
	callersDropped := false
	for _, c := range ctx.Callers {
		if IsTestName(baseName(c)) {
			tests = append(tests, c)
			continue
		}
		n := neighbor(files, c)
		if !add(estNeighbor(n)) {
			callersDropped = true
			continue
		}
		res.Callers = append(res.Callers, n)
	}
	if callersDropped {
		res.Omitted = append(res.Omitted, "callers(budget)")
	}
	sort.Strings(tests)
	for _, tname := range tests {
		if !add(estTokens(tname)) {
			res.Omitted = append(res.Omitted, "tests(budget)")
			break
		}
		res.Tests = append(res.Tests, tname)
	}

	// 6. Intent (why), if a provider is wired.
	if intent != nil {
		if rec, ok := intent.IntentFor(repo, ref, qid, path, span.StartLine, span.EndLine); ok {
			res.Intent = &rec
		}
	} else {
		res.Omitted = append(res.Omitted, "intent(no provider)")
	}

	// 7. Compat snapshot across the repo's other refs — for exported symbols only.
	if res.Target.Exported {
		var targets []RepoRef
		for _, r := range s.Refs(repo) {
			if r != ref {
				targets = append(targets, RepoRef{Repo: repo, Ref: r})
			}
		}
		if rep, err := CompatSymbol(s, RepoRef{Repo: repo, Ref: ref}, qid, targets); err == nil {
			for _, v := range rep.Verdicts {
				if !add(20) {
					res.Omitted = append(res.Omitted, "compat(budget)")
					break
				}
				res.Compat = append(res.Compat, BriefCompat{Ref: v.Ref, Verdict: string(v.Verdict), Confidence: v.Confidence, DirectConfidence: v.DirectConfidence})
			}
		}
	}

	res.Meta = Meta{Repo: repo, Ref: ref, Warnings: warns}
	return res
}

// neighbor builds a callee/caller entry: a body preview + a handle (the qid).
func neighbor(files []File, qid string) BriefNeighbor {
	path, _, body, ok := symbolSource(files, qid)
	n := BriefNeighbor{Name: baseName(qid), Handle: qid, Path: path}
	if ok {
		n.Preview = firstLines(body, calleePreviewLines)
	}
	return n
}

// symbolSource locates a symbol's file, line span, and source text at a ref by
// reconstructing the indexer's qid scheme (pkgOf(path) prefix + bare name). It
// picks the file whose package prefixes the qid (most specific wins). Same class
// of same-package same-name-method ambiguity as ResolveSymbol; returns the first
// such match. ok is false when the symbol's source isn't indexed.
func symbolSource(files []File, qid string) (path string, span SymbolSpan, body string, ok bool) {
	base := baseName(qid)
	best := -1
	for _, f := range files {
		p := pkgFromPath(f.Path)
		var score int
		switch {
		case p != "" && strings.HasPrefix(qid, p+"."):
			score = len(p)
		case p == "" && qid == base:
			score = 0
		default:
			continue
		}
		for _, sp := range f.Symbols {
			if sp.Name != base {
				continue
			}
			if score > best {
				best = score
				path, span, body, ok = f.Path, sp, sliceLines(f.Content, sp.StartLine, sp.EndLine), true
			}
			break
		}
	}
	return path, span, body, ok
}

// pkgFromPath mirrors processing.pkgOf: the file's directory, "" at repo root.
func pkgFromPath(path string) string {
	i := strings.LastIndex(path, "/")
	if i < 0 {
		return ""
	}
	return path[:i]
}

func sliceLines(content string, start, end int) string {
	if start <= 0 || end < start {
		return ""
	}
	lines := strings.Split(content, "\n")
	if start > len(lines) {
		return ""
	}
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[start-1:end], "\n")
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[:n], "\n") + "\n…"
}

// isExported is the exported heuristic: an initial upper-case ASCII letter
// (exact for Go; an approximation for languages without that convention).
func isExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

func estTokens(s string) int { return len(s)/4 + 1 }

func estNeighbor(n BriefNeighbor) int {
	return estTokens(n.Name) + estTokens(n.Handle) + estTokens(n.Preview) + estTokens(n.Path)
}

func truncateToTokens(s string, tokens int) string {
	max := tokens * 4
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n… (truncated)"
}
