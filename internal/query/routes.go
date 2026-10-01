// routes.go links HTTP clients to the server routes they call, across repos.
// A UI's `api.post(URLS.CREATE_LABEL, …)` and a Django
// `path("labels/", LabelView)` mounted under `include("store.v1.urls")`
// are joined only by a URL at runtime — no call graph sees it, the same way no
// call graph sees a ROS topic. Routes reads server route definitions (Django
// path/re_path/url with include() prefixes and DRF routers, FastAPI/Flask
// decorators with router prefixes, Express, Go net/http, gin/echo/chi) and
// client calls (fetch, axios and any client object's get/post/…, requests,
// httpx, sessions), resolves a client URL built from literals, template
// strings, f-strings, concatenation and named URL constants, and pairs each
// client with the routes whose path it ends in. Pure over Store.Files
// (ADR-018); medium confidence by nature, and said so.
package query

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Route is one server endpoint definition.
type Route struct {
	Repo, Path string
	Line       int
	Method     string // GET/POST/…; "*" = any (Django, DRF viewsets, Go HandleFunc)
	Pattern    string // full path as mounted ("v1/store/labels/{}"), placeholders as {}
	Handler    string
	Framework  string
}

// ClientCall is one HTTP call site whose URL could be resolved.
type ClientCall struct {
	Repo, Path string
	Line       int
	In         string
	Method     string
	URL        string // as resolved, placeholders as {}
	Text       string
	// Warning: the URL is malformed as written (a stray brace from a
	// template-literal typo is sent literally: "/operations/}42/errors/").
	Warning string
}

// RouteLink is a route and the client calls that reach it.
type RouteLink struct {
	Route   Route
	Clients []ClientCall
}

// RoutesResult is the HTTP edge map (or one endpoint's slice of it).
type RoutesResult struct {
	Filter    string
	Links     []RouteLink
	Unmatched []ClientCall // client calls (matching the filter) no indexed route serves
	Routes    int          // total routes found
	Clients   int          // total client calls found
	Note      string
	Meta      Meta
}

