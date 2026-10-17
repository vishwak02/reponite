// explain.go implements reponite_explain — one bundle to UNDERSTAND (not edit)
// one thing, resolved from whatever handle the agent holds:
//
//   - a symbol: what it is (kind, signature, decorators, doc comment), where
//     it lives, its body trimmed to the budget, direct callers and callees
//     with resolution confidence, which HTTP route / ROS topic / UI event
//     reaches it, the tests that reference it, and a pointer to `brief`;
//   - a file (path, or path:line for the symbol at that line): its symbols,
//     imports, role, and its inbound/outbound links by file;
//   - a directory (a module from `map`): its stated purpose, files and most
//     connected symbols;
//   - a route ("METHOD /path" or "/path"): the server handler and every
//     client call that reaches it.
//
// Pure composition over Store, Context, Routes, CommGraph and grep (ADR-018).
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultExplainBudget is the token budget when the caller passes <= 0.
const DefaultExplainBudget = 3000

const explainNeighbors = 12 // callers/callees listed before "more"

// ExplainEdge is a caller or callee with where it lives and how surely the
// edge is known (invariant 5).
type ExplainEdge struct {
	Symbol           string // qualified id — the drill-in handle
	Repo             string `json:",omitempty"`
	Path             string `json:",omitempty"`
	Line             int    `json:",omitempty"`
	ResolutionMethod string `json:",omitempty"`
	Confidence       float64
	External         bool `json:",omitempty"` // a callee outside the indexed code
	IsTest           bool `json:",omitempty"`
}

// Reach is a runtime path into the symbol the call graph cannot see.
type Reach struct {
	Kind   string // route | topic | ui-event
	Detail string // "GET /api/orders/{}", "topic orders (published by …)", "onClick"
	Repo   string
	Path   string
	Line   int
	Text   string `json:",omitempty"`
}

// TestRef is a test that references the target.
type TestRef struct {
	Repo, Path string
	Line       int
	In         string // enclosing test symbol
	Calls      bool   // the enclosing test is a resolved caller (not just a mention)
}

// ExplainSymbol is the symbol bundle.
type ExplainSymbol struct {
	Symbol        string
	Repo          string
	Lang          string
	Kind          string
	Signature     string
	Decorators    []string `json:",omitempty"`
	Doc           string
	DocLine       int `json:",omitempty"`
	Path          string
	StartLine     int
	EndLine       int
	Exported      bool
	IsTest        bool
	Recursive     bool // it calls itself (a resolved self edge: recursion, or a super() call resolved by name)
	Body          string
	BodyTruncated bool
	Callers       []ExplainEdge
	Callees       []ExplainEdge
	MoreCallers   int
	MoreCallees   int
	ReachedBy     []Reach
	Tests         []TestRef
	Edit          string // the command for an editing bundle
}

// FileSymbol is one symbol defined in a file.
type FileSymbol struct {
	Symbol    string
	Kind      string
	Line      int
	EndLine   int
	Signature string
	In, Out   int
}

// FileLink is another file this one is linked to through resolved calls.
type FileLink struct {
	Repo, Path string
	Calls      int
	Examples   []string // caller → callee
}

// ExplainFile is the file bundle.
type ExplainFile struct {
	Repo, Path, Lang string
	Lines            int
	Role             []string
	Purpose          string // the file's own leading doc (module docstring, @file, package doc)
	Symbols          []FileSymbol
	Imports          []string
	Inbound          []FileLink // files whose code calls into this one
	Outbound         []FileLink // files this one calls into
	EntryPoints      []EntryPoint
	Routes           []Route
	Topics           []CommEndpoint
	MoreSymbols      int
}

// ExplainDir is the directory/module bundle.
type ExplainDir struct {
	Repo, Path string
	Purpose    string
	Source     string
	Files      []DirFile
	Hubs       []Hub
	MoreFiles  int
}

// DirFile is one file of a directory with its symbol count and role.
type DirFile struct {
	Path    string
	Lang    string
	Symbols int
}

// ExplainRoute is one matched route with its handler and clients.
type ExplainRoute struct {
	Route   Route
	Handler *ExplainSymbol `json:",omitempty"`
	Clients []ClientCall
}

// ExplainCandidate is one match of an ambiguous target.
type ExplainCandidate struct {
	Repo    string
	Symbols []string
}

// ExplainResult is the explain bundle.
type ExplainResult struct {
	Target     string
	Kind       string // symbol | file | directory | route | ambiguous | not_found
	Repo, Ref  string
	Symbol     *ExplainSymbol `json:",omitempty"`
	File       *ExplainFile   `json:",omitempty"`
	Dir        *ExplainDir    `json:",omitempty"`
	Routes     []ExplainRoute `json:",omitempty"`
	Unmatched  []ClientCall   `json:",omitempty"` // client calls to the route no indexed server serves
	Candidates []ExplainCandidate
	Truncated  bool
	Omitted    []string
	Next       []string
	Note       string
	Meta       Meta
}

// ExplainOptions scope the resolution of a target.
type ExplainOptions struct {
	Repo   string // only this repo
	Prefer string // try this repo first (the working directory's)
	Budget int
}

// viewsetActions are the standard REST viewset methods, mapped to whether
// they serve the detail route (true) or the collection route (false).
var viewsetActions = map[string]bool{"list": false, "create": false, "retrieve": true, "update": true, "partial_update": true, "destroy": true}

// httpVerbs are class-based view methods named after the HTTP method they serve.
var httpVerbs = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true}

