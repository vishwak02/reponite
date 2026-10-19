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
	"fmt"
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
		m := NewMeta(s, repo, ref)
		m.Warnings = append(m.Warnings, "symbol not found at "+m.Ref)
		return BriefResult{Symbol: symbol, Ref: m.Ref, Meta: m}
	}
	qid := names[0]
	if len(names) > 1 {
		warns = append(warns, "ambiguous "+symbol+"; using "+qid+" (also: "+strings.Join(names[1:], ", ")+")")
	}

	res := BriefResult{Symbol: qid, Ref: EffectiveRef(s, repo, ref), Omitted: []string{}}
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
	if ok {
		// decorators and annotations above the definition are part of what
		// an edit must keep (@csrf_exempt, @Override, #[cfg(test)])
		if pre := decoratorLines(files, path, span.StartLine); pre != "" {
			body = pre + body
		}
	}
	if n := definitionCount(files, qid); n > 1 {
		warns = append(warns, fmt.Sprintf("%d overloads share the name %s: their callers and callees are merged, and the body shown is the one at %s:%d", n, baseName(qid), path, span.StartLine))
	}
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
		if IsTestQID(s, repo, ref, e.Name) {
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

	// 3. Callers (blast radius) and 5. covering tests, split by the index-time
	// is_test flag (§9A.1) — a Go-only NAME heuristic reported zero covering
	// tests for every other language while advertising the section.
	var tests []string
	callersDropped := false
	for _, c := range ctx.Callers {
		if IsTestQID(s, repo, ref, c) {
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
	// tests reaching it through a request to the route it serves
	have := map[string]bool{}
	for _, t := range tests {
		have[t] = true
	}
	for _, h := range Tests(s, repo, ref, qid, 1, 1<<20).Tests {
		if h.How == "http" && h.Handle != "" && !have[h.Handle] {
			have[h.Handle] = true
			tests = append(tests, h.Handle)
		}
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
			if r != ref && r != EffectiveRef(s, repo, ref) {
				targets = append(targets, RepoRef{Repo: repo, Ref: r})
			}
		}
		if rep, err := CompatSymbol(s, RepoRef{Repo: repo, Ref: EffectiveRef(s, repo, ref)}, qid, targets); err == nil {
			for _, v := range rep.Verdicts {
				if !add(20) {
					res.Omitted = append(res.Omitted, "compat(budget)")
					break
				}
				res.Compat = append(res.Compat, BriefCompat{Ref: v.Ref, Verdict: string(v.Verdict), Confidence: v.Confidence, DirectConfidence: v.DirectConfidence})
			}
		}
	}

	res.Meta = NewMeta(s, repo, ref)
	res.Meta.Warnings = append(res.Meta.Warnings, warns...)
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

// symbolSource locates a symbol's file, line span, and source text at a ref
// (locateSymbol: the most specific package, then the span owned by the qid's
// class).
func symbolSource(files []File, qid string) (path string, span SymbolSpan, body string, ok bool) {
	path, span, ok = locateSymbol(files, qid)
	if !ok {
		return "", SymbolSpan{}, "", false
	}
	for _, f := range files {
		if f.Path == path {
			return path, span, sliceLines(f.Content, span.StartLine, span.EndLine), true
		}
	}
	return "", SymbolSpan{}, "", false
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

// decoratorLines are the decorator / annotation / attribute lines directly
// above a definition's first line (with a trailing newline), "" for none.
func decoratorLines(files []File, path string, start int) string {
	for _, f := range files {
		if f.Path != path {
			continue
		}
		lines := strings.Split(f.Content, "\n")
		top := start - 1 // 0-based index of the first line kept
		depth := 0       // closing minus opening brackets seen going up
		for i := start - 2; i >= 0 && i >= start-60; i-- {
			t := strings.TrimSpace(lines[i])
			depth += strings.Count(t, ")") + strings.Count(t, "]") - strings.Count(t, "(") - strings.Count(t, "[")
			switch {
			case depth <= 0 && (strings.HasPrefix(t, "@") || strings.HasPrefix(t, "#[")):
				top, depth = i, 0 // a whole decorator (possibly multi-line)
			case depth > 0:
				// inside a decorator's argument list spanning lines
			default:
				i = -1
			}
		}
		if top == start-1 {
			return ""
		}
		return strings.Join(lines[top:start-1], "\n") + "\n"
	}
	return ""
}
