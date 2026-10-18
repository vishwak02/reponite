package query

import (
	"path"
	"regexp"
	"strings"
)

// --- JavaScript / TypeScript: Express (and routers built the same way),
// NestJS controllers, Next.js file routes ------------------------------------

// A JS router is a node: a variable holding express()/Router(), a module's
// default export, or a router built inline in a chain. Mount edges come from
// .use(prefix?, child...) and carry the prefix; registrations come from
// .get/.post/... and .route(path).get(...).
type jsNode struct {
	key  string // file#name
	root bool   // an application (express(), fastify(), new Hono())
}

type jsMount struct {
	parent, child, prefix string
	test                  bool
}

type jsReg struct {
	node, method, path, handler, file string
	line                              int
}

var (
	reJSImportDefault = regexp.MustCompile(`import\s+([A-Za-z_$][\w$]*)\s*(?:,\s*\{([^}]*)\})?\s*from\s*['"]([^'"]+)['"]`)
	reJSImportNamed   = regexp.MustCompile(`import\s*\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]`)
	reJSImportStar    = regexp.MustCompile(`import\s*\*\s*as\s+([A-Za-z_$][\w$]*)\s*from\s*['"]([^'"]+)['"]`)
	reJSRequire       = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)
	reJSRequireNamed  = regexp.MustCompile(`(?:const|let|var)\s*\{([^}]*)\}\s*=\s*require\(\s*['"]([^'"]+)['"]\s*\)`)
	reJSDecl          = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::\s*[\w.<>\[\]]+\s*)?=\s*`)
	reJSExportDefault = regexp.MustCompile(`export\s+default\s+`)
	reJSModuleExports = regexp.MustCompile(`module\.exports\s*=\s*`)
	reJSExportList    = regexp.MustCompile(`export\s*\{([^}]*)\}`)
	reJSChainStart    = regexp.MustCompile(`\b([A-Za-z_$][\w$]*)\s*\.\s*(use|get|post|put|patch|delete|all|options|head|route|register)\s*\(`)
	reJSRouterHead    = regexp.MustCompile(`^(?:new\s+)?(?:express\s*\(\s*\)|express\s*\.\s*Router\s*\(|Router\s*\(|fastify\s*\(|Fastify\s*\(|Hono\s*\(|KoaRouter\s*\(|Koa\s*\(|express\.Router\b)`)
	reJSAppHead       = regexp.MustCompile(`^(?:new\s+)?(?:express\s*\(\s*\)|fastify\s*\(|Fastify\s*\(|Hono\s*\(|Koa\s*\()`)
	reJSPrefixOpt     = regexp.MustCompile(`prefix\s*:\s*['"]([^'"]*)['"]`)
	reNestController  = regexp.MustCompile(`@Controller\(\s*(?:['"]([^'"]*)['"]|\{[^}]*?path\s*:\s*['"]([^'"]*)['"][^}]*\})?\s*\)`)
	reNestMethod      = regexp.MustCompile(`@(Get|Post|Put|Patch|Delete|All|Options|Head)\(\s*(?:['"]([^'"]*)['"])?[^)]*\)\s*(?:@[\w.]+(?:\([^)]*\))?\s*)*(?:public\s+|private\s+|protected\s+)?(?:async\s+)?([A-Za-z_$][\w$]*)\s*\(`)
	reNestPrefix      = regexp.MustCompile(`setGlobalPrefix\(\s*['"]([^'"]+)['"]`)
	reNextVerb        = regexp.MustCompile(`export\s+(?:async\s+)?(?:function\s+|const\s+)(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS)\b`)
	jsVerbs           = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "all": true, "options": true, "head": true}
)

func jsRoutes(repo string, files []File) []Route {
	paths := map[string]bool{}
	for _, f := range files {
		paths[f.Path] = true
	}
	var out []Route
	nodes := map[string]*jsNode{}
	var mounts []jsMount
	var regs []jsReg
	exports := map[string]string{} // file#export -> node key
	nestPrefix := ""
	for i := range files {
		f := &files[i]
		if !isJSPath(f.Path) || minified(*f) {
			continue
		}
		src := blankJSComments(f.Content)
		out = append(out, nextRoutes(repo, *f)...)
		if strings.Contains(src, "@Controller") {
			out = append(out, nestRoutes(repo, *f, src)...)
		}
		if m := reNestPrefix.FindStringSubmatch(src); m != nil {
			nestPrefix = m[1]
		}
		if !strings.Contains(src, ".use") && !strings.Contains(src, ".get") && !strings.Contains(src, ".post") &&
			!strings.Contains(src, ".route") && !strings.Contains(src, ".register") && !strings.Contains(src, ".put") &&
			!strings.Contains(src, ".delete") && !strings.Contains(src, ".patch") {
			continue
		}
		imports := jsImports(f.Path, src, paths)
		local := map[string]string{} // name -> node key
		ref := func(name string) string {
			if k, ok := local[name]; ok {
				return k
			}
			if b, ok := imports[name]; ok {
				return "@" + b // resolved after every file's exports are known
			}
			return ""
		}
		inTest := IsTestPath(f.Path)
		anon := 0
		// parseChain reads .use/.get/... calls after a router value starting
		// at pos, on node key; it returns the end of the chain.
		var parseChain func(key string, pos int) int
		var valueNode func(expr string, at int) string
		valueNode = func(expr string, at int) string {
			e := strings.TrimSpace(expr)
			if loc := reJSRouterHead.FindStringIndex(e); loc != nil {
				anon++
				k := f.Path + "#~" + itoa(at) + "_" + itoa(anon)
				n := &jsNode{key: k, root: reJSAppHead.MatchString(e)}
				nodes[k] = n
				if m := reJSPrefixOpt.FindStringSubmatch(e); m != nil {
					mounts = append(mounts, jsMount{parent: "", child: k, prefix: m[1]})
				}
				// the chain continues after the head's call
				head := e[loc[1]-1:]
				if strings.HasPrefix(head, "(") {
					rest := callArgs(e, loc[1])
					parseChain(k, at+loc[1]+len(rest)+1)
				}
				return k
			}
			if isGoIdent(strings.ReplaceAll(e, "$", "_")) {
				return ref(e)
			}
			return ""
		}
		parseChain = func(key string, pos int) int {
			sub := "" // .route(path) prefix for the following verbs
			for {
				j := pos
				for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
					j++
				}
				if j >= len(src) || src[j] != '.' {
					return pos
				}
				k := j + 1
				for k < len(src) && (src[k] == ' ' || src[k] == '\n') {
					k++
				}
				e := k
				for e < len(src) && (src[e] == '_' || src[e] == '$' || src[e] >= 'a' && src[e] <= 'z' || src[e] >= 'A' && src[e] <= 'Z') {
					e++
				}
				name := src[k:e]
				for e < len(src) && (src[e] == ' ' || src[e] == '\n') {
					e++
				}
				if e >= len(src) || src[e] != '(' {
					return pos
				}
				args := callArgs(src, e+1)
				next := e + 1 + len(args) + 1
				parts := splitArgs(args)
				switch {
				case name == "use" || name == "register":
					prefix := ""
					rest := parts
					if len(parts) > 0 {
						if s, ok := jsRouteLit(parts[0]); ok {
							prefix, rest = s, parts[1:]
						}
					}
					for _, a := range rest {
						if c := valueNode(a, e+1); c != "" {
							mounts = append(mounts, jsMount{parent: key, child: c, prefix: prefix, test: inTest})
						}
					}
					if name == "register" && len(rest) > 1 { // fastify.register(plugin, { prefix })
						if m := reJSPrefixOpt.FindStringSubmatch(args); m != nil {
							if c := valueNode(rest[0], e+1); c != "" {
								mounts[len(mounts)-1].prefix = m[1]
							}
						}
					}
				case name == "route":
					if len(parts) > 0 {
						if s, ok := jsRouteLit(parts[0]); ok {
							sub = s
						}
					}
				case jsVerbs[name]:
					p, rest := sub, parts
					if len(parts) >= 2 {
						if s, ok := jsRouteLit(parts[0]); ok {
							p, rest = s, parts[1:]
						} else if sub == "" {
							return next // not a route: app.get("setting") or a call on data
						}
					} else if sub == "" {
						return next
					}
					method := strings.ToUpper(name)
					if method == "ALL" {
						method = "*"
					}
					h := ""
					if len(rest) > 0 {
						h = jsHandlerName(rest[len(rest)-1])
					}
					regs = append(regs, jsReg{node: key, method: method, path: p, handler: h, file: f.Path, line: lineAtOffset(src, j)})
				default:
					return pos
				}
				pos = next
			}
		}
		// declarations: const X = <router value>
		for _, m := range reJSDecl.FindAllStringSubmatchIndex(src, -1) {
			rhs := src[m[1]:]
			if reJSRouterHead.MatchString(rhs) {
				k := valueNode(jsExprAt(src, m[1]), m[1])
				if k != "" {
					local[src[m[2]:m[3]]] = k
				}
			}
		}
		// statements on a named router: X.use(...), X.get(...), X.route(...)
		for _, m := range reJSChainStart.FindAllStringSubmatchIndex(src, -1) {
			name := src[m[2]:m[3]]
			k := ref(name)
			if k == "" {
				continue // not a router this file knows (axios.get, res.get, map.get)
			}
			parseChain(k, m[2]+len(name))
		}
		// exports
		for _, re := range []*regexp.Regexp{reJSExportDefault, reJSModuleExports} {
			for _, m := range re.FindAllStringIndex(src, -1) {
				e := jsExprAt(src, m[1])
				if k := valueNode(e, m[1]); k != "" {
					exports[f.Path+"#default"] = k
				}
			}
		}
		for _, m := range reJSDecl.FindAllStringSubmatchIndex(src, -1) {
			if m[0] >= 7 && strings.HasSuffix(strings.TrimSpace(src[max(0, m[0]-7):m[0]]), "export") {
				if k, ok := local[src[m[2]:m[3]]]; ok {
					exports[f.Path+"#"+src[m[2]:m[3]]] = k
				}
			}
		}
		for _, m := range reJSExportList.FindAllStringSubmatch(src, -1) {
			for _, it := range strings.Split(m[1], ",") {
				fs := strings.Fields(it)
				if len(fs) == 0 {
					continue
				}
				name, as := fs[0], fs[0]
				if len(fs) == 3 && fs[1] == "as" {
					as = fs[2]
				}
				if k, ok := local[name]; ok {
					exports[f.Path+"#"+as] = k
				}
			}
		}
	}
	// resolve imported references ("@file#export") to nodes
	resolve := func(k string) string {
		for i := 0; i < 4 && strings.HasPrefix(k, "@"); i++ {
			k = exports[k[1:]]
		}
		if strings.HasPrefix(k, "@") {
			return ""
		}
		return k
	}
	parents := map[string][]jsMount{}
	for _, m := range mounts {
		m.child = resolve(m.child)
		if m.parent != "" {
			m.parent = resolve(m.parent)
		}
		if m.child == "" {
			continue
		}
		parents[m.child] = append(parents[m.child], m)
	}
	var prefixes func(k string, depth int) []rpfx
	prefixes = func(k string, depth int) []rpfx {
		if n := nodes[k]; n != nil && n.root {
			return []rpfx{{}}
		}
		ps := parents[k]
		if len(ps) == 0 || depth > 8 {
			return []rpfx{{root: "?"}}
		}
		var out []rpfx
		for _, m := range ps {
			if m.parent == "" { // the router's own prefix option
				out = append(out, rpfx{root: "?", path: m.prefix})
				continue
			}
			for _, p := range prefixes(m.parent, depth+1) {
				out = append(out, rpfx{root: p.root, path: mountPath(p.path, m.prefix), test: p.test || m.test})
			}
		}
		return out
	}
	for _, r := range regs {
		k := resolve(r.node)
		if k == "" {
			continue
		}
		for _, p := range prefixes(k, 0) {
			out = append(out, Route{Repo: repo, Path: r.file, Line: r.line, Method: r.method,
				Pattern: normURL(mountPath(p.path, r.path)), Handler: r.handler, Framework: "express", Partial: p.root != "", Test: p.test})
		}
	}
	if nestPrefix != "" {
		for i := range out {
			if out[i].Framework == "nestjs" {
				out[i].Pattern = normURL(mountPath(nestPrefix, out[i].Pattern))
			}
		}
	}
	return out
}

func isJSPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs":
		return !strings.HasSuffix(p, ".d.ts")
	}
	return false
}

// jsImports maps a file's imported names to "file#export" of the repo module
// they come from (relative paths, @/ ~/ aliases); packages are left out.
func jsImports(from, src string, paths map[string]bool) map[string]string {
	out := map[string]string{}
	mod := func(spec string) string { return jsResolveModule(from, spec, paths) }
	named := func(list, file string) {
		for _, it := range strings.Split(list, ",") {
			fs := strings.Fields(strings.TrimSpace(it))
			if len(fs) == 0 {
				continue
			}
			name, as := fs[0], fs[0]
			if len(fs) == 3 && (fs[1] == "as" || fs[1] == ":") {
				as = fs[2]
			}
			if len(fs) == 2 && strings.HasSuffix(fs[0], ":") { // { router: r }
				name, as = strings.TrimSuffix(fs[0], ":"), fs[1]
			}
			out[as] = file + "#" + name
		}
	}
	for _, m := range reJSImportDefault.FindAllStringSubmatch(src, -1) {
		if file := mod(m[3]); file != "" {
			out[m[1]] = file + "#default"
			if m[2] != "" {
				named(m[2], file)
			}
		}
	}
	for _, m := range reJSImportNamed.FindAllStringSubmatch(src, -1) {
		if file := mod(m[2]); file != "" {
			named(m[1], file)
		}
	}
	for _, m := range reJSImportStar.FindAllStringSubmatch(src, -1) {
		if file := mod(m[2]); file != "" {
			out[m[1]] = file + "#default"
		}
	}
	for _, m := range reJSRequire.FindAllStringSubmatch(src, -1) {
		if file := mod(m[2]); file != "" {
			out[m[1]] = file + "#default"
		}
	}
	for _, m := range reJSRequireNamed.FindAllStringSubmatch(src, -1) {
		if file := mod(m[2]); file != "" {
			named(m[1], file)
		}
	}
	return out
}

// jsResolveModule is the repo file an import specifier names, "" for a package.
func jsResolveModule(from, spec string, paths map[string]bool) string {
	var cands []string
	switch {
	case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../") || spec == "." || spec == "..":
		cands = []string{path.Join(path.Dir(from), spec)}
	case strings.HasPrefix(spec, "@/") || strings.HasPrefix(spec, "~/"):
		cands = []string{path.Join("src", spec[2:]), spec[2:]}
	default:
		return ""
	}
	for _, c := range cands {
		c = strings.TrimPrefix(path.Clean(c), "./")
		if paths[c] && isJSPath(c) {
			return c
		}
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs"} {
			if paths[c+ext] {
				return c + ext
			}
		}
		for _, ext := range []string{".ts", ".tsx", ".js", ".jsx"} {
			if paths[path.Join(c, "index"+ext)] {
				return path.Join(c, "index"+ext)
			}
		}
	}
	return ""
}

// jsRouteLit reads '…', "…" or `…` (a template's ${…} becomes {}).
func jsRouteLit(a string) (string, bool) {
	a = strings.TrimSpace(a)
	if len(a) < 2 {
		return "", false
	}
	q := a[0]
	if (q != '\'' && q != '"' && q != '`') || a[len(a)-1] != q {
		return "", false
	}
	s := a[1 : len(a)-1]
	if q == '`' {
		s = regexp.MustCompile(`\$\{[^}]*\}`).ReplaceAllString(s, "{}")
	}
	return s, true
}

// jsHandlerName: controller.list -> list; an inline function -> "".
func jsHandlerName(h string) string {
	h = strings.TrimSpace(h)
	if h == "" || strings.Contains(h, "=>") || strings.HasPrefix(h, "function") || strings.HasPrefix(h, "async") || strings.ContainsAny(h, "({") {
		return ""
	}
	if i := strings.LastIndex(h, "."); i >= 0 {
		h = h[i+1:]
	}
	return h
}

// jsExprAt reads the expression starting at pos: up to a ';' or a newline at
// depth 0 that does not continue a call chain on the next line.
func jsExprAt(src string, pos int) string {
	depth := 0
	for i := pos; i < len(src); i++ {
		switch c := src[i]; c {
		case '\'', '"', '`':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			if depth--; depth < 0 {
				return src[pos:i]
			}
		case ';':
			if depth == 0 {
				return src[pos:i]
			}
		case '\n':
			if depth == 0 {
				j := i + 1
				for j < len(src) && (src[j] == ' ' || src[j] == '\t' || src[j] == '\n' || src[j] == '\r') {
					j++
				}
				if j >= len(src) || src[j] != '.' {
					return src[pos:i]
				}
			}
		}
	}
	return src[pos:]
}

// blankJSComments replaces // and /* */ comments with spaces (newlines kept,
// so offsets and lines do not move).
func blankJSComments(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		switch c := b[i]; c {
		case '\'', '"', '`':
			for i++; i < len(b) && b[i] != c; i++ {
				if b[i] == '\\' {
					i++
				} else if b[i] == '\n' && c != '`' {
					break
				}
			}
		case '/':
			if i+1 < len(b) && b[i+1] == '/' {
				for ; i < len(b) && b[i] != '\n'; i++ {
					b[i] = ' '
				}
			} else if i+1 < len(b) && b[i+1] == '*' {
				for ; i+1 < len(b) && !(b[i] == '*' && b[i+1] == '/'); i++ {
					if b[i] != '\n' {
						b[i] = ' '
					}
				}
				if i+1 < len(b) {
					b[i], b[i+1] = ' ', ' '
					i++
				}
			}
		}
	}
	return string(b)
}

// nestRoutes reads a NestJS controller: @Controller('x') + @Get('y') -> GET x/y.
func nestRoutes(repo string, f File, src string) []Route {
	var out []Route
	ctrls := reNestController.FindAllStringSubmatchIndex(src, -1)
	for ci, c := range ctrls {
		base := ""
		if c[2] >= 0 {
			base = src[c[2]:c[3]]
		} else if c[4] >= 0 {
			base = src[c[4]:c[5]]
		}
		end := len(src)
		if ci+1 < len(ctrls) {
			end = ctrls[ci+1][0]
		}
		for _, m := range reNestMethod.FindAllStringSubmatchIndex(src[c[1]:end], -1) {
			sub := ""
			if m[4] >= 0 {
				sub = src[c[1]+m[4] : c[1]+m[5]]
			}
			method := strings.ToUpper(src[c[1]+m[2] : c[1]+m[3]])
			if method == "ALL" {
				method = "*"
			}
			out = append(out, Route{Repo: repo, Path: f.Path, Line: lineAtOffset(src, c[1]+m[0]), Method: method,
				Pattern: normURL(mountPath(base, sub)), Handler: src[c[1]+m[6] : c[1]+m[7]], Framework: "nestjs"})
		}
	}
	return out
}

// nextRoutes reads Next.js file routes: app/**/route.ts exports one function
// per method; pages/api/** serves any method. Route groups "(x)" do not
// appear in the URL; [id] and [...rest] are parameters.
func nextRoutes(repo string, f File) []Route {
	p := f.Path
	seg := func(dir string) string {
		var out []string
		for _, s := range strings.Split(dir, "/") {
			switch {
			case s == "" || strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") || strings.HasPrefix(s, "@"):
			case strings.HasPrefix(s, "["):
				out = append(out, "{}")
			default:
				out = append(out, s)
			}
		}
		return "/" + strings.Join(out, "/")
	}
	base := path.Base(p)
	stem := strings.TrimSuffix(base, path.Ext(base))
	if i := strings.Index(p, "app/"); stem == "route" && i >= 0 && (i == 0 || p[i-1] == '/') {
		dir := path.Dir(p[i+len("app/"):])
		if dir == "." {
			dir = ""
		}
		var out []Route
		for _, m := range reNextVerb.FindAllStringSubmatchIndex(f.Content, -1) {
			out = append(out, Route{Repo: repo, Path: p, Line: lineAtOffset(f.Content, m[0]), Method: f.Content[m[2]:m[3]],
				Pattern: normURL(seg(dir)), Handler: f.Content[m[2]:m[3]], Framework: "nextjs"})
		}
		return out
	}
	if i := strings.Index(p, "pages/api/"); i >= 0 && (i == 0 || p[i-1] == '/') {
		rel := strings.TrimSuffix(p[i+len("pages/"):], path.Ext(p))
		if path.Base(rel) == "index" {
			rel = path.Dir(rel)
		}
		return []Route{{Repo: repo, Path: p, Line: 1, Method: "*", Pattern: normURL(seg(rel)), Handler: "default", Framework: "nextjs"}}
	}
	return nil
}
