// cppbodymacros.go expands the repo's own macros where a function body uses
// them, so the calls they make are the function's: `STEP_UNTIL(done())` in a
// test is `stepUntil([&] { return (done()); })` to the compiler, and the
// stepUntil call exists nowhere in the source text. Each use expands the
// definition its translation unit sees — the file's own above it, or a header
// it includes; two files may define one name differently — and one of
// several conflicting definitions in sight (an #ifdef's variants) is not
// guessed. The file is re-read with the uses replaced in place (its lines
// kept), and each function takes the call sites of its expanded body. Pure
// and stdlib-only (ADR-018); the parse is the tree-sitter layer's
// (cppFunctionBodies, cppReparse).
package processing

import (
	"regexp"
	"strings"
)

// cppFunctionBodies is a C/C++ file's function bodies (the outermost: a
// local class's methods are inside its function's); nil in builds without
// tree-sitter.
var cppFunctionBodies func(path, src string) []cppBody

// cppBody is a function body's bytes [start, end) and the scopes an
// unqualified name in it is looked up in: its namespaces ("a::b", "" the
// global one) and the class an out-of-line member is defined for (a::C).
type cppBody struct {
	start, end int
	scopes     []string
	usings     []string // the namespaces using directives open there
}

// cppReparse parses a C/C++ file's text into its symbols; nil in builds
// without tree-sitter.
var cppReparse func(path, src string, normVer int) []Symbol

// cppMacroDef is one #define of the repo.
type cppMacroDef struct {
	file string
	line int // the #define's line, 1-based
	mac  cppMacro
	key  string // what tells two definitions apart
}

// cppMacroDefs reads every #define of the C/C++ sources, all of them by name.
func cppMacroDefs(srcs map[string]string) map[string][]cppMacroDef {
	out := map[string][]cppMacroDef{}
	for file, src := range srcs {
		if !strings.Contains(src, "define") {
			continue
		}
		lines := strings.Split(src, "\n")
		for i := 0; i < len(lines); i++ {
			at := i + 1
			ln := lines[i]
			for strings.HasSuffix(strings.TrimRight(ln, " \t\r"), "\\") && i+1 < len(lines) {
				ln = strings.TrimSuffix(strings.TrimRight(ln, " \t\r"), "\\") + " " + lines[i+1]
				i++
			}
			m := reDefine.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			mac := cppMacro{body: strings.TrimSpace(stripBlockComments(stripLineComment(m[4])))}
			if m[2] != "" {
				mac.params = []string{}
				for _, p := range strings.Split(m[3], ",") {
					p = strings.TrimSpace(p)
					switch {
					case p == "":
					case p == "...":
						mac.variadic = true
					case strings.HasSuffix(p, "..."):
						mac.variadic = true
						mac.params = append(mac.params, strings.TrimSpace(strings.TrimSuffix(p, "...")))
					default:
						mac.params = append(mac.params, p)
					}
				}
			}
			key := strings.Join(strings.Fields(mac.body), " ")
			if mac.params != nil {
				key = "(" + strings.Join(mac.params, ",") + map[bool]string{true: ",...", false: ""}[mac.variadic] + ")" + key
			}
			out[m[1]] = append(out[m[1]], cppMacroDef{file: file, line: at, mac: mac, key: key})
		}
	}
	return out
}

// stripBlockComments drops /* … */ comments from one logical line.
func stripBlockComments(s string) string {
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + " " + s[i+2+j+2:]
	}
}

// reUsingNS is a using directive.
var reUsingNS = regexp.MustCompile(`\busing\s+namespace\s+([A-Za-z_][\w:\s]*?)\s*;`)

// reCallLike finds name( in an expansion: something it may call.
var reCallLike = regexp.MustCompile(`([A-Za-z_]\w*)\s*\(`)

// notCallees are the keywords and operators written like a call.
var notCallees = map[string]bool{
	"if": true, "while": true, "for": true, "switch": true, "return": true, "sizeof": true, "alignof": true,
	"decltype": true, "typeid": true, "noexcept": true, "static_assert": true, "catch": true, "defined": true,
	"alignas": true, "throw": true, "__attribute__": true, "__declspec": true, "_Pragma": true, "__typeof__": true,
	"typeof": true, "static_cast": true, "dynamic_cast": true, "const_cast": true, "reinterpret_cast": true,
	"do": true, "else": true, "case": true, "new": true, "delete": true, "__has_include": true,
}

