// modules.go gives Python and JavaScript/TypeScript symbols their module: in
// those languages a module is a FILE, so two files of one directory may each
// define create_user (or export GET), and they are different functions. A
// symbol's package is "dir.module" there ("dir" for __init__.py and index.*,
// which a directory import names), and an import is resolved to the module it
// names — relative paths, Python source roots (src/, backend/), JS index files —
// so a call through it lands on that module's definition. Pure (ADR-018).
package processing

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// fileModuleLangs are the languages whose module is the file.
var fileModuleLangs = map[string]bool{"python": true, "javascript": true, "typescript": true, "vue": true, "svelte": true, "rust": true}

var jsExts = []string{".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte"}

// pkgOfFile is the package qualifier of a file's symbols: its directory, plus
// the module name for languages whose module is the file.
func pkgOfFile(p, lang string) string {
	dir := pkgOf(p)
	if !fileModuleLangs[lang] {
		return dir
	}
	stem := moduleStem(p)
	if stem == "" || stem == "__init__" || stem == "index" {
		return dir
	}
	if lang == "rust" && (stem == "mod" || stem == "lib" || stem == "main") {
		return dir // the directory's module, or the crate root
	}
	return qualify(dir, stem)
}

// moduleStem is a file's module name: its base without the extension, with
// any other dots (x.test.ts, x.d.ts) made underscores so a qid stays one
// segment per name.
func moduleStem(p string) string {
	b := path.Base(p)
	if i := strings.Index(b, "."); i >= 0 {
		ext := b[i:]
		b = b[:i]
		if strings.Count(ext, ".") > 1 { // x.test.ts -> x_test
			b += "_" + strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(ext, "."), path.Ext(ext)), ".", "_")
		}
	}
	return b
}

// moduleIndex knows the tree's files and directories, to resolve imports.
type moduleIndex struct {
	files map[string]string // path -> language
	dirs  map[string]bool
	ts    []tsPaths // tsconfig/jsconfig path aliases, deepest config first
	// defaults is the name a JS/TS module exports as default (export
	// default Name), by the module's package id
	defaults map[string]string
	// JS/TS re-exports, by the exporting module's package id: names passed
	// on from another module (export { a as b } from './m', and imported
	// names), and whole modules (export * from './m')
	jsMods map[string]*jsModule
	// defaultClass is the class a module's default export is an instance of
	// (export default new Store(), or const store = new Store(); export
	// default store), by the module's package id
	defaultClass map[string]string
}

var reExportDefaultNew = regexp.MustCompile(`(?m)^export\s+default\s+new\s+([A-Z][\w$]*)\s*[(<]`)

type jsModule struct {
	path    string                   // the module's file
	lang    string                   // its language
	reexp   map[string]ImportBinding // exported name -> where it comes from (Symbol "default" = that module's default)
	imports map[string]ImportBinding // module-level imports: an exported name may be one of them
	stars   []string                 // export * from these modules
}

var (
	reExportFrom = regexp.MustCompile(`(?m)^export\s+(?:type\s+)?\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]`)
	reExportStar = regexp.MustCompile(`(?m)^export\s+\*\s+from\s*['"]([^'"]+)['"]`)
)

// reExportDefault reads `export default Name` (a declared function or
// class, or a name declared elsewhere in the module).
var reExportDefault = regexp.MustCompile(`(?m)^export\s+default\s+(?:async\s+)?(?:function\*?\s+|class\s+)?([A-Za-z_$][\w$]*)\s*(?:;|$|\(|\{|<|extends\b)`)

