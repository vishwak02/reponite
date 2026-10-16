// rosargs.go resolves a ROS endpoint whose name is a function parameter.
// Library code rarely names its own topics: a publisher class advertises
// `topic_name`, its caller passes "costmap", and the NodeHandle it publishes
// on was built as ros::NodeHandle("~/" + name) by a constructor whose caller
// passed "global_costmap" — inside a node that a launch file names
// move_base_flex. Following the arguments back through the callers, and the
// node handle's namespace the same way, recovers the runtime name
// (/move_base_flex/global_costmap/costmap) that no single file states.
//
// It is a static, best-effort walk: string literals and concatenations of
// literals and parameters are followed up to maxArgDepth call hops; anything
// else stops the walk, and every resolved name carries the chain it came
// from and a confidence below a literal's. Pure over the files the comm graph
// already reads (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/roslaunch"
)

const (
	maxArgDepth  = 5
	maxArgValues = 12
	argNameConf  = 0.55
	// maxCallSites bounds the callers followed for one function: a name
	// called from more places than this (init, start, create) is too generic
	// to say which call reached the endpoint, and is not followed.
	maxCallSites = 24
)

// argValue is one value an expression can take, and how it was derived.
type argValue struct {
	val   string
	chain []string // "file:line" hops, the use first
	from  string   // path of the outermost file the value came from
}

type argResolver struct {
	files   map[string][]File // repo -> its C/C++ and Python files
	byPath  map[string]File   // repo\x00path -> file
	nodes   []roslaunch.Node  // every launched node, for private names
	callIdx map[string][]callSite
	// callers maps a name to the files that call something by it, built once
	// on first use so a lookup reads a handful of files, not the fleet.
	callers map[string][]fileRef
	memo    map[string][]argValue
	lines   map[string][]string
}

type fileRef struct {
	repo string
	i    int
}

type callSite struct {
	repo, path string
	line       int // 1-based
	args       []string
}

func newArgResolver(files map[string][]File, lf []roslaunch.File) *argResolver {
	r := &argResolver{files: files, byPath: map[string]File{}, callIdx: map[string][]callSite{},
		memo: map[string][]argValue{}, lines: map[string][]string{}}
	for rp, fs := range files {
		for _, f := range fs {
			r.byPath[rp+"\x00"+f.Path] = f
		}
	}
	for _, l := range lf {
		r.nodes = append(r.nodes, l.Nodes...)
	}
	return r
}

var (
	// a function or constructor definition's name right before its parameter list
	defNameRe   = regexp.MustCompile(`([A-Za-z_~][\w]*)\s*\($`)
	declParamRe = regexp.MustCompile(`([A-Za-z_]\w*)\s*(?:=[^,]*)?$`)
	pyAnnotRe   = regexp.MustCompile(`^(\*{0,2}[A-Za-z_]\w*)\s*:[^:]`)
	recvRe      = regexp.MustCompile(`([A-Za-z_][\w]*)\s*(?:->|\.)\s*$`)
	nhDeclRe    = `(?:NodeHandle|NodeHandlePtr)\s*[&*]?\s*%s\s*[({]([^;]*)[)}]\s*;`
	nhAssignRe  = `\b%s\s*=\s*(?:ros::)?NodeHandle\s*\(([^;]*)\)\s*;`
)

