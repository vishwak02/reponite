// ui_pages.go finds an app's PAGES — its client-side routes — and the
// component each one renders: react-router (<Route path element|component|
// render> trees, nested routes, createBrowserRouter/useRoutes objects),
// vue-router / any `{ path, component }` route table (lazy imports and
// module-federation exposed names resolved), Next.js pages/ and app/,
// SvelteKit src/routes and Nuxt pages/. Pure over Store.Files (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// UIRef points at a component definition.
type UIRef struct {
	Repo, Path string
	Line       int
	Handle     string // drill-in id for brief/context/ui
	Resolution string // local | import | name | file
}

// UIPage is one client route and the component it renders.
type UIPage struct {
	Route     string // as mounted, parameters as :name
	Framework string // react-router | vue-router | route-table | next-pages | next-app | sveltekit | nuxt
	Component string // the component named at the route ("" for a redirect)
	Redirect  string // a redirect route's target
	Repo      string
	Path      string // where the route is declared
	Line      int
	Def       *UIRef // where Component is defined (nil: not found in the indexed repos)
	def       *uiDef
}

func refOf(d *uiDef, how string) *UIRef {
	if d == nil {
		return nil
	}
	return &UIRef{Repo: d.File.Repo, Path: d.File.F.Path, Line: d.Start, Handle: d.Handle(), Resolution: how}
}

var (
	routeTag     = regexp.MustCompile(`^\w*Route$`)
	routeKeys    = map[string]bool{"path": true, "component": true, "element": true, "Component": true, "lazy": true, "children": true, "redirect": true, "loadComponent": true, "loadChildren": true, "index": true, "name": true, "routes": true, "render": true}
	routeHasComp = []string{"component", "element", "Component", "lazy", "children", "redirect", "loadComponent", "loadChildren", "render", "routes"}
	nextConfig   = regexp.MustCompile(`(^|/)next\.config\.(js|mjs|cjs|ts)$`)
	nuxtConfig   = regexp.MustCompile(`(^|/)nuxt\.config\.(js|mjs|ts)$`)
	nextPage     = regexp.MustCompile(`^(?:src/)?pages/(.+)\.(jsx?|tsx?)$`)
	nextAppPage  = regexp.MustCompile(`^(?:src/)?app/(?:(.*)/)?page\.(jsx?|tsx?|mdx)$`)
	svelteKit    = regexp.MustCompile(`^(?:.*/)?src/routes/(?:(.*)/)?\+page\.svelte$`)
	nuxtPage     = regexp.MustCompile(`^(?:src/)?pages/(.+)\.vue$`)
	routerFile   = regexp.MustCompile(`\b(createBrowserRouter|createHashRouter|createMemoryRouter|useRoutes|RouteObject|createRoutesFromElements)\b`)
	vueRouter    = regexp.MustCompile(`\b(createRouter|VueRouter|RouteRecordRaw|vue-router)\b`)
)

// pages lists every client route found in the repos.
func (c *uiCtx) pages() []UIPage {
	var out []UIPage
	for _, r := range c.repoList() {
		consts := c.consts(r.Name)
		fileBased := r.fileRoutes()
		out = append(out, fileBased...)
		for _, uf := range r.Files {
			if IsTestPath(uf.F.Path) {
				continue
			}
			if !uf.SFC && strings.Contains(uf.Code, "Route") {
				out = append(out, r.jsxRoutes(uf, consts)...)
			}
			if strings.Contains(uf.Script, "path") {
				out = append(out, r.objectRoutes(uf, consts)...)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Route != out[j].Route {
			return out[i].Route < out[j].Route
		}
		return out[i].Path < out[j].Path
	})
	return out
}

// routeParam turns file-route segments into :params ([id] -> :id,
// [...slug] -> :slug*, [[opt]] -> :opt?) and drops (group) and @slot dirs.
func routeParam(rel string) string {
	var segs []string
	for _, s := range strings.Split(rel, "/") {
		switch {
		case s == "" || s == "index":
		case strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")"), strings.HasPrefix(s, "@"):
		case strings.HasPrefix(s, "[[...") && strings.HasSuffix(s, "]]"):
			segs = append(segs, ":"+s[5:len(s)-2]+"*?")
		case strings.HasPrefix(s, "[[") && strings.HasSuffix(s, "]]"):
			segs = append(segs, ":"+s[2:len(s)-2]+"?")
		case strings.HasPrefix(s, "[...") && strings.HasSuffix(s, "]"):
			segs = append(segs, ":"+s[4:len(s)-1]+"*")
		case strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"):
			segs = append(segs, ":"+s[1:len(s)-1])
		case strings.HasPrefix(s, "_") && len(s) > 1:
			segs = append(segs, ":"+s[1:]) // Nuxt 2 dynamic segment
		default:
			segs = append(segs, s)
		}
	}
	return "/" + strings.Join(segs, "/")
}

