// trace_cross.go carries reponite_trace across the boundaries a call graph
// cannot see, reusing the edge maps reponite already builds: HTTP (a client
// call -> the server route it reaches -> the handler that serves it, with a
// Django/DRF class view dispatched by HTTP method and its framework hooks),
// ROS topics (a publish -> the subscribers' callbacks), implementations (a
// base/interface method -> the classes deriving from it, fleet-wide, for C++,
// Python, TypeScript/JavaScript and Java class heads), and CLI commands (a
// command name -> the function its registration dispatches to). Each crossing
// is a medium-confidence link by convention or name, labeled with the rule
// that made it. Pure over Store.
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Confidence of the crossings: name/convention links, never type-proven.
const (
	confHTTP      = 0.6
	confHandler   = 0.75
	confFramework = 0.6
	confImpl      = 0.6
	confCLI       = 0.75
)

// --- HTTP ----------------------------------------------------------------------

type clientLink struct {
	call   ClientCall
	routes []Route
}

type routeIndex struct {
	routes  []Route
	clients map[string][]clientLink // repo\x00path -> its client calls
	byRoute map[string][]ClientCall // routeKey -> the clients reaching it
}

func routeKey(r Route) string {
	return r.Repo + "\x00" + r.Path + ":" + strconv.Itoa(r.Line) + " " + r.Method + " " + r.Pattern
}

func routeLabel(r Route) string {
	s := r.Method + " " + r.Pattern
	if r.Handler != "" {
		s += " → " + strings.TrimSuffix(r.Handler, ".as_view")
	}
	return s
}

func (t *tracer) routeIdx() *routeIndex {
	if t.routes != nil {
		return t.routes
	}
	res := RoutesWith(t.s, FleetRepo, t.ref, "", 1<<30, t.opt.Routes)
	ix := &routeIndex{clients: map[string][]clientLink{}, byRoute: map[string][]ClientCall{}}
	type ck struct {
		repo, path string
		line       int
	}
	at := map[ck]int{}
	put := func(c ClientCall, r *Route) {
		k := ck{c.Repo, c.Path, c.Line}
		fk := c.Repo + "\x00" + c.Path
		i, ok := at[k]
		if !ok {
			i = len(ix.clients[fk])
			at[k] = i
			ix.clients[fk] = append(ix.clients[fk], clientLink{call: c})
		}
		if r != nil {
			ix.clients[fk][i].routes = append(ix.clients[fk][i].routes, *r)
		}
	}
	for _, l := range res.Links {
		r := l.Route
		ix.routes = append(ix.routes, r)
		for _, c := range l.Clients {
			put(c, &r)
			ix.byRoute[routeKey(r)] = append(ix.byRoute[routeKey(r)], c)
		}
	}
	for _, c := range res.Unmatched {
		put(c, nil)
	}
	t.routes = ix
	return ix
}

// clientsIn are the client calls on lines [lo, hi] of a file, by line.
func (t *tracer) clientsIn(repo, path string, lo, hi int) map[int][]clientLink {
	var out map[int][]clientLink
	for _, c := range t.routeIdx().clients[repo+"\x00"+path] {
		if c.call.Line >= lo && c.call.Line <= hi {
			if out == nil {
				out = map[int][]clientLink{}
			}
			out[c.call.Line] = append(out[c.call.Line], c)
		}
	}
	return out
}

func (t *tracer) routeNode(r Route, verb, edge, method string, conf float64) *TraceNode {
	v := strings.ToUpper(verb)
	if v == "" || v == "REQUEST" {
		v = r.Method
	}
	shown := r.Method
	if shown == "*" && v != "*" {
		shown = v
	}
	lbl := shown + " /" + strings.TrimPrefix(r.Pattern, "/")
	if r.Handler != "" {
		lbl += " → " + strings.TrimSuffix(r.Handler, ".as_view")
	}
	rr := r
	return &TraceNode{Kind: NodeRoute, Repo: r.Repo, Label: lbl, Path: r.Path, Line: r.Line, Edge: edge, Method: method,
		Confidence: conf, key: "route\x00" + routeKey(r) + "\x00" + v, fi: -1, route: &rr, verb: v, Note: r.Framework}
}

// httpOut: each HTTP call in n's body that pairs with an indexed route.
func (t *tracer) httpOut(n *TraceNode) []*TraceNode {
	if n.fi < 0 {
		return nil
	}
	var out []*TraceNode
	for _, cs := range t.clientsIn(n.Repo, n.Path, n.lo, n.hi) {
		for _, c := range cs {
			for i, r := range c.routes {
				if i == 3 {
					break
				}
				k := t.routeNode(r, c.call.Method, EdgeHTTP, "route-match", confHTTP)
				k.Line = r.Line
				out = append(out, k)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].key < out[j].key })
	return out
}

var traceHTTPEntry = regexp.MustCompile(`(?i)^(?:(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|ANY|\*)\s+)?(\S+)$`)

// routeEntry: the routes an "VERB /path" (or a bare path) names, best match
// by literal segments and method; a tie keeps every best route.
func (t *tracer) routeEntry(entry string) []*TraceNode {
	m := traceHTTPEntry.FindStringSubmatch(strings.TrimSpace(entry))
	if m == nil {
		return nil
	}
	verb, path := strings.ToUpper(m[1]), m[2]
	if verb == "ANY" {
		verb = "*"
	}
	var roots []*TraceNode
	for _, r := range t.matchRoutes(verb, path, 4, t.opt.Repo) {
		roots = append(roots, t.routeNode(r, verb, "", "", 1))
	}
	return roots
}

