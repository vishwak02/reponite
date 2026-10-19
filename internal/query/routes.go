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
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Route is one server endpoint definition.
type Route struct {
	Repo, Path string
	Line       int
	Method     string // GET/POST/…; "*" = any (Django, DRF viewsets, Go HandleFunc)
	Pattern    string // full path as mounted ("v1/store/labels/{}"), placeholders as {}
	Handler    string
	Framework  string
	// Partial: a mount prefix above this route could not be followed, so
	// Pattern is the path below it.
	Partial bool `json:",omitempty"`
	// Test: defined in test code (a test server), not served by the app.
	Test bool `json:",omitempty"`
	// Name is the route's URL name where the framework has one (Django
	// name=, a DRF router's basename-list / -detail / -<action>), which
	// reverse("name") in code and tests resolves.
	Name string `json:",omitempty"`
	// Class is the class whose instance serves the route when the handler
	// is a method it inherits (a DRF viewset using a mixin's @action): self
	// calls inside dispatch on it.
	Class string `json:",omitempty"`
}

// ClientCall is one HTTP call site whose URL could be resolved.
type ClientCall struct {
	Repo, Path string
	Line       int
	In         string
	Method     string
	URL        string // as resolved, placeholders as {}
	Raw        string // as resolved before normalization (scheme/host kept)
	Recv       string // the client object (ims, this.http, requests)
	Via        string // the gateway mount it goes through ("/ims → localhost:8002 (inventory)")
	Text       string
	// Warning: the URL is malformed as written (a stray brace from a
	// template-literal typo is sent literally: "/operations/}42/errors/").
	Warning string
	// RouteName: the URL is reverse("name") / url_for("endpoint"): the route
	// is found by its name, not its path.
	RouteName string `json:",omitempty"`
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
	Unmatched []ClientCall   // client calls (matching the filter) no indexed route serves
	Gateway   []GatewayMount // the gateway mounts used, with the repos bound to them
	Routes    int            // total routes found
	Clients   int            // total client calls found
	Note      string
	Meta      Meta
}

var (
	djInclude    = regexp.MustCompile(`^include\(\s*\(?\s*(?:["']([\w.]+)["']|([\w.]+))`)
	drfPlusEq    = regexp.MustCompile(`urlpatterns\s*\+=\s*(\w+)\.urls`)
	pyMethods    = regexp.MustCompile(`methods\s*=\s*\[([^\]]*)\]`)
	pyDef        = regexp.MustCompile(`^\s*(?:async\s+)?def\s+(\w+)`)
	expressRoute = regexp.MustCompile(`\b(app|router|server|routes?|api)\.(get|post|put|patch|delete|all)\(\s*['"` + "`" + `](/[^'"` + "`" + `]*)['"` + "`" + `]`)
	jsFetch      = regexp.MustCompile(`\bfetch\(\s*(.*)`)
	clientCall   = regexp.MustCompile(`\b([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*(?:\(\))?)\.(get|post|put|patch|delete|head|request)\s*(?:<[^()]*>)?\(\s*(.*)`)
	jsConst      = regexp.MustCompile(`(?:^|[\s,{(])([A-Za-z_$][\w$]*)\s*[:=]\s*(['"` + "`" + `])([^'"` + "`" + `\n]*/[^'"` + "`" + `\n]*)['"` + "`" + `]`)
	jsSegConst   = regexp.MustCompile(`(?:^|[\s,{(])([A-Z][A-Z0-9_]+)\s*[:=]\s*['"]([\w.:-]+)['"]`)
	jsObjStart   = regexp.MustCompile(`^\s*(?:export\s+)?(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*\{`)
	jsMethodOpt  = regexp.MustCompile(`method:\s*(?:['"` + "`" + `]|[\w$.]*\.)(\w+)`)
	// axios(config), instance.request(config), an adapter called with a
	// request object: { method: 'GET', url: '/v2/work', … }
	jsURLKey     = regexp.MustCompile(`^\s*url\s*:\s*(.+?),?\s*$`)
	jsConfigOpen = regexp.MustCompile(`\b([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*(?:<[^()]*>)?\(\s*\{\s*$`)
	pyConst      = regexp.MustCompile(`^\s*([A-Za-z_]\w*)\s*[:=]\s*f?["']([^"'\n]*/[^"'\n]*)["']`)
	placeholder  = regexp.MustCompile(`\$\{[^}]*\}|\{[^}]*\}|<[^>]*>|\(\?P<[^>]*>[^)]*\)|\([^)]*\)|\[[^\]]*\][+*]?|\\[dw][+*]?|\.[+*]`)
)