// fileRoutes: routers whose routes are the file tree.
func (r *uiRepo) fileRoutes() []UIPage {
	next, nuxt := false, false
	for _, uf := range r.Files {
		if nextConfig.MatchString(uf.F.Path) || strings.Contains(uf.Script, "from 'next/") || strings.Contains(uf.Script, `from "next/`) {
			next = true
		}
		if nuxtConfig.MatchString(uf.F.Path) {
			nuxt = true
		}
	}
	var out []UIPage
	add := func(uf *uiFile, route, fw string) {
		d := r.fileDefault(uf.F.Path)
		p := UIPage{Route: route, Framework: fw, Repo: r.Name, Path: uf.F.Path, Line: 1, def: d, Def: refOf(d, "file")}
		if d != nil {
			p.Component, p.Line = d.Name, d.Start
		}
		out = append(out, p)
	}
	for _, uf := range r.Files {
		p := uf.F.Path
		if IsTestPath(p) {
			continue
		}
		if m := svelteKit.FindStringSubmatch(p); m != nil {
			add(uf, routeParam(m[1]), "sveltekit")
			continue
		}
		if next {
			if m := nextAppPage.FindStringSubmatch(p); m != nil {
				add(uf, routeParam(m[1]), "next-app")
				continue
			}
			if m := nextPage.FindStringSubmatch(p); m != nil {
				base := path.Base(m[1])
				if strings.HasPrefix(base, "_") || strings.HasPrefix(m[1], "api/") {
					continue // _app/_document wrap every page; api/ are server routes
				}
				add(uf, routeParam(m[1]), "next-pages")
				continue
			}
		}
		if nuxt {
			if m := nuxtPage.FindStringSubmatch(p); m != nil {
				add(uf, routeParam(m[1]), "nuxt")
			}
		}
	}
	return out
}

// joinRoute mounts a child route path under its parent's.
func joinRoute(parent, child string) string {
	switch {
	case strings.HasPrefix(child, "/"):
		return child
	case child == "":
		if parent == "" {
			return "/"
		}
		return parent
	case parent == "" || parent == "/":
		return "/" + child
	}
	return strings.TrimSuffix(parent, "/") + "/" + child
}

// routePathExpr evaluates a route path written as a literal, a template
// literal or a named constant; what it cannot evaluate stays visible as {EXPR}.
func routePathExpr(expr string, consts map[string]string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	switch expr[0] {
	case '"', '\'':
		return unquote(expr)
	case '`':
		return jsTmpl.ReplaceAllStringFunc(unquote(expr), func(m string) string {
			ref := strings.TrimSpace(m[2 : len(m)-1])
			if v, ok := lookupConst(ref, consts); ok {
				return v
			}
			return "{" + ref + "}"
		})
	}
	if v, ok := lookupConst(expr, consts); ok {
		return v
	}
	return "{" + expr + "}"
}

type routeTagInfo struct {
	t          uiTag
	childFrom  int
	childTo    int
	path       string
	parent     int
	hasPath    bool
	index      bool
	component  string
	redirect   string
	resolvable bool
}