// matchRoutes: the indexed routes serving verb+path, best literal-segment
// match first ("" or "*" verb = any); a tie keeps every best route.
func (t *tracer) matchRoutes(verb, path string, limit int, repo string) []Route {
	segs := segments(path)
	best, var0 := 0, []Route(nil)
	for _, r := range t.routeIdx().routes {
		if repo != "" && repo != FleetRepo && r.Repo != repo {
			continue
		}
		if verb != "" && verb != "*" && r.Method != "*" && r.Method != "" && r.Method != verb {
			continue
		}
		sc := matchSegs(segs, segments(r.Pattern))
		if sc > 0 && len(segments(r.Pattern)) == len(segs) {
			sc += 1 << 16 // an exact-length match beats a suffix match
		}
		switch {
		case sc > best:
			best, var0 = sc, []Route{r}
		case sc == best && sc > 0:
			var0 = append(var0, r)
		}
	}
	if len(var0) > limit {
		var0 = var0[:limit]
	}
	return var0
}

var (
	pyClassHead  = regexp.MustCompile(`class\s+\w+\s*\(`)
	drfQueryset  = regexp.MustCompile(`(?m)^\s*(?:queryset\s*=\s*([A-Z]\w*)\.objects|model\s*=\s*([A-Z]\w*))`)
	drfSerial    = regexp.MustCompile(`(?m)^\s*serializer_class\s*=\s*([A-Z]\w*)`)
	routeArgOpen = regexp.MustCompile(`\.(?:get|post|put|patch|delete|all|use|HandleFunc|Handle|GET|POST|PUT|PATCH|DELETE|Get|Post|Put|Patch|Delete)\s*\(`)
)

// routeHandlers: the code serving a route, by framework.
func (t *tracer) routeHandlers(n *TraceNode) []*TraceNode {
	r := n.route
	v := t.view(r.Repo)
	switch r.Framework {
	case "django":
		h := strings.TrimSuffix(strings.TrimSpace(r.Handler), ".as_view")
		parts := strings.Split(h, ".")
		if len(parts) >= 2 {
			cls, m := parts[len(parts)-2], parts[len(parts)-1]
			if m != "" && m[0] >= 'a' && m[0] <= 'z' && cls != "" && cls[0] >= 'A' && cls[0] <= 'Z' {
				for _, q := range v.byBase[m] {
					if c, _, ok := classOf(q); ok && c == cls {
						return []*TraceNode{t.symbolNode(r.Repo, q, EdgeHandler, "route-handler", confHandler)}
					}
				}
			}
		}
		name := parts[len(parts)-1]
		q := t.pickDef(v, name, r.Path)
		if q == "" {
			return []*TraceNode{t.unresolvedHandler(n, name)}
		}
		if t.isClass(v, q) {
			return t.classDispatch(n, v, q)
		}
		return []*TraceNode{t.symbolNode(r.Repo, q, EdgeHandler, "route-handler", confHandler)}
	case "fastapi/flask":
		fi := v.fileIndex(r.Path)
		q := v.qidIn(fi, r.Handler)
		if q == "" {
			q = t.pickDef(v, r.Handler, r.Path)
		}
		if q == "" {
			return []*TraceNode{t.unresolvedHandler(n, r.Handler)}
		}
		return []*TraceNode{t.symbolNode(r.Repo, q, EdgeHandler, "route-handler", confHandler)}
	case "express", "net/http", "gin/echo/chi":
		name, lo, hi := t.handlerArg(v, r)
		switch {
		case name != "":
			if q := t.pickDef(v, name, r.Path); q != "" {
				return []*TraceNode{t.symbolNode(r.Repo, q, EdgeHandler, "route-handler", confHandler)}
			}
			return []*TraceNode{t.unresolvedHandler(n, name)}
		case lo > 0:
			fi := v.fileIndex(r.Path)
			return []*TraceNode{{Kind: NodeInline, Repo: r.Repo, Label: "inline handler", Path: r.Path, Line: lo, Edge: EdgeHandler,
				Method: "route-handler", Confidence: confHandler, key: n.key + "\x00inline", fi: fi, lo: lo, hi: hi}}
		}
	}
	return []*TraceNode{{Kind: NodeFramework, Repo: r.Repo, Label: "handler of " + r.Pattern, Edge: EdgeHandler, Confidence: confHTTP,
		Stop: StopFramework, Note: "unrecognized framework " + r.Framework + ": its dispatch is not modeled", key: n.key + "\x00fw", fi: -1}}
}

func (t *tracer) unresolvedHandler(n *TraceNode, name string) *TraceNode {
	return &TraceNode{Kind: NodeSymbol, Repo: n.Repo, Symbol: name, Edge: EdgeHandler, Method: "route-handler", Confidence: confHTTP,
		Stop: StopUnresolved, Note: "handler named by the route is not defined in " + n.Repo, key: n.key + "\x00?" + name, fi: -1}
}

// handlerArg reads an Express/Go route registration's last argument: a named
// handler (name), or an inline function spanning lines [lo, hi].
func (t *tracer) handlerArg(v *repoView, r *Route) (name string, lo, hi int) {
	fi := v.fileIndex(r.Path)
	lines := v.fileLines(fi)
	if r.Line <= 0 || r.Line > len(lines) {
		return "", 0, 0
	}
	text := strings.Join(lines[r.Line-1:min(len(lines), r.Line+60)], "\n")
	loc := routeArgOpen.FindStringIndex(text)
	if loc == nil {
		return "", 0, 0
	}
	args, end, ok := splitCallArgs(text, loc[1]-1)
	if !ok || len(args) < 2 {
		return "", 0, 0
	}
	last := strings.TrimSpace(args[len(args)-1])
	if identPathRe.MatchString(last) {
		return lastMember(strings.ReplaceAll(last, "::", ".")), 0, 0
	}
	return "", r.Line, r.Line + strings.Count(text[:end], "\n")
}

