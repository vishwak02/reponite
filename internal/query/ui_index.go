// ui_index.go builds the per-repo frontend index `ui` reads: which files hold
// UI code, where each component is defined (a capitalized function/class
// that renders JSX, or a .vue/.svelte file), each file's imports and default
// export, and how a component name used in one file resolves to its
// definition (local → import → re-export → unique name). Pure over
// Store.Files (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// uiFile is one frontend source file.
type uiFile struct {
	Repo    string
	F       File
	Ext     string
	SFC     bool
	Code    string // comment-masked content (whole file)
	Script  string // the code to read declarations from (SFC: its <script>; else Code)
	Markup  string // SFC template / markup ("" for JS/TS: JSX lives in Code)
	Lines   lineIndex
	Imports map[string]uiImport
	Default string // the local name `export default` binds ("" unknown)
	defs    []*uiDef
}

// uiDef is one component definition.
type uiDef struct {
	Name       string
	File       *uiFile
	Start, End int    // 1-based lines
	Kind       string // function | class | sfc
}

// Handle is the drill-in id for brief/context: the indexed symbol id
// (dir.Name) when the component is a symbol; the file path otherwise.
func (d *uiDef) Handle() string {
	dir := path.Dir(d.File.F.Path)
	if dir == "." {
		return d.Name
	}
	return dir + "." + d.Name
}

// uiRepo is one repo's frontend index.
type uiRepo struct {
	Name    string
	Files   []*uiFile
	ByPath  map[string]*uiFile
	ByName  map[string][]*uiDef
	noExt   map[string]string // path without extension -> path (module resolution)
	exposes map[string]string // module-federation exposed name -> path
	snap    *RefSnapshot
}

var uiExts = map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".vue": true, ".svelte": true}

var (
	exportDefault     = regexp.MustCompile(`\bexport\s+default\s+`)
	exportDefaultDecl = regexp.MustCompile(`^(?:async\s+)?(?:function\s*\*?|class)\s*([A-Za-z_$][\w$]*)?`)
	exportAsDefault   = regexp.MustCompile(`\bexport\s*\{[^}]*?\b([A-Za-z_$][\w$]*)\s+as\s+default\b`)
	typeDeclLine      = regexp.MustCompile(`^\s*(?:export\s+)?(?:declare\s+)?(?:interface|type|enum|namespace)\b`)
	createsElements   = regexp.MustCompile(`\b(?:React\.)?createElement\(|\b_?jsxs?\(`)
	classComponent    = regexp.MustCompile(`\bextends\s+(?:React\.)?(?:Pure)?Component\b`)
	exposesBlock      = regexp.MustCompile(`\bexposes\s*:\s*\{`)
	exposeEntry       = regexp.MustCompile(`['"]\./([\w$./-]+)['"]\s*:\s*['"]([^'"]+)['"]`)
)

// buildUIRepo indexes one repo's UI files.
func buildUIRepo(s Store, repo, ref string) *uiRepo {
	r := &uiRepo{Name: repo, ByPath: map[string]*uiFile{}, ByName: map[string][]*uiDef{}, noExt: map[string]string{}, exposes: map[string]string{}}
	return buildUIRepoFrom(r, s.Files(repo, ref))
}

func buildUIRepoFrom(r *uiRepo, files []File) *uiRepo {
	repo := r.Name
	for _, f := range files {
		ext := strings.ToLower(path.Ext(f.Path))
		if !uiExts[ext] || minified(f) || strings.HasSuffix(f.Path, ".d.ts") {
			continue
		}
		uf := &uiFile{Repo: repo, F: f, Ext: ext, Lines: newLineIndex(f.Content)}
		if IsSFCPath(f.Path) {
			sfc := SplitSFC(f.Path, f.Content)
			uf.SFC = true
			uf.Script = maskComments(sfc.Script)
			uf.Markup = maskHTMLComments(sfc.Markup)
			uf.Code = uf.Script
		} else {
			uf.Code = maskComments(f.Content)
			uf.Script = uf.Code
		}
		uf.Imports = parseImports(uf.Script)
		uf.Default = defaultExport(uf.Script)
		r.Files = append(r.Files, uf)
		r.ByPath[f.Path] = uf
		r.noExt[strings.TrimSuffix(f.Path, path.Ext(f.Path))] = f.Path
		if exposesBlock.MatchString(uf.Code) {
			for _, m := range exposesBlock.FindAllStringIndex(uf.Code, -1) {
				if j := matchClose(uf.Code, m[1]-1); j > 0 {
					for _, e := range exposeEntry.FindAllStringSubmatch(uf.Code[m[1]:j], -1) {
						r.exposes[e[1]] = strings.TrimPrefix(e[2], "./")
					}
				}
			}
		}
		r.collectDefs(uf)
	}
	for _, defs := range r.ByName {
		sort.Slice(defs, func(i, j int) bool { return defs[i].File.F.Path < defs[j].File.F.Path })
	}
	return r
}

