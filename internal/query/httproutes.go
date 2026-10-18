// httproutes.go reads the HTTP routes a codebase serves, one extractor per
// framework family, each following the code that composes a route's path:
// a router variable, a group or sub-router made from it, a router passed to a
// function in another file or package, a module mounted under a prefix. The
// result is every endpoint with its full path as served, its HTTP method and
// its handler. A route whose prefix could not be followed is kept and marked
// Partial, never shown as if it were complete. Pure over Store.Files (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// serverRoutes is every route repo serves (test-file routes included, marked).
func serverRoutes(repo string, files []File) []Route {
	var out []Route
	out = append(out, drfMethods(djangoRoutes(repo, files), files)...)
	out = append(out, goRoutes(repo, files)...)
	out = append(out, jsRoutes(repo, files)...)
	out = append(out, pyRouterRoutes(repo, files)...)
	out = append(out, jvmRoutes(repo, files)...)
	for i := range out {
		out[i].Test = out[i].Test || IsTestPath(out[i].Path)
		if out[i].Pattern == "" {
			out[i].Pattern = "/" // a route on the mount point itself
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Pattern != b.Pattern {
			return a.Pattern < b.Pattern
		}
		return a.Method < b.Method
	})
	return dedupeRoutes(out)
}

// dedupeRoutes drops repeats of one endpoint from one place (a route
// registered on "" and "/" is one endpoint).
// The app's own copy wins over a test server's mount of the same route.
func dedupeRoutes(rs []Route) []Route {
	at := map[string]int{}
	var out []Route
	for _, r := range rs {
		k := r.Repo + "\x00" + r.Method + "\x00" + strings.TrimSuffix(r.Pattern, "/") + "\x00" + r.Handler + "\x00" + r.Framework
		if i, ok := at[k]; ok {
			if out[i].Test && !r.Test || out[i].Partial && !r.Partial {
				out[i] = r
			}
			continue
		}
		at[k] = len(out)
		out = append(out, r)
	}
	return out
}

// mountPath joins a mount prefix and a route path with one slash between.
func mountPath(prefix, p string) string {
	switch {
	case prefix == "":
		return p
	case p == "" || p == "/":
		return prefix
	}
	return strings.TrimSuffix(prefix, "/") + "/" + strings.TrimPrefix(p, "/")
}

// lineAtOffset is the 1-based line of byte offset pos in src.
func lineAtOffset(src string, pos int) int {
	if pos > len(src) {
		pos = len(src)
	}
	return 1 + strings.Count(src[:pos], "\n")
}

// --- Go: net/http, gin, echo, chi, gorilla/mux, fiber, httprouter ---------

// A route prefix is either a literal path from the server root (root ""), a
// path below parameter i of a function (root "F#i", bound later from every
// call of F), or unknown (root "?").
type rpfx struct {
	root, path string
	test       bool // the mount was made in test code (a test server)
}

type goFn struct {
	file   *File
	key    string // pkgdir.Name or pkgdir.Recv.Name
	name   string
	method bool
	params []string
	body   int // offset of the body's '{'
	end    int // offset of its '}'
}

var (
	reGoFuncHead  = regexp.MustCompile(`(?m)^func\s*(\(\s*\w*\s*\*?\s*([\w]+)[^)]*\)\s*)?(\w+)\s*(?:\[[^\]]*\])?\s*\(`)
	reGoPackage   = regexp.MustCompile(`(?m)^package\s+(\w+)`)
	reGoImportOne = regexp.MustCompile(`(?m)^\s*(?:import\s+)?(?:(\w+|\.|_)\s+)?"([^"]+)"\s*$`)
	reGoAssign    = regexp.MustCompile(`(?m)^\s*(?:var\s+)?(\w+)\s*(?::=|=)\s*`)
	reGoRegister  = regexp.MustCompile(`\.(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|Any|Get|Post|Put|Patch|Delete|Head|Options|Connect|Trace|All|Handle|HandleFunc|Method|MethodFunc|Match)\(`)
	reGoCtor      = regexp.MustCompile(`^(?:gin\.(?:Default|New)|echo\.New|chi\.(?:NewRouter|NewMux)|mux\.NewRouter|http\.NewServeMux|fiber\.New|httprouter\.New)\(`)
	reGoMethods   = regexp.MustCompile(`^\s*\.Methods\(([^)]*)\)`)
	reGoRouteFn   = regexp.MustCompile(`\.Route\(\s*"([^"]*)"\s*,\s*func\s*\(\s*(\w+)`)
)

