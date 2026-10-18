package query

import (
	"path"
	"regexp"
	"strings"
)

// --- Python: FastAPI, Flask, Quart, Sanic (routers and blueprints followed
// across modules); Django REST methods from each view class -----------------

var (
	rePyRouterVar  = regexp.MustCompile(`(?m)^\s*(\w+)\s*(?::\s*[\w.\[\]]+\s*)?=\s*(?:[\w.]+\.)?(APIRouter|FastAPI|Flask|Blueprint|Quart|Sanic|Starlette)\(`)
	rePyRouteDec   = regexp.MustCompile(`(?m)^[ \t]*@(\w+)\.(get|post|put|patch|delete|options|head|api_route|route|websocket)\(`)
	rePyMountCall  = regexp.MustCompile(`\b(\w+)\.(include_router|register_blueprint)\(`)
	rePyFromImport = regexp.MustCompile(`(?m)^\s*from\s+(\.*)([\w.]*)\s+import\s+\(?([^)\n]*(?:\n[^)\n]*)*?)\)?\s*$`)
	rePyImportAs   = regexp.MustCompile(`(?m)^\s*import\s+([\w.]+)(?:\s+as\s+(\w+))?\s*$`)
	rePyDefAfter   = regexp.MustCompile(`(?m)^\s*(?:async\s+)?def\s+(\w+)`)
	rePyKwarg      = regexp.MustCompile(`^\s*(\w+)\s*=\s*(.+)$`)
	rePyStrList    = regexp.MustCompile(`["'](\w+)["']`)
	rePyClassDef   = regexp.MustCompile(`(?m)^class\s+(\w+)\s*(?:\(([^)]*)\))?\s*:`)
	rePyMethodDef  = regexp.MustCompile(`(?m)^[ \t]+(?:async\s+)?def\s+(\w+)\s*\(`)
	rePyAPIView    = regexp.MustCompile(`@(?:api_view|require_http_methods)\(\s*\[([^\]]*)\]`)
)

type pyNode struct {
	key, own  string // file#var; the router's own prefix
	root      bool
	blueprint bool
}

type pyMount struct {
	parent, child, prefix string
	set                   bool // a prefix argument was given (a blueprint's replaces its own)
	test                  bool
}

