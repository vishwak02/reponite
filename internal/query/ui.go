// ui.go answers `reponite ui`: the frontend view of a codebase. With no
// target it lists the app's PAGES (client routes -> component); with a page
// route or a component it returns the COMPONENT TREE, budget-bounded, and per
// component its props, state, effects, event handlers and the API calls each
// one makes — linked through `routes` to the server endpoint that serves it,
// so an answer reads "button Save -> handleSave -> POST /api/orders ->
// OrdersView.create". With Uses it lists where a component is rendered.
// UIEdges exposes the same facts as a flat edge list for other queries
// (trace). Pure over Store.Files + Store.Snapshot (ADR-018); medium
// confidence by nature (text idioms, name-resolved calls) and said so.
package query

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// DefaultUIBudget is the token budget when the caller passes <= 0.
const DefaultUIBudget = 3000

// UIAPICall is an HTTP call reached from a handler/effect/component.
type UIAPICall struct {
	Method, URL string
	Repo, Path  string
	Line        int
	Via         []string // the call chain from the handler to the function making the call
	Routes      []UIRoute
}

// UIRoute is the server endpoint an API call reaches (from routes).
type UIRoute struct {
	Repo, Path string
	Line       int
	Method     string
	Pattern    string
	Handler    string // the view/handler, DRF viewset action inferred (OrdersViewSet.create)
	Framework  string
	Via        string // gateway mount it goes through
}

// UIComponent is one component in a tree.
type UIComponent struct {
	Name       string
	Kind       string // function | class | sfc
	Framework  string // react | vue | svelte
	Repo, Path string
	Line       int
	EndLine    int
	Handle     string
	Resolution string // how the parent's usage was tied to this definition
	Props      []UIProp
	State      []UIState
	Effects    []UIEffect
	Handlers   []UIHandler
	API        []UIAPICall // calls in the component body outside any handler/effect
	Renders    []UIRender
	Lib        []string // components rendered from packages (or unresolved)
	Children   []*UIComponent
}

// UIUse is one place a component is rendered or routed to.
type UIUse struct {
	Repo, Path string
	Line       int
	In         string // enclosing component (or symbol)
	Kind       string // render | prop | route | lazy
	Text       string
	Resolution string // local | import | name — how this site's name resolves to the component
}

// UIResult is the answer to `ui`.
type UIResult struct {
	Target     string
	Mode       string // pages | tree | uses | not_found
	Pages      []UIPage
	Page       *UIPage // the route the tree was asked by
	Root       *UIComponent
	Uses       []UIUse
	Candidates []UIRef // other definitions with the same name
	Components int     // components expanded in the tree
	Truncated  bool
	Next       string
	Note       string
	Meta       Meta
}

// UIOptions tune `ui`.
type UIOptions struct {
	Budget int
	Uses   bool          // list where Target is rendered instead of its tree
	Routes RoutesOptions // gateways for linking API calls to server routes
}

// uiCtx caches the per-call indexes.
type uiCtx struct {
	s       Store
	repo    string
	ref     string
	opt     UIOptions
	repos   map[string]*uiRepo
	order   []string
	constsM map[string]map[string]string
	// API linkage, built on first use.
	apiReady bool
	clients  map[string][]ClientCall // repo\x00dir\x00fn -> calls in that function
	byFile   map[string][]ClientCall // repo\x00path -> calls in that file
	routeOf  map[string][]UIRoute    // repo\x00path\x00line -> routes it reaches
	byBase   map[string]map[string][]string
	reach    map[string][]UIAPICall // memo: repo\x00qid -> calls reachable from it
}

func newUICtx(s Store, repo, ref string, opt UIOptions) *uiCtx {
	c := &uiCtx{s: s, repo: repo, ref: ref, opt: opt, repos: map[string]*uiRepo{}, constsM: map[string]map[string]string{}, reach: map[string][]UIAPICall{}}
	for _, rp := range reposFor(s, repo) {
		r := buildUIRepo(s, rp, ref)
		if len(r.Files) == 0 {
			continue
		}
		c.repos[rp] = r
		c.order = append(c.order, rp)
	}
	sort.Strings(c.order)
	return c
}