func goRoutes(repo string, files []File) []Route {
	mods := map[string]string{} // go.mod directory -> module path
	for _, f := range files {
		if path.Base(f.Path) == "go.mod" {
			for _, ln := range strings.Split(f.Content, "\n") {
				if strings.HasPrefix(strings.TrimSpace(ln), "module ") {
					d := path.Dir(f.Path)
					if d == "." {
						d = ""
					}
					mods[d] = strings.Trim(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "module")), `"`)
				}
			}
		}
	}
	importDir := func(ip string) (string, bool) {
		best, dir, ok := "", "", false
		for d, m := range mods {
			if (ip == m || strings.HasPrefix(ip, m+"/")) && len(m) > len(best) {
				best, dir, ok = m, path.Join(d, strings.TrimPrefix(strings.TrimPrefix(ip, m), "/")), true
			}
		}
		if dir == "." {
			dir = ""
		}
		return dir, ok
	}
	var fns []*goFn
	byName := map[string][]*goFn{} // bare name -> functions and methods
	type fileInfo struct {
		pkgDir  string
		imports map[string]string // alias -> package dir in this repo
	}
	info := map[*File]fileInfo{}
	for i := range files {
		f := &files[i]
		if path.Ext(f.Path) != ".go" || !strings.Contains(f.Content, "\"") {
			continue
		}
		src := f.Content
		pkgDir := path.Dir(f.Path)
		if pkgDir == "." {
			pkgDir = ""
		}
		fi := fileInfo{pkgDir: pkgDir, imports: map[string]string{}}
		if blk := goImportBlock(src); blk != "" {
			for _, m := range reGoImportOne.FindAllStringSubmatch(blk, -1) {
				if d, ok := importDir(m[2]); ok {
					alias := m[1]
					if alias == "" {
						alias = path.Base(m[2])
					}
					fi.imports[alias] = d
				}
			}
		}
		info[f] = fi
		for _, m := range reGoFuncHead.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[6]:m[7]]
			params := splitArgs(callArgs(src, m[1]))
			open := goBodyOpen(src, m[1]+len(callArgs(src, m[1]))+1)
			if open < 0 {
				continue
			}
			end := goMatchBrace(src, open)
			key := qualifyQID(pkgDir, name)
			if m[4] >= 0 {
				key = qualifyQID(pkgDir, src[m[4]:m[5]]+"."+name)
			}
			fn := &goFn{file: f, key: key, name: name, method: m[4] >= 0, body: open, end: end}
			for _, p := range params {
				fs := strings.Fields(strings.TrimSpace(p))
				if len(fs) > 0 {
					fn.params = append(fn.params, fs[0])
				} else {
					fn.params = append(fn.params, "")
				}
			}
			fns = append(fns, fn)
			byName[name] = append(byName[name], fn)
		}
	}

	type pending struct {
		r      Route
		prefix rpfx
	}
	var routes []pending
	binding := map[string][]rpfx{} // "F#i" -> prefixes of the arguments passed as parameter i
	for _, fn := range fns {
		src := fn.file.Content
		body := src[fn.body:fn.end]
		fi := info[fn.file]
		// variables bound to routers, in source order; closures (chi Route,
		// func literals) get their own parameter scopes by extent
		type binding1 struct {
			name     string
			from, to int // extent in which the name holds this value (absolute offsets)
			val      []rpfx
		}
		var env []binding1
		var eval func(expr string, at int) []rpfx
		lookup := func(name string, at int) ([]rpfx, bool) {
			var best *binding1
			for i := range env {
				b := &env[i]
				if b.name == name && b.from <= at && at <= b.to && (best == nil || b.from >= best.from) {
					best = b
				}
			}
			if best != nil {
				return best.val, true
			}
			for i, p := range fn.params {
				if p == name {
					return []rpfx{{root: fn.key + "#" + itoa(i)}}, true
				}
			}
			return nil, false
		}
		eval = func(expr string, at int) []rpfx {
			e := strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(expr), "&*"))
			if reGoCtor.MatchString(e) || e == "http.DefaultServeMux" {
				return []rpfx{{}}
			}
			if i := strings.LastIndex(e, ".Group("); i > 0 {
				if lit, ok := goStringLit(firstArg(callArgs(e, i+len(".Group(")))); ok {
					var out []rpfx
					for _, b := range eval(e[:i], at) {
						out = append(out, rpfx{b.root, mountPath(b.path, lit), b.test})
					}
					return out
				}
			}
			if i := strings.Index(e, ".PathPrefix("); i > 0 && strings.HasSuffix(e, ".Subrouter()") {
				if lit, ok := goStringLit(firstArg(callArgs(e, i+len(".PathPrefix(")))); ok {
					var out []rpfx
					for _, b := range eval(e[:i], at) {
						out = append(out, rpfx{b.root, mountPath(b.path, lit), b.test})
					}
					return out
				}
			}
			if isGoIdent(e) {
				v, _ := lookup(e, at)
				return v
			}
			return nil
		}
		for _, m := range reGoAssign.FindAllStringSubmatchIndex(body, -1) {
			at := fn.body + m[1]
			rhs := goExprAt(src, at)
			if v := eval(rhs, at); v != nil {
				env = append(env, binding1{name: body[m[2]:m[3]], from: at, to: fn.end, val: v})
			}
		}
		for _, m := range reGoRouteFn.FindAllStringSubmatchIndex(body, -1) {
			// r.Route("/p", func(r chi.Router) { ... }): the closure's router
			recvEnd := fn.body + m[0]
			recv := goRecvBefore(src, recvEnd)
			open := strings.Index(src[fn.body+m[1]:], "{")
			if open < 0 {
				continue
			}
			open += fn.body + m[1]
			var val []rpfx
			for _, b := range eval(recv, recvEnd) {
				val = append(val, rpfx{b.root, mountPath(b.path, body[m[2]:m[3]]), b.test})
			}
			env = append(env, binding1{name: body[m[4]:m[5]], from: open, to: goMatchBrace(src, open), val: val})
		}
		// route registrations
		for _, m := range reGoRegister.FindAllStringSubmatchIndex(body, -1) {
			at := fn.body + m[0]
			recv := goRecvBefore(src, at)
			if recv == "" {
				continue
			}
			args := splitArgs(callArgs(src, fn.body+m[1]))
			verb := body[m[2]:m[3]]
			method, pat, handler := "", "", ""
			switch verb {
			case "Handle", "HandleFunc", "All":
				if len(args) < 2 {
					continue
				}
				lit, ok := goStringLit(args[0])
				if !ok {
					continue
				}
				method = "*"
				if sp := strings.Index(lit, " "); sp > 0 && strings.ToUpper(lit[:sp]) == lit[:sp] {
					method, lit = lit[:sp], strings.TrimSpace(lit[sp+1:]) // Go 1.22 "GET /x"
				}
				pat, handler = lit, args[len(args)-1]
				after := src[fn.body+m[1]+len(callArgs(src, fn.body+m[1]))+1:]
				if mm := reGoMethods.FindStringSubmatch(after); mm != nil {
					var ms []string
					for _, a := range splitArgs(mm[1]) {
						if s, ok := goStringLit(a); ok {
							ms = append(ms, strings.ToUpper(s))
						}
					}
					if len(ms) > 0 {
						method = strings.Join(ms, ",")
					}
				}
			case "Method", "MethodFunc":
				if len(args) < 3 {
					continue
				}
				ms, ok1 := goStringLit(args[0])
				lit, ok2 := goStringLit(args[1])
				if !ok1 || !ok2 {
					continue
				}
				method, pat, handler = strings.ToUpper(ms), lit, args[len(args)-1]
			case "Match":
				if len(args) < 3 {
					continue
				}
				lit, ok := goStringLit(args[1])
				if !ok {
					continue
				}
				var ms []string
				for _, q := range regexp.MustCompile(`"(\w+)"`).FindAllStringSubmatch(args[0], -1) {
					ms = append(ms, strings.ToUpper(q[1]))
				}
				method, pat, handler = strings.Join(ms, ","), lit, args[len(args)-1]
			default:
				if len(args) < 2 {
					continue
				}
				lit, ok := goStringLit(args[0])
				if !ok {
					continue
				}
				method, pat, handler = strings.ToUpper(verb), lit, args[len(args)-1]
				if method == "ANY" || method == "ALL" {
					method = "*"
				}
			}
			prefixes := eval(recv, at)
			if prefixes == nil {
				// a receiver not followed to a router: a route only when the
				// path reads like one (zap.Any("roles", x) and
				// client.Get(url) are not routes)
				if !strings.HasPrefix(pat, "/") || !(isGoIdent(recv) || strings.HasSuffix(recv, ")")) {
					continue
				}
				prefixes = []rpfx{{root: "?"}}
			}
			for _, method := range strings.Split(method, ",") {
				for _, p := range prefixes {
					routes = append(routes, pending{Route{Repo: repo, Path: fn.file.Path, Line: lineAtOffset(src, at), Method: method,
						Pattern: pat, Handler: goHandlerName(handler), Framework: goFramework(src)}, p})
				}
			}
		}
		// router values passed to functions: bind their parameters
		for _, m := range reGoCall.FindAllStringSubmatchIndex(body, -1) {
			qual, name := "", body[m[4]:m[5]]
			if m[2] >= 0 {
				qual = body[m[2]:m[3]]
			}
			// F(): a function of this package; pkg.F(): of the imported
			// package; x.M(): the one method of that name in the repo
			var targets []*goFn
			dir, imported := fi.imports[qual]
			for _, t := range byName[name] {
				tdir := info[t.file].pkgDir
				switch {
				case qual == "" && !t.method && tdir == fi.pkgDir,
					imported && !t.method && tdir == dir,
					qual != "" && !imported && t.method && len(byName[name]) == 1:
					targets = append(targets, t)
				}
			}
			if len(targets) == 0 {
				continue
			}
			args := splitArgs(callArgs(src, fn.body+m[1]))
			at := fn.body + m[0]
			for i, a := range args {
				v := eval(a, at)
				if v == nil {
					continue
				}
				inTest := IsTestPath(fn.file.Path)
				for _, t := range targets {
					if i < len(t.params) {
						for _, p := range v {
							p.test = p.test || inTest
							binding[t.key+"#"+itoa(i)] = append(binding[t.key+"#"+itoa(i)], p)
						}
					}
				}
			}
		}
	}
	// expand parameter roots through the call bindings
	var expand func(p rpfx, depth int, seen map[string]bool) []rpfx
	expand = func(p rpfx, depth int, seen map[string]bool) []rpfx {
		if p.root == "" || p.root == "?" || depth > 6 {
			return []rpfx{p}
		}
		bs := binding[p.root]
		if len(bs) == 0 || seen[p.root] {
			return []rpfx{{root: "?", path: p.path}}
		}
		seen[p.root] = true
		var out []rpfx
		for _, b := range bs {
			for _, e := range expand(b, depth+1, seen) {
				out = append(out, rpfx{e.root, mountPath(e.path, p.path), e.test || b.test || p.test})
			}
		}
		delete(seen, p.root)
		return out
	}
	var out []Route
	for _, pr := range routes {
		for _, e := range expand(pr.prefix, 0, map[string]bool{}) {
			r := pr.r
			r.Pattern = normURL(mountPath(e.path, r.Pattern))
			r.Partial = e.root != ""
			r.Test = e.test
			out = append(out, r)
		}
	}
	return out
}