// resolve turns an endpoint whose name is an expression of identifiers and
// literals into bound endpoints, one per distinct runtime name found.
func (r *argResolver) resolve(ep CommEndpoint) []CommEndpoint {
	f, ok := r.byPath[ep.Repo+"\x00"+ep.Path]
	if !ok {
		return nil
	}
	leaves := r.evalExpr(ep.Repo, f, ep.Line, ep.expr, 0)
	if len(leaves) == 0 {
		return nil
	}
	spaces := []argValue{{val: "", from: ep.Path}}
	if ep.recv != "" {
		if ns := r.namespaceOf(ep.Repo, f, ep.Line, ep.recv, 0); len(ns) > 0 {
			spaces = ns
		}
	}
	var out []CommEndpoint
	seen := map[string]bool{}
	for _, ns := range spaces {
		for _, lv := range leaves {
			name := joinROSName(ns.val, lv.val)
			if name == "" {
				continue
			}
			from := lv.from
			if ns.val != "" {
				from = ns.from
			}
			for _, rt := range r.runtimeNames(name, from) {
				if seen[rt.name] || len(out) >= maxArgValues {
					continue
				}
				seen[rt.name] = true
				b := ep
				b.Name = normalizeTopic(rt.name)
				b.NameConfidence = argNameConf
				chain := "args: " + joinChain(lv.chain)
				if ns.val != "" {
					chain += "; namespace " + ns.val + " ← " + joinChain(ns.chain)
				}
				if rt.via != "" {
					chain += "; " + rt.via
				}
				b.NameResolution = chain
				b.expr, b.inline = "", nil
				out = append(out, b)
			}
		}
	}
	return out
}

type runtimeName struct{ name, via string }

// runtimeNames turns a private name (~x) into /<ns>/<node>/x for every
// launched node whose package holds the file the name came from.
func (r *argResolver) runtimeNames(name, from string) []runtimeName {
	if !strings.HasPrefix(name, "~") {
		return []runtimeName{{name: name}}
	}
	rest := strings.TrimPrefix(strings.TrimPrefix(name, "~"), "/")
	segs := map[string]bool{}
	for _, s := range strings.Split(path.Dir(from), "/") {
		segs[s] = true
	}
	var out []runtimeName
	seen := map[string]bool{}
	for _, n := range r.nodes {
		if n.Name == "" || !segs[n.Pkg] || roslaunch.HasSubstitution(n.Name) {
			continue
		}
		ns := strings.Trim(n.Ns, "/")
		if roslaunch.HasSubstitution(ns) {
			ns = ""
		}
		full := strings.Trim(ns+"/"+n.Name, "/") + "/" + rest
		if !seen[full] {
			seen[full] = true
			out = append(out, runtimeName{name: full, via: "node " + n.Name + " (pkg " + n.Pkg + ")"})
		}
	}
	if len(out) == 0 {
		return []runtimeName{{name: name, via: "private name of the node running " + path.Dir(from)}}
	}
	return out
}

// joinROSName joins a namespace and a name the way ros::NodeHandle does.
func joinROSName(ns, name string) string {
	if name == "" {
		return ""
	}
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, "~") || ns == "" {
		return name
	}
	if ns == "~" {
		return "~" + name
	}
	return strings.TrimSuffix(ns, "/") + "/" + name
}

// evalExpr evaluates a name expression at line of file f: literals,
// identifiers (a parameter of the enclosing function is followed to its
// callers) and their concatenations with '+'.
func (r *argResolver) evalExpr(repo string, f File, line int, expr string, depth int) []argValue {
	expr = strings.TrimSpace(expr)
	if depth > maxArgDepth || expr == "" {
		return nil
	}
	parts := splitPlus(expr)
	vals := []argValue{{val: "", from: f.Path}}
	for _, p := range parts {
		var pv []argValue
		if lit, ok := stringLiteral(p); ok {
			pv = []argValue{{val: lit, from: f.Path}}
		} else if id := strings.TrimSpace(strings.TrimLeft(p, "&*")); identPathRe.MatchString(id) {
			pv = r.evalIdent(repo, f, line, memberLeaf(id), depth)
		}
		if len(pv) == 0 {
			return nil
		}
		var next []argValue
		for _, a := range vals {
			for _, b := range pv {
				if len(next) >= maxArgValues {
					break
				}
				from := a.from
				if b.from != f.Path {
					from = b.from
				}
				next = append(next, argValue{val: a.val + b.val, chain: append(append([]string{}, a.chain...), b.chain...), from: from})
			}
		}
		vals = next
	}
	return vals
}

