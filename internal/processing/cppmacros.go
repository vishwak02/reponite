// cppmacros.go expands the repo's own C/C++ macros where a class body uses
// one to define members: `DECLARE_RECOVERY_LIST(_a, _b);` inside a class is
// `void save(SaveStream& s) const { s.write(_a, _b); } …` to the compiler, and
// those generated methods (and the calls they make) exist nowhere in the
// source text. The expansion is the preprocessor's for the common cases —
// parameters, __VA_ARGS__, # and ##, nested macros — over the macros the
// repo itself defines. Pure and stdlib-only (ADR-018); the parse of the
// expanded members is supplied by the tree-sitter layer (cppMemberParser).
package processing

import (
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// cppMemberParser parses C++ class member text (wrapped in `class Cls {…};`)
// into symbols; nil in builds without tree-sitter.
var cppMemberParser func(src string, normVer int) []Symbol

type cppMacro struct {
	params   []string // nil: object-like
	variadic bool
	body     string
}

var reDefine = regexp.MustCompile(`^\s*#\s*define\s+([A-Za-z_]\w*)(\(([^)]*)\))?(.*)$`)

// cppMacros reads every #define of the C/C++ sources given.
func cppMacros(srcs map[string]string) map[string]cppMacro {
	out := map[string]cppMacro{}
	for _, src := range srcs {
		if !strings.Contains(src, "define") {
			continue
		}
		lines := strings.Split(src, "\n")
		for i := 0; i < len(lines); i++ {
			ln := lines[i]
			for strings.HasSuffix(strings.TrimRight(ln, " \t\r"), "\\") && i+1 < len(lines) {
				ln = strings.TrimSuffix(strings.TrimRight(ln, " \t\r"), "\\") + " " + lines[i+1]
				i++
			}
			m := reDefine.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			mac := cppMacro{body: strings.TrimSpace(stripLineComment(m[4]))}
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
						mac.params = append(mac.params, strings.TrimSuffix(p, "..."))
					default:
						mac.params = append(mac.params, p)
					}
				}
			}
			if _, dup := out[m[1]]; !dup { // #ifdef variants: the first stands
				out[m[1]] = mac
			}
		}
	}
	return out
}

func stripLineComment(s string) string {
	if i := strings.Index(s, "//"); i >= 0 {
		return s[:i]
	}
	return s
}

// cppExpand expands the macros in text, to a bounded depth.
func cppExpand(text string, macros map[string]cppMacro, depth int) string {
	if depth > 8 {
		return text
	}
	var b strings.Builder
	changed := false
	for i := 0; i < len(text); {
		c := text[i]
		if !(c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z') {
			if c == '"' || c == '\'' { // a literal: copied as is
				j := i + 1
				for j < len(text) && text[j] != c {
					if text[j] == '\\' {
						j++
					}
					j++
				}
				if j >= len(text) {
					j = len(text) - 1
				}
				b.WriteString(text[i : j+1])
				i = j + 1
				continue
			}
			b.WriteByte(c)
			i++
			continue
		}
		j := i
		for j < len(text) && (text[j] == '_' || text[j] >= 'a' && text[j] <= 'z' || text[j] >= 'A' && text[j] <= 'Z' || text[j] >= '0' && text[j] <= '9') {
			j++
		}
		name := text[i:j]
		mac, ok := macros[name]
		if !ok {
			b.WriteString(name)
			i = j
			continue
		}
		if mac.params == nil {
			b.WriteString(" " + mac.body + " ")
			i, changed = j, true
			continue
		}
		k := j
		for k < len(text) && (text[k] == ' ' || text[k] == '\t' || text[k] == '\n') {
			k++
		}
		if k >= len(text) || text[k] != '(' {
			b.WriteString(name) // a function-like macro's name without a call
			i = j
			continue
		}
		args, end := macroArgs(text, k)
		if end < 0 {
			b.WriteString(text[i:])
			break
		}
		b.WriteString(" " + substituteMacro(mac, args) + " ")
		i, changed = end, true
	}
	out := b.String()
	if changed {
		return cppExpand(out, macros, depth+1)
	}
	return out
}