// goFramework names the router library a file imports.
func goFramework(src string) string {
	blk := goImportBlock(src)
	for _, x := range [][2]string{{"gin-gonic/gin", "gin"}, {"labstack/echo", "echo"}, {"go-chi/chi", "chi"},
		{"gorilla/mux", "gorilla/mux"}, {"gofiber/fiber", "fiber"}, {"julienschmidt/httprouter", "httprouter"}} {
		if strings.Contains(blk, x[0]) {
			return x[1]
		}
	}
	return "net/http"
}

var reGoCall = regexp.MustCompile(`(?:\b(\w+)\.)?\b([A-Za-z_]\w*)\(`)

func isGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || i > 0 && c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// goStringLit reads a Go string literal ("…" or `…`).
func goStringLit(a string) (string, bool) {
	a = strings.TrimSpace(a)
	if len(a) >= 2 && (a[0] == '"' || a[0] == '`') && a[len(a)-1] == a[0] {
		return a[1 : len(a)-1], true
	}
	return "", false
}

// goHandlerName is a handler expression's name: users.Create -> Create,
// h.List -> List, an inline func literal -> "".
func goHandlerName(h string) string {
	h = strings.TrimSpace(h)
	if strings.HasPrefix(h, "func") || h == "" {
		return ""
	}
	if i := strings.Index(h, "("); i > 0 {
		h = h[:i] // http.HandlerFunc(x) and friends: keep the wrapped name below
	}
	if i := strings.LastIndex(h, "."); i >= 0 {
		h = h[i+1:]
	}
	return h
}