// jsxRoutes reads <Route …> trees (react-router v4–v6 and *Route wrappers).
func (r *uiRepo) jsxRoutes(uf *uiFile, consts map[string]string) []UIPage {
	tags := scanTags(uf.Code, true)
	var routes []*routeTagInfo
	for _, t := range tags {
		if !routeTag.MatchString(t.Name) {
			continue
		}
		ri := &routeTagInfo{t: t, parent: -1, childFrom: t.End, childTo: t.End}
		if !t.SelfClosing {
			ri.childTo = closingTag(uf.Code, t.Name, t.End)
		}
		for _, a := range attrs(t.Attrs) {
			switch a.Name {
			case "path":
				ri.hasPath = true
				if a.Expr {
					ri.path = routePathExpr(a.Value, consts)
				} else {
					ri.path = a.Value
				}
			case "index":
				ri.index = true
			case "element", "render", "component", "Component", "children":
				name, redirect := leafComponent(a.Value)
				if !a.Expr {
					continue
				}
				if redirect != "" || name == "Redirect" || name == "Navigate" {
					ri.redirect = redirect
					if ri.redirect == "" {
						ri.redirect = "?"
					}
					continue
				}
				if name != "" {
					ri.component = name
				}
			case "to":
				if t.Name == "Route" {
					ri.redirect = a.Value
				}
			}
		}
		routes = append(routes, ri)
	}
	if len(routes) == 0 {
		return nil
	}
	// Parent: the innermost enclosing <Route>…</Route>.
	for i, ri := range routes {
		best := -1
		for j, p := range routes {
			if i == j || p.t.SelfClosing {
				continue
			}
			if ri.t.Off >= p.childFrom && ri.t.Off < p.childTo && (best < 0 || p.childFrom > routes[best].childFrom) {
				best = j
			}
		}
		ri.parent = best
	}
	// A v5 <Route path><Page/></Route>: the element is its child content.
	for _, ri := range routes {
		if ri.component != "" || ri.redirect != "" || ri.t.SelfClosing {
			continue
		}
		inner := uf.Code[ri.childFrom:ri.childTo]
		hasChildRoute := false
		for _, c := range routes {
			if c.parent >= 0 && routes[c.parent] == ri {
				hasChildRoute = true
			}
		}
		if hasChildRoute {
			continue
		}
		if name, redirect := leafComponent(inner); redirect != "" || name == "Redirect" || name == "Navigate" {
			ri.redirect = redirect
		} else {
			ri.component = name
		}
	}
	full := func(i int) string {
		var chain []*routeTagInfo
		for k := i; k >= 0; k = routes[k].parent {
			chain = append([]*routeTagInfo{routes[k]}, chain...)
		}
		p := ""
		for _, c := range chain {
			if c.hasPath {
				p = joinRoute(p, c.path)
			}
		}
		if p == "" {
			p = "/"
		}
		return p
	}
	fw := "react-router"
	var out []UIPage
	for i, ri := range routes {
		if !ri.hasPath && !ri.index {
			if ri.component == "" || ri.parent < 0 {
				continue // a pathless layout route with no element of its own
			}
		}
		if ri.component == "" && ri.redirect == "" {
			continue // layout-only parent: its children are the pages
		}
		p := UIPage{Route: full(i), Framework: fw, Component: ri.component, Redirect: ri.redirect, Repo: r.Name, Path: uf.F.Path, Line: uf.Lines.line(ri.t.Off)}
		if ri.component != "" {
			d, how, _ := r.resolve(uf, ri.component)
			p.def, p.Def = d, refOf(d, how)
		}
		out = append(out, p)
	}
	return out
}

// closingTag finds the offset of the </name> closing an element opened just
// before from, counting nested same-name elements.
func closingTag(code, name string, from int) int {
	open, close := "<"+name, "</"+name+">"
	depth := 1
	for i := from; i < len(code); {
		o := strings.Index(code[i:], open)
		c := strings.Index(code[i:], close)
		if c < 0 {
			return len(code)
		}
		if o >= 0 && o < c {
			// a nested opening of the same element (not a longer name)
			k := i + o + len(open)
			if k < len(code) && (code[k] == ' ' || code[k] == '>' || code[k] == '\n' || code[k] == '/' || code[k] == '\t') {
				if end, self := tagEnd(code, k); end > 0 && !self {
					depth++
				}
			}
			i = k
			continue
		}
		depth--
		if depth == 0 {
			return i + c
		}
		i += c + len(close)
	}
	return len(code)
}