// macroArgs splits the parenthesized argument list starting at text[open]
// on its top-level commas; end is the index after ')', -1 when unclosed.
func macroArgs(text string, open int) ([]string, int) {
	var args []string
	depth, start := 0, open+1
	for i := open; i < len(text); i++ {
		switch text[i] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
			if depth == 0 {
				if a := strings.TrimSpace(text[start:i]); a != "" || len(args) > 0 {
					args = append(args, a)
				}
				return args, i + 1
			}
		case ',':
			if depth == 1 {
				args = append(args, strings.TrimSpace(text[start:i]))
				start = i + 1
			}
		}
	}
	return nil, -1
}

var reIdent = regexp.MustCompile(`#?\s*[A-Za-z_]\w*`)

// substituteMacro is mac's body with its parameters replaced by args.
func substituteMacro(mac cppMacro, args []string) string {
	val := map[string]string{}
	for i, p := range mac.params {
		if i < len(args) {
			val[p] = args[i]
		} else {
			val[p] = ""
		}
	}
	if mac.variadic {
		var rest []string
		if len(args) > len(mac.params) {
			rest = args[len(mac.params):]
		}
		val["__VA_ARGS__"] = strings.Join(rest, ", ")
	}
	body := reIdent.ReplaceAllStringFunc(mac.body, func(tok string) string {
		if strings.HasPrefix(tok, "#") {
			name := strings.TrimSpace(tok[1:])
			if v, ok := val[name]; ok {
				return `"` + v + `"` // #param: the argument as a string
			}
			return tok
		}
		if v, ok := val[tok]; ok {
			return v
		}
		return tok
	})
	return regexp.MustCompile(`\s*##\s*`).ReplaceAllString(body, "")
}

// expandCppMacros adds the members repo macros define inside class bodies to
// each C++ file's symbols and spans (located at the macro's use).
func expandCppMacros(repo string, normVer int, files []ParsedFile) {
	if cppMemberParser == nil {
		return
	}
	srcs := map[string]string{}
	for _, f := range files {
		if f.Lang != "cpp" {
			continue
		}
		src := f.Content
		if src == "" && f.Load != nil {
			src, _ = f.Load()
		}
		srcs[f.Path] = src
	}
	macros := cppMacros(srcs)
	// only macros whose expansion defines a function body generate members
	defining := map[string]bool{}
	for name, m := range macros {
		if m.params != nil && strings.Contains(cppExpand(m.body, macros, 0), "{") {
			defining[name] = true
		}
	}
	if len(defining) == 0 {
		return
	}
	for i := range files {
		f := &files[i]
		src := srcs[f.Path]
		if src == "" {
			continue
		}
		classes := map[string]bool{}
		for _, s := range f.Symbols {
			if s.Kind == "type" {
				classes[s.Name] = true
			}
		}
		lines := strings.Split(src, "\n")
		for ln, text := range lines {
			t := strings.TrimSpace(text)
			name := t
			if k := strings.IndexAny(t, "( \t"); k > 0 {
				name = t[:k]
			}
			if !defining[name] || strings.HasPrefix(t, "#") {
				continue
			}
			cls := enclosingClassSpan(f.Spans, classes, ln+1)
			if cls == "" {
				continue
			}
			pos := 0 // the invocation, from its line on
			for k := 0; k < ln; k++ {
				pos += len(lines[k]) + 1
			}
			open := strings.IndexByte(src[pos:], '(')
			if open < 0 {
				continue
			}
			_, end := macroArgs(src, pos+open)
			if end < 0 {
				continue
			}
			expanded := cppExpand(src[pos:end], macros, 0)
			for _, s := range cppMemberParser("class "+cls+" {\n"+expanded+"\n};", normVer) {
				if s.Recv != cls || s.Kind == "type" {
					continue
				}
				one := []Symbol{s}
				sealSymbols(repo, f.Lang, normVer, one)
				f.Symbols = append(f.Symbols, one[0])
				f.Spans = append(f.Spans, query.SymbolSpan{Name: s.Name, StartLine: ln + 1, EndLine: ln + 1})
			}
		}
	}
}

// enclosingClassSpan is the innermost class span of the file holding line.
func enclosingClassSpan(spans []query.SymbolSpan, classes map[string]bool, line int) string {
	best, size := "", 1<<30
	for _, sp := range spans {
		if classes[sp.Name] && sp.StartLine <= line && line <= sp.EndLine && sp.EndLine-sp.StartLine < size {
			best, size = sp.Name, sp.EndLine-sp.StartLine
		}
	}
	return best
}