// maskHTMLComments blanks <!-- … --> in template markup.
func maskHTMLComments(s string) string {
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "-->")
		end := len(s)
		if j >= 0 {
			end = i + j + 3
		}
		s = s[:i] + blankAll(s[i:end]) + s[end:]
	}
}

// defaultExport is the local name the file's default export binds.
func defaultExport(code string) string {
	if m := exportAsDefault.FindStringSubmatch(code); m != nil {
		return m[1]
	}
	loc := exportDefault.FindStringIndex(code)
	if loc == nil {
		return ""
	}
	rest := code[loc[1]:]
	if m := exportDefaultDecl.FindStringSubmatch(rest); m != nil {
		return m[1] // "" for an anonymous function/class
	}
	// export default connect(mapState)(OrdersPage) / memo(Cart) / Cart;
	// The wrapped component is the last capitalized identifier of the expression.
	end := len(rest)
	if i := strings.IndexAny(rest, ";\n"); i >= 0 {
		end = i
	}
	if strings.HasPrefix(rest, "{") || strings.HasPrefix(rest, "defineComponent(") {
		return "" // an options object (Vue): the SFC itself is the component
	}
	expr := rest[:end]
	if i := strings.IndexByte(expr, '('); i >= 0 {
		// a wrapper call may span lines
		if j := matchClose(rest, i); j > 0 {
			expr = rest[:j+1]
			for k := j + 1; k < len(rest) && rest[k] == '('; {
				if l := matchClose(rest, k); l > 0 {
					expr = rest[:l+1]
					k = l + 1
				} else {
					break
				}
			}
		}
	}
	name := ""
	for _, id := range identRe.FindAllString(expr, -1) {
		if isUpperIdent(id) {
			name = id
		}
	}
	if name == "" && isIdent(strings.TrimSpace(expr)) {
		name = strings.TrimSpace(expr)
	}
	return name
}

var identRe = regexp.MustCompile(`[A-Za-z_$][\w$]*`)

// collectDefs finds the component definitions in a file: an SFC is one
// component; in JS/TS a capitalized function/class span whose text renders
// JSX (or creates elements) is one, and so is an anonymous default export.
func (r *uiRepo) collectDefs(uf *uiFile) {
	add := func(d *uiDef) {
		uf.defs = append(uf.defs, d)
		r.ByName[d.Name] = append(r.ByName[d.Name], d)
	}
	if uf.SFC {
		add(&uiDef{Name: SFCComponentName(uf.F.Path), File: uf, Start: 1, End: len(uf.Lines), Kind: "sfc"})
		return
	}
	seen := map[string]bool{}
	for _, sp := range uf.F.Symbols {
		if !isUpperIdent(sp.Name) || seen[sp.Name] {
			continue
		}
		body := uf.text(sp.StartLine, sp.EndLine)
		first := body
		if i := strings.IndexByte(first, '\n'); i >= 0 {
			first = first[:i]
		}
		if typeDeclLine.MatchString(first) {
			continue
		}
		kind := "function"
		switch {
		case classComponent.MatchString(first):
			kind = "class"
		case uf.Ext != ".ts" && hasJSX(body): // .ts cannot hold JSX (`<T>x` is a cast there)
		case createsElements.MatchString(body):
		default:
			continue
		}
		seen[sp.Name] = true
		add(&uiDef{Name: sp.Name, File: uf, Start: sp.StartLine, End: sp.EndLine, Kind: kind})
	}
	// An anonymous default export (`export default function () { return <div/> }`,
	// common for file-routed pages) is named by its file — or its directory
	// for an index file — the way the routers that load it name it.
	if uf.Default == "" {
		if loc := exportDefault.FindStringIndex(uf.Code); loc != nil {
			rest := uf.Code[loc[1]:]
			if m := exportDefaultDecl.FindStringSubmatch(rest); m != nil || strings.HasPrefix(rest, "(") || strings.HasPrefix(rest, "async") {
				if open := strings.IndexByte(rest, '{'); open >= 0 {
					if close := matchClose(rest, open); close > 0 && hasJSX(rest[open:close]) {
						name := fileComponentName(uf.F.Path)
						uf.Default = name
						add(&uiDef{Name: name, File: uf, Start: uf.Lines.line(loc[0]), End: uf.Lines.line(loc[1] + close), Kind: "function"})
					}
				}
			}
		}
	}
}