// leafComponent picks the component a route element expression renders: the
// innermost element outside attribute values (wrappers such as
// <ErrorBoundary><Suspense fallback={…}><Page/></Suspense></ErrorBoundary>
// reduce to Page), or the capitalized argument of a wrapper call
// (render={withBoundary(Page)}). redirect is a <Redirect>/<Navigate> target.
func leafComponent(expr string) (name, redirect string) {
	tags := scanTags(expr, true)
	var outer []uiTag
	for _, t := range tags {
		inAttr := false
		for _, o := range tags {
			if t.Off > o.Off && t.Off < o.End {
				inAttr = true
				break
			}
		}
		if !inAttr {
			outer = append(outer, t)
		}
	}
	for _, t := range outer {
		if t.Name == "Redirect" || t.Name == "Navigate" {
			for _, a := range attrs(t.Attrs) {
				if a.Name == "to" {
					redirect = a.Value
					if a.Expr {
						redirect = strings.Trim(a.Value, "`'\"")
					}
				}
			}
			if redirect == "" {
				redirect = "?"
			}
			return t.Name, redirect
		}
	}
	for i := len(outer) - 1; i >= 0; i-- {
		if n := componentTagName(outer[i].Name, false); n != "" && !routeTag.MatchString(n) && n != "Routes" && n != "Switch" {
			if strings.HasPrefix(n, "React.") {
				continue
			}
			return n, ""
		}
	}
	if len(tags) == 0 {
		ids := identRe.FindAllString(expr, -1)
		for i := len(ids) - 1; i >= 0; i-- {
			if isUpperIdent(ids[i]) && ids[i] != "React" {
				return ids[i], ""
			}
		}
	}
	return "", ""
}

// --- object route tables ------------------------------------------------------

type jsObj struct {
	open, close int
	parent      int            // offset of the enclosing bracket, -1 at top level
	keys        map[string]int // key -> offset where its value starts
}

// scanObjects records every {…} in code with its parent bracket and its
// top-level route-relevant keys.
func scanObjects(code string) map[int]*jsObj {
	objs := map[int]*jsObj{}
	parentOf := map[int]int{}
	var stack []int
	prevSig := byte(0)
	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case c == '"' || c == '\'':
			for i++; i < len(code) && code[i] != c && code[i] != '\n'; i++ {
				if code[i] == '\\' {
					i++
				}
			}
			prevSig = 'x'
			continue
		case c == '`':
			i = skipTemplate(code, i)
			prevSig = 'x'
			continue
		case c == '{' || c == '(' || c == '[':
			p := -1
			if len(stack) > 0 {
				p = stack[len(stack)-1]
			}
			parentOf[i] = p
			if c == '{' {
				objs[i] = &jsObj{open: i, parent: p, keys: map[string]int{}}
			}
			stack = append(stack, i)
		case c == '}' || c == ')' || c == ']':
			if len(stack) > 0 {
				o := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if ob := objs[o]; ob != nil {
					ob.close = i
				}
			}
		case (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') && (prevSig == '{' || prevSig == ',') && len(stack) > 0:
			top := stack[len(stack)-1]
			if ob := objs[top]; ob != nil {
				j := i
				for j < len(code) && (code[j] == '_' || code[j] >= 'a' && code[j] <= 'z' || code[j] >= 'A' && code[j] <= 'Z' || code[j] >= '0' && code[j] <= '9') {
					j++
				}
				key := code[i:j]
				k := j
				for k < len(code) && (code[k] == ' ' || code[k] == '\t') {
					k++
				}
				if k < len(code) && code[k] == ':' && routeKeys[key] {
					ob.keys[key] = k + 1
				} else if routeKeys[key] && (k < len(code) && (code[k] == ',' || code[k] == '}' || code[k] == '\n')) {
					ob.keys[key] = -1 // shorthand { path, component }
				}
				i = j - 1
				prevSig = 'x'
				continue
			}
		}
		if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
			prevSig = c
		}
	}
	_ = parentOf
	return objs
}

// objValue is the text of key's value in an object.
func objValue(code string, o *jsObj, key string) (string, bool) {
	at, ok := o.keys[key]
	if !ok {
		return "", false
	}
	if at < 0 {
		return key, true
	}
	end := o.close
	if end <= at {
		end = len(code)
	}
	return strings.TrimSpace(firstArg(code[at:end])), true
}