// callingMacro reports a macro whose expansion may call something.
func callingMacro(m cppMacro, first map[string]cppMacro) bool {
	for _, c := range reCallLike.FindAllStringSubmatch(cppExpand(m.body, first, 0), -1) {
		if !notCallees[c[1]] {
			return true
		}
	}
	return false
}

// expandBodyMacros gives each C/C++ function the call sites its body has
// once the repo's macros it uses are expanded.
func expandBodyMacros(files []ParsedFile, srcs map[string]string, normVer int) {
	if cppFunctionBodies == nil || cppReparse == nil {
		return
	}
	defs := cppMacroDefs(srcs)
	first := map[string]cppMacro{}
	for name, ds := range defs {
		first[name] = ds[0].mac
	}
	calling := map[string]bool{}
	for name, ds := range defs {
		for _, d := range ds {
			if callingMacro(d.mac, first) {
				calling[name] = true
				break
			}
		}
	}
	if len(calling) == 0 {
		return
	}
	// unqualified lookup of what an expansion calls: the repo's free
	// functions by namespace, and the names some class has a method of
	freeNS, methods := map[string][]string{}, map[string]bool{}
	for _, f := range files {
		if f.Types != nil {
			for n, nss := range f.Types.FuncNS {
				freeNS[n] = append(freeNS[n], nss...)
			}
		}
		for _, s := range f.Symbols {
			if s.Recv != "" && s.Kind != "type" {
				methods[s.Name] = true
			}
		}
	}
	usingsOf := map[string][]string{}
	var vis *cppVisibility
	visReady := false
	for i := range files {
		f := &files[i]
		src := srcs[f.Path]
		if src == "" || len(f.Symbols) == 0 || !usesAny(src, calling) {
			continue
		}
		if !visReady {
			vis, visReady = newCppVisibility(files), true
		}
		var seen map[string]bool
		if vis != nil {
			seen = vis.seen(f.Path)
		}
		// the definition of name a use at line sees; ok false when none or
		// several that disagree
		visible := func(name string, line int) (cppMacro, bool) {
			var in []cppMacroDef
			for _, d := range defs[name] {
				if d.file == f.Path && d.line < line || d.file != f.Path && (seen == nil && vis == nil || seen[d.file]) {
					in = append(in, d)
				}
			}
			if len(in) == 0 {
				return cppMacro{}, false
			}
			for _, d := range in[1:] {
				if d.key != in[0].key {
					return cppMacro{}, false
				}
			}
			return in[0].mac, true
		}
		bodies := cppFunctionBodies(f.Path, src)
		if len(bodies) == 0 {
			continue
		}
		// what a use's expansion expands in turn: the file's view at its end
		nested := map[string]cppMacro{}
		endLine := 2 + strings.Count(src, "\n")
		for n := range defs {
			if m, ok := visible(n, endLine); ok {
				nested[n] = m
			}
		}
		var usings []string // using namespace X; of the headers the file includes
		inFile := map[string]bool{}
		if vis != nil {
			inFile = vis.included(f.Path)
		}
		for p := range inFile {
			if p == f.Path {
				continue // the file's own are scoped (cppBody.usings)
			}
			u, ok := usingsOf[p]
			if !ok {
				for _, m := range reUsingNS.FindAllStringSubmatch(srcs[p], -1) {
					u = append(u, strings.Join(strings.Fields(m[1]), ""))
				}
				usingsOf[p] = u
			}
			usings = append(usings, u...)
		}
		// a free function an expansion names unqualified is found from the
		// use's scopes: one of its namespaces encloses them, or a using
		// directive opens it (a member of some class may be this's)
		reachable := func(callee string, body cppBody) bool {
			nss := freeNS[callee]
			if methods[callee] || len(nss) == 0 {
				return true
			}
			for _, t := range nss {
				if t == "" {
					return true
				}
				for _, c := range body.scopes {
					if c == t || strings.HasPrefix(c, t+"::") {
						return true
					}
				}
				for _, u := range append(body.usings, usings...) {
					if u == t || strings.HasSuffix(t, "::"+u) {
						return true
					}
				}
			}
			return false
		}
		var b strings.Builder
		last, changed := 0, false
		scanCode(src, func(start, end int) int {
			name := src[start:end]
			if !calling[name] {
				return end
			}
			body := bodyAt(bodies, start)
			if body < 0 {
				return end
			}
			line := 1 + strings.Count(src[:start], "\n")
			mac, ok := visible(name, line)
			if !ok {
				return end
			}
			stop := end
			if mac.params != nil {
				k := end
				for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
					k++
				}
				if k >= len(src) || src[k] != '(' {
					return end // the name of a function-like macro, not a use of it
				}
				if _, e := macroArgs(src, k); e < 0 {
					return end
				} else {
					stop = e
				}
			}
			table := nested
			if nested[name].body != mac.body || len(nested[name].params) != len(mac.params) {
				table = map[string]cppMacro{name: mac}
				for n, m := range nested {
					if n != name {
						table[n] = m
					}
				}
			}
			own := map[string]bool{} // what the definition calls, not its arguments
			for _, p := range mac.params {
				own[p] = true
			}
			for _, c := range reCallLike.FindAllStringSubmatch(cppExpand(mac.body, table, 0), -1) {
				if !notCallees[c[1]] && !own[c[1]] && !reachable(c[1], bodies[body]) {
					return stop // a name the use's scope cannot see: not this repo's function
				}
			}
			exp := cppExpand(src[start:stop], table, 0)
			b.WriteString(src[last:start])
			b.WriteString(strings.Join(strings.Fields(exp), " "))
			b.WriteString(strings.Repeat("\n", strings.Count(src[start:stop], "\n")))
			last, changed = stop, true
			return stop
		})
		if !changed {
			continue
		}
		b.WriteString(src[last:])
		syms := cppReparse(f.Path, b.String(), normVer)
		if len(syms) != len(f.Symbols) {
			continue // the expansion changed what the file defines: not read
		}
		same := true
		for k := range syms {
			a, o := syms[k], f.Symbols[k]
			if a.Name != o.Name || a.Recv != o.Recv || a.Kind != o.Kind {
				same = false
				break
			}
		}
		if !same {
			continue
		}
		for k := range syms {
			if syms[k].Kind != "type" {
				f.Symbols[k].CallSites = syms[k].CallSites
			}
		}
	}
}