var (
	djInclude    = regexp.MustCompile(`^include\(\s*\(?\s*(?:["']([\w.]+)["']|([\w.]+))`)
	drfPlusEq    = regexp.MustCompile(`urlpatterns\s*\+=\s*(\w+)\.urls`)
	pyDecorator  = regexp.MustCompile(`^\s*@(\w+)\.(get|post|put|patch|delete|route|api_route|websocket)\(\s*f?["']([^"']*)["'](.*)`)
	pyRouterDef  = regexp.MustCompile(`\b(\w+)\s*=\s*(?:APIRouter|Blueprint|FastAPI|Flask)\((.*)`)
	pyPrefixArg  = regexp.MustCompile(`(?:url_)?prefix\s*=\s*["']([^"']*)["']`)
	pyIncRouter  = regexp.MustCompile(`\.(?:include_router|register_blueprint)\(\s*([\w.]+)(.*)`)
	pyMethods    = regexp.MustCompile(`methods\s*=\s*\[([^\]]*)\]`)
	pyDef        = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	expressRoute = regexp.MustCompile(`\b(app|router|server|routes?|api)\.(get|post|put|patch|delete|all)\(\s*['"` + "`" + `](/[^'"` + "`" + `]*)['"` + "`" + `]`)
	goHandle     = regexp.MustCompile(`\.(HandleFunc|Handle)\(\s*"(/[^"]*)"`)
	goVerb       = regexp.MustCompile(`\.(GET|POST|PUT|PATCH|DELETE|Get|Post|Put|Patch|Delete)\(\s*"(/[^"]*)"`)
	jsFetch      = regexp.MustCompile(`\bfetch\(\s*(.*)`)
	clientCall   = regexp.MustCompile(`\b([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\.(get|post|put|patch|delete|head|request)\s*(?:<[^()]*>)?\(\s*(.*)`)
	jsConst      = regexp.MustCompile(`(?:^|[\s,{(])([A-Za-z_$][\w$]*)\s*[:=]\s*(['"` + "`" + `])([^'"` + "`" + `\n]*/[^'"` + "`" + `\n]*)['"` + "`" + `]`)
	jsObjStart   = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*\{`)
	jsMethodOpt  = regexp.MustCompile(`method:\s*['"` + "`" + `](\w+)`)
	pyConst      = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*[:=]\s*f?["']([^"'\n]*/[^"'\n]*)["']`)
	placeholder  = regexp.MustCompile(`\$\{[^}]*\}|\{[^}]*\}|<[^>]*>|\(\?P<[^>]*>[^)]*\)|\([^)]*\)|\[[^\]]*\][+*]?|\\[dw][+*]?|\.[+*]`)
)

// Routes maps HTTP clients to routes fleet-wide at ref. filter (optional)
// keeps routes and calls whose path contains it; limit 0 = 50 links.
func Routes(s Store, repo, ref, filter string, limit int) RoutesResult {
	if limit <= 0 {
		limit = 50
	}
	res := RoutesResult{Filter: filter, Meta: Meta{Repo: repo, Ref: ref}}
	var routes []Route
	var clients []ClientCall
	for _, rp := range reposFor(s, repo) {
		files := s.Files(rp, ref)
		routes = append(routes, djangoRoutes(rp, files)...)
		consts := urlConstants(files)
		for _, f := range files {
			lang := codeExts[strings.ToLower(filepath.Ext(f.Path))]
			switch lang {
			case "py":
				routes = append(routes, pyDecoratorRoutes(rp, f, files)...)
			case "js", "go":
				routes = append(routes, jsGoRoutes(rp, f, lang)...)
			}
			if lang == "py" || lang == "js" {
				clients = append(clients, clientCalls(rp, f, lang, consts)...)
			}
		}
	}
	res.Routes, res.Clients = len(routes), len(clients)
	norm := strings.Trim(normURL(filter), "/")
	keep := func(p string) bool { return norm == "" || strings.Contains(strings.Trim(normURL(p), "/"), norm) }

	links := make([]RouteLink, len(routes))
	for i, r := range routes {
		links[i].Route = r
	}
	for _, c := range clients {
		best, idx := 0, []int(nil)
		cs := segments(c.URL)
		for i, r := range routes {
			if c.Method != "" && r.Method != "*" && r.Method != "" && !strings.EqualFold(c.Method, r.Method) && !strings.EqualFold(c.Method, "request") {
				continue
			}
			sc := matchSegs(cs, segments(r.Pattern))
			switch {
			case sc > best:
				best, idx = sc, []int{i}
			case sc == best && sc > 0:
				idx = append(idx, i)
			}
		}
		if best == 0 {
			if keep(c.URL) {
				res.Unmatched = append(res.Unmatched, c)
			}
			continue
		}
		for _, i := range idx {
			links[i].Clients = append(links[i].Clients, c)
		}
	}
	var out []RouteLink
	for _, l := range links {
		if keep(l.Route.Pattern) || len(l.Clients) > 0 && norm != "" && anyClient(l.Clients, keep) {
			out = append(out, l)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if (len(a.Clients) > 0) != (len(b.Clients) > 0) {
			return len(a.Clients) > 0
		}
		if a.Route.Repo != b.Route.Repo {
			return a.Route.Repo < b.Route.Repo
		}
		return a.Route.Pattern < b.Route.Pattern
	})
	total := len(out)
	if total > limit {
		out = out[:limit]
	}
	res.Links = out
	switch {
	case len(routes) == 0 && len(clients) == 0:
		res.Note = "no HTTP routes or client calls found at this ref — the server or UI repo may not be indexed"
	default:
		res.Note = "client↔route pairing is by URL path (the client's path must end in the route's, placeholders match anything) and HTTP method — medium confidence: base URLs, proxies and gateway rewrites are not resolved, and a URL built at runtime from values the code does not name is missed"
		if total > limit {
			res.Note += " — more routes than shown"
		}
	}
	return res
}

func anyClient(cs []ClientCall, keep func(string) bool) bool {
	for _, c := range cs {
		if keep(c.URL) {
			return true
		}
	}
	return false
}

// normURL drops scheme/host and query, and turns every placeholder form
// (${x}, {x}, <int:pk>, (?P<pk>…), [^/]+, \d+, :id) into {}.
func normURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		rest := u[i+3:]
		if j := strings.Index(rest, "/"); j >= 0 {
			u = rest[j:]
		} else {
			u = "/"
		}
	}
	u = strings.TrimPrefix(u, "^")
	u = strings.TrimSuffix(u, "$")
	u = placeholder.ReplaceAllString(u, "{}") // before the query cut: (?P<pk>…) holds a '?'
	if i := strings.IndexAny(u, "?#"); i >= 0 {
		u = u[:i]
	}
	u = strings.ReplaceAll(u, `\`, "")
	segs := strings.Split(u, "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") {
			segs[i] = "{}"
		}
	}
	return strings.Join(segs, "/")
}

func segments(u string) []string {
	var out []string
	for _, s := range strings.Split(normURL(u), "/") {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func wild(s string) bool { return strings.Contains(s, "{}") }

// matchSegs aligns the two paths at their ends: every segment of the shorter
// must match (a placeholder matches anything), and the score is the number of
// literal segments that matched. A client's leading {} (a base URL variable)
// may stand for any number of segments.
func matchSegs(client, route []string) int {
	if len(route) == 0 || len(client) == 0 {
		return 0
	}
	n := len(route)
	if len(client) < n {
		if !wild(client[0]) {
			return 0
		}
		n = len(client) - 1
		if n == 0 {
			return 0
		}
	}
	score := 0
	for i := 1; i <= n; i++ {
		c, r := client[len(client)-i], route[len(route)-i]
		switch {
		case wild(c) || wild(r):
		case c == r:
			score++
		default:
			return 0
		}
	}
	literals := 0
	for _, r := range route {
		if !wild(r) {
			literals++
		}
	}
	if score < literals && score < 2 {
		return 0 // one shared word ("list", "status") is not a link
	}
	return score
}

// --- Django ---------------------------------------------------------------

type djFile struct {
	routers  map[string]bool // DRF router variables registered here
	file     File
	module   string
	entries  []djRoute
	includes []djInc
}

type djRoute struct {
	line             int
	pattern, handler string
	router           string // DRF router var: pattern is relative to where it is included
}

type djInc struct {
	prefix, module string
}

var (
	djCall     = regexp.MustCompile(`\b(path|re_path|url)\(`)
	drfRegCall = regexp.MustCompile(`\b(\w+)\.register\(`)
	drfNested  = regexp.MustCompile(`\b(\w+)\s*=\s*[\w.]*Nested\w*Router\(`)
	pyListFor  = regexp.MustCompile(`^\s*(?:\)\s*)?for\s+(\w+)\s+in\s+\[([^\]]*)\]`)
)

// parseDjango reads a urls module's path()/re_path()/url() entries,
// include()s, DRF router registrations and nested routers. Calls may span
// lines and sit in lists or comprehensions, so it works on call extents, not
// lines.
func parseDjango(d *djFile, src string) {
	lineAt := func(pos int) int { return 1 + strings.Count(src[:pos], "\n") }
	nested := map[string]string{} // nested router -> its pattern prefix ("operations/{}/")
	for _, m := range drfNested.FindAllStringSubmatchIndex(src, -1) {
		args := splitArgs(callArgs(src, m[1]))
		if len(args) >= 2 {
			if lit, ok := pyStringLit(args[1]); ok {
				nested[src[m[2]:m[3]]] = strings.Trim(lit, "^$/") + "/{}/"
			}
		}
	}
	for _, m := range drfRegCall.FindAllStringSubmatchIndex(src, -1) {
		router := src[m[2]:m[3]]
		args := splitArgs(callArgs(src, m[1]))
		if len(args) < 2 {
			continue
		}
		lit, ok := pyStringLit(args[0])
		if !ok {
			continue
		}
		d.routers[router] = true
		base := nested[router] + strings.Trim(lit, "^$/")
		vs := strings.TrimSpace(args[1])
		d.entries = append(d.entries, djRoute{line: lineAt(m[0]), pattern: base + "/", handler: vs, router: router},
			djRoute{line: lineAt(m[0]), pattern: base + "/{}/", handler: vs, router: router})
	}
	for _, m := range drfPlusEq.FindAllStringSubmatch(src, -1) {
		d.includes = append(d.includes, djInc{prefix: "", module: m[1] + ".urls"})
	}
	for _, m := range djCall.FindAllStringSubmatchIndex(src, -1) {
		if m[0] > 0 && (src[m[0]-1] == '.' || src[m[0]-1] == '_') {
			continue // os.path(…), re_path matched as path
		}
		kind := src[m[2]:m[3]]
		body := callArgs(src, m[1])
		args := splitArgs(body)
		if len(args) < 2 {
			continue
		}
		pat, ok := pyStringLit(args[0])
		if !ok {
			continue
		}
		if kind != "path" {
			pat = strings.Trim(pat, "^$")
		}
		rest := strings.TrimSpace(args[1])
		if im := djInclude.FindStringSubmatch(rest); im != nil {
			mod := im[1]
			if mod == "" {
				mod = im[2]
			}
			// path("v2/", include(router.urls)) for router in [a, b]
			if v := strings.TrimSuffix(mod, ".urls"); v != mod && !d.routers[v] {
				after := src[m[1]+len(body)+1:]
				if len(after) > 300 {
					after = after[:300]
				}
				if lf := pyListFor.FindStringSubmatch(after); lf != nil && lf[1] == v {
					for _, r := range strings.Split(lf[2], ",") {
						if r = strings.TrimSpace(r); r != "" {
							d.includes = append(d.includes, djInc{prefix: pat, module: r + ".urls"})
						}
					}
					continue
				}
			}
			d.includes = append(d.includes, djInc{prefix: pat, module: mod})
			continue
		}
		h := rest
		if i := strings.IndexAny(h, ",("); i >= 0 {
			h = h[:i]
		}
		d.entries = append(d.entries, djRoute{line: lineAt(m[0]), pattern: pat, handler: strings.TrimSpace(h)})
	}
}

// callArgs is the text inside the parentheses opened just before pos.
func callArgs(src string, pos int) string {
	depth := 1
	var q byte
	for i := pos; i < len(src); i++ {
		c := src[i]
		if q != 0 {
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			q = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth--; depth == 0 {
				return src[pos:i]
			}
		}
	}
	return src[pos:]
}

// splitArgs splits a call's argument text at top-level commas.
func splitArgs(s string) []string {
	var out []string
	for rest := s; rest != ""; {
		a := firstArg(rest)
		out = append(out, a)
		if len(a) >= len(rest) {
			break
		}
		rest = rest[len(a)+1:]
	}
	return out
}

// pyStringLit reads r"…", '…', f"…" (placeholders kept).
func pyStringLit(a string) (string, bool) {
	a = strings.TrimSpace(a)
	a = strings.TrimLeft(a, "rRbBfFuU")
	if len(a) >= 2 && (a[0] == '"' || a[0] == '\'') && a[len(a)-1] == a[0] {
		return a[1 : len(a)-1], true
	}
	return "", false
}

// stripPyComments blanks # comments outside strings, keeping newlines.
func stripPyComments(src string) string {
	b := []byte(src)
	var q byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		if q != 0 {
			if c == '\\' {
				i++
			} else if c == q || c == '\n' {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			q = c
		case '#':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		}
	}
	return string(b)
}

// djangoRoutes composes every urls module's patterns with the include()
// prefixes that mount it, from the root URLconfs down.
func djangoRoutes(repo string, files []File) []Route {
	var mods []*djFile
	for _, f := range files {
		if filepath.Ext(f.Path) != ".py" || !strings.Contains(f.Content, "urlpatterns") && !strings.Contains(f.Content, ".register(") {
			continue
		}
		d := &djFile{file: f, module: strings.ReplaceAll(strings.TrimSuffix(f.Path, ".py"), "/", "."), routers: map[string]bool{}}
		parseDjango(d, stripPyComments(f.Content))
		if len(d.entries) > 0 || len(d.includes) > 0 {
			mods = append(mods, d)
		}
	}
	byModule := func(mod string) []*djFile {
		var out []*djFile
		for _, d := range mods {
			if d.module == mod || strings.HasSuffix(d.module, "."+mod) {
				out = append(out, d)
			}
		}
		return out
	}
	// prefixes[d] = every mount point of d, found walking down from roots.
	prefixes := map[*djFile][]string{}
	routerPrefix := map[*djFile]map[string][]string{} // file -> router var -> mount prefixes
	included := map[*djFile]bool{}
	for _, d := range mods {
		for _, inc := range d.includes {
			for _, t := range byModule(inc.module) {
				if t != d {
					included[t] = true
				}
			}
		}
	}
	var walk func(d *djFile, prefix string, depth int)
	walk = func(d *djFile, prefix string, depth int) {
		if depth > 8 {
			return
		}
		for _, p := range prefixes[d] {
			if p == prefix {
				return
			}
		}
		prefixes[d] = append(prefixes[d], prefix)
		for _, inc := range d.includes {
			if r := strings.TrimSuffix(inc.module, ".urls"); d.routers[r] {
				if routerPrefix[d] == nil {
					routerPrefix[d] = map[string][]string{}
				}
				routerPrefix[d][r] = append(routerPrefix[d][r], prefix+inc.prefix)
				continue
			}
			for _, t := range byModule(inc.module) {
				if t != d {
					walk(t, prefix+inc.prefix, depth+1)
				}
			}
		}
	}
	for _, d := range mods {
		if !included[d] {
			walk(d, "", 0)
		}
	}
	var out []Route
	viewsets := map[string][]string{} // DRF viewset class -> mounted list route ("v1/x/")
	for _, d := range mods {
		pfx := prefixes[d]
		if len(pfx) == 0 {
			pfx = []string{""}
		}
		for _, e := range d.entries {
			mounts := pfx
			if rp := routerPrefix[d][e.router]; e.router != "" && len(rp) > 0 {
				mounts = rp // include(router.urls) under each of this file's mounts
			}
			for _, p := range mounts {
				out = append(out, Route{Repo: repo, Path: d.file.Path, Line: e.line, Method: "*",
					Pattern: normURL(p + e.pattern), Handler: e.handler, Framework: "django"})
				if e.router != "" && !strings.Contains(e.pattern, "{}") {
					vs := e.handler[strings.LastIndex(e.handler, ".")+1:]
					viewsets[vs] = append(viewsets[vs], normURL(p+e.pattern))
				}
			}
		}
	}
	if len(viewsets) > 0 {
		out = append(out, drfActions(repo, files, viewsets)...)
	}
	return out
}

var (
	pyClass    = regexp.MustCompile(`^class\s+(\w+)\s*[(:]`)
	drfDetail  = regexp.MustCompile(`detail\s*=\s*(True|False)`)
	drfURLPath = regexp.MustCompile(`url_path\s*=\s*r?["']([^"']*)["']`)
)

// drfActions: a registered viewset's @action methods are routes of their own
// (list-level base/<path>/, or detail-level base/{}/<path>/).
func drfActions(repo string, files []File, viewsets map[string][]string) []Route {
	var out []Route
	for _, f := range files {
		if filepath.Ext(f.Path) != ".py" || !strings.Contains(f.Content, "@action") {
			continue
		}
		lines := strings.Split(f.Content, "\n")
		class := ""
		for i := 0; i < len(lines); i++ {
			if m := pyClass.FindStringSubmatch(lines[i]); m != nil {
				class = m[1]
				continue
			}
			t := strings.TrimSpace(lines[i])
			bases, ok := viewsets[class]
			if !ok || !strings.HasPrefix(t, "@action") {
				continue
			}
			dec := t // the decorator may span lines: read until its parens close
			for j := i + 1; j < len(lines) && strings.Count(dec, "(") > strings.Count(dec, ")") && j < i+10; j++ {
				dec += " " + strings.TrimSpace(lines[j])
			}
			name := ""
			for j := i + 1; j < len(lines) && j < i+12; j++ {
				if d := pyDef.FindStringSubmatch(lines[j]); d != nil {
					name = d[1]
					break
				}
			}
			if name == "" {
				continue
			}
			sub := name
			if m := drfURLPath.FindStringSubmatch(dec); m != nil {
				sub = m[1]
			}
			method := "GET"
			if m := pyMethods.FindStringSubmatch(dec); m != nil {
				method = strings.ToUpper(strings.Trim(strings.Split(m[1], ",")[0], ` "'`))
			}
			detail := ""
			if m := drfDetail.FindStringSubmatch(dec); m != nil && m[1] == "True" {
				detail = "{}/"
			}
			for _, b := range bases {
				out = append(out, Route{Repo: repo, Path: f.Path, Line: i + 1, Method: method,
					Pattern: normURL(strings.TrimSuffix(b, "/") + "/" + detail + sub + "/"), Handler: class + "." + name, Framework: "django"})
			}
		}
	}
	return out
}