// pickDef resolves a bare name to one definition in v: the one in the file
// at near, else the one sharing the longest directory prefix with it.
func (t *tracer) pickDef(v *repoView, name, near string) string {
	var cands []string
	for _, q := range v.byBase[name] {
		if !t.isTest(v.repo, q, "") {
			cands = append(cands, q)
		}
	}
	if len(cands) <= 1 {
		if len(cands) == 1 {
			return cands[0]
		}
		return ""
	}
	best, bestScore := "", -1
	for _, q := range cands {
		l, ok := v.where(q)
		score := 0
		if ok {
			p := v.files[l.file].Path
			if p == near {
				score = 1000
			} else {
				score = traceCommonPrefix(filepath.Dir(p), filepath.Dir(near))
			}
		}
		if score > bestScore {
			best, bestScore = q, score
		}
	}
	return best
}

func traceCommonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

func (t *tracer) isClass(v *repoView, q string) bool {
	for _, m := range []string{"get", "post", "put", "patch", "delete", "list", "create", "retrieve", "update", "destroy", "partial_update", "__init__"} {
		if _, ok := v.syms[q+"."+m]; ok {
			return true
		}
	}
	b := baseName(q)
	if b == "" || b[0] < 'A' || b[0] > 'Z' {
		return false
	}
	for k := range v.syms {
		if strings.HasPrefix(k, q+".") {
			return true
		}
	}
	return t.classHeader(v, q) != ""
}

// classHeader is a Python class's base list as written ("" when unknown).
func (t *tracer) classHeader(v *repoView, q string) string {
	l, ok := v.where(q)
	if !ok {
		return ""
	}
	lines := v.fileLines(l.file)
	text := strings.Join(lines[l.span.StartLine-1:min(len(lines), l.span.StartLine+8)], "\n")
	loc := pyClassHead.FindStringIndex(text)
	if loc == nil {
		return ""
	}
	return strings.Trim(strings.Join(strings.Fields(callArgs(text, loc[1])), " "), ", ")
}

// classModel is the model a class view or serializer works on: its
// `queryset = Model.objects…` or `model = Model` ("" when not declared).
func (t *tracer) classModel(v *repoView, cls string) string {
	l, ok := v.where(cls)
	if !ok {
		return ""
	}
	lines := v.fileLines(l.file)
	body := strings.Join(lines[l.span.StartLine-1:min(len(lines), l.span.EndLine)], "\n")
	if m := drfQueryset.FindStringSubmatch(body); m != nil {
		return m[1] + m[2]
	}
	return ""
}

// fwInfo is a framework-supplied action of a class view.
type fwInfo struct {
	class, action, model, serializer string
}

// drfVerbs: HTTP method -> (APIView method, ViewSet action).
func drfAction(verb string, detail bool) (string, string) {
	switch verb {
	case "GET":
		if detail {
			return "get", "retrieve"
		}
		return "get", "list"
	case "POST":
		return "post", "create"
	case "PUT":
		return "put", "update"
	case "PATCH":
		return "patch", "partial_update"
	case "DELETE":
		return "delete", "destroy"
	}
	return "", ""
}

// basesAllow: whether a DRF/Django class's bases provide an action.
func basesAllow(bases, action string) bool {
	if strings.Contains(bases, "ModelViewSet") && !strings.Contains(bases, "ReadOnly") {
		return true
	}
	switch action {
	case "list", "retrieve":
		return strings.Contains(bases, "ReadOnly") || strings.Contains(bases, "List") || strings.Contains(bases, "Retrieve")
	case "create":
		return strings.Contains(bases, "Create")
	case "update", "partial_update":
		return strings.Contains(bases, "Update")
	case "destroy":
		return strings.Contains(bases, "Destroy")
	}
	return false
}

// classDispatch: a class-based view serving a route. The HTTP method picks
// the method that runs — the class's own get/post/…, or its ViewSet action
// (create/list/retrieve/update/destroy); an action the class inherits from the
// framework becomes a framework node whose children are the hooks the class
// does override (perform_create, get_queryset, its serializer's create …).
func (t *tracer) classDispatch(n *TraceNode, v *repoView, cls string) []*TraceNode {
	r := n.route
	detail := strings.HasSuffix(strings.TrimSuffix(r.Pattern, "/"), "{}")
	verbs := []string{n.verb}
	if n.verb == "" || n.verb == "*" {
		verbs = []string{"GET", "POST", "PUT", "PATCH", "DELETE"}
	}
	bases := t.classHeader(v, cls)
	viewset := strings.Contains(bases, "ViewSet")
	for _, a := range t.classGraph().ancestors(baseName(cls)) {
		viewset = viewset || strings.Contains(a, "ViewSet")
	}
	l, _ := v.where(cls)
	body := ""
	if l.span.StartLine > 0 {
		lines := v.fileLines(l.file)
		body = strings.Join(lines[l.span.StartLine-1:min(len(lines), l.span.EndLine)], "\n")
	}
	info := fwInfo{class: cls}
	if m := drfQueryset.FindStringSubmatch(body); m != nil {
		info.model = m[1] + m[2]
	}
	if m := drfSerial.FindStringSubmatch(body); m != nil {
		info.serializer = m[1]
	}
	var out []*TraceNode
	for _, verb := range verbs {
		vm, action := drfAction(verb, detail)
		if vm == "" {
			continue
		}
		// A ViewSet binds each HTTP method to its action (DELETE -> destroy),
		// shadowing a same-named get/post/delete method; a plain view runs the
		// method named after the HTTP method.
		if !viewset {
			if rp, q := t.methodOf(v, cls, vm); q != "" {
				out = append(out, t.symbolNode(rp, q, EdgeHandler, "http-method-dispatch", confHandler))
				continue
			}
		}
		if rp, q := t.methodOf(v, cls, action); q != "" {
			out = append(out, t.symbolNode(rp, q, EdgeHandler, "viewset-action", confHandler))
			continue
		}
		if len(verbs) > 1 && !basesAllow(bases, action) {
			continue
		}
		fi := info
		fi.action = action
		k := &TraceNode{Kind: NodeFramework, Repo: r.Repo, Label: baseName(cls) + "." + action + " (inherited)", Path: n.Path,
			Line: l.span.StartLine, Edge: EdgeHandler, Method: "viewset-action", Confidence: confFramework,
			key: n.key + "\x00" + cls + "." + action, fi: -1, fw: &fi}
		if l.span.StartLine > 0 {
			k.Path = v.files[l.file].Path
		}
		if bases != "" {
			k.Note = "from bases: " + clipN(bases, 80)
		}
		if e, ok := inferredEffect(action, info.model, k.Line); ok {
			k.Effects = append(k.Effects, e)
		}
		out = append(out, k)
	}
	if len(out) == 0 {
		var methods []string
		for q := range v.syms {
			if strings.HasPrefix(q, cls+".") && !strings.Contains(q[len(cls)+1:], ".") {
				methods = append(methods, q)
			}
		}
		sort.Strings(methods)
		out = append(out, &TraceNode{Kind: NodeFramework, Repo: r.Repo, Label: baseName(cls) + " dispatch", Edge: EdgeHandler, Confidence: confFramework,
			Stop: StopFramework, Note: "unrecognized framework: how " + baseName(cls) + " dispatches " + n.verb + " is not modeled", Alt: traceFirstN(methods, 6),
			key: n.key + "\x00" + cls, fi: -1})
	}
	return out
}