// RoutesOptions are the gateway hops between clients and servers.
type RoutesOptions struct {
	Gateways  []GatewayMount    // from ParseCaddyJSON
	Upstreams map[string]string // explicit upstream dial -> repo ("localhost:8003=wms")
	// Tests includes routes defined in test files (test servers).
	Tests bool
}

// Routes maps HTTP clients to routes fleet-wide at ref. filter (optional)
// keeps routes and calls whose path contains it; limit 0 = 50 links.
func Routes(s Store, repo, ref, filter string, limit int) RoutesResult {
	return RoutesWith(s, repo, ref, filter, limit, RoutesOptions{})
}

// RoutesWith is Routes through a gateway: a client whose URL (or client
// object) goes through a gateway mount is matched only against the routes of
// the repo behind that mount, with the mount's prefix removed.
func RoutesWith(s Store, repo, ref, filter string, limit int, opt RoutesOptions) RoutesResult {
	args := fmt.Sprintf("%q|%d|%+v", filter, limit, opt)
	return cachedFact(s, repo, ref, "routes", args, func() RoutesResult { return routesWith(s, repo, ref, filter, limit, opt) })
}

func routesWith(s Store, repo, ref, filter string, limit int, opt RoutesOptions) RoutesResult {
	if limit <= 0 {
		limit = 50
	}
	res := RoutesResult{Filter: filter, Meta: NewMeta(s, repo, ref)}
	var routes []Route
	var clients []ClientCall
	for _, rp := range reposFor(s, repo) {
		files := s.Files(rp, ref)
		for _, r := range serverRoutes(rp, files) {
			if !r.Test || opt.Tests {
				routes = append(routes, r) // a test file's server is not the app's
			}
		}
		consts := urlConstants(files)
		for _, f := range files {
			lang := codeExts[strings.ToLower(filepath.Ext(f.Path))]
			if lang == "py" || lang == "js" {
				clients = append(clients, clientCalls(rp, f, lang, consts)...)
			}
		}
	}
	res.Routes, res.Clients = len(routes), len(clients)
	mounts := bindMounts(append([]GatewayMount(nil), opt.Gateways...), reposFor(s, repo), opt.Upstreams)
	res.Gateway = mounts
	norm := strings.Trim(normURL(filter), "/")
	keep := func(p string) bool { return norm == "" || strings.Contains(strings.Trim(normURL(p), "/"), norm) }

	links := make([]RouteLink, len(routes))
	for i, r := range routes {
		links[i].Route = r
	}
	for _, c := range clients {
		if c.RouteName != "" {
			// reverse("name"): the routes of that name (url_for: the endpoint,
			// which is the view function's name)
			var hit []int
			for i, r := range routes {
				if c.Method != "" && r.Method != "*" && r.Method != "" && !strings.EqualFold(c.Method, r.Method) && !strings.EqualFold(c.Method, "request") {
					continue
				}
				if r.Name == c.RouteName || r.Name == "" && baseName(r.Handler) == c.RouteName && strings.Contains(c.Raw, "url_for") {
					hit = append(hit, i)
				}
			}
			if len(hit) == 0 {
				if keep(c.URL) {
					res.Unmatched = append(res.Unmatched, c)
				}
				continue
			}
			for _, i := range hit {
				links[i].Clients = append(links[i].Clients, c)
			}
			continue
		}
		best, idx := 0, []int(nil)
		cs := segments(c.URL)
		var only map[string]bool
		if m, path, ok := viaGateway(mounts, c.Raw, c.Recv); ok {
			c.Via = m.Prefix + " → " + m.Dial
			if len(m.Repos) > 0 {
				c.Via += " (" + strings.Join(m.Repos, ", ") + ")"
				only = map[string]bool{}
				for _, r := range m.Repos {
					only[r] = true
				}
				cs = segments(path)
			}
		}
		for i, r := range routes {
			if only != nil && !only[r.Repo] {
				continue
			}
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
		if len(idx) > 1 && only == nil {
			// A tie across services with no gateway hop to decide it: a client
			// calling its own service's route (a test client, a self-call) means
			// that one.
			var own []int
			for _, i := range idx {
				if routes[i].Repo == c.Repo {
					own = append(own, i)
				}
			}
			if len(own) > 0 {
				idx = own
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

// segCache memoizes segments: matching compares every client call with every
// route, so the same few hundred URLs are split thousands of times.
var segCache sync.Map

func segments(u string) []string {
	if v, ok := segCache.Load(u); ok {
		return v.([]string)
	}
	var out []string
	for _, s := range strings.Split(normURL(u), "/") {
		if s == "" {
			continue
		}
		if len(out) == 0 && strings.HasPrefix(s, "{}") && len(s) > 2 && !strings.Contains(s[2:], "{}") {
			// `${BASE_URL}v1/map`: a base URL variable glued to the path
			out = append(out, "{}", s[2:])
			continue
		}
		out = append(out, s)
	}
	segCache.Store(u, out)
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
	score, shape := 0, 0
	for i := 1; i <= n; i++ {
		c, r := client[len(client)-i], route[len(route)-i]
		switch {
		case wild(c) && wild(r):
			shape++ // a value where the route takes one
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
	// literal agreement first; on a tie, the route whose parameters sit
	// where the client passes values (/work/{id} is /work/{}, not
	// /work/clear_all)
	return score*16 + shape
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
	name             string // URL name (name=, or a router's basename-list/-detail)
	basename         string // a DRF registration's basename
}

// pyKwarg is the string value of keyword argument kw among args.
func pyKwarg(args []string, kw string) string {
	re := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(kw) + `\s*=\s*`)
	for _, a := range args {
		if loc := re.FindStringIndex(a); loc != nil {
			if v, ok := pyStringLit(strings.TrimSpace(a[loc[1]:])); ok {
				return v
			}
		}
	}
	return ""
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
		bn := pyKwarg(args, "basename")
		if bn == "" && len(args) >= 3 && !strings.Contains(args[2], "=") {
			bn, _ = pyStringLit(strings.TrimSpace(args[2]))
		}
		list, detail := "", ""
		if bn != "" {
			list, detail = bn+"-list", bn+"-detail"
		}
		d.entries = append(d.entries, djRoute{line: lineAt(m[0]), pattern: base + "/", handler: vs, router: router, name: list, basename: bn},
			djRoute{line: lineAt(m[0]), pattern: base + "/{}/", handler: vs, router: router, name: detail, basename: bn})
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
		d.entries = append(d.entries, djRoute{line: lineAt(m[0]), pattern: pat, handler: strings.TrimSpace(h), name: pyKwarg(args[2:], "name")})
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
		parseDjango(d, blankPyDocstrings(stripPyComments(f.Content)))
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
	viewsets := map[string][]drfMount{} // DRF viewset class -> mounted list routes
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
				out = append(out, Route{Repo: repo, Path: d.file.Path, Line: e.line, Method: "*", Name: e.name,
					Pattern: normURL(p + e.pattern), Handler: e.handler, Framework: "django"})
				if e.router != "" && !strings.HasSuffix(strings.TrimSuffix(e.pattern, "/"), "{}") {
					// the list route (a nested router's has {} for its parent)
					vs := e.handler[strings.LastIndex(e.handler, ".")+1:]
					viewsets[vs] = append(viewsets[vs], drfMount{pattern: normURL(p + e.pattern), basename: e.basename})
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
	pyClass      = regexp.MustCompile(`^class\s+(\w+)\s*[(:]`)
	pyClassBases = regexp.MustCompile(`^class\s+\w+\s*\(([^)]*)\)`)
	drfDetail    = regexp.MustCompile(`detail\s*=\s*(True|False)`)
	drfURLPath   = regexp.MustCompile(`url_path\s*=\s*r?["']([^"']*)["']`)
	drfURLName   = regexp.MustCompile(`url_name\s*=\s*r?["']([^"']*)["']`)
	// reverse("ns:name"), reverse_lazy(...), Flask url_for("bp.endpoint")
	pyReverse = regexp.MustCompile(`^\s*(reverse|reverse_lazy|url_for)\(\s*f?["']([^"']+)["']`)
)

// drfActions: a registered viewset's @action methods are routes of their own
// (list-level base/<path>/, or detail-level base/{}/<path>/) — its own and
// those it inherits from base classes and mixins of the repo.
type drfMount struct{ pattern, basename string }

func drfActions(repo string, files []File, viewsets map[string][]drfMount) []Route {
	type action struct {
		path, name, sub, urlName string
		line                     int
		methods                  []string
		detail                   bool
	}
	actions := map[string][]action{} // class -> its own @action methods
	bases := map[string][]string{}   // class -> base class names
	for _, f := range files {
		if filepath.Ext(f.Path) != ".py" {
			continue
		}
		lines := strings.Split(f.Content, "\n")
		class := ""
		for i := 0; i < len(lines); i++ {
			if m := pyClass.FindStringSubmatch(lines[i]); m != nil {
				class = m[1]
				if b := pyClassBases.FindStringSubmatch(lines[i]); b != nil {
					for _, x := range strings.Split(b[1], ",") {
						x = strings.TrimSpace(x)
						if j := strings.LastIndexByte(x, '.'); j >= 0 {
							x = x[j+1:]
						}
						if x != "" && !strings.Contains(x, "=") {
							bases[class] = append(bases[class], x)
						}
					}
				}
				continue
			}
			t := strings.TrimSpace(lines[i])
			if class == "" || !strings.HasPrefix(t, "@action") {
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
			a := action{path: f.Path, name: name, sub: name, urlName: strings.ReplaceAll(name, "_", "-"), line: i + 1, methods: []string{"GET"}}
			if m := drfURLPath.FindStringSubmatch(dec); m != nil {
				a.sub = m[1]
			}
			if m := drfURLName.FindStringSubmatch(dec); m != nil {
				a.urlName = m[1]
			}
			if m := pyMethods.FindStringSubmatch(dec); m != nil {
				a.methods = nil
				for _, x := range strings.Split(m[1], ",") {
					if x = strings.ToUpper(strings.Trim(strings.TrimSpace(x), `"'`)); x != "" {
						a.methods = append(a.methods, x)
					}
				}
			}
			if m := drfDetail.FindStringSubmatch(dec); m != nil && m[1] == "True" {
				a.detail = true
			}
			actions[class] = append(actions[class], a)
		}
	}
	var out []Route
	for class, mounts := range viewsets {
		// the class first, then its bases (depth-first, left to right):
		// a method a subclass redefines is the subclass's
		seen, taken := map[string]bool{}, map[string]bool{}
		var walk func(c string)
		var own []action
		var owner []string
		walk = func(c string) {
			if seen[c] {
				return
			}
			seen[c] = true
			for _, a := range actions[c] {
				if !taken[a.name] {
					taken[a.name] = true
					own = append(own, a)
					owner = append(owner, c)
				}
			}
			for _, b := range bases[c] {
				walk(b)
			}
		}
		walk(class)
		for k, a := range own {
			detail := ""
			if a.detail {
				detail = "{}/"
			}
			handler := class + "." + a.name
			if owner[k] != class {
				handler = owner[k] + "." + a.name // inherited: the code that runs
			}
			for _, b := range mounts {
				name := ""
				if b.basename != "" {
					name = b.basename + "-" + a.urlName
				}
				for _, method := range a.methods {
					out = append(out, Route{Repo: repo, Path: a.path, Line: a.line, Method: method, Name: name, Class: class,
						Pattern: normURL(strings.TrimSuffix(b.pattern, "/") + "/" + detail + a.sub + "/"), Handler: handler, Framework: "django"})
				}
			}
		}
	}
	return out
}

// --- FastAPI / Flask --------------------------------------------------------

// --- Express / Go -----------------------------------------------------------

// --- clients ----------------------------------------------------------------

// urlConstants are the repo's named path-like strings (URLS.CREATE_LABEL:
// 'v1/store/labels/', ORDERS_URL = "/api/orders"), by name. A name bound
// to two different paths is dropped.
func urlConstants(files []File) map[string]string {
	out := map[string]string{}
	bad := map[string]bool{}
	put := func(k, v string) {
		if len(k) < 3 {
			return // minified code's one-letter bindings are not URL constants
		}
		if old, ok := out[k]; ok && old != v {
			bad[k] = true
		}
		out[k] = v
	}
	for _, f := range files {
		switch codeExts[strings.ToLower(filepath.Ext(f.Path))] {
		case "js":
			if minified(f) {
				continue
			}
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
				// a path segment held by a constant (ROUTES = { LOGIN: 'login' }),
				// used as `/${ROUTES.LOGIN}`: UPPER_CASE names only
				for _, m := range jsSegConst.FindAllStringSubmatch(ln, -1) {
					if obj != "" && strings.ToUpper(obj) == obj {
						put(obj+"."+m[1], m[2])
					} else if obj == "" {
						put(m[1], m[2])
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

// minified: bundled/minified JavaScript — one-letter names and kilobyte
// lines whose `x.get(s)` calls are not HTTP.
func minified(f File) bool {
	if strings.Contains(f.Path, ".min.") {
		return true
	}
	lines := strings.Count(f.Content, "\n") + 1
	return len(f.Content)/lines > 200
}

func clientCalls(repo string, f File, lang string, consts map[string]string) []ClientCall {
	if lang == "js" && minified(f) {
		return nil
	}
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
		method, arg, recv := "", "", ""
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
		if arg == "" && lang == "js" {
			if m := jsURLKey.FindStringSubmatch(ln); m != nil {
				// the object this key is in must be the argument of a call
				for j := i - 1; j >= 0 && j >= i-8; j-- {
					o := jsConfigOpen.FindStringSubmatch(lines[j])
					if o == nil && strings.Contains(lines[j], "}") {
						break // a previous object closed: not ours
					}
					if o != nil && !nonClientRecv[o[1]] {
						method, arg, recv = "GET", m[1], o[1]
						for k := j; k < len(lines) && k < j+12; k++ {
							if mm := jsMethodOpt.FindStringSubmatch(lines[k]); mm != nil {
								method = strings.ToUpper(mm[1])
								break
							}
							if k > j && strings.HasPrefix(strings.TrimSpace(lines[k]), "})") {
								break
							}
						}
						break
					}
					if o != nil {
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
			method, arg, recv = strings.ToUpper(m[2]), m[3], m[1]
		}
		if strings.TrimSpace(arg) == "" && i+1 < len(lines) {
			arg = strings.TrimSpace(lines[i+1]) // client.post(\n    reverse("x"), …
		}
		if lang == "py" {
			if rv := pyReverse.FindStringSubmatch(firstArg(arg)); rv != nil {
				name := rv[2]
				if k := strings.LastIndexAny(name, ":."); k >= 0 {
					name = name[k+1:] // a namespace or blueprint prefix
				}
				out = append(out, ClientCall{Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1), Method: method,
					URL: "{" + rv[1] + ":" + rv[2] + "}", Raw: rv[0], Recv: recv, Text: t, RouteName: name})
				continue
			}
		}
		raw := resolveURL(firstArg(arg), lang, consts)
		if raw == "" {
			continue
		}
		u := normURL(raw)
		c := ClientCall{Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1), Method: method, URL: u, Raw: raw, Recv: recv, Text: t}
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
// f-strings, `+` concatenation and named constants, placeholders as {} and
// scheme/host kept; "" unless the result is path-like (holds a '/' and a
// literal segment).
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
			return placeholder.ReplaceAllString(u, "{}")
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