// --- FastAPI / Flask --------------------------------------------------------

func pyDecoratorRoutes(repo string, f File, files []File) []Route {
	if !strings.Contains(f.Content, "@") {
		return nil
	}
	lines := strings.Split(f.Content, "\n")
	prefix := map[string]string{} // router var -> its own prefix
	for _, ln := range lines {
		if m := pyRouterDef.FindStringSubmatch(ln); m != nil {
			if p := pyPrefixArg.FindStringSubmatch(m[2]); p != nil {
				prefix[m[1]] = p[1]
			} else {
				prefix[m[1]] = ""
			}
		}
	}
	var out []Route
	for i, ln := range lines {
		if !strings.HasPrefix(strings.TrimSpace(ln), "@") {
			continue
		}
		dec := ln // a decorator's arguments may span lines: @router.get(\n    "/me", …)
		for j := i + 1; j < len(lines) && j < i+10 && strings.Count(dec, "(") > strings.Count(dec, ")"); j++ {
			dec += " " + strings.TrimSpace(lines[j])
		}
		m := pyDecorator.FindStringSubmatch(dec)
		if m == nil {
			continue
		}
		if _, ok := prefix[m[1]]; !ok && m[1] != "app" && m[1] != "router" && m[1] != "bp" && m[1] != "blueprint" {
			continue // not a router/app this file defines (a mock.patch…)
		}
		method := strings.ToUpper(m[2])
		if method == "ROUTE" || method == "API_ROUTE" {
			method = "*"
			if mm := pyMethods.FindStringSubmatch(m[4]); mm != nil {
				method = strings.ToUpper(strings.Trim(strings.Split(mm[1], ",")[0], ` "'`))
			}
		}
		handler := ""
		for j := i + 1; j < len(lines) && j < i+14; j++ {
			if d := pyDef.FindStringSubmatch(lines[j]); d != nil {
				handler = d[1]
				break
			}
		}
		mounts := []string{prefix[m[1]]}
		if ext := includePrefixes(files, m[1]); len(ext) > 0 {
			mounts = nil
			for _, e := range ext {
				mounts = append(mounts, e+prefix[m[1]])
			}
		}
		for _, p := range mounts {
			out = append(out, Route{Repo: repo, Path: f.Path, Line: i + 1, Method: method,
				Pattern: normURL(p + m[3]), Handler: handler, Framework: "fastapi/flask"})
		}
	}
	return out
}

