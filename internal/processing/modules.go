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
	"strings"
)

// fileModuleLangs are the languages whose module is the file.
var fileModuleLangs = map[string]bool{"python": true, "javascript": true, "typescript": true, "vue": true, "svelte": true}

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
}

func newModuleIndex(files []ParsedFile) moduleIndex {
	mi := moduleIndex{files: map[string]string{}, dirs: map[string]bool{"": true}}
	for _, f := range files {
		mi.files[f.Path] = f.Lang
		for d := path.Dir(f.Path); d != "." && d != "/" && !mi.dirs[d]; d = path.Dir(d) {
			mi.dirs[d] = true
		}
	}
	return mi
}

// resolve is the package of the repo module an import names, from a file of
// lang at callerPath; false for a module outside the repo.
func (mi moduleIndex) resolve(lang, callerPath, module string) (string, bool) {
	switch {
	case lang == "python":
		return mi.resolvePython(callerPath, module)
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
		return "", false // a package name: outside the repo
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