func pyRouterRoutes(repo string, files []File) []Route {
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	consts := pyConstants(files)
	evalStr := func(expr string) (string, bool) {
		if s, ok := pyStringLit(expr); ok {
			return s, true
		}
		e := strings.TrimSpace(expr)
		if i := strings.LastIndex(e, "."); i >= 0 {
			e = e[i+1:]
		}
		v, ok := consts[e]
		return v, ok
	}
	nodes := map[string]*pyNode{}
	var mounts []pyMount
	type reg struct {
		node, method, path, handler, file string
		line                              int
	}
	var regs []reg
	for i := range files {
		f := &files[i]
		if path.Ext(f.Path) != ".py" {
			continue
		}
		src := stripPyComments(f.Content)
		if !strings.Contains(src, "@") && !strings.Contains(src, "include_router") && !strings.Contains(src, "register_blueprint") && !rePyRouterVar.MatchString(src) {
			continue
		}
		imports := pyImports(f.Path, src, paths) // local -> file#name or file (a module)
		local := map[string]string{}
		for _, m := range rePyRouterVar.FindAllStringSubmatchIndex(src, -1) {
			name, kind := src[m[2]:m[3]], src[m[4]:m[5]]
			k := f.Path + "#" + name
			n := &pyNode{key: k, root: kind != "APIRouter" && kind != "Blueprint", blueprint: kind == "Blueprint"}
			for _, a := range splitArgs(callArgs(src, m[1])) {
				if kv := rePyKwarg.FindStringSubmatch(a); kv != nil && (kv[1] == "prefix" || kv[1] == "url_prefix") {
					if v, ok := evalStr(kv[2]); ok {
						n.own = v
					}
				}
			}
			nodes[k] = n
			local[name] = k
		}
		ref := func(expr string) string {
			e := strings.TrimSpace(expr)
			if k, ok := local[e]; ok {
				return k
			}
			if b, ok := imports[e]; ok && strings.Contains(b, "#") {
				return b
			}
			if i := strings.Index(e, "."); i > 0 { // module.router
				if mod, ok := imports[e[:i]]; ok && !strings.Contains(mod, "#") {
					return mod + "#" + e[i+1:]
				}
			}
			return ""
		}
		inTest := IsTestPath(f.Path)
		for _, m := range rePyMountCall.FindAllStringSubmatchIndex(src, -1) {
			parentName := src[m[2]:m[3]]
			parent := ref(parentName)
			args := splitArgs(callArgs(src, m[1]))
			if len(args) == 0 {
				continue
			}
			if parent == "" {
				// the receiver of include_router/register_blueprint is an
				// application defined out of sight (a factory, another module)
				parent = f.Path + "#" + parentName
				if nodes[parent] == nil {
					nodes[parent] = &pyNode{key: parent, root: true}
				}
			}
			child := ref(args[0])
			if child == "" {
				child = "?" + strings.TrimSpace(args[0]) // resolved by name once every file is read
			}
			mt := pyMount{parent: parent, child: child, test: inTest}
			for _, a := range args[1:] {
				if kv := rePyKwarg.FindStringSubmatch(a); kv != nil && (kv[1] == "prefix" || kv[1] == "url_prefix") {
					if v, ok := evalStr(kv[2]); ok {
						mt.prefix, mt.set = v, true
					}
				}
			}
			mounts = append(mounts, mt)
		}
		for _, m := range rePyRouteDec.FindAllStringSubmatchIndex(src, -1) {
			node := ref(src[m[2]:m[3]])
			if node == "" {
				continue
			}
			argText := callArgs(src, m[1])
			args := splitArgs(argText)
			if len(args) == 0 {
				continue
			}
			p, ok := evalStr(args[0])
			if !ok {
				continue
			}
			verb := src[m[4]:m[5]]
			methods := []string{strings.ToUpper(verb)}
			if verb == "route" || verb == "api_route" {
				methods = []string{"GET"}
				for _, a := range args[1:] {
					if kv := rePyKwarg.FindStringSubmatch(a); kv != nil && kv[1] == "methods" {
						methods = nil
						for _, q := range rePyStrList.FindAllStringSubmatch(kv[2], -1) {
							methods = append(methods, strings.ToUpper(q[1]))
						}
					}
				}
			}
			if verb == "websocket" {
				methods = []string{"WEBSOCKET"}
			}
			handler := ""
			if d := rePyDefAfter.FindStringSubmatch(src[m[1]+len(argText):]); d != nil {
				handler = d[1]
			}
			for _, mt := range methods {
				regs = append(regs, reg{node: node, method: mt, path: p, handler: handler, file: f.Path, line: lineAtOffset(src, m[0])})
			}
		}
	}
	// a child named mod.router or router whose import was not resolved: the
	// one router of that name in a module of that name
	byName := func(expr string) string {
		mod, name := "", expr
		if i := strings.LastIndex(expr, "."); i >= 0 {
			mod, name = expr[:i], expr[i+1:]
			if j := strings.LastIndex(mod, "."); j >= 0 {
				mod = mod[j+1:]
			}
		}
		found := ""
		for k := range nodes {
			file, v, _ := strings.Cut(k, "#")
			if v != name || mod != "" && strings.TrimSuffix(path.Base(file), ".py") != mod {
				continue
			}
			if found != "" {
				return ""
			}
			found = k
		}
		return found
	}
	parents := map[string][]pyMount{}
	for _, m := range mounts {
		if strings.HasPrefix(m.child, "?") {
			if m.child = byName(m.child[1:]); m.child == "" {
				continue
			}
		}
		parents[m.child] = append(parents[m.child], m)
	}
	// prefixes of a router: its mounts' prefixes, then its own (FastAPI
	// applies both; a blueprint's url_prefix at registration replaces its own)
	var prefixes func(k string, depth int) []rpfx
	prefixes = func(k string, depth int) []rpfx {
		n := nodes[k]
		if n == nil {
			return []rpfx{{root: "?"}}
		}
		if n.root {
			return []rpfx{{path: n.own}}
		}
		ps := parents[k]
		if len(ps) == 0 || depth > 8 {
			return []rpfx{{root: "?", path: n.own}}
		}
		var out []rpfx
		for _, m := range ps {
			own := n.own
			if n.blueprint && m.set {
				own = ""
			}
			for _, p := range prefixes(m.parent, depth+1) {
				out = append(out, rpfx{root: p.root, path: mountPath(mountPath(p.path, m.prefix), own), test: p.test || m.test})
			}
		}
		return out
	}
	var out []Route
	for _, r := range regs {
		fw := "fastapi/flask"
		for _, p := range prefixes(r.node, 0) {
			out = append(out, Route{Repo: repo, Path: r.file, Line: r.line, Method: r.method, Pattern: normURL(mountPath(p.path, r.path)),
				Handler: r.handler, Framework: fw, Partial: p.root != "", Test: p.test})
		}
	}
	return out
}