// goImportBlock is the text of a file's import declarations.
func goImportBlock(src string) string {
	var b strings.Builder
	for _, m := range regexp.MustCompile(`(?s)\bimport\s*\((.*?)\)|\bimport\s+((?:\w+\s+)?"[^"]+")`).FindAllStringSubmatch(src, -1) {
		b.WriteString(m[1])
		b.WriteString("\n")
		b.WriteString(m[2])
		b.WriteString("\n")
	}
	return b.String()
}

// goBodyOpen finds the '{' opening a function body at or after pos, skipping
// a result list and type literals (interface{}, struct{}) in it.
func goBodyOpen(src string, pos int) int {
	depth := 0
	for i := pos; i < len(src); i++ {
		switch src[i] {
		case '(':
			depth++
		case ')':
			depth--
		case '{':
			if depth > 0 {
				continue
			}
			w := strings.TrimSpace(src[max(0, i-10):i])
			if strings.HasSuffix(w, "interface") || strings.HasSuffix(w, "struct") {
				i = goMatchBrace(src, i)
				continue
			}
			return i
		case '\n':
			if depth == 0 && i > pos && strings.TrimSpace(src[pos:i]) != "" && !strings.ContainsAny(src[pos:i], "({") {
				// a declaration without a body (assembly stub) ends at its line
				if !strings.HasSuffix(strings.TrimSpace(src[pos:i]), ",") {
					return -1
				}
			}
		}
	}
	return -1
}