func (c *uiCtx) repoList() []*uiRepo {
	out := make([]*uiRepo, 0, len(c.order))
	for _, n := range c.order {
		out = append(out, c.repos[n])
	}
	return out
}

func (c *uiCtx) consts(repo string) map[string]string {
	if m, ok := c.constsM[repo]; ok {
		return m
	}
	files := c.s.Files(repo, c.ref)
	m := routeConstants(files, urlConstants(files))
	c.constsM[repo] = m
	return m
}

const uiNote = "frontend facts are read from source idioms (JSX/templates, hooks, route tables) and API calls are followed through the call graph by name — medium confidence; a component built at runtime or a URL the code does not name is missed"

// UI answers `ui [target]` over the repos (FleetRepo = all) at ref.
func UI(s Store, repo, ref, target string, opt UIOptions) UIResult {
	if opt.Budget <= 0 {
		opt.Budget = DefaultUIBudget
	}
	res := UIResult{Target: target, Meta: NewMeta(s, repo, ref)}
	c := newUICtx(s, repo, ref, opt)
	if len(c.order) == 0 {
		res.Mode = "not_found"
		res.Note = "no frontend source (.js/.jsx/.ts/.tsx/.vue/.svelte) indexed at this ref — the UI repo may not be indexed, or the framework is unrecognized"
		return res
	}
	pages := c.pages()
	switch {
	case target == "":
		res.Mode = "pages"
		spent := 0
		for i, p := range pages {
			cost := estTokens(p.Route+p.Component+p.Path+p.Repo) + 12
			if p.Def != nil {
				cost += estTokens(p.Def.Path+p.Def.Handle) + 6
			}
			if spent+cost > opt.Budget && i > 0 {
				res.Truncated = true
				res.Next = "raise --budget (" + itoa(opt.Budget*2) + ") to list the remaining " + itoa(len(pages)-i) + " routes, or ask for one: reponite ui <route>"
				break
			}
			spent += cost
			res.Pages = append(res.Pages, p)
		}
		res.Note = uiNote
		if len(pages) == 0 {
			res.Note = "no client routes recognized (react-router, vue-router/route tables, Next.js, SvelteKit, Nuxt) — unrecognized router; ask for a component by name instead"
		}
		return res
	case opt.Uses:
		res.Mode = "uses"
		res.Uses = c.uses(target, pages)
		res.Note = "render sites by tag/route/lazy-import name, each with how it resolves to a definition (import = the site imports it; name = only the name matches)"
		if len(res.Uses) == 0 {
			res.Note = "no render site of " + target + " found in the indexed UI repos"
		}
		return res
	}
	var root *uiDef
	how := ""
	if strings.HasPrefix(target, "/") {
		if p := matchPage(pages, target); p != nil {
			pc := *p
			res.Page = &pc
			root, how = p.def, "route"
			if root == nil {
				res.Mode = "not_found"
				res.Note = "route " + p.Route + " renders " + p.Component + ", whose definition is not in the indexed repos"
				return res
			}
		}
	}
	if root == nil {
		var alts []*uiDef
		root, alts = c.findDef(target)
		for _, a := range alts {
			res.Candidates = append(res.Candidates, *refOf(a, resName))
		}
	}
	if root == nil {
		res.Mode = "not_found"
		res.Note = "no page route or component named " + target + " — run `reponite ui` for the pages"
		return res
	}
	res.Mode = "tree"
	res.Root, res.Components, res.Truncated, res.Next = c.tree(root, how, opt.Budget)
	res.Note = uiNote
	return res
}

// matchPage finds the page for a route, parameters matched by position.
func matchPage(pages []UIPage, route string) *UIPage {
	norm := func(s string) []string {
		var out []string
		for _, seg := range strings.Split(strings.Trim(s, "/"), "/") {
			if strings.HasPrefix(seg, ":") || strings.HasPrefix(seg, "[") || strings.HasPrefix(seg, "{") {
				seg = ":"
			}
			out = append(out, seg)
		}
		return out
	}
	want := norm(route)
	var best *UIPage
	for i := range pages {
		got := norm(pages[i].Route)
		if len(got) != len(want) {
			continue
		}
		ok := true
		for k := range got {
			if got[k] != want[k] && got[k] != ":" && want[k] != ":" {
				ok = false
				break
			}
		}
		if ok && (best == nil || best.def == nil && pages[i].def != nil) {
			best = &pages[i]
		}
	}
	return best
}