// methodOf is the definition of method m a class (id cls in v) runs: its own,
// else the nearest indexed base class's (fleet-wide, in derivation order — an
// approximation of the language's method resolution order).
func (t *tracer) methodOf(v *repoView, cls, m string) (repo, qid string) {
	if _, ok := v.syms[cls+"."+m]; ok {
		return v.repo, cls + "." + m
	}
	g := t.classGraph()
	for _, b := range g.ancestors(baseName(cls)) {
		repos := append([]string{v.repo}, t.s.Repos()...)
		for _, rp := range repos {
			if def := g.defOf(t.s, t.ref, rp, b, m); def != "" && !t.isTest(rp, def, "") {
				return rp, def
			}
		}
	}
	return "", ""
}

// inferredEffect: what a framework's default action does to the database.
func inferredEffect(action, model string, line int) (TraceEffect, bool) {
	what := model
	if what == "" {
		what = "the model"
	}
	e := TraceEffect{Kind: "db", Line: line}
	switch action {
	case "create", "perform_create":
		e.Op, e.Text = "write", "serializer.save() → INSERT "+what+" (inferred: framework default)"
	case "update", "partial_update", "perform_update":
		e.Op, e.Text = "write", "serializer.save() → UPDATE "+what+" (inferred: framework default)"
	case "destroy", "perform_destroy":
		e.Op, e.Text = "write", "instance.delete() → DELETE "+what+" (inferred: framework default)"
	case "list", "retrieve":
		e.Op, e.Text = "read", "queryset → SELECT "+what+" (inferred: framework default)"
	default:
		return e, false
	}
	return e, true
}

var drfHooks = map[string][]string{
	"create":         {"get_serializer", "get_serializer_class", "perform_create"},
	"list":           {"get_queryset", "filter_queryset", "get_serializer", "get_serializer_class", "paginate_queryset"},
	"retrieve":       {"get_queryset", "get_object", "get_serializer", "get_serializer_class"},
	"update":         {"get_queryset", "get_object", "get_serializer", "get_serializer_class", "perform_update"},
	"partial_update": {"get_queryset", "get_object", "get_serializer", "get_serializer_class", "perform_update"},
	"destroy":        {"get_queryset", "get_object", "perform_destroy"},
}

var serializerHooks = map[string][]string{
	"create": {"validate", "create", "save", "to_internal_value"}, "update": {"validate", "update", "save", "to_internal_value"},
	"partial_update": {"validate", "update", "save"}, "list": {"to_representation"}, "retrieve": {"to_representation"},
}

// frameworkChildren: the hooks a class overrides for the inherited action,
// in the order the framework calls them, then its serializer's.
func (t *tracer) frameworkChildren(n *TraceNode) []*TraceNode {
	if n.fw == nil {
		return nil
	}
	v := t.view(n.Repo)
	var out []*TraceNode
	for _, h := range drfHooks[n.fw.action] {
		if rp, q := t.methodOf(v, n.fw.class, h); q != "" {
			k := t.symbolNode(rp, q, EdgeFramework, "drf-convention", confFramework)
			if !strings.HasPrefix(q, n.fw.class+".") {
				k.Note = "inherited from an indexed base"
			}
			out = append(out, k)
		}
	}
	if n.fw.serializer != "" {
		if sq := t.pickDef(v, n.fw.serializer, n.Path); sq != "" {
			hooks := serializerHooks[n.fw.action]
			for q := range v.syms {
				if strings.HasPrefix(q, sq+".validate_") && len(hooks) > 0 && hooks[0] == "validate" {
					hooks = append(hooks, baseName(q))
				}
			}
			for _, h := range hooks {
				if rp, q := t.methodOf(v, sq, h); q != "" {
					k := t.symbolNode(rp, q, EdgeFramework, "drf-convention", confFramework)
					k.Note = "serializer_class"
					out = append(out, k)
				}
			}
		}
	}
	return out
}

// routeClients (UP): the client calls reaching a route, at their enclosing
// function.
func (t *tracer) routeClients(n *TraceNode) []*TraceNode {
	var out []*TraceNode
	for _, c := range t.routeIdx().byRoute[routeKey(*n.route)] {
		if !t.opt.IncludeTests && testCode(c.Path, "") {
			continue
		}
		if n.verb != "" && n.verb != "*" && c.Method != "" && !strings.EqualFold(c.Method, n.verb) && !strings.EqualFold(c.Method, "request") {
			continue
		}
		v := t.view(c.Repo)
		fi := v.fileIndex(c.Path)
		note := fmt.Sprintf("%s %s at L%d", c.Method, c.URL, c.Line)
		if q := v.qidAtLine(fi, c.Line); q != "" {
			if t.isTest(c.Repo, q, "") {
				continue
			}
			k := t.symbolNode(c.Repo, q, EdgeClient, "route-match", confHTTP)
			k.Note = note
			out = append(out, k)
			continue
		}
		out = append(out, &TraceNode{Kind: NodeSite, Repo: c.Repo, Label: c.Text, Path: c.Path, Line: c.Line, Edge: EdgeClient,
			Method: "route-match", Confidence: confHTTP, Note: note, key: "site\x00" + c.Repo + c.Path + strconv.Itoa(c.Line), fi: -1})
	}
	return out
}

