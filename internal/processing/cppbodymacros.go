// cppbodymacros.go adds the calls a repo macro makes where a function body
// uses it: `TRACE_ERROR(msg)` defined as `Tracer::instance().logError(msg)`
// calls Tracer::instance and logError, and `__METHOD_NAME__` defined as
// `methodName(__PRETTY_FUNCTION__)` calls methodName — calls the source text
// of the body never shows. Only macros the repo defines, once (a name
// defined differently under #if is the build's to pick), in a file the
// caller includes, are expanded; the arguments' own calls are already the
// body's. Test code is left to the test helpers' own handling. Pure over
// the source text (ADR-018); the expansion's calls are read by the
// tree-sitter layer (cppMemberParser).
package processing

import (
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// macroArgPlaceholder stands for each argument of an expanded macro call:
// the arguments' calls are the body's own, read where they are written.
const macroArgPlaceholder = "reponite_macro_arg_"

var reMacroLike = regexp.MustCompile(`^(__[A-Za-z0-9_]+__|[A-Z][A-Z0-9_]*[A-Z0-9])$`)

// macroLikeIdents are the identifiers of a body written like an object-like
// macro (FOO_BAR, __FOO__), each once.
func macroLikeIdents(body content.AST) []string {
	if body == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, id := range descendantsAny(body, []string{"identifier"}) {
		if t := id.Text(); reMacroLike.MatchString(t) && !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

type bodyMacro struct {
	mac  cppMacro
	file string
}

// expandCppBodyMacros appends to each production C++ function the calls of
// the repo macros its body uses.
func expandCppBodyMacros(files []ParsedFile) {
	if cppMemberParser == nil {
		return
	}
	uses := false
	for _, f := range files {
		if f.Lang != "cpp" || f.IsTest {
			continue
		}
		for _, s := range f.Symbols {
			if len(s.MacroIdents) > 0 || len(s.CallSites) > 0 {
				uses = true
				break
			}
		}
	}
	if !uses {
		return
	}
	defs := map[string][]bodyMacro{} // name -> its distinct definitions
	all := map[string]string{}       // C/C++ file -> source holding a #define
	for _, f := range files {
		if langFamily(f.Lang) != "c" {
			continue
		}
		src := f.Content
		if src == "" && f.Load != nil {
			src, _ = f.Load()
		}
		if !strings.Contains(src, "define") {
			continue
		}
		all[f.Path] = src
		for _, nm := range everyDefine(src) {
			name, m := nm.name, nm.mac
			dup := false
			for _, d := range defs[name] {
				dup = dup || d.mac.body == m.body && len(d.mac.params) == len(m.params)
			}
			if !dup {
				defs[name] = append(defs[name], bodyMacro{mac: m, file: f.Path})
			}
		}
	}
	if len(defs) == 0 {
		return
	}
	macros := cppMacros(all) // for the macros a macro's body uses
	var vis *cppVisibility
	cache := map[string][]CallSite{}
	for i := range files {
		f := &files[i]
		if f.Lang != "cpp" || f.IsTest {
			continue
		}
		for j := range f.Symbols {
			s := &f.Symbols[j]
			var texts []string
			for _, cs := range s.CallSites {
				if cs.Kind != CallPlain || len(defs[cs.Name]) == 0 || cs.NArgs == 0 {
					continue
				}
				args := make([]string, cs.NArgs-1)
				for k := range args {
					args[k] = macroArgPlaceholder
				}
				texts = append(texts, cs.Name+"("+strings.Join(args, ", ")+")")
			}
			for _, id := range s.MacroIdents {
				if ds := defs[id]; len(ds) > 0 && ds[0].mac.params == nil {
					texts = append(texts, id)
				}
			}
			if len(texts) == 0 {
				continue
			}
			if vis == nil {
				vis = newCppVisibility(files)
			}
			have := map[CallSite]bool{}
			for _, cs := range s.CallSites {
				have[cs] = true
			}
			for _, text := range texts {
				name := text
				if k := strings.IndexByte(text, '('); k >= 0 {
					name = text[:k]
				}
				if !macroVisible(defs[name], f.Path, vis) {
					continue
				}
				sites, ok := cache[text]
				if !ok {
					sites = macroCallSites(text, macros)
					cache[text] = sites
				}
				for _, cs := range sites {
					if !have[cs] {
						have[cs] = true
						s.CallSites = append(s.CallSites, cs)
					}
				}
			}
		}
	}
}

// macroVisible: the macro has one definition the file can see — its own,
// or one in a header it includes.
func macroVisible(ds []bodyMacro, file string, vis *cppVisibility) bool {
	if len(ds) != 1 {
		return false
	}
	if ds[0].file == file || vis == nil {
		return true
	}
	return vis.seen(file)[ds[0].file]
}

type namedMacro struct {
	name string
	mac  cppMacro
}

// everyDefine is each #define of src, an #if/#else variant included (where
// cppMacros keeps the first of a name).
func everyDefine(src string) []namedMacro {
	var out []namedMacro
	lines := strings.Split(src, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		for strings.HasSuffix(strings.TrimRight(ln, " \t\r"), "\\") && i+1 < len(lines) {
			ln = strings.TrimSuffix(strings.TrimRight(ln, " \t\r"), "\\") + " " + lines[i+1]
			i++
		}
		if !reDefine.MatchString(ln) {
			continue
		}
		for name, m := range cppMacros(map[string]string{"": ln}) {
			out = append(out, namedMacro{name: name, mac: m})
		}
	}
	return out
}

// macroCallSites are the calls a macro use expands to, its arguments'
// own calls left out.
func macroCallSites(text string, macros map[string]cppMacro) []CallSite {
	expanded := cppExpand(text, macros, 0)
	if !strings.Contains(expanded, "(") {
		return nil
	}
	syms := cppMemberParser("class reponite_macro_expansion {\nvoid reponite_macro_use() {\n"+expanded+";\n}\n};", 1)
	var out []CallSite
	for _, s := range syms {
		if s.Name != "reponite_macro_use" {
			continue
		}
		for _, cs := range s.CallSites {
			if strings.Contains(cs.Recv, macroArgPlaceholder) || strings.Contains(cs.ArgTypes, macroArgPlaceholder) || cs.Name == macroArgPlaceholder {
				if strings.Contains(cs.Recv, macroArgPlaceholder) || cs.Name == macroArgPlaceholder {
					continue // a call on an argument: the body's own
				}
				cs.ArgTypes = "" // an argument stands for something unknown here
			}
			if cs.Kind == CallSelf {
				continue // this-> inside the expansion: the wrapper's, not the caller's
			}
			out = append(out, cs)
		}
	}
	return out
}