// splitPlus splits a + b + c at the top level (not inside quotes or parens).
func splitPlus(s string) []string {
	var out []string
	depth, start := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case q != 0:
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
		case c == '"' || c == '\'':
			q = c
		case c == '(' || c == '[' || c == '{':
			depth++
		case c == ')' || c == ']' || c == '}':
			depth--
		case c == '+' && depth == 0:
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	return append(out, strings.TrimSpace(s[start:]))
}

// evalIdent finds the values identifier id can hold at line: a parameter of
// the enclosing function (followed to every call site), or a local
// initialized from an expression earlier in the function.
func (r *argResolver) evalIdent(repo string, f File, line int, id string, depth int) []argValue {
	fn, params, defLine, ok := enclosingParams(f, line)
	if !ok {
		return nil
	}
	key := repo + "\x00" + f.Path + "\x00" + itoaQ(line) + "\x00" + id
	if v, ok := r.memo[key]; ok {
		return v
	}
	r.memo[key] = nil // a recursive call back here finds nothing, never loops
	v := r.evalIdentUncached(repo, f, line, id, depth, fn, params, defLine)
	r.memo[key] = v
	return v
}

func (r *argResolver) evalIdentUncached(repo string, f File, line int, id string, depth int, fn string, params []string, defLine int) []argValue {
	here := f.Path + ":" + itoaQ(line)
	for i, p := range params {
		if p != id {
			continue
		}
		var out []argValue
		for _, c := range r.callsOf(fn) {
			if c.repo == repo && c.path == f.Path && c.line == defLine || i >= len(c.args) {
				continue
			}
			cf := r.byPath[c.repo+"\x00"+c.path]
			for _, v := range r.evalExpr(c.repo, cf, c.line, c.args[i], depth+1) {
				v.chain = append([]string{here + " " + fn + "(" + id + ")", c.path + ":" + itoaQ(c.line)}, v.chain...)
				out = append(out, v)
				if len(out) >= maxArgValues {
					return out
				}
			}
		}
		return out
	}
	// A local: `std::string id = "x" + y;` / `id = …` above the use, in this function.
	lines := r.linesOf(repo, f)
	assign := regexp.MustCompile(`\b` + regexp.QuoteMeta(id) + `\s*=\s*([^=;][^;]*);`)
	for ln := line - 1; ln >= defLine && ln >= 1; ln-- {
		if m := assign.FindStringSubmatch(lines[ln-1]); m != nil {
			out := r.evalExpr(repo, f, ln, m[1], depth+1)
			for i := range out {
				out[i].chain = append([]string{f.Path + ":" + itoaQ(ln)}, out[i].chain...)
			}
			return out
		}
	}
	return nil
}