// pyImports maps a file's imported names: a module to its file, a name from
// a module to "file#name".
func pyImports(from, src string, paths map[string]bool) map[string]string {
	out := map[string]string{}
	for _, m := range rePyFromImport.FindAllStringSubmatch(src, -1) {
		level, mod := len(m[1]), m[2]
		base := pyModuleFile(from, mod, level, paths)
		for _, it := range strings.Split(m[3], ",") {
			fs := strings.Fields(strings.TrimSpace(it))
			if len(fs) == 0 {
				continue
			}
			name, as := fs[0], fs[0]
			if len(fs) == 3 && fs[1] == "as" {
				as = fs[2]
			}
			sub := mod
			if sub != "" {
				sub += "."
			}
			if file := pyModuleFile(from, sub+name, level, paths); file != "" {
				out[as] = file // a submodule
			} else if base != "" {
				out[as] = base + "#" + name
			}
		}
	}
	for _, m := range rePyImportAs.FindAllStringSubmatch(src, -1) {
		if file := pyModuleFile(from, m[1], 0, paths); file != "" {
			as := m[2]
			if as == "" {
				as = m[1]
			}
			out[as] = file
		}
	}
	return out
}

// pyModuleFile is the file of a module: relative to the importing file for
// level > 0, else under a source root (the repo root, src/, backend/ — never a
// package itself), the deepest one above the importer first.
func pyModuleFile(from, module string, level int, paths map[string]bool) string {
	target := func(p string) string {
		p = strings.TrimPrefix(path.Clean(p), "./")
		if paths[p+".py"] {
			return p + ".py"
		}
		if paths[path.Join(p, "__init__.py")] {
			return path.Join(p, "__init__.py")
		}
		return ""
	}
	rel := strings.ReplaceAll(module, ".", "/")
	if level > 0 {
		base := path.Dir(from)
		for i := 1; i < level; i++ {
			base = path.Dir(base)
		}
		if base == "." {
			base = ""
		}
		return target(path.Join(base, rel))
	}
	if module == "" {
		return ""
	}
	for d := path.Dir(from); ; d = path.Dir(d) {
		if d == "." {
			d = ""
		}
		if !paths[path.Join(d, "__init__.py")] {
			if t := target(path.Join(d, rel)); t != "" {
				return t
			}
		}
		if d == "" {
			break
		}
	}
	return ""
}

