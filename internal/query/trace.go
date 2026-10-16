// trace.go implements reponite_trace — "what happens when …" as one compact
// tree. Starting from an entry (a symbol in any spelling reponite resolves, an
// HTTP route, a file:line call site, a CLI command, or a ROS topic) it walks the
// resolved call graph DOWN (what this sets off) or UP (how execution gets
// here), crossing the boundaries reponite already links: a client call to the
// server route handler that serves it (routes.go), a publish to the
// subscribers' callbacks (roscomms.go), a call through a base class to its
// implementations (impls.go / xdispatch.go), and an import of another indexed
// repo's module. Each node carries the side effects its body performs (HTTP
// out, DB/ORM access, publish/emit, file writes, subprocesses, log lines,
// raised errors — trace_effects.go), every edge its resolution provenance and
// confidence (invariant 5), and the walk says where and why it stopped
// (depth, budget, cycle, an already-expanded subtree, an ambiguous call).
// Breadth-first, so a budget cut keeps the shallow, most explanatory levels.
// Pure over Store (ADR-018), tested in-sandbox against storage.Mem.
package query

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Defaults for a trace when the caller passes <= 0.
const (
	DefaultTraceDepth  = 5
	DefaultTraceBudget = 2000

	traceMaxChildren  = 12 // children shown per node before "+N more"
	traceMaxFanout    = 6  // implementations shown per interface method
	traceMaxEffects   = 8  // side effects shown per node
	traceMaxExternals = 8  // external callees listed per node
)

// Edge kinds: how a node is reached from its parent.
const (
	EdgeCall      = "call"         // a resolved CALLS edge
	EdgeHTTP      = "http"         // client call -> server route (DOWN) / handler -> route (UP)
	EdgeHandler   = "handler"      // route -> the code that serves it
	EdgeTopic     = "topic"        // publish -> topic (DOWN) / callback -> topic (UP)
	EdgeCallback  = "callback"     // topic -> a subscriber's callback
	EdgeImpl      = "impl"         // interface/base method -> an implementation
	EdgeFramework = "framework"    // a framework-dispatched hook (DRF perform_create, …)
	EdgeSuper     = "super"        // super().m(): the inherited implementation
	EdgeCaller    = "caller"       // UP: a function calling this one
	EdgeClient    = "client"       // UP: route <- the client call site
	EdgePublisher = "publisher"    // UP: topic <- the code publishing it
	EdgeCLI       = "cli"          // CLI command -> its handler
	EdgeXRepo     = "xrepo-import" // a call into (or from) another indexed repo's module
)

// Stop reasons: why a node was not expanded further.
const (
	StopDepth      = "depth"      // --depth reached; More = children not walked
	StopBudget     = "budget"     // the token budget ran out; More = children not walked
	StopCycle      = "cycle"      // the node is already on the path above it
	StopRepeat     = "repeat"     // expanded elsewhere in the tree (Same = that node's handle)
	StopAmbiguous  = "ambiguous"  // several definitions match the call; Alt lists them
	StopUnresolved = "unresolved" // the target could not be located in any indexed repo
	StopFramework  = "framework"  // dispatch inside a framework this trace does not model
)

// Node kinds.
const (
	NodeSymbol    = "symbol"
	NodeRoute     = "route"
	NodeTopic     = "topic"
	NodeFramework = "framework" // behavior a framework supplies (an inherited DRF create)
	NodeInline    = "inline"    // an anonymous handler/callback (lambda, arrow function)
	NodeCLI       = "cli"
	NodeSite      = "site" // UP: a call site whose enclosing function is unknown
)

// TraceEffect is one side effect a node's body performs.
type TraceEffect struct {
	Kind   string // http | db | publish | file | exec | log | raise
	Op     string // db: read/write/sql/tx; http: the method; log: the level call
	Line   int
	Text   string // compact, as written (≤ ~100 chars)
	Target string // the linked route / topic, when one is known
}

// TraceNode is one step of the walk.
type TraceNode struct {
	Kind   string
	Repo   string
	Symbol string // the drill-in handle (qualified id) for brief/context/trace
	Label  string // what a non-symbol node is ("POST v1/orders/", "topic scan")
	Path   string
	Line   int
	// Edge is how this node is reached from its parent; Method the resolution
	// method of that edge (a CALLS edge's resolution_method, or the crossing's
	// rule) and Confidence its confidence.
	Edge       string
	Method     string
	Confidence float64
	Note       string
	Effects    []TraceEffect
	MoreEffect int      // effects beyond those shown
	External   []string // external callees (library calls), noise skipped
	Children   []*TraceNode
	Stop       string   // why the walk stopped here ("" = expanded, or a leaf)
	More       int      // children not walked (depth/budget/fan-out cut)
	Same       string   // StopRepeat: the handle of the node expanded elsewhere
	Alt        []string // StopAmbiguous: the candidate definitions

	parent *TraceNode
	key    string
	lo, hi int // body line range (for effects and crossings)
	fi     int // file index in its repo view, -1 = unknown
	route  *Route
	verb   string // the HTTP method asked for at a route node
	group  *CommGroup
	fw     *fwInfo
	pre    []*TraceNode // children resolved with the node (a CLI command's handlers)
}