// findDef finds a component by name or handle (dir.Name, or a file path).
func (c *uiCtx) findDef(target string) (*uiDef, []*uiDef) {
	var all []*uiDef
	for _, r := range c.repoList() {
		if uf := r.ByPath[target]; uf != nil {
			if d := r.fileDefault(target); d != nil {
				return d, nil
			}
			if len(uf.defs) > 0 {
				return uf.defs[0], nil
			}
		}
		name := target
		if i := strings.LastIndexByte(target, '.'); i >= 0 && strings.Contains(target, "/") {
			name = target[i+1:]
		}
		for _, d := range r.ByName[name] {
			if d.Handle() == target {
				return d, nil
			}
			all = append(all, d)
		}
	}
	if len(all) == 0 {
		return nil, nil
	}
	// Prefer the definition other code renders or routes to (a page beats a
	// story/test double of the same name), then the shortest path.
	sort.SliceStable(all, func(i, j int) bool {
		ti, tj := IsTestPath(all[i].File.F.Path), IsTestPath(all[j].File.F.Path)
		if ti != tj {
			return !ti
		}
		return len(all[i].File.F.Path) < len(all[j].File.F.Path)
	})
	return all[0], all[1:]
}

// frameworkOf names a definition's framework.
func frameworkOf(d *uiDef) string {
	switch d.File.Ext {
	case ".vue":
		return "vue"
	case ".svelte":
		return "svelte"
	}
	return "react"
}

// analyze reads one definition into a component node (children not expanded).
func (c *uiCtx) analyze(d *uiDef, how string) *UIComponent {
	r := c.repos[d.File.Repo]
	n := &UIComponent{Name: d.Name, Kind: d.Kind, Framework: frameworkOf(d), Repo: d.File.Repo, Path: d.File.F.Path,
		Line: d.Start, EndLine: d.End, Handle: d.Handle(), Resolution: how}
	n.Props = analyzeProps(r, d)
	n.State = analyzeState(r, d)
	setters := map[string]bool{}
	for _, m := range reUseState.FindAllStringSubmatch(d.File.text(d.Start, d.End), -1) {
		if m[2] != "" {
			setters[m[2]] = true
		}
	}
	n.Effects = analyzeEffects(d)
	n.Handlers = analyzeHandlers(d, n.Props)
	covered := map[int]bool{} // lines whose API calls belong to a handler/effect
	for i := range n.Effects {
		e := &n.Effects[i]
		e.API = c.apiFrom(r, d, e.body, e.bodyOff, setters, nil, 0)
		markLines(covered, d.File, e.bodyOff, len(e.body))
	}
	for i := range n.Handlers {
		h := &n.Handlers[i]
		if h.Prop || h.Handler == "(inline)" {
			continue
		}
		body, off := h.expr, h.exprOff
		via := []string{}
		if b, o, ok := c.handlerBody(d, h.Handler); ok {
			body, off = b, o
			via = []string{h.Handler}
			markLines(covered, d.File, o, len(b))
		}
		h.API = c.apiFrom(r, d, body, off, setters, via, 0)
		if len(h.API) == 0 && len(via) == 0 {
			// an imported/store function bound directly: follow it by name
			h.API = c.apiFromNames(r, d, []calledName{{name: h.Handler}}, nil)
		}
	}
	// Calls in the body not under a handler/effect (render-time / data hooks).
	c.ensureAPI()
	for _, cc := range c.byFile[d.File.Repo+"\x00"+d.File.F.Path] {
		if cc.Line >= d.Start && cc.Line <= d.End && !covered[cc.Line] {
			n.API = append(n.API, c.apiCall(cc, nil))
		}
	}
	for _, rd := range renders(d) {
		def, how, ext := r.resolve(d.File, rd.Name)
		if def == nil || def == d {
			if ext != "" || def == nil {
				n.Lib = appendUniq(n.Lib, rd.Name)
			}
			continue
		}
		rd.def, rd.resolution = def, how
		rd.Def = refOf(def, how)
		n.Renders = append(n.Renders, rd)
	}
	return n
}