// includePrefixes: the prefixes app.include_router(<name>, prefix=…) mounts a
// router variable under anywhere in the repo.
func includePrefixes(files []File, name string) []string {
	var out []string
	for _, f := range files {
		if filepath.Ext(f.Path) != ".py" || !strings.Contains(f.Content, "include_router") && !strings.Contains(f.Content, "register_blueprint") {
			continue
		}
		for _, ln := range strings.Split(f.Content, "\n") {
			m := pyIncRouter.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			v := m[1]
			if i := strings.LastIndex(v, "."); i >= 0 {
				v = v[i+1:]
			}
			if v != name {
				continue
			}
			p := ""
			if pm := pyPrefixArg.FindStringSubmatch(m[2]); pm != nil {
				p = pm[1]
			}
			out = append(out, p)
		}
	}
	return out
}

// --- Express / Go -----------------------------------------------------------

func jsGoRoutes(repo string, f File, lang string) []Route {
	var out []Route
	for i, ln := range strings.Split(f.Content, "\n") {
		if lang == "js" {
			if m := expressRoute.FindStringSubmatch(ln); m != nil && strings.Contains(ln, "req") {
				out = append(out, Route{Repo: repo, Path: f.Path, Line: i + 1, Method: strings.ToUpper(m[2]),
					Pattern: normURL(m[3]), Framework: "express"})
			}
			continue
		}
		if m := goHandle.FindStringSubmatch(ln); m != nil {
			out = append(out, Route{Repo: repo, Path: f.Path, Line: i + 1, Method: "*", Pattern: normURL(m[2]), Framework: "net/http"})
		} else if m := goVerb.FindStringSubmatch(ln); m != nil {
			out = append(out, Route{Repo: repo, Path: f.Path, Line: i + 1, Method: strings.ToUpper(m[1]), Pattern: normURL(m[2]), Framework: "gin/echo/chi"})
		}
	}
	return out
}