// TraceOptions tunes a trace.
type TraceOptions struct {
	Direction    string // "down" (default) or "up"
	Depth        int    // levels below the entry (0 = DefaultTraceDepth)
	Budget       int    // tokens of the text rendering (0 = DefaultTraceBudget)
	IncludeTests bool
	// Repo prefers this repo when a symbol entry resolves in several.
	Repo string
	// Skip are extra handle/module prefixes treated as library noise, on top
	// of DefaultTraceSkip.
	Skip []string
	// Routes carries the gateway hops between clients and servers.
	Routes RoutesOptions
}

// TraceResult is the walked tree.
type TraceResult struct {
	Entry     string
	EntryKind string // symbol | route | topic | cli | site
	Direction string
	Depth     int
	Budget    int
	Roots     []*TraceNode
	Nodes     int
	Tokens    int // estimated tokens of the text rendering
	Truncated bool
	// Stops counts the nodes the walk stopped at, by reason.
	Stops map[string]int
	// Next is a concrete follow-up when the walk was cut.
	Next string
	Note string
	Meta Meta
}

// DefaultTraceSkip are the module/handle prefixes of standard and common
// third-party libraries: listed by name, never walked or ranked.
var DefaultTraceSkip = []string{
	"std::", "std.", "boost::", "ros::", "rclcpp::", "rospy", "rclpy", "tf2",
	"os", "sys", "json", "re", "time", "datetime", "math", "typing", "collections", "itertools",
	"functools", "copy", "uuid", "logging", "pathlib", "asyncio", "dataclasses", "enum", "abc",
	"fmt", "strings", "strconv", "errors", "sort", "sync", "context", "bytes", "io", "net/http",
	"react", "react-dom", "lodash", "Math", "JSON", "Object", "Array", "Promise", "console", "Number", "String", "Date",
	"java.lang", "java.util", "javax", "org.slf4j",
}

// traceNoise are bare external callee names too generic to tell a reader
// anything (container/string methods and builtins).
var traceNoise = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`get set first last all append extend push pop map filter reduce forEach find
		len str int float bool list dict tuple print format join split strip lower upper replace items keys values update
		add remove insert index count sorted sum min max any range enumerate zip isinstance hasattr getattr setattr
		then catch finally resolve reject toString valueOf includes indexOf slice splice concat trim parseInt parseFloat
		size empty begin end at front back clear emplace_back push_back make_shared make_unique c_str data
		super __init__ is_valid exists none NotImplemented`) {
		traceNoise[n] = true
	}
}