func appendUniq(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func markLines(m map[int]bool, uf *uiFile, off, n int) {
	for l := uf.Lines.line(off); l <= uf.Lines.line(off+n); l++ {
		m[l] = true
	}
}

// handlerBody finds a handler's definition: inside the component first, then
// at file level (module helpers, Vue methods, Svelte script functions).
func (c *uiCtx) handlerBody(d *uiDef, name string) (string, int, bool) {
	uf := d.File
	if !uf.SFC {
		region := uf.text(d.Start, d.End)
		if b, o, ok := funcBody(region, name); ok {
			return b, uf.Lines.offset(d.Start) + o, true
		}
	}
	if b, o, ok := funcBody(uf.Script, name); ok {
		return b, o, true
	}
	return "", 0, false
}

// tree expands the component tree breadth-first until the budget is spent.
func (c *uiCtx) tree(root *uiDef, how string, budget int) (*UIComponent, int, bool, string) {
	type item struct {
		d      *uiDef
		node   *UIComponent
		parent *UIComponent
		ri     int
	}
	cost := func(n *UIComponent) int {
		cp := *n
		cp.Children = nil
		b, _ := json.Marshal(cp)
		return len(b)/4 + 1
	}
	rootNode := c.analyze(root, how)
	spent := cost(rootNode)
	expanded := map[*uiDef]bool{root: true}
	count := 1
	truncated, next := false, ""
	queue := []item{{d: root, node: rootNode}}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		for i := range it.node.Renders {
			rd := &it.node.Renders[i]
			if expanded[rd.def] {
				rd.Seen = true
				continue
			}
			child := c.analyze(rd.def, rd.resolution)
			k := cost(child)
			if spent+k > budget {
				if !truncated {
					truncated = true
					next = "reponite ui " + rd.Def.Handle + " (subtree not expanded for budget; or raise --budget to " + itoa(budget*2) + ")"
				}
				continue
			}
			spent += k
			count++
			expanded[rd.def] = true
			rd.Expanded = true
			it.node.Children = append(it.node.Children, child)
			queue = append(queue, item{d: rd.def, node: child})
		}
	}
	return rootNode, count, truncated, next
}

// --- API linkage --------------------------------------------------------------

// ensureAPI builds the client-call and route indexes once per call.
func (c *uiCtx) ensureAPI() {
	if c.apiReady {
		return
	}
	c.apiReady = true
	c.clients, c.byFile, c.routeOf = map[string][]ClientCall{}, map[string][]ClientCall{}, map[string][]UIRoute{}
	c.byBase = map[string]map[string][]string{}
	rr := RoutesWith(c.s, FleetRepo, c.ref, "", 1<<30, c.opt.Routes)
	put := func(cc ClientCall) {
		k := cc.Repo + "\x00" + path.Dir(cc.Path) + "\x00" + cc.In
		for _, x := range c.clients[k] {
			if x.Path == cc.Path && x.Line == cc.Line {
				return
			}
		}
		c.clients[k] = append(c.clients[k], cc)
		fk := cc.Repo + "\x00" + cc.Path
		c.byFile[fk] = append(c.byFile[fk], cc)
	}
	for _, l := range rr.Links {
		for _, cc := range l.Clients {
			put(cc)
			k := cc.Repo + "\x00" + cc.Path + "\x00" + itoa(cc.Line)
			c.routeOf[k] = append(c.routeOf[k], UIRoute{Repo: l.Route.Repo, Path: l.Route.Path, Line: l.Route.Line,
				Method: l.Route.Method, Pattern: l.Route.Pattern, Handler: routeHandler(l.Route, cc.Method), Framework: l.Route.Framework, Via: cc.Via})
		}
	}
	for _, cc := range rr.Unmatched {
		put(cc)
	}
}