// hasJSX reports whether code renders a JSX element.
func hasJSX(code string) bool {
	return len(scanTags(code, true)) > 0
}

// fileComponentName names a file's anonymous component after the file, or its
// directory for index/page/route files.
func fileComponentName(p string) string {
	stem := strings.TrimSuffix(path.Base(p), path.Ext(p))
	switch stem {
	case "index", "page", "layout", "route", "+page", "+layout":
		if d := path.Base(path.Dir(p)); d != "." && d != "/" {
			return pascal(strings.Trim(d, "[]()."))
		}
	}
	return pascal(stem)
}

// text returns lines [from, to] of the file's masked code.
func (uf *uiFile) text(from, to int) string {
	a := uf.Lines.offset(from)
	b := len(uf.Code)
	if to < len(uf.Lines) {
		b = uf.Lines.offset(to+1) - 1
	}
	if a > b || a > len(uf.Code) {
		return ""
	}
	if b > len(uf.Code) {
		b = len(uf.Code)
	}
	return uf.Code[a:b]
}

// defNamed is the component definition named name in this file.
func (uf *uiFile) defNamed(name string) *uiDef {
	for _, d := range uf.defs {
		if d.Name == name {
			return d
		}
	}
	return nil
}

// resolveSpec maps an import specifier used in file `from` to a repo file:
// relative paths, then path aliases (`@/x`, `~/x`, `@app/x` → a file whose
// path ends in x/…). ok=false means a package (or an unindexed file).
func (r *uiRepo) resolveSpec(from, spec string) (string, bool) {
	try := func(base string) (string, bool) {
		base = strings.TrimSuffix(path.Clean(base), "/")
		if _, ok := r.ByPath[base]; ok {
			return base, true
		}
		if p, ok := r.noExt[base]; ok {
			return p, true
		}
		if p, ok := r.noExt[base+"/index"]; ok {
			return p, true
		}
		return "", false
	}
	if strings.HasPrefix(spec, ".") {
		return try(path.Join(path.Dir(from), spec))
	}
	if strings.HasPrefix(spec, "/") {
		return try(strings.TrimPrefix(spec, "/"))
	}
	rest := spec
	segs := strings.Split(spec, "/")
	if len(segs) > 1 && segs[0] == "$lib" {
		rest = "src/lib/" + strings.Join(segs[1:], "/") // SvelteKit
	} else if len(segs) > 1 && (strings.HasPrefix(segs[0], "@") || segs[0] == "~" || segs[0] == "#" || segs[0] == "src") {
		rest = strings.Join(segs[1:], "/")
	}
	for _, root := range []string{"", "src/", "app/", "lib/"} {
		if p, ok := try(root + rest); ok {
			return p, true
		}
	}
	if rest == spec || !strings.Contains(rest, "/") {
		return "", false // a bare package name: never matched to repo files by suffix
	}
	// An alias root we cannot see (tsconfig paths): the longest unique file
	// whose path ends in the remainder on a segment boundary.
	var hits []string
	restNoExt := strings.TrimSuffix(rest, path.Ext(rest))
	for noext, p := range r.noExt {
		if strings.HasSuffix(noext, "/"+rest) || strings.HasSuffix(noext, "/"+rest+"/index") || strings.HasSuffix(noext, "/"+restNoExt) {
			hits = append(hits, p)
		}
	}
	if len(hits) == 0 {
		return "", false
	}
	sort.Slice(hits, func(i, j int) bool {
		return len(hits[i]) < len(hits[j]) || len(hits[i]) == len(hits[j]) && hits[i] < hits[j]
	})
	return hits[0], true
}

// uiResolution says how a usage was tied to its definition.
const (
	resLocal  = "local"
	resImport = "import"
	resName   = "name"
)