var (
	traceVerbEntry = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|ANY|\*)\s+(\S+)$`)
	traceSiteEntry = regexp.MustCompile(`^(\S+\.\w+):(\d+)$`)
)

// Trace walks from entry at ref. The entry kind is chosen by its spelling: a
// "route:"/"topic:"/"cli:"/"symbol:" prefix forces one; "VERB /path" is an
// HTTP route; "path/file.ext:42" a call site; otherwise a symbol, then a ROS
// topic, then a route path, then a CLI command — the first that resolves.
func Trace(s Store, entry, ref string, opt TraceOptions) TraceResult {
	if opt.Direction != "up" {
		opt.Direction = "down"
	}
	if opt.Depth <= 0 {
		opt.Depth = DefaultTraceDepth
	}
	if opt.Budget <= 0 {
		opt.Budget = DefaultTraceBudget
	}
	t := newTracer(s, ref, opt)
	res := TraceResult{Entry: entry, Direction: opt.Direction, Depth: opt.Depth, Budget: opt.Budget,
		Stops: map[string]int{}, Meta: NewMeta(s, FleetRepo, ref)}
	if opt.Repo != "" {
		res.Meta = NewMeta(s, opt.Repo, ref)
	}
	roots, kind, warn := t.resolveEntry(strings.TrimSpace(entry))
	res.EntryKind = kind
	if warn != "" {
		res.Meta.Warnings = append(res.Meta.Warnings, warn)
	}
	if len(roots) == 0 {
		res.Note = t.miss(entry)
		return res
	}
	t.walk(roots)
	res.Roots = roots
	res.Nodes, res.Tokens, res.Truncated, res.Stops = t.nodes, t.spent, t.truncated, t.stops
	res.Next = t.next(entry, res)
	res.Note = t.note(res)
	return res
}

// tracer holds the per-trace caches: one view per repo, and the fleet-wide
// route, topic and class indexes built only when a crossing needs them.
type tracer struct {
	s         Store
	ref       string
	opt       TraceOptions
	skip      []string
	views     map[string]*repoView
	routes    *routeIndex
	comms     *commIndex
	classes   *classGraph
	expanded  map[string]*TraceNode
	annotated map[string]bool
	stops     map[string]int
	nodes     int
	spent     int
	truncated bool
	exhausted bool
	cut       []*TraceNode
}

func newTracer(s Store, ref string, opt TraceOptions) *tracer {
	return &tracer{s: s, ref: ref, opt: opt, skip: append(append([]string{}, DefaultTraceSkip...), opt.Skip...),
		views: map[string]*repoView{}, expanded: map[string]*TraceNode{}, annotated: map[string]bool{}, stops: map[string]int{}}
}

// --- per-repo view ---------------------------------------------------------

type symLoc struct {
	file int
	span SymbolSpan
}

// repoView is one repo at the trace's ref: its files, symbols, call graph and
// where each symbol is defined.
type repoView struct {
	repo    string
	files   []File
	lines   map[int][]string
	syms    map[string]SymbolRef
	snap    *RefSnapshot
	loc     map[string]symLoc
	inFile  map[int]map[string][]string // file -> span name -> qids defined there
	byBase  map[string][]string
	callers map[string][]Callee // callee qid -> its callers (Name = caller)
}

func (t *tracer) view(repo string) *repoView {
	if v, ok := t.views[repo]; ok {
		return v
	}
	v := &repoView{repo: repo, files: t.s.Files(repo, t.ref), syms: t.s.SymbolsAt(repo, t.ref), lines: map[int][]string{}}
	v.byBase = map[string][]string{}
	for q := range v.syms {
		v.byBase[baseName(q)] = append(v.byBase[baseName(q)], q)
	}
	for _, qs := range v.byBase {
		sort.Strings(qs)
	}
	t.views[repo] = v
	return v
}

func (v *repoView) snapshot(s Store, ref string) *RefSnapshot {
	if v.snap == nil {
		sn := s.Snapshot(v.repo, ref)
		v.snap = &sn
	}
	return v.snap
}

func (v *repoView) fileLines(i int) []string {
	if i < 0 || i >= len(v.files) {
		return nil
	}
	if l, ok := v.lines[i]; ok {
		return l
	}
	l := strings.Split(v.files[i].Content, "\n")
	v.lines[i] = l
	return l
}

func (v *repoView) fileIndex(path string) int {
	for i, f := range v.files {
		if f.Path == path {
			return i
		}
	}
	return -1
}

var cppScopedDef = regexp.MustCompile(`(\w+)\s*::\s*~?(\w+)\s*\(`)

// locate maps every symbol to its defining span by rebuilding the indexer's
// id from the span nesting: pkg + "." + enclosing span names + name. A C++
// out-of-class definition (void Class::m() {…}) is not nested in its class
// span, so its class is read from the definition line.
func (v *repoView) locate() {
	if v.loc != nil {
		return
	}
	v.loc = map[string]symLoc{}
	v.inFile = map[int]map[string][]string{}
	for i, f := range v.files {
		pkg := pkgFromPath(f.Path)
		for _, sp := range f.Symbols {
			var chain []SymbolSpan
			for _, o := range f.Symbols {
				if o != sp && o.StartLine <= sp.StartLine && o.EndLine >= sp.EndLine && (o.EndLine-o.StartLine > sp.EndLine-sp.StartLine || o.StartLine < sp.StartLine) {
					chain = append(chain, o)
				}
			}
			sort.Slice(chain, func(a, b int) bool {
				return chain[a].EndLine-chain[a].StartLine > chain[b].EndLine-chain[b].StartLine
			})
			names := make([]string, 0, len(chain)+1)
			for _, c := range chain {
				names = append(names, c.Name)
			}
			names = append(names, sp.Name)
			cands := []string{qualifyID(pkg, strings.Join(names, "."))}
			if len(names) > 1 {
				cands = append(cands, qualifyID(pkg, sp.Name))
			}
			if lines := v.fileLines(i); sp.StartLine-1 < len(lines) && sp.StartLine > 0 {
				if m := cppScopedDef.FindStringSubmatch(lines[sp.StartLine-1]); m != nil && m[2] == sp.Name {
					cands = append([]string{qualifyID(pkg, m[1]+"."+sp.Name)}, cands...)
				}
			}
			for _, q := range cands {
				if _, ok := v.syms[q]; !ok {
					continue
				}
				if _, dup := v.loc[q]; !dup {
					v.loc[q] = symLoc{file: i, span: sp}
				}
				if v.inFile[i] == nil {
					v.inFile[i] = map[string][]string{}
				}
				v.inFile[i][sp.Name] = append(v.inFile[i][sp.Name], q)
				break
			}
		}
	}
}

func qualifyID(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// where returns a symbol's file index and span.
func (v *repoView) where(qid string) (symLoc, bool) {
	v.locate()
	l, ok := v.loc[qid]
	return l, ok
}

// qidIn is the symbol named name defined in file i ("" when none).
func (v *repoView) qidIn(i int, name string) string {
	v.locate()
	if qs := v.inFile[i][name]; len(qs) > 0 {
		return qs[0]
	}
	return ""
}

// qidAtLine is the innermost symbol whose span holds line in file i.
func (v *repoView) qidAtLine(i, line int) string {
	if i < 0 {
		return ""
	}
	if name := enclosing(v.files[i].Symbols, line); name != "" {
		return v.qidIn(i, name)
	}
	return ""
}

// reverse is the callers index (callee -> callers), built once.
func (v *repoView) reverse(s Store, ref string) map[string][]Callee {
	if v.callers != nil {
		return v.callers
	}
	v.callers = map[string][]Callee{}
	for caller, cs := range v.snapshot(s, ref).Callees {
		for _, c := range cs {
			v.callers[c.Name] = append(v.callers[c.Name], Callee{Name: caller, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence})
		}
	}
	for k := range v.callers {
		sort.Slice(v.callers[k], func(i, j int) bool { return v.callers[k][i].Name < v.callers[k][j].Name })
	}
	return v.callers
}

// --- nodes -----------------------------------------------------------------

// symbolNode builds the node for qid in repo, with its location and body range.
func (t *tracer) symbolNode(repo, qid, edge, method string, conf float64) *TraceNode {
	n := &TraceNode{Kind: NodeSymbol, Repo: repo, Symbol: qid, Edge: edge, Method: method, Confidence: conf,
		key: repo + "\x00" + qid, fi: -1}
	v := t.view(repo)
	if l, ok := v.where(qid); ok {
		n.Path, n.Line, n.fi = v.files[l.file].Path, l.span.StartLine, l.file
		n.lo, n.hi = l.span.StartLine, l.span.EndLine
	}
	return n
}

func (t *tracer) isTest(repo, qid, path string) bool {
	if t.opt.IncludeTests {
		return false
	}
	return IsTestQID(t.s, repo, t.ref, qid) || (path != "" && testCode(path, ""))
}

func (t *tracer) skipped(name string) bool {
	for _, p := range t.skip {
		if p == "" {
			continue
		}
		if name == p || strings.HasPrefix(name, p) && !isIdentByte(name[len(p)-1]) ||
			strings.HasPrefix(name, p) && len(name) > len(p) && !isIdentByte(name[len(p)]) {
			return true
		}
	}
	return false
}

func isIdentByte(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// --- the walk --------------------------------------------------------------

type traceItem struct {
	n     *TraceNode
	depth int
}

// walk expands the roots breadth-first: shallow levels first, so a budget cut
// drops the deepest detail rather than a whole sibling branch.
func (t *tracer) walk(roots []*TraceNode) {
	queue := make([]traceItem, 0, len(roots))
	for _, r := range roots {
		t.annotate(r)
		t.spent += estTokens(nodeLine(r, "")) + effectTokens(r)
		t.nodes++
		queue = append(queue, traceItem{r, 0})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		n := it.n
		if n.Stop != "" {
			continue
		}
		if first, ok := t.expanded[n.key]; ok && first != n {
			n.Stop, n.Same = StopRepeat, first.handle()
			t.stops[StopRepeat]++
			continue
		}
		t.expanded[n.key] = n
		kids := t.children(n)
		if len(kids) == 0 {
			continue
		}
		if it.depth >= t.opt.Depth {
			t.cutAt(n, StopDepth, len(kids))
			continue
		}
		if t.exhausted {
			t.cutAt(n, StopBudget, len(kids))
			continue
		}
		for i, k := range kids {
			if i >= traceMaxChildren {
				n.More += len(kids) - i
				break
			}
			k.parent = n
			if k.key != "" && n.onPath(k.key) {
				k.Stop = StopCycle
				t.stops[StopCycle]++
			}
			t.annotate(k)
			cost := estTokens(nodeLine(k, n.Repo)) + effectTokens(k)
			if t.spent+cost > t.opt.Budget {
				t.exhausted, t.truncated = true, true
				t.cutAt(n, StopBudget, len(kids)-i)
				break
			}
			t.spent += cost
			t.nodes++
			n.Children = append(n.Children, k)
			if k.Stop == "" {
				queue = append(queue, traceItem{k, it.depth + 1})
			} else if k.Stop != StopCycle {
				t.stops[k.Stop]++
			}
		}
	}
}

func (t *tracer) cutAt(n *TraceNode, why string, more int) {
	n.Stop, n.More = why, n.More+more
	t.stops[why]++
	if why == StopBudget {
		t.truncated = true
	}
	t.cut = append(t.cut, n)
}

func (n *TraceNode) onPath(key string) bool {
	for p := n; p != nil; p = p.parent {
		if p.key == key {
			return true
		}
	}
	return false
}

func (n *TraceNode) handle() string {
	if n.Symbol != "" {
		return n.Symbol
	}
	return n.Label
}

// annotate attaches a node's side effects and external callees.
func (t *tracer) annotate(n *TraceNode) {
	if n.Stop == StopCycle || n.Stop == StopRepeat || n.fi < 0 || n.lo <= 0 {
		return
	}
	if t.annotated[n.key] {
		return // the first occurrence carries them
	}
	t.annotated[n.key] = true
	v := t.view(n.Repo)
	effs := t.effectsIn(v, n.fi, n.lo, n.hi)
	if len(effs) > traceMaxEffects {
		n.MoreEffect = len(effs) - traceMaxEffects
		effs = effs[:traceMaxEffects]
	}
	n.Effects = append(n.Effects, effs...)
	if n.Kind != NodeSymbol || t.opt.Direction == "up" {
		return
	}
	for _, c := range v.snapshot(t.s, t.ref).Callees[n.Symbol] {
		if c.ResolutionMethod != "unresolved-external" {
			continue
		}
		name := c.Name
		bare := !strings.Contains(name, "::") && !strings.Contains(name, ".")
		if bare && effectsMention(n.Effects, name) {
			continue // already reported as the effect it is (an ORM or HTTP client method)
		}
		if bare && traceNoise[name] || t.skipped(strings.ReplaceAll(name, "::", ".")) || t.skipped(name) {
			continue
		}
		if len(n.External) >= traceMaxExternals {
			n.External = append(n.External, "…")
			break
		}
		n.External = append(n.External, name)
	}
}

func effectsMention(effs []TraceEffect, name string) bool {
	for _, e := range effs {
		if strings.Contains(e.Text, "."+name+"(") || strings.Contains(e.Text, "."+name+" ") || e.Op == strings.ToUpper(name) {
			return true
		}
	}
	return false
}

// children are the next nodes from n in the trace's direction, ranked:
// in-repo first, then cross-repo, then framework/low-confidence guesses;
// within a tier in the order they appear in n's body.
func (t *tracer) children(n *TraceNode) []*TraceNode {
	var kids []*TraceNode
	switch {
	case t.opt.Direction == "up":
		kids = t.upChildren(n)
	case n.Kind == NodeRoute:
		kids = t.routeHandlers(n)
	case n.Kind == NodeTopic:
		kids = t.topicCallbacks(n)
	case n.Kind == NodeCLI:
		kids = t.cliHandlers(n)
	case n.Kind == NodeFramework:
		kids = t.frameworkChildren(n)
	case n.Kind == NodeSymbol || n.Kind == NodeInline:
		kids = t.downChildren(n)
	}
	t.rank(n, kids)
	return kids
}

func (t *tracer) rank(parent *TraceNode, kids []*TraceNode) {
	var body string
	if parent.fi >= 0 && parent.lo > 0 {
		lines := t.view(parent.Repo).fileLines(parent.fi)
		body = strings.Join(lines[min(parent.lo-1, len(lines)):min(parent.hi, len(lines))], "\n")
	}
	tier := func(k *TraceNode) int {
		switch {
		case k.Repo == parent.Repo && k.Confidence >= 0.7 && k.Edge != EdgeImpl:
			return 0
		case k.Repo != parent.Repo:
			return 1
		}
		return 2
	}
	pos := func(k *TraceNode) int {
		if k.Line > 0 && k.Kind != NodeSymbol {
			return 0
		}
		name := baseName(k.Symbol)
		if name == "" {
			return 1 << 30
		}
		if i := strings.Index(body, name+"("); i >= 0 {
			return i
		}
		if i := strings.Index(body, name); i >= 0 {
			return i
		}
		return 1 << 29
	}
	sort.SliceStable(kids, func(i, j int) bool {
		a, b := kids[i], kids[j]
		if ta, tb := tier(a), tier(b); ta != tb {
			return ta < tb
		}
		return pos(a) < pos(b)
	})
}

var superCall = regexp.MustCompile(`super\s*\([^)]*\)\s*\.\s*(\w+)\s*\(|super\.(\w+)\s*\(`)

// downChildren: n's callees (with ambiguity resolved where the body or the
// imports decide it), the implementations of n when it is an interface
// method, the routes its HTTP calls reach, the topics it publishes, and the
// modules of other indexed repos it calls into.
func (t *tracer) downChildren(n *TraceNode) []*TraceNode {
	var kids []*TraceNode
	seen := map[string]bool{}
	add := func(k *TraceNode) {
		if k == nil || seen[k.key] {
			return
		}
		seen[k.key] = true
		kids = append(kids, k)
	}
	v := t.view(n.Repo)
	body := t.bodyOf(n)
	if n.Kind == NodeSymbol {
		supers := map[string]bool{}
		for _, m := range superCall.FindAllStringSubmatch(body, -1) {
			supers[m[1]+m[2]] = true
		}
		for _, c := range v.snapshot(t.s, t.ref).Callees[n.Symbol] {
			switch {
			case c.Name == n.Symbol && supers[baseName(c.Name)]:
				add(t.superNode(n))
			case c.ResolutionMethod == "unresolved-external":
				add(t.importedFrom(n, c))
			case c.ResolutionMethod == "ambiguous" || !t.known(v, c.Name):
				add(t.disambiguate(n, v, body, c))
			default:
				if t.isTest(n.Repo, c.Name, "") || t.skipped(c.Name) || c.ResolutionMethod == "member-name-resolved" && ormOnly(body, baseName(c.Name)) {
					continue
				}
				add(t.symbolNode(n.Repo, c.Name, EdgeCall, c.ResolutionMethod, c.Confidence))
			}
		}
		for _, k := range t.implsOf(n) {
			add(k)
		}
	} else {
		for _, k := range t.inlineCalls(n, v, body) {
			add(k)
		}
	}
	for _, k := range t.httpOut(n) {
		add(k)
	}
	for _, k := range t.publishes(n) {
		add(k)
	}
	return kids
}

// ormOnly: every call of name in body is an ORM manager/queryset method
// (Model.objects….create()), so a member-name guess pinning it on a
// same-named method of the repo is wrong — the call is a DB effect.
func ormOnly(body, name string) bool {
	all := regexp.MustCompile(`\b`+regexp.QuoteMeta(name)+`\s*\(`).FindAllStringIndex(body, -1)
	orm := regexp.MustCompile(`\.objects(?:\.\w+\([^()]*\))*\.`+regexp.QuoteMeta(name)+`\s*\(`).FindAllStringIndex(body, -1)
	return len(all) > 0 && len(orm) == len(all)
}

func (t *tracer) known(v *repoView, qid string) bool {
	_, ok := v.syms[qid]
	return ok
}

func (t *tracer) bodyOf(n *TraceNode) string {
	if n.fi < 0 || n.lo <= 0 {
		return ""
	}
	lines := t.view(n.Repo).fileLines(n.fi)
	return strings.Join(lines[min(n.lo-1, len(lines)):min(n.hi, len(lines))], "\n")
}

// superNode is a super().m() call: the inherited implementation, which lives
// in a base class (often a framework's) rather than in this repo.
func (t *tracer) superNode(n *TraceNode) *TraceNode {
	m := baseName(n.Symbol)
	k := &TraceNode{Kind: NodeFramework, Repo: n.Repo, Label: "super()." + m, Edge: EdgeSuper, Method: "inherited",
		Confidence: 0.6, key: n.key + "\x00super", fi: -1, Stop: StopFramework}
	if cls, _, ok := classOf(n.Symbol); ok {
		g := t.classGraph()
		for _, b := range g.ancestors(cls) {
			for _, rp := range t.s.Repos() {
				if def := g.defOf(t.s, t.ref, rp, b, m); def != "" {
					sym := t.symbolNode(rp, def, EdgeSuper, "class-derivation", 0.75)
					return sym
				}
			}
		}
		k.Note = "inherited from a base class outside the indexed repos"
	}
	if strings.HasPrefix(m, "perform_") {
		model := ""
		if _, _, ok := classOf(n.Symbol); ok {
			model = t.classModel(t.view(n.Repo), strings.TrimSuffix(n.Symbol, "."+m))
		}
		if e, ok := inferredEffect(m, model, n.Line); ok {
			k.Effects = append(k.Effects, e)
		}
	}
	return k
}

var pyImportFrom = regexp.MustCompile(`(?m)^\s*from\s+([\w.]+)\s+import\s+\(?([^)\n]*(?:\n[^)\n]*)*?)\)?\s*$`)

// disambiguate picks the target of an ambiguous call when the caller's body
// or imports decide it: `label.reprint()` -> the class named like the
// receiver; `self.m()` -> the caller's own class; a from-import of the name ->
// the definition in that module. Otherwise the call stays an ambiguous leaf
// listing its candidates.
func (t *tracer) disambiguate(n *TraceNode, v *repoView, body string, c Callee) *TraceNode {
	name := baseName(c.Name)
	var cands []string
	for _, q := range v.byBase[name] {
		if q != n.Symbol && !t.isTest(n.Repo, q, "") {
			cands = append(cands, q)
		}
	}
	if q := c.Name; strings.Contains(q, ".") {
		if _, ok := v.syms[q]; ok {
			cands = []string{q}
		}
	}
	pick := func(q, why string) *TraceNode {
		k := t.symbolNode(n.Repo, q, EdgeCall, c.ResolutionMethod, c.Confidence)
		k.Note = why
		return k
	}
	switch len(cands) {
	case 0:
		if t.skipped(c.Name) || traceNoise[name] {
			return nil
		}
		return &TraceNode{Kind: NodeSymbol, Repo: n.Repo, Symbol: c.Name, Edge: EdgeCall, Method: c.ResolutionMethod,
			Confidence: c.Confidence, Stop: StopUnresolved, key: n.Repo + "\x00?" + c.Name, fi: -1}
	case 1:
		return pick(cands[0], "picked: the only definition")
	}
	// self.m() / this->m(): the caller's own class.
	if cls, _, ok := classOf(n.Symbol); ok && regexp.MustCompile(`\b(?:self|this|cls)\s*(?:\.|->)\s*`+regexp.QuoteMeta(name)+`\s*\(`).MatchString(body) {
		for _, q := range cands {
			if c2, _, ok := classOf(q); ok && c2 == cls {
				return pick(q, "picked: own class")
			}
		}
	}
	// recv.m(): a class named like the receiver (label -> Label, tote_type -> ToteType).
	for _, m := range regexp.MustCompile(`([A-Za-z_]\w*)\s*(?:\.|->)\s*`+regexp.QuoteMeta(name)+`\s*\(`).FindAllStringSubmatch(body, -1) {
		recv := strings.ToLower(strings.ReplaceAll(m[1], "_", ""))
		for _, q := range cands {
			if c2, _, ok := classOf(q); ok && strings.ToLower(c2) == recv {
				return pick(q, "picked: receiver "+m[1])
			}
		}
	}
	// from mod import name: the definition whose file is that module.
	if n.fi >= 0 {
		src := v.files[n.fi].Content
		for _, m := range pyImportFrom.FindAllStringSubmatch(src, -1) {
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`).MatchString(m[2]) {
				continue
			}
			mod := strings.ReplaceAll(m[1], ".", "/")
			for _, q := range cands {
				l, ok := v.where(q)
				if !ok {
					continue
				}
				p := strings.TrimSuffix(strings.TrimSuffix(v.files[l.file].Path, ".py"), "/__init__")
				if p == mod || strings.HasSuffix(p, "/"+mod) {
					return pick(q, "picked: import "+m[1])
				}
			}
		}
	}
	k := &TraceNode{Kind: NodeSymbol, Repo: n.Repo, Symbol: name, Edge: EdgeCall, Method: c.ResolutionMethod,
		Confidence: c.Confidence, Stop: StopAmbiguous, key: n.Repo + "\x00?" + n.Symbol + "\x00" + name, fi: -1}
	k.Alt = cands
	if len(k.Alt) > 4 {
		k.Alt = append(k.Alt[:4:4], fmt.Sprintf("+%d more", len(cands)-4))
	}
	return k
}