// routeHandler names the code serving a client call: a DRF viewset route
// mounted by a router serves list/create on its collection path and
// retrieve/update/partial_update/destroy on its detail path.
func routeHandler(r Route, method string) string {
	h := strings.TrimSuffix(r.Handler, ".as_view")
	if r.Framework != "django" || r.Method != "*" || !strings.Contains(h, "ViewSet") && !strings.Contains(h, "Viewset") {
		return h
	}
	detail := strings.HasSuffix(strings.TrimSuffix(r.Pattern, "/"), "{}")
	act := map[string]string{"GET": "list", "POST": "create"}
	if detail {
		act = map[string]string{"GET": "retrieve", "PUT": "update", "PATCH": "partial_update", "DELETE": "destroy"}
	}
	if a, ok := act[strings.ToUpper(method)]; ok {
		return h + "." + a
	}
	return h
}

func (c *uiCtx) apiCall(cc ClientCall, via []string) UIAPICall {
	return UIAPICall{Method: cc.Method, URL: cc.URL, Repo: cc.Repo, Path: cc.Path, Line: cc.Line, Via: via,
		Routes: c.routeOf[cc.Repo+"\x00"+cc.Path+"\x00"+itoa(cc.Line)]}
}

const maxAPIPerSite = 6

// apiFrom collects the API calls a body makes: calls written in it, calls of
// local functions it invokes, and calls reached through the call graph from
// the repo functions it invokes.
func (c *uiCtx) apiFrom(r *uiRepo, d *uiDef, body string, off int, setters map[string]bool, via []string, depth int) []UIAPICall {
	c.ensureAPI()
	uf := d.File
	var out []UIAPICall
	from, to := uf.Lines.line(off), uf.Lines.line(off+len(body))
	for _, cc := range c.byFile[uf.Repo+"\x00"+uf.F.Path] {
		if cc.Line >= from && cc.Line <= to {
			out = append(out, c.apiCall(cc, via))
		}
	}
	var external []calledName
	for _, cn := range callNames(body, setters) {
		if depth < 3 && cn.qual == "" {
			if b, o, ok := c.handlerBody(d, cn.name); ok && o != off {
				out = append(out, c.apiFrom(r, d, b, o, setters, append(append([]string{}, via...), cn.name), depth+1)...)
				continue
			}
		}
		external = append(external, cn)
	}
	out = append(out, c.apiFromNames(r, d, external, via)...)
	return dedupAPI(out)
}

// collapseVia drops a name repeated back-to-back in a call chain (the called
// name and the function it resolved to are the same hop).
func collapseVia(v []string) []string {
	out := v[:0:0]
	for _, x := range v {
		if len(out) == 0 || out[len(out)-1] != x {
			out = append(out, x)
		}
	}
	return out
}

func dedupAPI(in []UIAPICall) []UIAPICall {
	seen := map[string]bool{}
	var out []UIAPICall
	for _, a := range in {
		k := a.Repo + a.Path + itoa(a.Line)
		if seen[k] || len(out) >= maxAPIPerSite {
			continue
		}
		seen[k] = true
		out = append(out, a)
	}
	return out
}

// apiFromNames resolves called names to repo functions (by import, else by a
// unique name) and walks the call graph from each to the HTTP calls it reaches.
func (c *uiCtx) apiFromNames(r *uiRepo, d *uiDef, names []calledName, via []string) []UIAPICall {
	var out []UIAPICall
	for _, cn := range names {
		for _, qid := range c.qidsFor(r, d.File, cn) {
			for _, a := range c.reachable(r.Name, qid) {
				a.Via = collapseVia(append(append(append([]string{}, via...), cn.name), a.Via...))
				out = append(out, a)
			}
		}
	}
	return dedupAPI(out)
}

