// cppvis.go decides which definitions a C++ file can name. A C++ call can
// only reach what its translation unit declares: the file itself and the
// headers it includes, directly or through other headers. A function or class
// with internal linkage (an anonymous namespace, a static function, a class
// defined in a source file) belongs to its own file. Name rules that ignore
// this link a call to whichever same-named definition the repo happens to
// hold — another test file's fixture, another .cpp's helper. Pure and
// stdlib-only (ADR-018).
package processing

import (
	"path"
	"strings"
)

// cppVisibility answers visible(caller file, definition) for C/C++.
type cppVisibility struct {
	facts    map[string]*TypeFacts      // file -> its C++ facts
	includes map[string][]string        // file -> the repo files its includes name
	closure  map[string]map[string]bool // file -> what it can name (seen)
	incl     map[string]map[string]bool // file -> its transitive includes
	rev      map[string][]string        // header -> the files including it
	homes    map[string][]string        // class name -> files defining it with a body
	declares map[string][]string        // free function name -> files declaring or defining it
	defaults map[string]int             // names some declaration gives a defaulted parameter -> the fewest arguments it takes
}

// maxVisFiles bounds the include closures kept: beyond it (a kernel-sized C
// tree) every definition stays visible, as before.
const maxVisFiles = 30000

func newCppVisibility(files []ParsedFile) *cppVisibility {
	v := &cppVisibility{facts: map[string]*TypeFacts{}, includes: map[string][]string{},
		closure: map[string]map[string]bool{}, incl: map[string]map[string]bool{}, homes: map[string][]string{}, declares: map[string][]string{}, defaults: map[string]int{}}
	byBase := map[string][]string{} // file basename -> C/C++ files
	n := 0
	for _, f := range files {
		if langFamily(f.Lang) != "c" {
			continue
		}
		n++
		byBase[path.Base(f.Path)] = append(byBase[path.Base(f.Path)], f.Path)
		if f.Types == nil {
			continue
		}
		v.facts[f.Path] = f.Types
		for cls := range f.Types.Classes {
			v.homes[cls] = append(v.homes[cls], f.Path)
		}
		for n := range f.Types.Nested {
			v.homes[n] = append(v.homes[n], f.Path)
		}
		for fn := range f.Types.Declared {
			v.declares[fn] = append(v.declares[fn], f.Path)
		}
		for fn, need := range f.Types.Defaults {
			if prev, ok := v.defaults[fn]; !ok || need < prev {
				v.defaults[fn] = need
			}
		}
	}
	if n == 0 || n > maxVisFiles || len(v.facts) == 0 {
		return nil
	}
	for p, tf := range v.facts {
		for _, inc := range tf.Includes {
			v.includes[p] = append(v.includes[p], resolveInclude(p, inc, byBase)...)
		}
	}
	return v
}

// resolveInclude maps an #include path to the repo files it can name: the
// file relative to the includer when there is one, else every file whose path
// ends with it (include directories are not known here).
func resolveInclude(from, inc string, byBase map[string][]string) []string {
	cands := byBase[path.Base(inc)]
	if len(cands) == 0 {
		return nil
	}
	rel := path.Clean(path.Join(path.Dir(from), inc))
	var out []string
	for _, c := range cands {
		if c == rel {
			return []string{c}
		}
		if c == inc || strings.HasSuffix(c, "/"+inc) {
			out = append(out, c)
		}
	}
	if len(out) > 1 {
		// two packages ship a header of that name: the build lists the
		// including package's own include directories first — the nearest
		best, keep := -1, []string(nil)
		for _, c := range out {
			switch n := sharedDirs(from, c); {
			case n > best:
				best, keep = n, []string{c}
			case n == best:
				keep = append(keep, c)
			}
		}
		out = keep
	}
	return out
}

// sharedDirs is how many leading directories paths a and b have in common.
func sharedDirs(a, b string) int {
	as, bs := strings.Split(path.Dir(a), "/"), strings.Split(path.Dir(b), "/")
	n := 0
	for n < len(as) && n < len(bs) && as[n] == bs[n] {
		n++
	}
	return n
}

// included is the file and every header it includes, transitively.
func (v *cppVisibility) included(file string) map[string]bool {
	if c, ok := v.incl[file]; ok {
		return c
	}
	c := map[string]bool{file: true}
	stack := []string{file}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, h := range v.includes[f] {
			if !c[h] {
				c[h] = true
				stack = append(stack, h)
			}
		}
	}
	v.incl[file] = c
	return c
}