// importedFrom follows an external call into another indexed repo whose
// module root the call's module names (`mod::Name` from an import binding):
// a cross-repo edge at the import-resolved tier (§8B).
func (t *tracer) importedFrom(n *TraceNode, c Callee) *TraceNode {
	mod, name, ok := strings.Cut(c.Name, "::")
	if !ok || mod == "" || name == "" || t.skipped(mod) {
		return nil
	}
	for _, rp := range t.s.Repos() {
		if rp == n.Repo || !ModuleMatches(t.s.ModulePath(rp), mod) {
			continue
		}
		v := t.view(rp)
		for _, q := range v.byBase[name] {
			if !t.isTest(rp, q, "") {
				return t.symbolNode(rp, q, EdgeXRepo, "import-resolved", 0.75)
			}
		}
	}
	return nil
}

var (
	inlineCall  = regexp.MustCompile(`\b([A-Za-z_]\w*)\s*\(`)
	callKeyword = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "return": true, "function": true,
		"catch": true, "await": true, "async": true, "new": true, "typeof": true, "func": true, "def": true, "print": true}
)

// inlineCalls are the named calls inside an anonymous handler, resolved by a
// unique bare name in its repo (it has no call-graph node of its own).
func (t *tracer) inlineCalls(n *TraceNode, v *repoView, body string) []*TraceNode {
	var out []*TraceNode
	seen := map[string]bool{}
	for _, m := range inlineCall.FindAllStringSubmatch(body, -1) {
		name := m[1]
		if callKeyword[name] || traceNoise[name] || seen[name] {
			continue
		}
		seen[name] = true
		if qs := v.byBase[name]; len(qs) == 1 && !t.isTest(n.Repo, qs[0], "") {
			out = append(out, t.symbolNode(n.Repo, qs[0], EdgeCall, "name-in-inline", 0.5))
		}
	}
	return out
}