// routesServedBy (UP): the routes whose handler is n — a function handler,
// a class view's HTTP-method/action method, or one of its framework hooks.
func (t *tracer) routesServedBy(n *TraceNode) []*TraceNode {
	cls, m, isMethod := classOf(n.Symbol)
	base := baseName(n.Symbol)
	v := t.view(n.Repo)
	var out []*TraceNode
	for _, r := range t.routeIdx().routes {
		if r.Repo != n.Repo {
			continue
		}
		h := strings.TrimSuffix(strings.TrimSpace(r.Handler), ".as_view")
		if h == "" {
			if name, _, _ := t.handlerArg(v, &r); name != "" {
				h = name
			}
		}
		parts := strings.Split(h, ".")
		last := parts[len(parts)-1]
		verb := r.Method
		switch {
		case isMethod && len(parts) >= 2 && parts[len(parts)-2] == cls && last == m:
		case !isMethod && last == base:
		case isMethod && last == cls:
			v2 := httpVerbOf(m)
			if v2 == "" {
				continue
			}
			if r.Method != "*" && r.Method != v2 && v2 != "*" {
				continue
			}
			if v2 != "*" {
				verb = v2
			}
		default:
			continue
		}
		out = append(out, t.routeNode(r, verb, EdgeHTTP, "route-handler", confHTTP))
	}
	return out
}

// httpVerbOf: the HTTP method a view method/action/hook serves ("*" = any).
func httpVerbOf(m string) string {
	switch m {
	case "get", "list", "retrieve":
		return "GET"
	case "post", "create", "perform_create":
		return "POST"
	case "put", "update", "perform_update":
		return "PUT"
	case "patch", "partial_update":
		return "PATCH"
	case "delete", "destroy", "perform_destroy":
		return "DELETE"
	case "get_queryset", "get_object", "get_serializer", "get_serializer_class", "dispatch", "initial":
		return "*"
	}
	return ""
}

// --- ROS topics ------------------------------------------------------------------

type commIndex struct {
	groups    []CommGroup
	pubVar    map[string]map[string]string // repo -> publisher variable -> topic ("" = ambiguous)
	callbacks map[string][]int             // repo\x00callback qid -> topic group indexes
	resolved  bool
}

var pubAssign = regexp.MustCompile(`^\s*(?:(?:const\s+)?[\w:<>]+\s+)?([\w.]+(?:->\w+)?)\s*=[^=]`)

func (t *tracer) commIdx() *commIndex {
	if t.comms != nil {
		return t.comms
	}
	g := CommGraph(t.s, FleetRepo, t.ref)
	ix := &commIndex{groups: g.Groups, pubVar: map[string]map[string]string{}}
	for _, gr := range ix.groups {
		if gr.Family != "topic" {
			continue
		}
		for _, p := range gr.Producers {
			m := pubAssign.FindStringSubmatch(p.Text)
			if m == nil {
				continue
			}
			vr := lastMember(m[1])
			if ix.pubVar[p.Repo] == nil {
				ix.pubVar[p.Repo] = map[string]string{}
			}
			if old, ok := ix.pubVar[p.Repo][vr]; ok && old != gr.Name {
				ix.pubVar[p.Repo][vr] = ""
			} else {
				ix.pubVar[p.Repo][vr] = gr.Name
			}
		}
	}
	t.comms = ix
	return ix
}

// publishTopics: publisher variable -> topic, for one repo.
func (t *tracer) publishTopics(repo string) map[string]string {
	if m := t.commIdx().pubVar[repo]; m != nil {
		return m
	}
	return map[string]string{}
}

func (t *tracer) groupNamed(name string) *CommGroup {
	ix := t.commIdx()
	for i := range ix.groups {
		if ix.groups[i].Family == "topic" && ix.groups[i].Name == name {
			return &ix.groups[i]
		}
	}
	return nil
}

func (t *tracer) topicNode(g *CommGroup, edge, method string) *TraceNode {
	lbl := g.Family + " /" + g.Name
	for _, ep := range append(append([]CommEndpoint{}, g.Producers...), g.Consumers...) {
		if ep.MsgType != "" {
			lbl += " [" + ep.MsgType + "]"
			break
		}
	}
	conf := g.Confidence
	if edge == "" {
		conf = 1
	}
	return &TraceNode{Kind: NodeTopic, Label: lbl, Edge: edge, Method: method, Confidence: conf,
		key: "topic\x00" + g.Family + "\x00" + g.Name, fi: -1, group: g}
}

var rosPublish = regexp.MustCompile(`([\w.\->\[\]]*?\w)\s*(?:\.|->)\s*publish\s*\(`)

// publishes: the topics n's body publishes to (publisher variable -> topic).
func (t *tracer) publishes(n *TraceNode) []*TraceNode {
	body := t.bodyOf(n)
	if !strings.Contains(body, "publish") || commLang(n.Path) == "" {
		return nil
	}
	pubs := t.publishTopics(n.Repo)
	var out []*TraceNode
	seen := map[string]bool{}
	for _, m := range rosPublish.FindAllStringSubmatch(body, -1) {
		topic := pubs[lastMember(m[1])]
		if topic == "" || seen[topic] {
			continue
		}
		seen[topic] = true
		if g := t.groupNamed(topic); g != nil {
			out = append(out, t.topicNode(g, EdgeTopic, "topic-name"))
		}
	}
	return out
}