var (
	reRouteTarget = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|ANY|\*)\s+(\S+)$`)
	reLineSuffix  = regexp.MustCompile(`^(.*):(\d+)$`)
)

// Explain resolves target (symbol | path[:line] | directory | "METHOD /route")
// and assembles its understanding bundle.
func Explain(s Store, ref, target string, opt ExplainOptions) ExplainResult {
	r := explainTarget(s, ref, target, opt)
	r.Next = withRef(r.Next, ref)
	if r.Symbol != nil {
		r.Symbol.Edit = hintRef(r.Symbol.Edit, ref)
	}
	return r
}

func explainTarget(s Store, ref, target string, opt ExplainOptions) ExplainResult {
	budget := opt.Budget
	if budget <= 0 {
		budget = DefaultExplainBudget
	}
	target = strings.TrimSpace(target)
	res := ExplainResult{Target: target, Ref: ref, Omitted: []string{}}
	repos := explainRepos(s, opt)

	if m := reRouteTarget.FindStringSubmatch(target); m != nil {
		return explainRoute(s, ref, strings.ToUpper(m[1]), m[2], opt, budget, res)
	}
	path, line := target, 0
	if m := reLineSuffix.FindStringSubmatch(target); m != nil {
		path = m[1]
		line, _ = strconv.Atoi(m[2])
	}
	if !strings.HasPrefix(target, "/") || line > 0 {
		want := strings.TrimPrefix(path, "./")
		named := func(fp string) bool { // the file itself, or a file under the directory
			return fp == want || strings.HasSuffix(fp, "/"+want) || want == "." ||
				strings.HasPrefix(fp, want+"/") || strings.Contains(fp, "/"+want+"/")
		}
		for _, rp := range repos {
			// a symbol name rarely names a file: look the path up first, decode the repo only on a hit
			hits := filesMatching(s, rp, ref, named)
			if len(hits) == 0 {
				continue
			}
			files := s.Files(rp, ref)
			if p, ok := findFile(files, want); ok {
				res.Repo, res.Ref, res.Meta = rp, EffectiveRef(s, rp, ref), NewMeta(s, rp, ref)
				idx := newSpanIndex(files)
				if line > 0 {
					if qid, _ := idx.qidAt(s, rp, ref, p, line); qid != "" {
						res.Kind = "symbol"
						res.Symbol = explainSymbol(s, rp, ref, qid, files, idx, budget, &res)
						res.Next = symbolNext(res.Symbol)
						return res
					}
					res.Note = fmt.Sprintf("no symbol encloses %s:%d — explaining the file", p, line)
				}
				res.Kind = "file"
				res.File = explainFile(s, rp, ref, p, files, idx, budget, &res)
				res.Next = []string{"reponite explain <symbol> --repo " + rp + "   (one of the symbols above)"}
				return res
			}
			if d, ok := findDir(files, strings.TrimSuffix(strings.TrimPrefix(path, "./"), "/")); ok {
				res.Repo, res.Ref, res.Meta = rp, EffectiveRef(s, rp, ref), NewMeta(s, rp, ref)
				res.Kind = "directory"
				res.Dir = explainDir(s, rp, ref, d, files, budget, &res)
				res.Next = []string{"reponite explain <file> --repo " + rp + "   (one of the files above)"}
				return res
			}
		}
	}
	if strings.HasPrefix(target, "/") {
		return explainRoute(s, ref, "", target, opt, budget, res)
	}

	// A symbol: resolve in each repo; the preferred repo wins outright, a
	// unique match elsewhere is used, several are the answer (never guessed).
	var found []ExplainCandidate
	for _, rp := range repos {
		if names := ResolveSymbol(s, rp, ref, target); len(names) > 0 {
			found = append(found, ExplainCandidate{Repo: rp, Symbols: names})
			if rp == opt.Prefer || rp == opt.Repo {
				break
			}
		}
	}
	if len(found) > 0 && (found[0].Repo == opt.Prefer || found[0].Repo == opt.Repo) {
		found = found[:1]
	}
	switch len(found) {
	case 0:
		res.Kind = "not_found"
		res.Meta = NewMeta(s, FleetRepo, ref)
		res.Note = fmt.Sprintf("%q is not a symbol, file, directory or route at %s in %v", target, ref, repos)
		for _, h := range Suggest(s, scopeOf(opt), ref, target, 6) {
			res.Next = append(res.Next, fmt.Sprintf("reponite explain %s --repo %s", h.Name, h.Repo))
		}
		return res
	case 1:
	default:
		res.Kind = "ambiguous"
		res.Candidates = found
		res.Meta = NewMeta(s, FleetRepo, ref)
		res.Note = fmt.Sprintf("%q is defined in %d repos — rerun with --repo <repo> (or a more qualified name)", target, len(found))
		return res
	}
	rp := found[0].Repo
	res.Repo, res.Ref, res.Meta = rp, EffectiveRef(s, rp, ref), NewMeta(s, rp, ref)
	qid := found[0].Symbols[0]
	if len(found[0].Symbols) > 1 {
		res.Meta.Warnings = append(res.Meta.Warnings, "ambiguous "+target+"; using "+qid+" (also: "+strings.Join(found[0].Symbols[1:], ", ")+")")
		res.Candidates = found
	}
	files := s.Files(rp, ref)
	res.Kind = "symbol"
	res.Symbol = explainSymbol(s, rp, ref, qid, files, newSpanIndex(files), budget, &res)
	res.Next = symbolNext(res.Symbol)
	return res
}

// withRef adds a non-default ref to the drill-in commands of a hint list, so
// following a hint reads the same ref the answer was read at.
func withRef(hints []string, ref string) []string {
	for i, h := range hints {
		hints[i] = hintRef(h, ref)
	}
	return hints
}

func hintRef(h, ref string) string {
	if ref == "" || ref == "HEAD" {
		return h
	}
	for _, c := range []string{"reponite explain ", "reponite map", "reponite brief "} {
		if strings.HasPrefix(h, c) {
			cmd, rest, _ := strings.Cut(h, "   (")
			if rest != "" {
				rest = "   (" + rest
			}
			return cmd + " " + ref + rest
		}
	}
	return h
}

func scopeOf(opt ExplainOptions) string {
	if opt.Repo != "" {
		return opt.Repo
	}
	return FleetRepo
}

func explainRepos(s Store, opt ExplainOptions) []string {
	if opt.Repo != "" {
		return []string{opt.Repo}
	}
	var out []string
	if opt.Prefer != "" && containsStr(s.Repos(), opt.Prefer) {
		out = append(out, opt.Prefer)
	}
	for _, r := range s.Repos() {
		if r != opt.Prefer {
			out = append(out, r)
		}
	}
	return out
}

// findFile matches a path exactly, else by a unique "/"-bounded suffix.
func findFile(files []File, p string) (string, bool) {
	var suffix []string
	for _, f := range files {
		if f.Path == p {
			return p, true
		}
		if strings.HasSuffix(f.Path, "/"+p) {
			suffix = append(suffix, f.Path)
		}
	}
	if len(suffix) == 1 {
		return suffix[0], true
	}
	return "", false
}

func findDir(files []File, d string) (string, bool) {
	if d == "." || d == "" {
		return "", len(files) > 0
	}
	var suffix map[string]bool
	for _, f := range files {
		if strings.HasPrefix(f.Path, d+"/") {
			return d, true
		}
		if i := strings.Index(f.Path, "/"+d+"/"); i >= 0 {
			if suffix == nil {
				suffix = map[string]bool{}
			}
			suffix[f.Path[:i+1+len(d)]] = true
		}
	}
	if len(suffix) == 1 {
		for k := range suffix {
			return k, true
		}
	}
	return "", false
}

func symbolNext(sym *ExplainSymbol) []string {
	if sym == nil {
		return nil
	}
	next := []string{sym.Edit}
	if len(sym.Callers) > 0 {
		next = append(next, "reponite explain "+sym.Callers[0].Symbol+" --repo "+sym.Repo+"   (a caller)")
	}
	next = append(next, "reponite usages "+baseName(sym.Symbol)+"   (every call site with its line)")
	if sym.Exported {
		next = append(next, "reponite blast-radius "+sym.Symbol+"   (before changing it)")
	}
	return next
}

// explainSymbol assembles the symbol bundle, spending budget in priority
// order: facts > callers/callees > reached-by > tests > body.
func explainSymbol(s Store, repo, ref, qid string, files []File, idx *spanIndex, budget int, res *ExplainResult) *ExplainSymbol {
	symRef, _ := s.SymbolAt(repo, qid, ref)
	sym := &ExplainSymbol{Symbol: qid, Repo: repo, Lang: symRef.Lang, IsTest: IsTestSymbol(symRef, qid),
		Exported: IsExportedName(symRef.Lang, baseName(qid)),
		Edit:     "reponite brief " + qid + " --repo " + repo + "   (full body + covering tests + compat, for editing)"}
	loc, ok := idx.locate(qid)
	var content string
	if ok {
		f := idx.byPath[loc.Path]
		content = f.Content
		sym.Path, sym.StartLine, sym.EndLine = loc.Path, loc.Span.StartLine, loc.Span.EndLine
		if sym.Lang == "" {
			sym.Lang = langOfPath(loc.Path)
		}
		_, local := idx.splitQID(qid)
		facts := sourceFacts(content, normLang(sym.Lang), loc.Span.StartLine, loc.Span.EndLine, strings.Contains(local, "."))
		sym.Kind, sym.Signature, sym.Decorators, sym.Doc, sym.DocLine = facts.Kind, facts.Signature, facts.Decorators, facts.Doc, facts.DocLine
	} else {
		res.Omitted = append(res.Omitted, "body(source not indexed)")
	}
	spent := estTokens(sym.Signature+sym.Doc+strings.Join(sym.Decorators, " ")) + 40

	// Callers and callees, with resolution provenance.
	snap := s.Snapshot(repo, ref)
	syms := s.SymbolsAt(repo, ref)
	var callers, callees []ExplainEdge
	for caller, cs := range snap.Callees {
		for _, c := range cs {
			if c.Name == qid && caller == qid {
				sym.Recursive = true
				break
			}
			if c.Name == qid {
				e := ExplainEdge{Symbol: caller, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence, IsTest: IsTestQID(s, repo, ref, caller)}
				if l, ok := idx.locate(caller); ok {
					e.Path, e.Line = l.Path, l.Span.StartLine
				}
				callers = append(callers, e)
				break
			}
		}
	}
	for _, c := range snap.Callees[qid] {
		if c.Name == qid {
			continue // the self edge, reported as Recursive
		}
		e := ExplainEdge{Symbol: c.Name, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence}
		if _, known := syms[c.Name]; known {
			if l, ok := idx.locate(c.Name); ok {
				e.Path, e.Line = l.Path, l.Span.StartLine
			}
		} else {
			e.External = true
		}
		callees = append(callees, e)
	}
	// Cross-repo virtual dispatch callers (a subclass in another repo).
	ctx := WithCrossRepoDispatch(s, repo, ref, ContextResult{Symbol: qid, Callers: []string{}, CallerEdges: []CallerEdge{}}, true)
	for _, ce := range ctx.CallerEdges {
		if ce.Repo != "" {
			callers = append(callers, ExplainEdge{Symbol: ce.Name, Repo: ce.Repo, ResolutionMethod: "cross-repo-dispatch via " + ce.Via, IsTest: ce.IsTest})
		}
	}
	sortEdges(callers, true)
	sortEdges(callees, false)
	var tests []ExplainEdge
	for _, c := range callers {
		if c.IsTest {
			tests = append(tests, c)
			continue
		}
		if len(sym.Callers) >= explainNeighbors || spent+estEdge(c) > budget {
			sym.MoreCallers++
			continue
		}
		spent += estEdge(c)
		sym.Callers = append(sym.Callers, c)
	}
	for _, c := range callees {
		if IsTestQID(s, repo, ref, c.Symbol) {
			continue
		}
		if len(sym.Callees) >= explainNeighbors || spent+estEdge(c) > budget {
			sym.MoreCallees++
			continue
		}
		spent += estEdge(c)
		sym.Callees = append(sym.Callees, c)
	}
	if sym.MoreCallers > 0 {
		res.Omitted = append(res.Omitted, fmt.Sprintf("callers(+%d: reponite context %s)", sym.MoreCallers, qid))
	}
	if sym.MoreCallees > 0 {
		res.Omitted = append(res.Omitted, fmt.Sprintf("callees(+%d: reponite context %s)", sym.MoreCallees, qid))
	}

	// Runtime reach: routes, topics, UI events.
	for _, r := range reachOf(s, repo, ref, qid, sym.Kind, sym.Path) {
		c := estTokens(r.Detail+r.Path+r.Text) + 6
		if spent+c > budget {
			res.Omitted = append(res.Omitted, "reached_by(budget)")
			break
		}
		spent += c
		sym.ReachedBy = append(sym.ReachedBy, r)
	}

	// Tests: resolved test callers first, then test files that mention it.
	for _, t := range testRefs(s, repo, ref, qid, tests, idx) {
		c := estTokens(t.Path+t.In) + 6
		if len(sym.Tests) >= explainNeighbors || spent+c > budget {
			res.Omitted = append(res.Omitted, "tests(+more: reponite usages "+baseName(qid)+")")
			break
		}
		spent += c
		sym.Tests = append(sym.Tests, t)
	}

	// Body, trimmed to what is left.
	if ok {
		body := sliceLines(content, loc.Span.StartLine, loc.Span.EndLine)
		left := budget - spent
		if left < 60 {
			left = 60
		}
		if estTokens(body) > left {
			body = truncateToTokens(body, left)
			sym.BodyTruncated = true
			res.Omitted = append(res.Omitted, "body(truncated: "+sym.Edit[:strings.Index(sym.Edit, "   ")]+" for all of it)")
		}
		sym.Body = body
	}
	if len(res.Omitted) > 0 {
		res.Truncated = true
	}
	return sym
}

func estEdge(e ExplainEdge) int { return estTokens(e.Symbol+e.Path+e.ResolutionMethod) + 6 }

func sortEdges(es []ExplainEdge, callers bool) {
	sort.SliceStable(es, func(i, j int) bool {
		a, b := es[i], es[j]
		if a.External != b.External {
			return !a.External
		}
		if a.Confidence != b.Confidence {
			return a.Confidence > b.Confidence
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Symbol < b.Symbol
	})
}

// reachOf finds the runtime paths into a symbol: HTTP routes whose handler
// is it (or its class), ROS endpoints it publishes/subscribes in or is the
// callback of, and UI event props bound to it.
func reachOf(s Store, repo, ref, qid, kind, path string) []Reach {
	base := baseName(qid)
	class := ""
	if parts := strings.Split(qid[strings.LastIndex(qid, "/")+1:], "."); len(parts) >= 2 {
		class = parts[len(parts)-2]
	}
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(base) + `\b`)
	var out []Reach
	// The fleet-wide graphs are built only when the symbol's own repo has a
	// route or a ROS endpoint at all: only its own routes and endpoints are
	// reported, and building them for a 40-repo fleet takes tens of seconds.
	type hit struct {
		l   RouteLink
		via string
	}
	var direct, viaClass []hit
	match := func(rr RoutesResult) {
		direct, viaClass = nil, nil
		for _, l := range rr.Links {
			r := l.Route
			if r.Repo != repo {
				continue
			}
			h := strings.TrimSuffix(strings.Split(r.Handler, "(")[0], ".as_view")
			segs := strings.Split(h, ".")
			last := segs[len(segs)-1]
			switch {
			case last == base && (len(segs) < 2 || class == "" || segs[len(segs)-2] == class || kind == "class"):
				direct = append(direct, hit{l, ""})
			case r.Handler == "" && word.MatchString(lineAtFile(s, repo, ref, r.Path, r.Line)):
				direct = append(direct, hit{l, ""})
			case class != "" && last == class:
				// A class-based view/viewset routed as a whole: only its standard
				// action and HTTP-verb methods are reached through the class.
				detail := strings.HasSuffix(strings.TrimSuffix(r.Pattern, "/"), "{}")
				if act, std := viewsetActions[base]; std && act == detail ||
					httpVerbs[base] && (r.Method == "*" || strings.EqualFold(r.Method, base)) {
					viaClass = append(viaClass, hit{l, " (via its class " + class + ")"})
				}
			}
		}
	}
	// The repo's own routes decide whether the symbol is routed at all; only
	// then are the fleet's clients matched to them.
	match(RoutesWith(s, repo, ref, "", 1<<30, RoutesOptions{}))
	if len(direct)+len(viaClass) > 0 {
		match(RoutesWith(s, FleetRepo, ref, "", 1<<30, RoutesOptions{}))
	}
	if len(direct) > 0 {
		viaClass = nil // a method with its own route is not also reached through its class
	}
	for _, x := range append(direct, viaClass...) {
		r, l := x.l.Route, x.l
		d := fmt.Sprintf("%s /%s [%s]%s", r.Method, strings.TrimPrefix(r.Pattern, "/"), r.Framework, x.via)
		out = append(out, Reach{Kind: "route", Detail: d, Repo: r.Repo, Path: r.Path, Line: r.Line})
		for i, c := range l.Clients {
			if i == 3 {
				out = append(out, Reach{Kind: "route-client", Detail: fmt.Sprintf("+%d more clients (reponite routes %s)", len(l.Clients)-3, r.Pattern)})
				break
			}
			out = append(out, Reach{Kind: "route-client", Detail: c.Method + " " + c.URL + " from " + c.In, Repo: c.Repo, Path: c.Path, Line: c.Line, Text: c.Text})
		}
	}
	var cg CommGraphResult
	if repoHasComms(s, repo, ref) {
		cg = CommGraph(s, FleetRepo, ref)
	}
	for _, g := range cg.Groups {
		for _, side := range []struct {
			eps   []CommEndpoint
			other []CommEndpoint
			verb  string
		}{{g.Consumers, g.Producers, "receives"}, {g.Producers, g.Consumers, "sends"}} {
			for _, ep := range side.eps {
				if ep.Repo != repo {
					continue
				}
				inIt := baseName(ep.In) == base && (path == "" || ep.Path == path)
				callback := side.verb == "receives" && word.MatchString(ep.Text)
				if !inIt && !callback {
					continue
				}
				var peers []string
				for i, o := range side.other {
					if i == 3 {
						peers = append(peers, fmt.Sprintf("+%d", len(side.other)-3))
						break
					}
					peers = append(peers, o.Repo+":"+o.Path)
				}
				d := fmt.Sprintf("%s %s %s %q", ep.Role, side.verb, g.Family, g.Name)
				if len(peers) > 0 {
					d += " ↔ " + strings.Join(peers, ", ")
				} else {
					d += " (no indexed peer)"
				}
				out = append(out, Reach{Kind: "topic", Detail: d, Repo: ep.Repo, Path: ep.Path, Line: ep.Line, Text: ep.Text})
			}
		}
	}
	// UI events: JSX on*={…name…}, Vue @event="name", Angular (event)="name(…)".
	pat := `on[A-Z]\w*=\{[^}]*\b` + regexp.QuoteMeta(base) + `\b|@[\w.:-]+="` + regexp.QuoteMeta(base) + `\b|\(\w+\)="` + regexp.QuoteMeta(base) + `\(`
	// A template binds a handler of its own codebase: the symbol's repo is searched.
	if g, err := GrepRepo(s, repo, ref, pat, GrepOptions{Limit: 10}); err == nil {
		evt := regexp.MustCompile(`on[A-Z]\w*|@[\w.:-]+|\(\w+\)`)
		for _, m := range g.Matches {
			out = append(out, Reach{Kind: "ui-event", Detail: evt.FindString(m.Text) + " in " + m.Symbol, Repo: m.Repo, Path: m.Path, Line: m.Line, Text: strings.TrimSpace(m.Text)})
		}
	}
	return out
}

func lineAtFile(s Store, repo, ref, path string, line int) string {
	for _, f := range filesMatching(s, repo, ref, func(p string) bool { return p == path }) {
		return lineAt(f.Content, line)
	}
	return ""
}

// testRefs lists test code referencing qid: resolved test callers, then test
// files mentioning its name, one entry per (file, enclosing symbol).
func testRefs(s Store, repo, ref, qid string, testCallers []ExplainEdge, idx *spanIndex) []TestRef {
	var out []TestRef
	seen := map[string]bool{}
	for _, c := range testCallers {
		t := TestRef{Repo: repo, Path: c.Path, Line: c.Line, In: c.Symbol, Calls: true}
		if c.Repo != "" {
			t.Repo = c.Repo
		}
		seen[t.Path+"\x00"+baseName(c.Symbol)] = true
		out = append(out, t)
	}
	// Test files naming it, scanned directly: one pattern over the test files
	// only is far cheaper than a trigram index of the whole fleet.
	base := baseName(qid)
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(base) + `\b`)
	var found []TestRef
	for _, rp := range reposFor(s, FleetRepo) {
		for _, f := range filesMatching(s, rp, ref, IsTestPath) {
			if !strings.Contains(f.Content, base) {
				continue
			}
			for i, ln := range strings.Split(f.Content, "\n") {
				if !word.MatchString(ln) {
					continue
				}
				in := enclosing(f.Symbols, i+1)
				k := f.Path + "\x00" + in
				if seen[k] {
					continue
				}
				seen[k] = true
				found = append(found, TestRef{Repo: rp, Path: f.Path, Line: i + 1, In: in})
			}
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].Repo != found[j].Repo {
			return found[i].Repo < found[j].Repo
		}
		if found[i].Path != found[j].Path {
			return found[i].Path < found[j].Path
		}
		return found[i].Line < found[j].Line
	})
	return append(out, found...)
}

var (
	reImportGo   = regexp.MustCompile(`^\s*(?:import\s+)?(?:\w+\s+)?"([^"]+)"\s*$`)
	reImportPy   = regexp.MustCompile(`^\s*(?:from\s+([\w.]+)\s+import\b|import\s+([\w.]+))`)
	reImportJS   = regexp.MustCompile(`^\s*(?:import\b[^'"]*?from\s+|import\s+|export\b[^'"]*?from\s+)['"]([^'"]+)['"]|\brequire\(\s*['"]([^'"]+)['"]\s*\)`)
	reImportC    = regexp.MustCompile(`^\s*#\s*include\s+[<"]([^>"]+)[>"]`)
	reImportRust = regexp.MustCompile(`^\s*(?:pub\s+)?use\s+([\w:]+)`)
	reImportJava = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([\w.]+)`)
)

// fileImports lists a file's imports as written, by language.
func fileImports(content, lang string) []string {
	var out []string
	seen := map[string]bool{}
	inGoBlock := false
	for _, ln := range strings.Split(content, "\n") {
		var m []string
		switch lang {
		case "go":
			t := strings.TrimSpace(ln)
			if strings.HasPrefix(t, "import (") {
				inGoBlock = true
				continue
			}
			if inGoBlock && t == ")" {
				inGoBlock = false
				continue
			}
			if inGoBlock || strings.HasPrefix(t, "import ") {
				m = reImportGo.FindStringSubmatch(strings.TrimPrefix(t, "import "))
			}
		case "python":
			m = reImportPy.FindStringSubmatch(ln)
		case "javascript", "typescript":
			m = reImportJS.FindStringSubmatch(ln)
		case "c", "cpp":
			m = reImportC.FindStringSubmatch(ln)
		case "rust":
			m = reImportRust.FindStringSubmatch(ln)
		case "java":
			m = reImportJava.FindStringSubmatch(ln)
		}
		if m == nil {
			continue
		}
		for _, g := range m[1:] {
			if g != "" && !seen[g] {
				seen[g] = true
				out = append(out, g)
			}
		}
	}
	return out
}

// fileDoc is a file's own leading documentation: a Python module docstring,
// a Go package comment, a JS/TS/C++ @file block or a Rust //! block.
func fileDoc(content, lang string) string {
	lines := strings.Split(content, "\n")
	switch lang {
	case "python":
		for k, ln := range lines {
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			d, _ := docstringAt(lines, k)
			return firstSentence(d)
		}
	case "go":
		if loc := reGoPkgDoc.FindStringIndex(content); loc != nil {
			start := lineOf(content, loc[0])
			var block []string
			for k := start - 1; k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "//"); k++ {
				block = append(block, lines[k])
			}
			return firstSentence(cleanDoc(block))
		}
		// A file comment above `package`.
		for k, ln := range lines {
			if strings.HasPrefix(ln, "package ") {
				d, _ := commentAbove(lines, k-1)
				if !strings.Contains(strings.ToLower(d), "copyright") && !strings.HasPrefix(d, "go:build") {
					return firstSentence(d)
				}
				break
			}
		}
	case "rust":
		var block []string
		for _, m := range reRustInner.FindAllStringSubmatch(content, -1) {
			block = append(block, m[1])
		}
		return firstSentence(strings.Join(block, " "))
	default:
		if m := reJSFileDoc.FindStringSubmatch(content); m != nil {
			if t := regexp.MustCompile(`@(?:file|fileoverview|brief|module)\b\s*(.*)`).FindStringSubmatch(m[1]); t != nil {
				return firstSentence(t[1])
			}
		}
	}
	return ""
}

func explainFile(s Store, repo, ref, path string, files []File, idx *spanIndex, budget int, res *ExplainResult) *ExplainFile {
	f := idx.byPath[path]
	lang := langOfPath(path)
	ef := &ExplainFile{Repo: repo, Path: path, Lang: lang, Lines: strings.Count(f.Content, "\n") + 1}
	if IsTestPath(path) {
		ef.Role = append(ef.Role, "test")
	}
	if k := ProjectFileKind(path); k != "" {
		ef.Role = append(ef.Role, "project file: "+k)
	}
	ef.Purpose = fileDoc(f.Content, lang)
	ef.Imports = fileImports(f.Content, lang)
	for _, e := range EntryPoints(repo, []File{*f}) {
		ef.EntryPoints = append(ef.EntryPoints, e)
		ef.Role = append(ef.Role, "entry point: "+e.Kind)
	}
	for _, r := range RoutesWith(s, repo, ref, "", 1<<30, RoutesOptions{}).Links {
		if r.Route.Path == path {
			ef.Routes = append(ef.Routes, r.Route)
		}
	}
	if len(ef.Routes) > 0 {
		ef.Role = append(ef.Role, fmt.Sprintf("serves %d HTTP routes", len(ef.Routes)))
	}
	eps, _ := scanComms(repo, path, f.Content, f.Symbols)
	ef.Topics = eps
	if len(eps) > 0 {
		ef.Role = append(ef.Role, fmt.Sprintf("%d ROS endpoints", len(eps)))
	}

	// Symbols defined here, and file-level links through the call graph.
	syms := s.SymbolsAt(repo, ref)
	snap := s.Snapshot(repo, ref)
	fileOf := map[string]string{}
	for q := range syms {
		if l, ok := idx.locate(q); ok {
			fileOf[q] = l.Path
		}
	}
	inDeg := map[string]int{}
	inbound := map[string]*FileLink{}
	outbound := map[string]*FileLink{}
	link := func(m map[string]*FileLink, p, ex string) {
		l := m[p]
		if l == nil {
			l = &FileLink{Repo: repo, Path: p}
			m[p] = l
		}
		l.Calls++
		if len(l.Examples) < 2 && !containsStr(l.Examples, ex) {
			l.Examples = append(l.Examples, ex)
		}
	}
	for caller, cs := range snap.Callees {
		cp := fileOf[caller]
		for _, c := range cs {
			tp, ok := fileOf[c.Name]
			if !ok {
				continue
			}
			if tp == path {
				inDeg[c.Name]++
			}
			switch {
			case tp == path && cp != path && cp != "":
				link(inbound, cp, baseName(caller)+" → "+baseName(c.Name))
			case cp == path && tp != path:
				link(outbound, tp, baseName(caller)+" → "+baseName(c.Name))
			}
		}
	}
	var fsyms []FileSymbol
	for q, p := range fileOf {
		if p != path {
			continue
		}
		l, _ := idx.locate(q)
		_, local := idx.splitQID(q)
		facts := sourceFacts(f.Content, lang, l.Span.StartLine, l.Span.EndLine, strings.Contains(local, "."))
		fsyms = append(fsyms, FileSymbol{Symbol: q, Kind: facts.Kind, Line: l.Span.StartLine, EndLine: l.Span.EndLine, Signature: facts.Signature, In: inDeg[q], Out: len(snap.Callees[q])})
	}
	sort.Slice(fsyms, func(i, j int) bool {
		if fsyms[i].Line != fsyms[j].Line {
			return fsyms[i].Line < fsyms[j].Line
		}
		return fsyms[i].Symbol < fsyms[j].Symbol
	})
	spent := estTokens(ef.Purpose+strings.Join(ef.Imports, " ")) + 40
	for _, fs := range fsyms {
		c := estTokens(fs.Symbol+fs.Signature) + 6
		if spent+c > budget*2/3 {
			ef.MoreSymbols++
			continue
		}
		spent += c
		ef.Symbols = append(ef.Symbols, fs)
	}
	if ef.MoreSymbols > 0 {
		res.Omitted = append(res.Omitted, fmt.Sprintf("symbols(+%d)", ef.MoreSymbols))
	}
	ef.Inbound = rankLinks(inbound, budget-spent, res, "inbound")
	ef.Outbound = rankLinks(outbound, budget-spent-estLinks(ef.Inbound), res, "outbound")
	if len(res.Omitted) > 0 {
		res.Truncated = true
	}
	return ef
}

func rankLinks(m map[string]*FileLink, budget int, res *ExplainResult, name string) []FileLink {
	var out []FileLink
	for _, l := range m {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Calls != out[j].Calls {
			return out[i].Calls > out[j].Calls
		}
		return out[i].Path < out[j].Path
	})
	spent := 0
	for i, l := range out {
		c := estTokens(l.Path+strings.Join(l.Examples, " ")) + 4
		if spent+c > budget || i >= 15 {
			res.Omitted = append(res.Omitted, fmt.Sprintf("%s(+%d files)", name, len(out)-i))
			return out[:i]
		}
		spent += c
	}
	return out
}

func estLinks(ls []FileLink) int {
	n := 0
	for _, l := range ls {
		n += estTokens(l.Path+strings.Join(l.Examples, " ")) + 4
	}
	return n
}

func explainDir(s Store, repo, ref, dir string, files []File, budget int, res *ExplainResult) *ExplainDir {
	ed := &ExplainDir{Repo: repo, Path: dir}
	if dir == "" {
		ed.Path = "."
	}
	var inDir []*File
	var under []File
	for i := range files {
		f := &files[i]
		if dir == "" || strings.HasPrefix(f.Path, dir+"/") {
			under = append(under, *f)
			if pkgFromPath(f.Path) == dir {
				inDir = append(inDir, f)
			}
		}
	}
	ed.Purpose, ed.Source = modulePurpose(dir, inDir)
	if ed.Purpose == "" && dir == "" {
		ed.Purpose, ed.Source = readmeSummary(files)
	}
	var dfs []DirFile
	for _, f := range under {
		l := langOfPath(f.Path)
		if !isCodeLang(l) && ProjectFileKind(f.Path) == "" {
			continue
		}
		dfs = append(dfs, DirFile{Path: f.Path, Lang: l, Symbols: len(f.Symbols)})
	}
	sort.Slice(dfs, func(i, j int) bool {
		if dfs[i].Symbols != dfs[j].Symbols {
			return dfs[i].Symbols > dfs[j].Symbols
		}
		return dfs[i].Path < dfs[j].Path
	})
	spent := estTokens(ed.Purpose) + 30
	for _, d := range dfs {
		c := estTokens(d.Path) + 5
		if spent+c > budget*2/3 {
			ed.MoreFiles++
			continue
		}
		spent += c
		ed.Files = append(ed.Files, d)
	}
	if ed.MoreFiles > 0 {
		res.Omitted = append(res.Omitted, fmt.Sprintf("files(+%d)", ed.MoreFiles))
		res.Truncated = true
	}
	idx := newSpanIndex(files)
	all := s.SymbolsAt(repo, ref)
	syms := map[string]SymbolRef{}
	for q, r := range all {
		if l, ok := idx.locate(q); ok && (dir == "" || strings.HasPrefix(l.Path, dir+"/")) {
			syms[q] = r
		}
	}
	for _, h := range hubsIn(syms, all, s.Snapshot(repo, ref), idx) {
		c := estTokens(h.Symbol+h.Path) + 8
		if spent+c > budget || len(ed.Hubs) >= 10 {
			break
		}
		spent += c
		ed.Hubs = append(ed.Hubs, h)
	}
	return ed
}

// explainRoute explains "METHOD /path": the matching server routes with
// their handler symbol, and every client call that reaches them.
func explainRoute(s Store, ref, method, p string, opt ExplainOptions, budget int, res ExplainResult) ExplainResult {
	res.Kind = "route"
	scope := scopeOf(opt)
	res.Meta = NewMeta(s, scope, ref)
	rr := RoutesWith(s, scope, ref, p, 1<<30, RoutesOptions{})
	want := segments(normURL(p))
	spent := 40
	for _, l := range rr.Links {
		if method != "" && method != "ANY" && method != "*" && l.Route.Method != "*" && !strings.EqualFold(l.Route.Method, method) {
			continue
		}
		rs := segments(l.Route.Pattern)
		if matchSegs(want, rs) == 0 && matchSegs(rs, want) == 0 {
			continue
		}
		er := ExplainRoute{Route: l.Route}
		if h := handlerSymbol(s, ref, l.Route); h != "" {
			files := s.Files(l.Route.Repo, ref)
			tmp := ExplainResult{}
			hs := explainSymbol(s, l.Route.Repo, ref, h, files, newSpanIndex(files), budget/3, &tmp)
			hs.Body, hs.Callers, hs.Tests, hs.ReachedBy = firstLines(hs.Body, 12), nil, nil, nil
			er.Handler = hs
		}
		c := estTokens(l.Route.Pattern+l.Route.Handler) + 20
		if er.Handler != nil {
			c += estTokens(er.Handler.Signature + er.Handler.Doc + er.Handler.Body)
		}
		if spent+c > budget {
			res.Omitted = append(res.Omitted, "routes(budget)")
			break
		}
		spent += c
		for _, cl := range l.Clients {
			cc := estTokens(cl.Path+cl.Text+cl.In) + 6
			if spent+cc > budget {
				res.Omitted = append(res.Omitted, fmt.Sprintf("clients of %s(budget)", l.Route.Pattern))
				break
			}
			spent += cc
			er.Clients = append(er.Clients, cl)
		}
		res.Routes = append(res.Routes, er)
	}
	for _, c := range rr.Unmatched {
		if len(res.Unmatched) >= 10 {
			break
		}
		res.Unmatched = append(res.Unmatched, c)
	}
	if len(res.Routes) == 0 {
		res.Note = fmt.Sprintf("no indexed server route matches %s %s (%d routes, %d client calls read)", method, p, rr.Routes, rr.Clients)
		res.Next = []string{"reponite routes " + p + "   (routes and calls whose path contains it)"}
	} else {
		res.Note = rr.Note
		res.Next = []string{"reponite routes " + p + "   (the full route ↔ client map for this path)"}
		if res.Routes[0].Handler != nil {
			res.Next = append(res.Next, "reponite explain "+res.Routes[0].Handler.Symbol+" --repo "+res.Routes[0].Route.Repo+"   (the handler in depth)")
		}
	}
	if len(res.Omitted) > 0 {
		res.Truncated = true
	}
	return res
}

// handlerSymbol resolves a route's handler to a symbol in its repo, preferring
// one defined in the route's own file's package.
func handlerSymbol(s Store, ref string, r Route) string {
	h := strings.TrimSuffix(strings.Split(r.Handler, "(")[0], ".as_view")
	if h == "" {
		// Frameworks whose route line names the handler as an argument.
		line := lineAtFile(s, r.Repo, ref, r.Path, r.Line)
		if m := regexp.MustCompile(`,\s*([A-Za-z_][\w.]*)\s*\)?\s*;?\s*$`).FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			h = m[1]
		}
		if h == "" {
			// A decorated function directly below the route line.
			for k := 1; k <= 3; k++ {
				if m := rePyDefName.FindStringSubmatch(lineAtFile(s, r.Repo, ref, r.Path, r.Line+k)); m != nil {
					h = m[1]
					break
				}
			}
		}
	}
	if h == "" {
		return ""
	}
	names := ResolveSymbol(s, r.Repo, ref, h)
	if len(names) == 0 && strings.Contains(h, ".") {
		names = ResolveSymbol(s, r.Repo, ref, lastSegDot(h))
	}
	dir := filepath.ToSlash(filepath.Dir(r.Path))
	for _, n := range names {
		if strings.HasPrefix(n, dir+".") {
			return n
		}
	}
	if len(names) > 0 {
		return names[0]
	}
	return ""
}

// repoHasComms reports whether any source file of repo uses a ROS
// communication idiom.
func repoHasComms(s Store, repo, ref string) bool {
	for _, f := range s.Files(repo, ref) {
		if commLang(f.Path) == "" || !mayHaveComms(f.Content) {
			continue
		}
		if eps, un := scanComms(repo, f.Path, f.Content, f.Symbols); len(eps) > 0 || un > 0 {
			return true
		}
	}
	return false
}