// upChildren: who calls n (in-repo callers, other repos calling it through
// a base class or an import of its module), the route n serves and the client
// calls reaching it, the topic whose callback n is and who publishes it.
func (t *tracer) upChildren(n *TraceNode) []*TraceNode {
	switch n.Kind {
	case NodeRoute:
		return t.routeClients(n)
	case NodeTopic:
		return t.topicPublishers(n)
	case NodeSymbol:
	default:
		return nil
	}
	var kids []*TraceNode
	seen := map[string]bool{}
	add := func(k *TraceNode) {
		if k != nil && !seen[k.key] {
			seen[k.key] = true
			kids = append(kids, k)
		}
	}
	v := t.view(n.Repo)
	for _, c := range v.reverse(t.s, t.ref)[n.Symbol] {
		if c.Name == n.Symbol || t.isTest(n.Repo, c.Name, "") {
			continue
		}
		add(t.symbolNode(n.Repo, c.Name, EdgeCaller, c.ResolutionMethod, c.Confidence))
	}
	// Through a base class: a caller of Base.m reaches this override.
	if cls, m, ok := classOf(n.Symbol); ok {
		g := t.classGraph()
		for _, b := range g.ancestors(cls) {
			for _, rp := range t.s.Repos() {
				rv := t.view(rp)
				for callee, cs := range rv.reverse(t.s, t.ref) {
					if !strings.HasSuffix(callee, "."+b+"."+m) && callee != b+"."+m {
						continue
					}
					for _, c := range cs {
						if t.isTest(rp, c.Name, "") {
							continue
						}
						method := "base-call"
						if rp != n.Repo {
							method = MethodXRepoCaller
						}
						k := t.symbolNode(rp, c.Name, EdgeCaller, method, ConfXRepo)
						k.Note = "calls " + callee
						add(k)
					}
				}
			}
		}
	}
	// Other repos importing this repo's module.
	if mod := t.s.ModulePath(n.Repo); mod != "" {
		for _, h := range t.s.ExternalRefsTo(mod, baseName(n.Symbol)) {
			if h.Repo == n.Repo || h.Ref != "" && h.Ref != EffectiveRef(t.s, h.Repo, t.ref) || t.isTest(h.Repo, h.Caller, "") {
				continue
			}
			add(t.symbolNode(h.Repo, h.Caller, EdgeXRepo, h.ResolutionMethod, h.Confidence))
		}
	}
	for _, k := range t.routesServedBy(n) {
		add(k)
	}
	for _, k := range t.topicsCalling(n) {
		add(k)
	}
	return kids
}