// linked reports whether a and b are one file, or one includes the other:
// a header's inline overload and its source's out-of-line one.
func (v *cppVisibility) linked(a, b string) bool {
	return a == b || v.included(a)[b] || v.included(b)[a]
}

// seen is what code in file can name. A source file sees what it includes;
// a header is compiled as part of each source file that includes it, so it
// sees what those translation units see (a header relying on its includer's
// declarations is common). A header no source includes sees its own includes.
func (v *cppVisibility) seen(file string) map[string]bool {
	if c, ok := v.closure[file]; ok {
		return c
	}
	c := v.included(file)
	if !isCSource(file) {
		var tus []string
		for _, src := range v.includers(file) {
			if isCSource(src) {
				tus = append(tus, src)
			}
		}
		if len(tus) > 0 {
			u := map[string]bool{}
			for k := range c {
				u[k] = true
			}
			for _, tu := range tus {
				for k := range v.included(tu) {
					u[k] = true
				}
			}
			c = u
		}
	}
	v.closure[file] = c
	return c
}

// includers are the files that include file, directly or transitively.
func (v *cppVisibility) includers(file string) []string {
	if v.rev == nil {
		v.rev = map[string][]string{}
		for f, hs := range v.includes {
			for _, h := range hs {
				v.rev[h] = append(v.rev[h], f)
			}
		}
	}
	seen := map[string]bool{file: true}
	var out []string
	stack := []string{file}
	for len(stack) > 0 {
		f := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, g := range v.rev[f] {
			if !seen[g] {
				seen[g] = true
				out = append(out, g)
				stack = append(stack, g)
			}
		}
	}
	return out
}

// visible reports whether code in file caller can name the definition name
// (on class recv, "" for a free function) written in file def.
func (v *cppVisibility) visible(caller, def, recv, name string) bool {
	if v == nil || caller == def {
		return true
	}
	if v.facts[caller] == nil {
		return true // a file without C++ facts (C, a parse failure): no claim
	}
	cls, _, _ := strings.Cut(recv, "@")
	if v.internal(def, recv, name) {
		return false
	}
	seen := v.seen(caller)
	if !isCSource(def) {
		return seen[def]
	}
	if homes := v.homes[cls]; cls != "" && len(homes) > 0 {
		// two classes of one name in two packages: the one this source
		// file includes is the class it defines members of
		var own []string
		defIncl := v.included(def)
		for _, h := range homes {
			if defIncl[h] {
				own = append(own, h)
			}
		}
		if len(own) == 0 {
			// the source file includes no header defining the class: its
			// members are another class of that name (one whose header the
			// tree does not hold)
			return false
		}
		homes = own
		for _, h := range homes {
			if seen[h] {
				return true
			}
		}
		return false
	}
	// a free function (void ns::f() scopes it to a namespace, not a class):
	// visible through a file that declares it
	for _, d := range v.declares[name] {
		if d != def && seen[d] {
			return true
		}
	}
	return false
}

// internal: the definition belongs to its own source file (internal
// linkage, or a member of a class that source file defines).
func (v *cppVisibility) internal(def, recv, name string) bool {
	df := v.facts[def]
	if df == nil || !isCSource(def) {
		return false
	}
	cls, _, _ := strings.Cut(recv, "@")
	if cls == "" {
		return df.Internal[name]
	}
	return df.Internal[cls] || df.Classes[cls] != nil
}

// inNamespace: file def defines name in a namespace a call written
// root::…::scope::name can mean — the innermost written namespace is the
// definition's last one, and the outermost one appears in its chain (a call
// inside namespace a may write b::f for a::b::f). A file without namespace
// facts makes no claim.
func (v *cppVisibility) inNamespace(def, name, root, scope string) bool {
	df := v.facts[def]
	if df == nil || len(df.FuncNS[name]) == 0 {
		return true
	}
	for _, ns := range df.FuncNS[name] {
		segs := strings.Split(ns, "::")
		if segs[len(segs)-1] == scope && containsStr(segs, root) {
			return true
		}
	}
	return false
}

// ofNested keeps the definitions belonging to the nested class Outer::Inner:
// written in the file that defines it, or in a source file including that one.
func (v *cppVisibility) ofNested(defs []string, nested string, defPath map[string]string, alsoIn map[string][]string) []string {
	homes := v.homes[nested]
	if len(homes) == 0 {
		return defs
	}
	var out []string
	for _, q := range defs {
		match := false
		for _, p := range append([]string{defPath[q]}, alsoIn[q]...) {
			for _, h := range homes {
				if p == h || isCSource(p) && v.included(p)[h] {
					match = true
				}
			}
		}
		if match {
			out = append(out, q)
		}
	}
	return out
}