// resolve maps a component name used in file uf to its definition. ext is
// the package specifier when the name is imported from outside the repo.
func (r *uiRepo) resolve(uf *uiFile, name string) (d *uiDef, how string, ext string) {
	if head, tail, ok := strings.Cut(name, "."); ok {
		// <UI.Button>: a namespace import, or a compound component (Form.Item)
		if im, ok := uf.Imports[head]; ok {
			p, found := r.resolveSpec(uf.F.Path, im.Spec)
			if !found {
				return nil, "", im.Spec
			}
			if im.Name == "*" {
				if d := r.exported(p, tail, 0); d != nil {
					return d, resImport, ""
				}
			}
		}
		name = tail
	}
	if d := uf.defNamed(name); d != nil {
		return d, resLocal, ""
	}
	if im, ok := uf.Imports[name]; ok {
		p, found := r.resolveSpec(uf.F.Path, im.Spec)
		if !found {
			return nil, "", im.Spec
		}
		if d := r.exported(p, im.Name, 0); d != nil {
			return d, resImport, ""
		}
		if im.Name == "default" {
			if t := r.ByPath[p]; t != nil && t.SFC && len(t.defs) > 0 {
				return t.defs[0], resImport, ""
			}
		}
	}
	defs := r.ByName[name]
	if len(defs) == 0 {
		return nil, "", ""
	}
	return closest(defs, uf.F.Path), resName, ""
}

// exported resolves export `name` ("default" or a named export) of file p,
// following `export … from` re-exports (index files) a few levels deep.
func (r *uiRepo) exported(p, name string, depth int) *uiDef {
	t := r.ByPath[p]
	if t == nil || depth > 4 {
		return nil
	}
	if t.SFC && name == "default" && len(t.defs) > 0 {
		return t.defs[0]
	}
	local := name
	if name == "default" {
		local = t.Default
		if local == "" {
			return nil
		}
	}
	if d := t.defNamed(local); d != nil {
		return d
	}
	if im, ok := t.Imports[local]; ok { // import X from './X'; export default X
		if q, found := r.resolveSpec(t.F.Path, im.Spec); found {
			return r.exported(q, im.Name, depth+1)
		}
	}
	named, star := reExports(t.Code)
	if im, ok := named[name]; ok {
		if q, found := r.resolveSpec(t.F.Path, im.Spec); found {
			return r.exported(q, im.Name, depth+1)
		}
	}
	for _, spec := range star {
		if q, found := r.resolveSpec(t.F.Path, spec); found {
			if d := r.exported(q, name, depth+1); d != nil {
				return d
			}
		}
	}
	return nil
}

// closest picks, among same-named definitions, the one sharing the longest
// directory prefix with the using file.
func closest(defs []*uiDef, from string) *uiDef {
	best, bestN := defs[0], -1
	for _, d := range defs {
		n := commonPrefix(path.Dir(d.File.F.Path), path.Dir(from))
		if n > bestN {
			best, bestN = d, n
		}
	}
	return best
}

func commonPrefix(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// defAt returns the component definition enclosing line ln of uf (innermost).
func (uf *uiFile) defAt(ln int) *uiDef {
	var best *uiDef
	for _, d := range uf.defs {
		if ln >= d.Start && ln <= d.End && (best == nil || d.End-d.Start < best.End-best.Start) {
			best = d
		}
	}
	return best
}

// fileDefault is the component a file's default export is (SFC: the file).
func (r *uiRepo) fileDefault(p string) *uiDef {
	return r.exported(p, "default", 0)
}

// SFCFacts is what the indexer records for a single-file component: the
// components its template renders, the handlers its markup binds, and its
// props contract (the component's signature).
type SFCFacts struct {
	Name     string
	Renders  []string
	Handlers []string
	Props    string // "name?:type;…" in declaration order
}

// SFCComponentFacts reads a .vue/.svelte file's component facts at index
// time, with the same rules `ui` applies at query time.
func SFCComponentFacts(p, src string) SFCFacts {
	r := &uiRepo{Name: "", ByPath: map[string]*uiFile{}, ByName: map[string][]*uiDef{}, noExt: map[string]string{}, exposes: map[string]string{}}
	r = buildUIRepoFrom(r, []File{{Path: p, Content: src}})
	uf := r.ByPath[p]
	if uf == nil || len(uf.defs) == 0 {
		return SFCFacts{}
	}
	d := uf.defs[0]
	out := SFCFacts{Name: d.Name}
	for _, rd := range renders(d) {
		out.Renders = append(out.Renders, rd.Name)
	}
	props := analyzeProps(r, d)
	for _, h := range analyzeHandlers(d, props) {
		if h.Handler != "(inline)" && !h.Prop {
			out.Handlers = appendUniq(out.Handlers, h.Handler)
		}
	}
	var b strings.Builder
	for _, pr := range props {
		b.WriteString(pr.Name)
		if pr.Optional {
			b.WriteByte('?')
		}
		b.WriteString(":" + pr.Type + ";")
	}
	out.Props = b.String()
	return out
}