// --- entry resolution --------------------------------------------------------

func (t *tracer) resolveEntry(entry string) ([]*TraceNode, string, string) {
	kind, rest := "", entry
	if k, r, ok := strings.Cut(entry, ":"); ok && (k == "route" || k == "http" || k == "topic" || k == "cli" || k == "symbol") {
		kind, rest = k, strings.TrimSpace(r)
	}
	switch kind {
	case "route", "http":
		return t.routeEntry(rest), "route", ""
	case "topic":
		return t.topicEntry(rest), "topic", ""
	case "cli":
		return t.cliEntry(rest), "cli", ""
	case "symbol":
		roots, w := t.symbolEntry(rest)
		return roots, "symbol", w
	}
	if traceVerbEntry.MatchString(entry) {
		return t.routeEntry(entry), "route", ""
	}
	if m := traceSiteEntry.FindStringSubmatch(entry); m != nil {
		line, _ := strconv.Atoi(m[2])
		if r := t.siteEntry(m[1], line); len(r) > 0 {
			return r, "site", ""
		}
	}
	if !strings.HasPrefix(entry, "/") {
		if roots, w := t.symbolEntry(entry); len(roots) > 0 {
			return roots, "symbol", w
		}
	}
	if !strings.ContainsAny(entry, " ") {
		if r := t.topicEntry(entry); len(r) > 0 {
			return r, "topic", ""
		}
	}
	if strings.Contains(entry, "/") {
		if r := t.routeEntry(entry); len(r) > 0 {
			return r, "route", ""
		}
	}
	if r := t.cliEntry(entry); len(r) > 0 {
		return r, "cli", ""
	}
	return nil, "", ""
}