// namespaceOf evaluates the namespace of the node handle a call is made on:
// a parameter of the enclosing function is followed to its callers; a
// handle declared as ros::NodeHandle h("~/" + name) is evaluated.
func (r *argResolver) namespaceOf(repo string, f File, line int, recv string, depth int) []argValue {
	if depth > maxArgDepth {
		return nil
	}
	recv = strings.TrimLeft(memberLeaf(strings.TrimSpace(recv)), "&*")
	fn, params, defLine, ok := enclosingParams(f, line)
	if ok {
		for i, p := range params {
			if p != recv {
				continue
			}
			var out []argValue
			for _, c := range r.callsOf(fn) {
				if c.repo == repo && c.path == f.Path && c.line == defLine || i >= len(c.args) {
					continue
				}
				cf := r.byPath[c.repo+"\x00"+c.path]
				for _, v := range r.namespaceOf(c.repo, cf, c.line, c.args[i], depth+1) {
					v.chain = append([]string{c.path + ":" + itoaQ(c.line)}, v.chain...)
					out = append(out, v)
				}
			}
			return out
		}
	}
	lines := r.linesOf(repo, f)
	decl := regexp.MustCompile(strings.ReplaceAll(nhDeclRe, "%s", regexp.QuoteMeta(recv)))
	asg := regexp.MustCompile(strings.ReplaceAll(nhAssignRe, "%s", regexp.QuoteMeta(recv)))
	for ln := min(line, len(lines)); ln >= 1; ln-- {
		m := decl.FindStringSubmatch(lines[ln-1])
		if m == nil {
			m = asg.FindStringSubmatch(lines[ln-1])
		}
		if m == nil {
			continue
		}
		args := splitTopArgs(m[1])
		if len(args) == 0 || strings.TrimSpace(args[0]) == "" {
			return []argValue{{val: "", from: f.Path, chain: []string{f.Path + ":" + itoaQ(ln)}}}
		}
		nsExpr := args[0]
		if len(args) > 1 { // NodeHandle(parent, "sub")
			nsExpr = args[1]
		}
		out := r.evalExpr(repo, f, ln, nsExpr, depth+1)
		for i := range out {
			out[i].chain = append([]string{f.Path + ":" + itoaQ(ln)}, out[i].chain...)
		}
		return out
	}
	return nil
}

func splitTopArgs(s string) []string {
	args, _, ok := splitCallArgs("("+s+")", 0)
	if !ok {
		return nil
	}
	return args
}

// enclosingParams returns the function enclosing line in f, its parameter
// names in order and the line its definition starts on.
func enclosingParams(f File, line int) (fn string, params []string, defLine int, ok bool) {
	best := -1
	for i, s := range f.Symbols {
		if line >= s.StartLine && line <= s.EndLine && (best < 0 || s.EndLine-s.StartLine < f.Symbols[best].EndLine-f.Symbols[best].StartLine) {
			best = i
		}
	}
	if best < 0 {
		return "", nil, 0, false
	}
	sp := f.Symbols[best]
	fn = sp.Name
	if i := strings.LastIndexAny(fn, ".:"); i >= 0 {
		fn = fn[i+1:]
	}
	lines := strings.Split(f.Content, "\n")
	text := strings.Join(lines[sp.StartLine-1:min(len(lines), sp.StartLine+12)], "\n")
	at := strings.Index(text, fn+"(")
	if at < 0 {
		if at = strings.Index(text, fn+" ("); at < 0 {
			return fn, nil, sp.StartLine, true
		}
	}
	open := strings.IndexByte(text[at:], '(') + at
	args, _, aok := splitCallArgs(text, open)
	if !aok {
		return fn, nil, sp.StartLine, true
	}
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "self" || a == "cls" || a == "void" {
			continue
		}
		if m := pyAnnotRe.FindStringSubmatch(a); m != nil {
			a = m[1] // python `name: Type = default`
		}
		a = strings.TrimLeft(a, "*") // *args, **kwargs
		if m := declParamRe.FindStringSubmatch(a); m != nil {
			params = append(params, m[1])
		}
	}
	return fn, params, sp.StartLine, true
}

// callsOf lists the call sites of fn (a function, method or constructor) in
// every indexed file: fn(…), new fn(…), ns::fn(…), `fn var(…)`. Declarations
// (every argument a typed parameter) are left out.
func (r *argResolver) callsOf(fn string) []callSite {
	if v, ok := r.callIdx[fn]; ok {
		return v
	}
	if r.callers == nil {
		r.indexCallers()
	}
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(fn) + `(?:\s+[A-Za-z_]\w*)?\s*\(`)
	var out []callSite
	for _, ref := range r.callers[fn] {
		f := r.files[ref.repo][ref.i]
		for _, m := range re.FindAllStringIndex(f.Content, -1) {
			open := m[1] - 1
			args, _, ok := splitCallArgs(f.Content, open)
			if !ok || looksLikeDecl(args) {
				continue
			}
			ln := 1 + strings.Count(f.Content[:m[0]], "\n")
			out = append(out, callSite{repo: ref.repo, path: f.Path, line: ln, args: args})
		}
		if len(out) > maxCallSites {
			out = nil
			break
		}
	}
	r.callIdx[fn] = out
	return out
}