func (t *tracer) topicEntry(name string) []*TraceNode {
	want := normalizeTopic(name)
	ix := t.commIdx()
	var out []*TraceNode
	for i := range ix.groups {
		if ix.groups[i].Name == want {
			out = append(out, t.topicNode(&ix.groups[i], "", ""))
		}
	}
	return out
}

var subscribeOpen = regexp.MustCompile(`(?:subscribe|create_subscription|Subscriber)\s*(?:<[^()]*>)?\s*\(`)

// callbackOf resolves the callback a subscriber endpoint binds: a named
// function/method (its id), or an inline lambda (its line range).
func (t *tracer) callbackOf(ep CommEndpoint) (qid string, lo, hi int) {
	v := t.view(ep.Repo)
	fi := v.fileIndex(ep.Path)
	lines := v.fileLines(fi)
	if ep.Line <= 0 || ep.Line > len(lines) {
		return "", 0, 0
	}
	text := strings.Join(lines[ep.Line-1:min(len(lines), ep.Line+8)], "\n")
	loc := subscribeOpen.FindStringIndex(text)
	if loc == nil {
		return "", 0, 0
	}
	args, end, ok := splitCallArgs(text, loc[1]-1)
	if !ok {
		return "", 0, 0
	}
	at := -1
	for i, a := range args {
		if ep.Raw != "" && strings.Contains(a, ep.Raw) {
			at = i
			break
		}
	}
	name := ""
	for _, a := range args[at+1:] {
		a = strings.TrimSpace(a)
		if k, val, isKw := strings.Cut(a, "="); isKw && !strings.Contains(k, "(") {
			if kk := strings.TrimSpace(k); kk != "callback" && kk != "cb" {
				continue
			}
			a = strings.TrimSpace(val)
		}
		if strings.Contains(a, "](") || strings.HasPrefix(a, "[") || strings.HasPrefix(a, "lambda") && !strings.Contains(a, "self.") {
			return "", ep.Line, ep.Line + strings.Count(text[:end], "\n")
		}
		if m := cppCallbackRefRe.FindStringSubmatch(a); m != nil {
			name = lastSegment(m[1])
			break
		}
		if a == ep.MsgType || a == "" || a[0] >= '0' && a[0] <= '9' {
			continue
		}
		if m := regexp.MustCompile(`^(?:self\.|this->)?([A-Za-z_]\w*)$`).FindStringSubmatch(a); m != nil {
			if commLang(ep.Path) == "py" && m[1][0] >= 'A' && m[1][0] <= 'Z' {
				continue // a message type class, not a callback
			}
			name = m[1]
			break
		}
		if m := regexp.MustCompile(`self\.([A-Za-z_]\w*)`).FindStringSubmatch(a); m != nil {
			name = m[1]
			break
		}
	}
	if name == "" {
		return "", 0, 0
	}
	if q := v.qidIn(fi, name); q != "" {
		return q, 0, 0
	}
	return t.pickDef(v, name, ep.Path), 0, 0
}

// topicCallbacks: a topic's subscribers, at their callbacks.
func (t *tracer) topicCallbacks(n *TraceNode) []*TraceNode {
	var out []*TraceNode
	for _, ep := range n.group.Consumers {
		if !t.opt.IncludeTests && testCode(ep.Path, "") {
			continue
		}
		q, lo, hi := t.callbackOf(ep)
		switch {
		case q != "":
			k := t.symbolNode(ep.Repo, q, EdgeCallback, "subscriber-callback", n.group.Confidence)
			k.Note = fmt.Sprintf("subscribed at %s:%d", ep.Path, ep.Line)
			out = append(out, k)
		case lo > 0:
			v := t.view(ep.Repo)
			out = append(out, &TraceNode{Kind: NodeInline, Repo: ep.Repo, Label: "inline callback", Path: ep.Path, Line: lo,
				Edge: EdgeCallback, Method: "subscriber-callback", Confidence: n.group.Confidence,
				key: "inline\x00" + ep.Repo + ep.Path + strconv.Itoa(lo), fi: v.fileIndex(ep.Path), lo: lo, hi: hi})
		default:
			out = append(out, &TraceNode{Kind: NodeSite, Repo: ep.Repo, Label: "subscriber in " + ep.In, Path: ep.Path, Line: ep.Line,
				Edge: EdgeCallback, Method: "subscriber-callback", Confidence: n.group.Confidence, Stop: StopUnresolved,
				Note: "callback not resolved from the subscribe call", key: "site\x00" + ep.Repo + ep.Path + strconv.Itoa(ep.Line), fi: -1})
		}
	}
	return out
}

// topicPublishers (UP): the functions publishing a topic, found as
// `<publisher variable>.publish(` in the publisher's repo; the advertising
// function when no publish site is found.
func (t *tracer) topicPublishers(n *TraceNode) []*TraceNode {
	var out []*TraceNode
	seen := map[string]bool{}
	for _, ep := range n.group.Producers {
		if !t.opt.IncludeTests && testCode(ep.Path, "") {
			continue
		}
		v := t.view(ep.Repo)
		found := false
		if m := pubAssign.FindStringSubmatch(ep.Text); m != nil {
			re := regexp.MustCompile(`\b` + regexp.QuoteMeta(lastMember(m[1])) + `\s*(?:\.|->)\s*publish\s*\(`)
			for fi, f := range v.files {
				if commLang(f.Path) == "" || !strings.Contains(f.Content, "publish") || !re.MatchString(f.Content) {
					continue
				}
				for ln, l := range v.fileLines(fi) {
					if !re.MatchString(l) {
						continue
					}
					if q := v.qidAtLine(fi, ln+1); q != "" && !seen[ep.Repo+q] {
						seen[ep.Repo+q] = true
						k := t.symbolNode(ep.Repo, q, EdgePublisher, "topic-name", n.group.Confidence)
						k.Note = fmt.Sprintf("publishes at %s:%d", f.Path, ln+1)
						out = append(out, k)
						found = true
					}
				}
			}
		}
		if !found {
			fi := v.fileIndex(ep.Path)
			if q := v.qidAtLine(fi, ep.Line); q != "" && !seen[ep.Repo+q] {
				seen[ep.Repo+q] = true
				k := t.symbolNode(ep.Repo, q, EdgePublisher, "topic-name", n.group.Confidence)
				k.Note = "advertises here; no publish site found"
				out = append(out, k)
			}
		}
	}
	return out
}