// objectRoutes reads { path, component|element|lazy|children … } route
// tables: react-router objects, vue-router, Angular-style tables and plain
// arrays a host app loads (module federation).
func (r *uiRepo) objectRoutes(uf *uiFile, consts map[string]string) []UIPage {
	code := uf.Script
	objs := scanObjects(code)
	isRoute := func(o *jsObj) bool {
		if _, ok := o.keys["path"]; !ok {
			return false
		}
		for _, k := range routeHasComp {
			if _, ok := o.keys[k]; ok {
				return true
			}
		}
		return false
	}
	fw := "route-table"
	switch {
	case routerFile.MatchString(code):
		fw = "react-router"
	case vueRouter.MatchString(code):
		fw = "vue-router"
	case strings.Contains(code, "@angular/router"):
		fw = "angular-router"
	}
	// parentRoute: the route object whose children: [ … ] holds o.
	parentRoute := func(o *jsObj) *jsObj {
		arr := o.parent
		if arr < 0 || code[arr] != '[' {
			return nil
		}
		for _, p := range objs {
			for _, k := range []string{"children", "routes"} {
				if at, ok := p.keys[k]; ok && at >= 0 {
					t := at
					for t < len(code) && (code[t] == ' ' || code[t] == '\n' || code[t] == '\t') {
						t++
					}
					if t == arr && isRoute(p) {
						return p
					}
				}
			}
		}
		return nil
	}
	var out []UIPage
	var opens []int
	for o := range objs {
		opens = append(opens, o)
	}
	sort.Ints(opens)
	for _, op := range opens {
		o := objs[op]
		if !isRoute(o) {
			continue
		}
		pv, _ := objValue(code, o, "path")
		route := routePathExpr(pv, consts)
		for p := parentRoute(o); p != nil; p = parentRoute(p) {
			ppv, _ := objValue(code, p, "path")
			route = joinRoute(routePathExpr(ppv, consts), route)
		}
		if !strings.HasPrefix(route, "/") && !strings.HasPrefix(route, "{") {
			route = "/" + route
		}
		page := UIPage{Route: route, Framework: fw, Repo: r.Name, Path: uf.F.Path, Line: uf.Lines.line(op)}
		if v, ok := objValue(code, o, "redirect"); ok {
			page.Redirect = strings.Trim(v, "'\"`")
		}
		var d *uiDef
		how := ""
		for _, k := range []string{"component", "element", "Component", "lazy", "loadComponent", "render"} {
			v, ok := objValue(code, o, k)
			if !ok {
				continue
			}
			page.Component, d, how = r.routeValue(uf, k, v)
			if page.Component != "" {
				break
			}
		}
		if page.Component == "" && page.Redirect == "" {
			continue // a parent whose children are the pages
		}
		page.def, page.Def = d, refOf(d, how)
		if d != nil && page.Component == "" {
			page.Component = d.Name
		}
		out = append(out, page)
	}
	return out
}

// routeValue resolves a route table's component value: an identifier, an
// element, a lazy import, or a module-federation exposed name.
func (r *uiRepo) routeValue(uf *uiFile, key, v string) (string, *uiDef, string) {
	if m := dynImport.FindStringSubmatch(v); m != nil {
		p, ok := r.resolveSpec(uf.F.Path, m[1])
		if !ok {
			return "", nil, ""
		}
		var d *uiDef
		if after := regexp.MustCompile(`\.then\(\s*\(?\s*\w+\s*\)?\s*=>\s*\w+\.([\w$]+)`).FindStringSubmatch(v); after != nil {
			d = r.exported(p, after[1], 0)
		} else if key == "lazy" {
			if d = r.exported(p, "Component", 0); d == nil {
				d = r.fileDefault(p)
			}
		} else {
			d = r.fileDefault(p)
		}
		if d == nil {
			return fileComponentName(p), nil, ""
		}
		return d.Name, d, resImport
	}
	if v != "" && (v[0] == '\'' || v[0] == '"' || v[0] == '`') {
		name := unquote(v)
		if p, ok := r.exposes[name]; ok {
			if d := r.fileDefault(r.lookupPath(p)); d != nil {
				return name, d, "exposes"
			}
		}
		if defs := r.ByName[name]; len(defs) > 0 {
			return name, closest(defs, uf.F.Path), resName
		}
		return name, nil, ""
	}
	name, redirect := leafComponent(v)
	if redirect != "" {
		return "", nil, ""
	}
	if name == "" {
		return "", nil, ""
	}
	d, how, _ := r.resolve(uf, name)
	return name, d, how
}

// lookupPath maps a path written in a config (./src/x/Y.tsx, src/x) to an
// indexed file.
func (r *uiRepo) lookupPath(p string) string {
	p = strings.TrimPrefix(path.Clean(p), "./")
	if _, ok := r.ByPath[p]; ok {
		return p
	}
	if q, ok := r.noExt[strings.TrimSuffix(p, path.Ext(p))]; ok {
		return q
	}
	if q, ok := r.noExt[p+"/index"]; ok {
		return q
	}
	return p
}