// qidsFor maps a call in file uf to the symbol ids it may invoke.
func (c *uiCtx) qidsFor(r *uiRepo, uf *uiFile, cn calledName) []string {
	byBase := c.basesOf(r.Name)
	cands := byBase[cn.name]
	if len(cands) == 0 {
		return nil
	}
	// An import pins the defining file: qual.name() on a namespace/default
	// import, or a named import of name.
	spec := ""
	if im, ok := uf.Imports[cn.name]; ok && cn.qual == "" {
		spec = im.Spec
	} else if im, ok := uf.Imports[cn.qual]; ok && cn.qual != "" {
		spec = im.Spec
	}
	if spec != "" {
		p, ok := r.resolveSpec(uf.F.Path, spec)
		if !ok {
			return nil // a package function, not repo code
		}
		dir := path.Dir(p)
		var out []string
		for _, q := range cands {
			if qidDir(q) == dir {
				out = append(out, q)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	if len(cands) > 3 {
		return nil // too common a name to follow without an import
	}
	return cands
}

func (c *uiCtx) basesOf(repo string) map[string][]string {
	if m, ok := c.byBase[repo]; ok {
		return m
	}
	m := map[string][]string{}
	for qid := range c.s.Snapshot(repo, c.ref).Symbols {
		b := qid[strings.LastIndexByte(qid, '.')+1:]
		m[b] = append(m[b], qid)
	}
	for _, v := range m {
		sort.Strings(v)
	}
	c.byBase[repo] = m
	return m
}

// qidDir is the directory part of a symbol id (dir.Name or dir.Class.method).
func qidDir(qid string) string {
	slash := strings.LastIndexByte(qid, '/')
	rest := qid[slash+1:]
	dot := strings.IndexByte(rest, '.')
	if dot < 0 {
		return "."
	}
	if slash < 0 {
		return rest[:dot]
	}
	return qid[:slash+1+dot]
}

// reachable lists the HTTP calls reachable from qid through the call graph
// (breadth-first, bounded), each with the chain of function names to it.
func (c *uiCtx) reachable(repo, qid string) []UIAPICall {
	key := repo + "\x00" + qid
	if v, ok := c.reach[key]; ok {
		return v
	}
	c.reach[key] = nil // cycle guard
	snap := c.s.Snapshot(repo, c.ref)
	type node struct {
		q   string
		via []string
	}
	var out []UIAPICall
	seen := map[string]bool{qid: true}
	queue := []node{{qid, []string{baseOf(qid)}}}
	for steps := 0; len(queue) > 0 && steps < 400 && len(out) < maxAPIPerSite; steps++ {
		n := queue[0]
		queue = queue[1:]
		dir := qidDir(n.q)
		if dir == "." {
			dir = "."
		}
		for _, cc := range c.clients[repo+"\x00"+dir+"\x00"+baseOf(n.q)] {
			out = append(out, c.apiCall(cc, n.via))
		}
		if len(n.via) >= 7 {
			continue
		}
		for _, ce := range snap.Callees[n.q] {
			if seen[ce.Name] {
				continue
			}
			seen[ce.Name] = true
			queue = append(queue, node{ce.Name, append(append([]string{}, n.via...), baseOf(ce.Name))})
		}
	}
	out = dedupAPI(out)
	c.reach[key] = out
	return out
}

func baseOf(qid string) string { return qid[strings.LastIndexByte(qid, '.')+1:] }

// --- uses ---------------------------------------------------------------------

// uses lists where a component is rendered, passed or routed to.
func (c *uiCtx) uses(target string, pages []UIPage) []UIUse {
	want, _ := c.findDef(target)
	name := target
	if want != nil {
		name = want.Name
	} else if i := strings.LastIndexByte(target, '.'); i >= 0 {
		name = target[i+1:]
	}
	var out []UIUse
	seen := map[string]bool{}
	add := func(u UIUse) {
		k := u.Repo + u.Path + itoa(u.Line) + u.Kind
		if !seen[k] {
			seen[k] = true
			out = append(out, u)
		}
	}
	lineText := func(uf *uiFile, ln int) string {
		t := strings.TrimSpace(uf.F.Content[uf.Lines.offset(ln):min(len(uf.F.Content), uf.Lines.offset(ln+1))])
		if len(t) > 120 {
			t = t[:120] + "…"
		}
		return t
	}
	for _, r := range c.repoList() {
		for _, uf := range r.Files {
			src, jsx := uf.Code, true
			if uf.SFC {
				src, jsx = uf.Markup, false
			}
			for _, t := range scanTags(src, jsx) {
				kind := "render"
				hit := componentTagName(t.Name, !jsx) == name || strings.HasSuffix(t.Name, "."+name)
				if !hit {
					for _, a := range attrs(t.Attrs) {
						if a.Expr && (a.Name == "component" || a.Name == "Component" || a.Name == "render" || a.Name == "as") {
							for _, id := range identRe.FindAllString(a.Value, -1) {
								if id == name {
									hit, kind = true, "prop"
								}
							}
						}
					}
				}
				if !hit {
					continue
				}
				d, how, _ := r.resolve(uf, name)
				if want != nil && d != nil && d != want {
					continue // a different component with the same name
				}
				ln := uf.Lines.line(t.Off)
				in := ""
				if e := uf.defAt(ln); e != nil {
					in = e.Name
				} else {
					in = enclosing(uf.F.Symbols, ln)
				}
				if in == name && d == want && kind == "render" && uf.defAt(ln) == want {
					// recursion renders itself: still a use
				}
				if how == "" {
					how = resName
				}
				add(UIUse{Repo: r.Name, Path: uf.F.Path, Line: ln, In: in, Kind: kind, Text: lineText(uf, ln), Resolution: how})
			}
		}
	}
	for _, p := range pages {
		if p.Component == name && (want == nil || p.def == nil || p.def == want) {
			r := c.repos[p.Repo]
			uf := r.ByPath[p.Path]
			add(UIUse{Repo: p.Repo, Path: p.Path, Line: p.Line, In: p.Route, Kind: "route", Text: lineText(uf, p.Line), Resolution: resolutionOf(p.Def)})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Repo != out[j].Repo {
			return out[i].Repo < out[j].Repo
		}
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

func resolutionOf(r *UIRef) string {
	if r == nil {
		return resName
	}
	return r.Resolution
}

// --- edges for other queries ---------------------------------------------------

// UIEdge is one frontend edge: a component rendering another, a markup event
// bound to a handler, or a handler/effect reaching an HTTP endpoint.
type UIEdge struct {
	Kind       string // renders | handles | calls_api
	From       string // component handle (or handle#handler for calls_api)
	To         string // component handle | handler name | "METHOD URL"
	Repo, Path string
	Line       int
	Detail     string // event+element for handles; matched server route for calls_api
	Resolution string
}

// UIEdges returns every frontend edge in the repos at ref: what each
// component renders, the handlers its markup binds, and the API calls those
// handlers and its effects reach (with the server route when one matches).
// It is the query helper other commands (trace) consume.
func UIEdges(s Store, repo, ref string) []UIEdge {
	c := newUICtx(s, repo, ref, UIOptions{})
	var out []UIEdge
	for _, r := range c.repoList() {
		for _, uf := range r.Files {
			for _, d := range uf.defs {
				n := c.analyze(d, "")
				for _, rd := range n.Renders {
					out = append(out, UIEdge{Kind: "renders", From: n.Handle, To: rd.Def.Handle, Repo: r.Name, Path: uf.F.Path, Line: rd.Line, Resolution: rd.resolution})
				}
				for _, h := range n.Handlers {
					out = append(out, UIEdge{Kind: "handles", From: n.Handle, To: h.Handler, Repo: r.Name, Path: uf.F.Path, Line: h.Line, Detail: h.Event + " on " + h.Element})
					for _, a := range h.API {
						out = append(out, apiEdge(n.Handle+"#"+h.Handler, a))
					}
				}
				for _, e := range n.Effects {
					for _, a := range e.API {
						out = append(out, apiEdge(n.Handle+"#"+e.Kind+"@"+itoa(e.Line), a))
					}
				}
				for _, a := range n.API {
					out = append(out, apiEdge(n.Handle, a))
				}
			}
		}
	}
	return out
}

func apiEdge(from string, a UIAPICall) UIEdge {
	e := UIEdge{Kind: "calls_api", From: from, To: a.Method + " " + a.URL, Repo: a.Repo, Path: a.Path, Line: a.Line, Resolution: "call-graph"}
	if len(a.Routes) > 0 {
		rt := a.Routes[0]
		e.Detail = rt.Repo + ":" + rt.Path + ":" + itoa(rt.Line) + " " + rt.Handler
	}
	return e
}