// topicsCalling (UP): the topics whose subscriber callback is n.
func (t *tracer) topicsCalling(n *TraceNode) []*TraceNode {
	if commLang(n.Path) == "" {
		return nil
	}
	ix := t.commIdx()
	if !ix.resolved {
		ix.resolved = true
		ix.callbacks = map[string][]int{}
		for i, g := range ix.groups {
			if g.Family != "topic" {
				continue
			}
			for _, ep := range g.Consumers {
				if q, _, _ := t.callbackOf(ep); q != "" {
					ix.callbacks[ep.Repo+"\x00"+q] = append(ix.callbacks[ep.Repo+"\x00"+q], i)
				}
			}
		}
	}
	var out []*TraceNode
	for _, i := range ix.callbacks[n.key] {
		out = append(out, t.topicNode(&ix.groups[i], EdgeTopic, "subscriber-callback"))
	}
	return out
}

// --- implementations ------------------------------------------------------------------

var (
	pyClassDecl = regexp.MustCompile(`(?m)^[ \t]*class\s+([A-Za-z_]\w*)\s*\(`)
	jvClassDecl = regexp.MustCompile(`\b(?:class|interface)\s+([A-Za-z_$][\w$]*)(?:\s*<[^{]*?>)?\s+(?:extends\s+([\w.$]+(?:\s*<[^{]*?>)?(?:\s*,\s*[\w.$]+(?:\s*<[^{]*?>)?)*))?\s*(?:implements\s+([^{]+?))?\s*\{`)
)

// classGraph is the fleet's class derivations: impls.go's C++ class heads
// plus Python, JavaScript/TypeScript and Java ones, built once per trace.
func (t *tracer) classGraph() *classGraph {
	if t.classes != nil {
		return t.classes
	}
	repos := t.s.Repos()
	g := scanClasses(t.s, repos, t.ref)
	add := func(d classDecl) {
		if len(d.bases) == 0 {
			return
		}
		g.byName[d.name] = append(g.byName[d.name], d)
		for _, b := range d.bases {
			g.derived[b] = append(g.derived[b], d)
		}
	}
	for _, rp := range repos {
		for _, f := range t.s.Files(rp, t.ref) {
			switch codeExts[strings.ToLower(filepath.Ext(f.Path))] {
			case "py":
				for _, m := range pyClassDecl.FindAllStringSubmatchIndex(f.Content, -1) {
					d := classDecl{repo: rp, path: f.Path, line: 1 + strings.Count(f.Content[:m[0]], "\n"), name: f.Content[m[2]:m[3]]}
					for _, b := range splitArgs(callArgs(f.Content, m[1])) {
						b = strings.TrimSpace(b)
						if i := strings.Index(b, "["); i >= 0 {
							b = b[:i]
						}
						if b == "" || strings.Contains(b, "=") || b == "object" {
							continue
						}
						d.bases = append(d.bases, b[strings.LastIndex(b, ".")+1:])
					}
					add(d)
				}
			case "js", "c":
				if ext := strings.ToLower(filepath.Ext(f.Path)); ext != ".java" && codeExts[ext] != "js" {
					continue
				}
				for _, m := range jvClassDecl.FindAllStringSubmatchIndex(f.Content, -1) {
					d := classDecl{repo: rp, path: f.Path, line: 1 + strings.Count(f.Content[:m[0]], "\n"), name: f.Content[m[2]:m[3]]}
					for _, gi := range []int{4, 6} {
						if m[gi] < 0 {
							continue
						}
						for _, b := range splitBases(f.Content[m[gi]:m[gi+1]]) {
							d.bases = append(d.bases, b[strings.LastIndex(b, ".")+1:])
						}
					}
					add(d)
				}
			}
		}
	}
	t.classes = g
	return g
}

// implsOf: when n is a method of a class others derive from, each derived
// class's own definition of it, fleet-wide (a call through the base may run
// any of them).
func (t *tracer) implsOf(n *TraceNode) []*TraceNode {
	cls, m, ok := classOf(n.Symbol)
	if !ok {
		return nil
	}
	g := t.classGraph()
	var out []*TraceNode
	extra := 0
	for _, d := range g.descendants(cls) {
		if !t.opt.IncludeTests && testCode(d.path, d.name) {
			continue
		}
		def := g.defOf(t.s, t.ref, d.repo, d.name, m)
		if def == "" || d.repo == n.Repo && def == n.Symbol {
			continue
		}
		if len(out) >= traceMaxFanout {
			extra++
			continue
		}
		method := "class-derivation"
		if d.repo != n.Repo {
			method = MethodXRepoOverride
		}
		k := t.symbolNode(d.repo, def, EdgeImpl, method, confImpl)
		k.Note = d.name + " derives from " + cls
		out = append(out, k)
	}
	n.More += extra
	return out
}

// --- CLI commands -------------------------------------------------------------------------