// pyConstants are the repo's string constants and settings defaults by name
// (API_V1_STR: str = "/api/v1"); a name bound to two values is dropped.
func pyConstants(files []File) map[string]string {
	re := regexp.MustCompile(`(?m)^\s*([A-Za-z_]\w*)\s*(?::\s*[\w.\[\]| ]+)?=\s*[rbuf]*["']([^"'\n]*)["']\s*(?:#.*)?$`)
	out, bad := map[string]string{}, map[string]bool{}
	for _, f := range files {
		if path.Ext(f.Path) != ".py" || IsTestPath(f.Path) {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(f.Content, -1) {
			if v, ok := out[m[1]]; ok && v != m[2] {
				bad[m[1]] = true
			}
			out[m[1]] = m[2]
		}
	}
	for k := range bad {
		delete(out, k)
	}
	return out
}

// drfMethods expands Django routes to the HTTP methods their view serves:
// an APIView's get/post/… methods, a DRF generic view's (ListCreateAPIView:
// GET and POST), a viewset's actions on its list and detail routes, an
// @api_view's list. A route whose view is not found keeps "*".
func drfMethods(routes []Route, files []File) []Route {
	type class struct {
		bases   []string
		methods map[string]bool
	}
	classes := map[string]class{}
	funcMethods := map[string][]string{}
	for _, f := range files {
		if path.Ext(f.Path) != ".py" || !strings.Contains(f.Content, "class ") && !strings.Contains(f.Content, "@api_view") {
			continue
		}
		src := f.Content
		defs := rePyClassDef.FindAllStringSubmatchIndex(src, -1)
		for i, m := range defs {
			end := len(src)
			if i+1 < len(defs) {
				end = defs[i+1][0]
			}
			c := class{methods: map[string]bool{}}
			if m[4] >= 0 {
				for _, b := range strings.Split(src[m[4]:m[5]], ",") {
					b = strings.TrimSpace(b)
					if j := strings.LastIndex(b, "."); j >= 0 {
						b = b[j+1:]
					}
					if b != "" {
						c.bases = append(c.bases, b)
					}
				}
			}
			for _, mm := range rePyMethodDef.FindAllStringSubmatch(src[m[1]:end], -1) {
				c.methods[mm[1]] = true
			}
			classes[src[m[2]:m[3]]] = c
		}
		for _, m := range rePyAPIView.FindAllStringSubmatchIndex(src, -1) {
			if d := rePyDefAfter.FindStringSubmatch(src[m[1]:]); d != nil {
				for _, q := range rePyStrList.FindAllStringSubmatch(src[m[2]:m[3]], -1) {
					funcMethods[d[1]] = append(funcMethods[d[1]], strings.ToUpper(q[1]))
				}
			}
		}
	}
	generic := map[string][]string{
		"ListAPIView": {"GET"}, "CreateAPIView": {"POST"}, "ListCreateAPIView": {"GET", "POST"}, "RetrieveAPIView": {"GET"},
		"DestroyAPIView": {"DELETE"}, "UpdateAPIView": {"PUT", "PATCH"}, "RetrieveUpdateAPIView": {"GET", "PUT", "PATCH"},
		"RetrieveDestroyAPIView": {"GET", "DELETE"}, "RetrieveUpdateDestroyAPIView": {"GET", "PUT", "PATCH", "DELETE"},
	}
	mixin := map[string][]string{
		"CreateModelMixin": {"create"}, "ListModelMixin": {"list"}, "RetrieveModelMixin": {"retrieve"},
		"UpdateModelMixin": {"update", "partial_update"}, "DestroyModelMixin": {"destroy"},
		"ModelViewSet": {"create", "list", "retrieve", "update", "partial_update", "destroy"}, "ReadOnlyModelViewSet": {"list", "retrieve"},
	}
	viewsetBase := map[string]bool{"ViewSet": true, "GenericViewSet": true, "ModelViewSet": true, "ReadOnlyModelViewSet": true, "ViewSetMixin": true}
	// lineage folds a class's own methods with its repo bases' and the
	// framework bases' contributions
	var lineage func(name string, depth int) (methods map[string]bool, viewset bool, known bool)
	lineage = func(name string, depth int) (map[string]bool, bool, bool) {
		ms := map[string]bool{}
		if gm, ok := generic[name]; ok {
			for _, m := range gm {
				ms[strings.ToLower(m)] = true
			}
			return ms, false, true
		}
		if mx, ok := mixin[name]; ok {
			for _, m := range mx {
				ms[m] = true
			}
			return ms, viewsetBase[name], true
		}
		if viewsetBase[name] {
			return ms, true, true
		}
		if name == "APIView" || name == "View" || name == "GenericAPIView" {
			return ms, false, true
		}
		c, ok := classes[name]
		if !ok || depth > 4 {
			return ms, false, false
		}
		vs, known := false, false
		for m := range c.methods {
			ms[m] = true
		}
		for _, b := range c.bases {
			bm, bv, bk := lineage(b, depth+1)
			for m := range bm {
				ms[m] = true
			}
			vs, known = vs || bv, known || bk
		}
		return ms, vs, known
	}
	httpOf := map[string]string{"get": "GET", "post": "POST", "put": "PUT", "patch": "PATCH", "delete": "DELETE"}
	var out []Route
	for _, r := range routes {
		if r.Framework == "django" {
			r.Handler = strings.TrimSuffix(r.Handler, ".as_view") // the view class
		}
		if r.Framework != "django" || r.Method != "*" {
			out = append(out, r)
			continue
		}
		h := strings.TrimSuffix(r.Handler, ".as_view")
		if i := strings.LastIndex(h, "."); i >= 0 {
			h = h[i+1:]
		}
		var methods []string
		if fm, ok := funcMethods[h]; ok {
			methods = fm
		} else if ms, viewset, known := lineage(h, 0); known {
			if viewset {
				detail := strings.HasSuffix(strings.TrimSuffix(r.Pattern, "/"), "{}")
				acts := [][2]string{{"list", "GET"}, {"create", "POST"}}
				if detail {
					acts = [][2]string{{"retrieve", "GET"}, {"update", "PUT"}, {"partial_update", "PATCH"}, {"destroy", "DELETE"}}
				}
				for _, a := range acts {
					if ms[a[0]] {
						methods = append(methods, a[1])
					}
				}
			} else {
				for _, m := range []string{"get", "post", "put", "patch", "delete"} {
					if ms[m] {
						methods = append(methods, httpOf[m])
					}
				}
			}
		}
		if len(methods) == 0 {
			out = append(out, r)
			continue
		}
		for _, m := range methods {
			x := r
			x.Method = m
			out = append(out, x)
		}
	}
	return out
}

// blankPyDocstrings blanks triple-quoted strings (newlines kept): examples in
// a urls module's docstring are not its routes.
func blankPyDocstrings(src string) string {
	b := []byte(src)
	for i := 0; i+2 < len(b); i++ {
		if (b[i] == '"' || b[i] == '\'') && b[i+1] == b[i] && b[i+2] == b[i] {
			q := b[i]
			j := i + 3
			for ; j+2 < len(b) && !(b[j] == q && b[j+1] == q && b[j+2] == q); j++ {
			}
			end := min(j+3, len(b))
			for k := i; k < end; k++ {
				if b[k] != '\n' {
					b[k] = ' '
				}
			}
			i = end - 1
		}
	}
	return string(b)
}