// --- clients ----------------------------------------------------------------

// urlConstants are the repo's named path-like strings (URLS.CREATE_LABEL:
// 'v1/store/labels/', ORDERS_URL = "/api/orders"), by name. A name bound
// to two different paths is dropped.
func urlConstants(files []File) map[string]string {
	out := map[string]string{}
	bad := map[string]bool{}
	put := func(k, v string) {
		if old, ok := out[k]; ok && old != v {
			bad[k] = true
		}
		out[k] = v
	}
	for _, f := range files {
		switch codeExts[strings.ToLower(filepath.Ext(f.Path))] {
		case "js":
			// A key inside `const API_URLS = { … }` is also known as
			// API_URLS.KEY, which tells two same-named keys apart.
			obj := ""
			for _, ln := range strings.Split(f.Content, "\n") {
				if m := jsObjStart.FindStringSubmatch(ln); m != nil {
					obj = m[1]
				} else if strings.HasPrefix(ln, "}") {
					obj = ""
				}
				for _, m := range jsConst.FindAllStringSubmatch(ln, -1) {
					put(m[1], m[3])
					if obj != "" {
						put(obj+"."+m[1], m[3])
					}
				}
			}
		case "py":
			for _, ln := range strings.Split(f.Content, "\n") {
				if m := pyConst.FindStringSubmatch(ln); m != nil {
					put(m[1], m[2])
				}
			}
		}
	}
	for k := range bad {
		delete(out, k)
	}
	return out
}