// calledNames lists the identifiers in src written as `F(` or as the type
// of a declaration `F var(` — every name callsOf could be asked about. A
// hand scan: a regexp over every source file of a fleet costs seconds.
func calledNames(src string) []string {
	var out []string
	isW := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	skipSpace := func(i int) int {
		for i < len(src) && (src[i] == ' ' || src[i] == '\t' || src[i] == '\n' || src[i] == '\r') {
			i++
		}
		return i
	}
	for i := 0; i < len(src); {
		c := src[i]
		if !isW(c) || c >= '0' && c <= '9' {
			i++
			continue
		}
		j := i
		for j < len(src) && isW(src[j]) {
			j++
		}
		k := skipSpace(j)
		switch {
		case k < len(src) && src[k] == '(':
			out = append(out, src[i:j])
		case k > j && k < len(src) && isW(src[k]):
			e := k
			for e < len(src) && isW(src[e]) {
				e++
			}
			if p := skipSpace(e); p < len(src) && src[p] == '(' {
				out = append(out, src[i:j])
			}
		}
		i = j
	}
	return out
}

// indexCallers records, per called name, the files that call it.
func (r *argResolver) indexCallers() {
	r.callers = map[string][]fileRef{}
	repos := make([]string, 0, len(r.files))
	for rp := range r.files {
		repos = append(repos, rp)
	}
	sort.Strings(repos)
	for _, rp := range repos {
		for i, f := range r.files[rp] {
			seen := map[string]bool{}
			for _, n := range calledNames(f.Content) {
				if !seen[n] {
					seen[n] = true
					r.callers[n] = append(r.callers[n], fileRef{rp, i})
				}
			}
		}
	}
}

func (r *argResolver) linesOf(repo string, f File) []string {
	k := repo + "\x00" + f.Path
	if l, ok := r.lines[k]; ok {
		return l
	}
	l := strings.Split(f.Content, "\n")
	r.lines[k] = l
	return l
}

var typedParamRe = regexp.MustCompile(`^(?:const\s+)?[\w:<>,\s]+?[\s&*]+[A-Za-z_]\w*(?:\s*=.*)?$|^[A-Za-z_]\w*\s*:\s*\w`)

func looksLikeDecl(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		a = strings.TrimSpace(a)
		if strings.ContainsAny(a, `"'`) || !typedParamRe.MatchString(a) {
			return false
		}
	}
	return true
}

func itoaQ(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// recvOf returns the receiver identifier of a member call ending code[:at]
// (`ros_node->advertise` → ros_node), "" for a free call.
func recvOf(before string) string {
	if m := recvRe.FindStringSubmatch(before); m != nil {
		return m[1]
	}
	return ""
}

// isNameConcat reports whether arg is a '+' concatenation of string literals
// and identifiers with at least one identifier — a name the caller completes.
func isNameConcat(arg string) bool {
	parts := splitPlus(strings.TrimSpace(arg))
	if len(parts) < 2 {
		return false
	}
	ident := false
	for _, p := range parts {
		if _, ok := stringLiteral(p); ok {
			continue
		}
		if !identPathRe.MatchString(strings.TrimSpace(p)) {
			return false
		}
		ident = true
	}
	return ident
}

// joinChain joins hops, dropping one that repeats the location before it
// (a parameter's use and the definition it is followed from share a line).
func joinChain(hops []string) string {
	var out []string
	last := ""
	for _, h := range hops {
		loc := strings.SplitN(h, " ", 2)[0]
		if loc == last {
			if len(h) > len(out[len(out)-1]) {
				out[len(out)-1] = h
			}
			continue
		}
		last = loc
		out = append(out, h)
	}
	return strings.Join(out, " ← ")
}