// goMatchBrace is the offset of the '}' closing the '{' at open (strings,
// runes and comments skipped); len(src) when unbalanced.
func goMatchBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '\'':
			for i++; i < len(src) && src[i] != c && src[i] != '\n'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '`':
			for i++; i < len(src) && src[i] != '`'; i++ {
			}
		case '/':
			if i+1 < len(src) && src[i+1] == '/' {
				for ; i < len(src) && src[i] != '\n'; i++ {
				}
			} else if i+1 < len(src) && src[i+1] == '*' {
				if j := strings.Index(src[i+2:], "*/"); j >= 0 {
					i += j + 3
				}
			}
		case '{':
			depth++
		case '}':
			if depth--; depth == 0 {
				return i
			}
		}
	}
	return len(src)
}

// goExprAt reads the expression starting at pos, up to the end of its line
// (continuing while brackets are open).
func goExprAt(src string, pos int) string {
	depth := 0
	for i := pos; i < len(src); i++ {
		switch src[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth < 0 {
				return src[pos:i]
			}
		case '\n', ';':
			if depth <= 0 {
				return src[pos:i]
			}
		}
	}
	return src[pos:]
}

// goRecvBefore is the receiver expression ending at pos (the text before a
// ".GET("): an identifier, a selector, or a call chain like r.Group("/x").
func goRecvBefore(src string, pos int) string {
	i := pos
	for i > 0 {
		c := src[i-1]
		switch {
		case c == ')':
			depth := 0
			j := i - 1
			for ; j >= 0; j-- {
				if src[j] == ')' {
					depth++
				} else if src[j] == '(' {
					if depth--; depth == 0 {
						break
					}
				}
			}
			if j < 0 {
				return ""
			}
			i = j
		case c == '_' || c == '.' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9':
			i--
		default:
			return strings.TrimPrefix(src[i:pos], ".")
		}
	}
	return src[i:pos]
}