func newModuleIndex(files []ParsedFile) moduleIndex {
	mi := moduleIndex{files: map[string]string{}, dirs: map[string]bool{"": true}, defaults: map[string]string{}, jsMods: map[string]*jsModule{}, defaultClass: map[string]string{}}
	for _, f := range files {
		mi.files[f.Path] = f.Lang
		for d := path.Dir(f.Path); d != "." && d != "/" && !mi.dirs[d]; d = path.Dir(d) {
			mi.dirs[d] = true
		}
		if langFamily(f.Lang) == "js" {
			src := f.Content
			if src == "" && f.Load != nil {
				src, _ = f.Load()
			}
			pkg := pkgOfFile(f.Path, f.Lang)
			if m := reExportDefault.FindStringSubmatch(src); m != nil && !jsDefaultNotName[m[1]] {
				mi.defaults[pkg] = m[1]
				inst := regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:const|let|var)\s+` + regexp.QuoteMeta(m[1]) + `\s*(?::[^=]+)?=\s*new\s+([A-Z][\w$]*)\s*[(<]`)
				if c := inst.FindStringSubmatch(src); c != nil {
					mi.defaultClass[pkg] = c[1]
				}
			}
			if m := reExportDefaultNew.FindStringSubmatch(src); m != nil {
				mi.defaultClass[pkg] = m[1]
			}
			jm := &jsModule{path: f.Path, lang: f.Lang, reexp: map[string]ImportBinding{}, imports: importsByLocal(f.Imports)}
			for _, m := range reExportFrom.FindAllStringSubmatch(src, -1) {
				for _, spec := range strings.Split(m[1], ",") {
					fs := strings.Fields(strings.TrimPrefix(strings.TrimSpace(spec), "type "))
					switch {
					case len(fs) == 1:
						jm.reexp[fs[0]] = ImportBinding{Local: fs[0], Module: m[2], Symbol: fs[0]}
					case len(fs) == 3 && fs[1] == "as":
						jm.reexp[fs[2]] = ImportBinding{Local: fs[2], Module: m[2], Symbol: fs[0]}
					}
				}
			}
			for _, m := range reExportStar.FindAllStringSubmatch(src, -1) {
				jm.stars = append(jm.stars, m[1])
			}
			mi.jsMods[pkg] = jm
		}
		if b := path.Base(f.Path); (strings.HasPrefix(b, "tsconfig") || strings.HasPrefix(b, "jsconfig")) && strings.HasSuffix(b, ".json") {
			src := f.Content
			if src == "" && f.Load != nil {
				src, _ = f.Load()
			}
			if tp, ok := parseTSConfig(f.Path, src); ok {
				mi.ts = append(mi.ts, tp)
			}
		}
	}
	sort.SliceStable(mi.ts, func(i, j int) bool { return len(mi.ts[i].dir) > len(mi.ts[j].dir) })
	return mi
}

// resolve is the package of the repo module an import names, from a file of
// lang at callerPath; false for a module outside the repo.
func (mi moduleIndex) resolve(lang, callerPath, module string) (string, bool) {
	switch {
	case lang == "python":
		return mi.resolvePython(callerPath, module)
	case lang == "rust":
		return "", false // use paths are resolved by their first segment (rustModules)
	case lang == "java":
		return mi.resolveJava(callerPath, module)
	case fileModuleLangs[lang]:
		return mi.resolveJS(callerPath, module)
	}
	return "", false
}

func (mi moduleIndex) resolvePython(callerPath, module string) (string, bool) {
	var base string
	rest := module
	if strings.HasPrefix(module, ".") {
		n := len(module) - len(strings.TrimLeft(module, "."))
		rest = module[n:]
		base = path.Dir(callerPath)
		for i := 1; i < n; i++ {
			base = path.Dir(base)
		}
		if base == "." {
			base = ""
		}
		return mi.pyTarget(path.Join(base, strings.ReplaceAll(rest, ".", "/")))
	}
	// an absolute import is found under a source root: the repo root, src/,
	// or a directory such as backend/ holding the package — the one nearest
	// the caller wins
	first := strings.SplitN(rest, ".", 2)[0]
	var ancestors, others []string
	for d := range mi.dirs {
		if !mi.dirs[path.Join(d, first)] && mi.files[path.Join(d, first+".py")] == "" {
			continue
		}
		if mi.files[path.Join(d, "__init__.py")] != "" {
			continue // a package is not a source root: `import json` inside it is the stdlib's
		}
		if d == "" || strings.HasPrefix(callerPath, d+"/") {
			ancestors = append(ancestors, d)
		} else {
			others = append(others, d)
		}
	}
	best, found := "", false
	for _, d := range ancestors { // the deepest root above the caller
		if !found || len(d) > len(best) {
			best, found = d, true
		}
	}
	if !found {
		for _, d := range others { // else the shallowest root anywhere
			if !found || len(d) < len(best) {
				best, found = d, true
			}
		}
	}
	if !found {
		return "", false
	}
	return mi.pyTarget(path.Join(best, strings.ReplaceAll(rest, ".", "/")))
}

func (mi moduleIndex) pyTarget(p string) (string, bool) {
	p = strings.TrimPrefix(path.Clean(p), "./")
	if p == "." {
		p = ""
	}
	if mi.files[p+".py"] != "" {
		return pkgOfFile(p+".py", "python"), true
	}
	if mi.files[path.Join(p, "__init__.py")] != "" || mi.dirs[p] {
		return pkgOf(path.Join(p, "__init__.py")), true
	}
	return "", false
}

func (mi moduleIndex) resolveJS(callerPath, module string) (string, bool) {
	var cands []string
	switch {
	case strings.HasPrefix(module, "./") || strings.HasPrefix(module, "../") || module == "." || module == "..":
		cands = []string{path.Join(path.Dir(callerPath), module)}
	case strings.HasPrefix(module, "@/") || strings.HasPrefix(module, "~/") || strings.HasPrefix(module, "$lib/"):
		// a path alias to a source root: src/ (or src/lib for $lib), the one
		// nearest the caller in a monorepo
		rest, under := module[2:], "src"
		if strings.HasPrefix(module, "$lib/") {
			rest, under = module[5:], "src/lib"
		}
		for _, root := range mi.rootsNamed(under, callerPath) {
			cands = append(cands, path.Join(root, rest))
		}
		cands = append(cands, rest)
	case strings.HasPrefix(module, "/"):
		cands = []string{strings.TrimPrefix(module, "/")}
	default:
		// a package name — unless a tsconfig/jsconfig over the caller maps it
		// (paths aliases, baseUrl) onto the repo's own files
		for _, tp := range mi.ts {
			if tp.dir == "" || strings.HasPrefix(callerPath, tp.dir+"/") {
				cands = append(cands, tp.candidates(module)...)
			}
		}
		if len(cands) == 0 {
			return "", false
		}
	}
	for _, c := range cands {
		c = strings.TrimPrefix(path.Clean(c), "./")
		if mi.files[c] != "" && fileModuleLangs[mi.files[c]] {
			return pkgOfFile(c, mi.files[c]), true // written with its extension
		}
		for _, ext := range jsExts {
			if l := mi.files[c+ext]; l != "" {
				return pkgOfFile(c+ext, l), true
			}
		}
		for _, ext := range jsExts {
			if l := mi.files[path.Join(c, "index"+ext)]; l != "" {
				return pkgOfFile(path.Join(c, "index"+ext), l), true
			}
		}
	}
	return "", false
}

// rootsNamed lists the directories named under ("src", "src/lib") — at the
// top or nested in a monorepo package — nearest the caller first.
func (mi moduleIndex) rootsNamed(under, callerPath string) []string {
	var out []string
	for d := range mi.dirs {
		if d == under || strings.HasSuffix(d, "/"+under) {
			out = append(out, d)
		}
	}
	shared := func(d string) int {
		n := 0
		for n < len(d) && n < len(callerPath) && d[n] == callerPath[n] {
			n++
		}
		return n
	}
	for i := 1; i < len(out); i++ { // insertion sort: few roots
		for j := i; j > 0 && (shared(out[j]) > shared(out[j-1]) || shared(out[j]) == shared(out[j-1]) && out[j] < out[j-1]); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// qualifyModule is qualify for a package that may carry a module segment
// ("dir.module"): a symbol named after its module — a component in its own
// file (CartItem in CartItem.tsx) or that class's methods — is not repeated
// ("dir.CartItem", not "dir.CartItem.CartItem").
func qualifyModule(pkg, local string) string {
	if i := strings.LastIndex(pkg, "."); i >= 0 && !strings.Contains(pkg[i:], "/") {
		if stem := pkg[i+1:]; local == stem || strings.HasPrefix(local, stem+".") {
			return qualify(pkg[:i], local)
		}
	}
	return qualify(pkg, local)
}

// resolveJava is the directory of a Java package (io.spring.core ->
// .../io/spring/core). The same package can sit in several source sets
// (src/main/java, src/test/java): the one nearest the importing file wins.
func (mi moduleIndex) resolveJava(callerPath, pkg string) (string, bool) {
	suffix := strings.ReplaceAll(pkg, ".", "/")
	best, bestShared := "", -1
	for d := range mi.dirs {
		if d != suffix && !strings.HasSuffix(d, "/"+suffix) {
			continue
		}
		shared := 0
		for shared < len(d) && shared < len(callerPath) && d[shared] == callerPath[shared] {
			shared++
		}
		if shared > bestShared || shared == bestShared && d < best {
			best, bestShared = d, shared
		}
	}
	return best, bestShared >= 0
}

// jsDefaultNotName are words reExportDefault can meet that name no binding.
var jsDefaultNotName = map[string]bool{"new": true, "function": true, "class": true, "async": true, "await": true}

// resolveExport follows a JS/TS module's exports to the definition of name
// (a re-export, an imported name it exports, `export *`, the default
// export) and returns the module and name it is defined under, for which
// defined reports true. ok is false when the chain leaves the repo or ends
// without a definition.
func (mi moduleIndex) resolveExport(pkg, name string, defined func(pkg, name string) bool) (string, string, bool) {
	seen := map[string]bool{}
	var walk func(pkg, name string, depth int) (string, string, bool)
	walk = func(pkg, name string, depth int) (string, string, bool) {
		if depth > 10 || seen[pkg+"\x00"+name] {
			return "", "", false
		}
		seen[pkg+"\x00"+name] = true
		if name == "default" {
			if d := mi.defaults[pkg]; d != "" {
				return walk(pkg, d, depth+1)
			}
			return "", "", false
		}
		if defined(pkg, name) {
			return pkg, name, true
		}
		jm := mi.jsMods[pkg]
		if jm == nil {
			return "", "", false
		}
		follow := func(b ImportBinding) (string, string, bool) {
			p, ok := mi.resolve(jm.lang, jm.path, b.Module)
			if !ok {
				return "", "", false
			}
			n := b.Symbol
			if n == "" {
				if !b.Default {
					return "", "", false // a namespace (import * as x) is not a definition
				}
				n = "default"
			}
			return walk(p, n, depth+1)
		}
		if b, ok := jm.reexp[name]; ok {
			return follow(b)
		}
		if b, ok := jm.imports[name]; ok {
			return follow(b)
		}
		for _, star := range jm.stars {
			if p, ok := mi.resolve(jm.lang, jm.path, star); ok {
				if rp, rn, ok := walk(p, name, depth+1); ok {
					return rp, rn, true
				}
			}
		}
		return "", "", false
	}
	return walk(pkg, name, 0)
}