var nonClientRecv = map[string]bool{"Map": true, "map": true, "dict": true, "os.environ": true, "request.GET": true,
	"request.POST": true, "self.request.GET": true, "params": true, "headers": true, "searchParams": true, "cache": true,
	"localStorage": true, "sessionStorage": true, "config": true, "settings": true}

func clientCalls(repo string, f File, lang string, consts map[string]string) []ClientCall {
	var out []ClientCall
	lines := strings.Split(f.Content, "\n")
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") || strings.HasPrefix(t, "@") {
			continue // comments, and decorators (a server route, not a call)
		}
		if lang == "js" && expressRoute.MatchString(ln) && strings.Contains(ln, "req") {
			continue
		}
		method, arg := "", ""
		if lang == "js" {
			if m := jsFetch.FindStringSubmatch(ln); m != nil {
				method, arg = "GET", m[1]
				for j := i; j < len(lines) && j < i+8; j++ { // fetch(url, {\n  method: 'POST', …
					if mm := jsMethodOpt.FindStringSubmatch(lines[j]); mm != nil {
						method = strings.ToUpper(mm[1])
						break
					}
					if j > i && strings.Contains(lines[j], "fetch(") {
						break
					}
				}
			}
		}
		if arg == "" {
			m := clientCall.FindStringSubmatch(ln)
			if m == nil || nonClientRecv[m[1]] || strings.HasSuffix(m[1], "Map") {
				continue
			}
			method, arg = strings.ToUpper(m[2]), m[3]
		}
		u := resolveURL(firstArg(arg), lang, consts)
		if u == "" {
			continue
		}
		c := ClientCall{Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1), Method: method, URL: u, Text: t}
		if strings.ContainsAny(strings.ReplaceAll(u, "{}", ""), "{}") {
			c.Warning = "stray brace in the URL as written — it is sent literally, so the request path is not the one intended"
		}
		out = append(out, c)
	}
	return out
}