func (t *tracer) repos() []string {
	if t.opt.Repo != "" && t.opt.Repo != FleetRepo {
		return []string{t.opt.Repo}
	}
	return t.s.Repos()
}

// symbolEntry resolves a symbol (bare, qualified, Class.method, Class::method)
// in every repo; several definitions become several roots, with a warning.
func (t *tracer) symbolEntry(q string) ([]*TraceNode, string) {
	var roots []*TraceNode
	var all []string
	for _, rp := range t.repos() {
		names := ResolveSymbol(t.s, rp, t.ref, q)
		for _, name := range names {
			if t.isTest(rp, name, "") && len(names) > 1 {
				continue
			}
			all = append(all, rp+":"+name)
			if len(roots) < 3 {
				roots = append(roots, t.symbolNode(rp, name, "", "", 1))
			}
		}
	}
	warn := ""
	if len(all) > 1 {
		warn = fmt.Sprintf("ambiguous %q: %d definitions (%s); traced %d — pass a qualified id or --repo", q, len(all), strings.Join(firstN(all, 6), ", "), len(roots))
	}
	return roots, warn
}

// siteEntry is a call site given as path:line: the function enclosing it.
func (t *tracer) siteEntry(path string, line int) []*TraceNode {
	for _, rp := range t.repos() {
		v := t.view(rp)
		i := v.fileIndex(path)
		if i < 0 {
			continue
		}
		if q := v.qidAtLine(i, line); q != "" {
			n := t.symbolNode(rp, q, "", "", 1)
			n.Note = fmt.Sprintf("encloses %s:%d", path, line)
			return []*TraceNode{n}
		}
		return []*TraceNode{{Kind: NodeSite, Repo: rp, Label: fmt.Sprintf("%s:%d", path, line), Path: path, Line: line,
			key: rp + "\x00" + path + ":" + strconv.Itoa(line), fi: i, lo: line, hi: line, Confidence: 1}}
	}
	return nil
}