// usesAny reports whether src names any of names (a quick filter).
func usesAny(src string, names map[string]bool) bool {
	found := false
	scanCode(src, func(start, end int) int {
		if names[src[start:end]] {
			found = true
			return len(src)
		}
		return end
	})
	return found
}

// bodyAt is the body holding offset, -1 none.
func bodyAt(bodies []cppBody, off int) int {
	for i, b := range bodies {
		if b.start <= off && off < b.end {
			return i
		}
	}
	return -1
}

// scanCode calls fn with each identifier of C/C++ source outside comments,
// literals and preprocessor lines; fn returns where to go on from.
func scanCode(src string, fn func(start, end int) int) {
	lineStart := true
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == '\n':
			lineStart = true
			i++
			continue
		case c == ' ' || c == '\t' || c == '\r':
			i++
			continue
		case c == '#' && lineStart:
			// a directive, with its continuation lines
			for i < len(src) && src[i] != '\n' {
				if src[i] == '\\' && i+1 < len(src) && src[i+1] == '\n' {
					i++
				}
				i++
			}
			continue
		}
		lineStart = false
		switch {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				i += j + 4
			} else {
				i = len(src)
			}
		case c == '"' || c == '\'':
			i++
			for i < len(src) && src[i] != c && src[i] != '\n' {
				if src[i] == '\\' {
					i++
				}
				i++
			}
			i++
		case c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z':
			j := i
			for j < len(src) && (src[j] == '_' || src[j] >= 'a' && src[j] <= 'z' || src[j] >= 'A' && src[j] <= 'Z' || src[j] >= '0' && src[j] <= '9') {
				j++
			}
			if j < len(src) && (src[j] == '"' || src[j] == '\'') && isLiteralPrefix(src[i:j]) {
				if strings.HasSuffix(src[i:j], "R") && src[j] == '"' {
					// R"d( … )d": nothing inside ends it but )d"
					if open := strings.IndexByte(src[j:], '('); open > 0 {
						if k := strings.Index(src[j+open:], ")"+src[j+1:j+open]+"\""); k >= 0 {
							i = j + open + k + open + 1
							continue
						}
					}
					i = len(src)
					continue
				}
				i = j // u8"…", L'x': the literal is read next
				continue
			}
			i = max(fn(i, j), j)
		case c >= '0' && c <= '9':
			for i < len(src) && (src[i] == '.' || src[i] == '\'' || src[i] == '_' || src[i] >= '0' && src[i] <= '9' || src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z') {
				i++
			}
		default:
			i++
		}
	}
}

func isLiteralPrefix(s string) bool {
	switch s {
	case "u8", "u", "U", "L", "R", "u8R", "uR", "UR", "LR":
		return true
	}
	return false
}