// firstArg is the text of a call's first argument.
func firstArg(s string) string {
	depth := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			q = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth == 0 {
				return s[:i]
			}
			depth--
		case ',':
			if depth == 0 {
				return s[:i]
			}
		}
	}
	return s
}

// resolveURL evaluates a URL expression made of literals, template literals,
// f-strings, `+` concatenation and named constants; "" unless the result is
// path-like (holds a '/' and a literal segment).
func resolveURL(expr, lang string, consts map[string]string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	var b strings.Builder
	for _, part := range splitConcat(expr) {
		part = strings.TrimSpace(part)
		switch {
		case part == "":
		case strings.HasPrefix(part, "f\"") || strings.HasPrefix(part, "f'"):
			b.WriteString(unquote(part[1:]))
		case part[0] == '`':
			// ${API_URLS.DOCUMENTS}${id}/apply/: a named constant inside a template
			b.WriteString(jsTmpl.ReplaceAllStringFunc(unquote(part), func(m string) string {
				if v, ok := lookupConst(strings.TrimSpace(m[2:len(m)-1]), consts); ok {
					return v
				}
				return "{}"
			}))
		case part[0] == '"' || part[0] == '\'':
			b.WriteString(unquote(part))
		default:
			if v, ok := lookupConst(part, consts); ok {
				b.WriteString(v)
			} else {
				b.WriteString("{}")
			}
		}
	}
	u := b.String()
	if !strings.Contains(u, "/") {
		return ""
	}
	for _, s := range segments(u) {
		if !wild(s) {
			return normURL(u)
		}
	}
	return ""
}

// lookupConst resolves A.B.KEY by its last two segments, then by KEY alone.
func lookupConst(ref string, consts map[string]string) (string, bool) {
	segs := strings.Split(ref, ".")
	if len(segs) >= 2 {
		if v, ok := consts[segs[len(segs)-2]+"."+segs[len(segs)-1]]; ok {
			return v, true
		}
	}
	v, ok := consts[segs[len(segs)-1]]
	return v, ok
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'' || s[0] == '`') && s[len(s)-1] == s[0] {
		return s[1 : len(s)-1]
	}
	return strings.Trim(s, "\"'`")
}

// splitConcat splits on top-level '+'.
func splitConcat(s string) []string {
	var out []string
	depth, start := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			q = c
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '+':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	return append(out, s[start:])
}