func (t *tracer) miss(entry string) string {
	msg := fmt.Sprintf("%q matched no symbol, route, topic or CLI command at %s", entry, t.ref)
	if near := Suggest(t.s, FleetRepo, t.ref, NormalizeSymbol(entry), 5); len(near) > 0 {
		names := make([]string, 0, len(near))
		for _, n := range near {
			names = append(names, n.Repo+":"+n.Name)
		}
		msg += "; near symbols: " + strings.Join(names, ", ")
	}
	return msg + " — force a kind with route:/topic:/cli:/symbol:, or check the ref is indexed"
}

func firstN(xs []string, n int) []string {
	if len(xs) > n {
		return append(xs[:n:n], fmt.Sprintf("+%d more", len(xs)-n))
	}
	return xs
}

// --- summary -----------------------------------------------------------------

func (t *tracer) next(entry string, r TraceResult) string {
	if len(t.cut) == 0 {
		return ""
	}
	var parts []string
	if r.Stops[StopBudget] > 0 {
		parts = append(parts, fmt.Sprintf("--budget %d", r.Budget*2))
	}
	if r.Stops[StopDepth] > 0 {
		parts = append(parts, fmt.Sprintf("--depth %d", r.Depth+2))
	}
	hint := fmt.Sprintf("re-run with %s", strings.Join(parts, " "))
	for _, n := range t.cut {
		if n.Symbol != "" && n.Kind == NodeSymbol {
			hint += fmt.Sprintf(", or continue from a cut node: trace %q %s --direction %s", n.Symbol, t.ref, r.Direction)
			if t.opt.Repo == "" && len(t.s.Repos()) > 1 {
				hint += " --repo " + n.Repo
			}
			break
		}
	}
	return hint
}

func (t *tracer) note(r TraceResult) string {
	var stops []string
	for _, k := range []string{StopDepth, StopBudget, StopCycle, StopRepeat, StopAmbiguous, StopUnresolved, StopFramework} {
		if c := r.Stops[k]; c > 0 {
			stops = append(stops, fmt.Sprintf("%s×%d", k, c))
		}
	}
	note := "walk stopped at: none (every reachable node expanded)"
	if len(stops) > 0 {
		note = "walk stopped at: " + strings.Join(stops, " ") +
			" (depth/budget: More = children not walked; repeat: expanded elsewhere, see Same; ambiguous: Alt lists candidates)"
	}
	return note + ". Edges carry the call graph's resolution_method/confidence; http (route match by path+method), topic (name match), impl (class derivation) and framework (convention) crossings are medium confidence. Effects are read from each body's source lines; a framework's own behavior is inferred and labeled so."
}