var (
	cliClick   = regexp.MustCompile(`@[\w.]+\.command\(\s*(?:name\s*=\s*)?(?:["']([\w:.-]+)["'])?`)
	cliArgp    = regexp.MustCompile(`add_parser\(\s*["']([\w:.-]+)["']`)
	cliArgpFn  = regexp.MustCompile(`set_defaults\(\s*(?:func|handler|command|cmd)\s*=\s*([\w.]+)`)
	cliCobra   = regexp.MustCompile(`Use:\s*"([\w:.-]+)`)
	cliCobraFn = regexp.MustCompile(`\bRunE?:\s*([\w.]+)`)
	cliCase    = regexp.MustCompile(`^\s*case\s+((?:["'][\w:.-]+["']\s*(?:,|\|)?\s*)+):`)
	cliCaseLit = regexp.MustCompile(`["']([\w:.-]+)["']`)
	cliCmdr    = regexp.MustCompile(`\.command\(\s*['"]([\w:.-]+)`)
	cliCmdrFn  = regexp.MustCompile(`\.action\(\s*([\w.]+)\s*\)`)
	cliCallee  = regexp.MustCompile(`([A-Za-z_][\w.]*)\s*\(`)
	cliMgmt    = regexp.MustCompile(`(?:^|/)management/commands/([\w]+)\.py$`)
)

// cliEntry finds where a command named like entry's last word is registered
// — a Django management command, click/typer, argparse sub-parser, cobra,
// commander, or a `case "name":` dispatch — and the handler it runs.
func (t *tracer) cliEntry(entry string) []*TraceNode {
	f := strings.Fields(entry)
	if len(f) == 0 {
		return nil
	}
	name := f[len(f)-1]
	var roots []*TraceNode
	for _, rp := range t.repos() {
		v := t.view(rp)
		for fi, file := range v.files {
			if !t.opt.IncludeTests && testCode(file.Path, "") {
				continue
			}
			if m := cliMgmt.FindStringSubmatch(file.Path); m != nil && (m[1] == name || m[1] == strings.ReplaceAll(name, "-", "_")) {
				if q := v.qidIn(fi, "handle"); q != "" {
					roots = append(roots, t.cliNode(rp, file.Path, 1, name, "django-management-command", []*TraceNode{t.symbolNode(rp, q, EdgeCLI, "django-management-command", confCLI)}))
				}
				continue
			}
			if codeExts[strings.ToLower(filepath.Ext(file.Path))] == "" || !strings.Contains(file.Content, name) {
				continue
			}
			lines := v.fileLines(fi)
			for i, ln := range lines {
				if !strings.Contains(ln, name) && !strings.Contains(ln, ".command(") {
					continue
				}
				if h, rule := t.cliHandlerAt(v, fi, lines, i, name); rule != "" {
					roots = append(roots, t.cliNode(rp, file.Path, i+1, name, rule, h))
				}
			}
		}
	}
	if len(roots) > 4 {
		roots = roots[:4]
	}
	return roots
}

func (t *tracer) cliNode(repo, path string, line int, name, rule string, handlers []*TraceNode) *TraceNode {
	return &TraceNode{Kind: NodeCLI, Repo: repo, Label: "cli " + name, Path: path, Line: line, Note: rule, Confidence: 1,
		key: "cli\x00" + repo + path + strconv.Itoa(line), fi: -1, pre: handlers}
}

// cliHandlerAt: the handler a registration on line i dispatches to.
func (t *tracer) cliHandlerAt(v *repoView, fi int, lines []string, i int, name string) ([]*TraceNode, string) {
	ln := lines[i]
	win := func(n int) []string { return lines[i+1 : min(len(lines), i+1+n)] }
	resolve := func(fn, rule string) []*TraceNode {
		fn = lastMember(fn)
		if q := v.qidIn(fi, fn); q != "" {
			return []*TraceNode{t.symbolNode(v.repo, q, EdgeCLI, rule, confCLI)}
		}
		if q := t.pickDef(v, fn, v.files[fi].Path); q != "" {
			return []*TraceNode{t.symbolNode(v.repo, q, EdgeCLI, rule, confCLI)}
		}
		return nil
	}
	if m := cliClick.FindStringSubmatch(ln); m != nil {
		for _, l := range win(6) {
			if d := pyDef.FindStringSubmatch(l); d != nil {
				if m[1] == name || m[1] == "" && (d[1] == name || strings.ReplaceAll(d[1], "_", "-") == name) {
					return resolve(d[1], "click/typer"), "click/typer"
				}
				break
			}
		}
	}
	if m := cliArgp.FindStringSubmatch(ln); m != nil && m[1] == name {
		for _, l := range win(15) {
			if d := cliArgpFn.FindStringSubmatch(l); d != nil {
				return resolve(d[1], "argparse"), "argparse"
			}
		}
		return nil, "argparse"
	}
	if m := cliCobra.FindStringSubmatch(ln); m != nil && m[1] == name {
		for _, l := range win(20) {
			if d := cliCobraFn.FindStringSubmatch(l); d != nil && d[1] != "func" {
				return resolve(d[1], "cobra"), "cobra"
			}
		}
		return nil, "cobra"
	}
	if m := cliCmdr.FindStringSubmatch(ln); m != nil && m[1] == name {
		for _, l := range append([]string{ln}, win(10)...) {
			if d := cliCmdrFn.FindStringSubmatch(l); d != nil {
				return resolve(d[1], "commander"), "commander"
			}
		}
		return nil, "commander"
	}
	if m := cliCase.FindStringSubmatch(ln); m != nil {
		hit := false
		for _, l := range cliCaseLit.FindAllStringSubmatch(m[1], -1) {
			if l[1] == name {
				hit = true
			}
		}
		if !hit {
			return nil, ""
		}
		for _, l := range win(3) {
			for _, c := range cliCallee.FindAllStringSubmatch(l, -1) {
				if callKeyword[c[1]] || traceNoise[c[1]] {
					continue
				}
				if h := resolve(c[1], "switch-dispatch"); h != nil {
					return h, "switch-dispatch"
				}
			}
		}
	}
	return nil, ""
}

func (t *tracer) cliHandlers(n *TraceNode) []*TraceNode { return n.pre }
