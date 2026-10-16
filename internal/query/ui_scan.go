// ui_scan.go holds the lexical helpers behind `ui` (ui.go): comment masking,
// bracket matching, single-file-component splitting, import parsing and the
// JSX/template tag scanner. Everything works on byte offsets of the stored file
// content and never rewrites a byte to a different position, so every line
// number reported is the line in the file as committed. Pure (ADR-018).
package query

import (
	"path"
	"regexp"
	"strings"
	"unicode"
)

// maskComments blanks JS/TS line and block comments (newlines kept) so a
// commented-out `<OldPage/>` or `axios.get(…)` is never read as code. String
// literals are kept: route paths and URLs live in them. A quote that is not
// closed on its own line ends at the newline — JSX text such as `Don't` is not
// a string, and this bounds the damage of mistaking it for one to that line.
func maskComments(src string) string {
	b := []byte(src)
	n := len(b)
	for i := 0; i < n; i++ {
		c := b[i]
		switch {
		case c == '"' || c == '\'':
			for i++; i < n && b[i] != c && b[i] != '\n'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case c == '`':
			for i++; i < n && b[i] != '`'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case c == '/' && i+1 < n && b[i+1] == '/':
			if i > 0 && b[i-1] == ':' {
				continue // a URL scheme in unquoted text (JSX), not a comment
			}
			for ; i < n && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case c == '/' && i+1 < n && b[i+1] == '*':
			for ; i < n && !(b[i] == '*' && i+1 < n && b[i+1] == '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < n {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}

// matchClose returns the index of the bracket closing the one at open
// ('{', '(', '[' or '<' for a JSX tag is not handled here), or -1. Strings and
// template literals are skipped; a template literal's ${…} is a nested scope.
func matchClose(src string, open int) int {
	if open < 0 || open >= len(src) {
		return -1
	}
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; c {
		case '"', '\'':
			for i++; i < len(src) && src[i] != c && src[i] != '\n'; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case '`':
			i = skipTemplate(src, i)
		case '{', '(', '[':
			depth++
		case '}', ')', ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// skipTemplate returns the index of the backtick closing the template literal
// opened at i, stepping over ${…} expressions.
func skipTemplate(src string, i int) int {
	for i++; i < len(src); i++ {
		switch src[i] {
		case '\\':
			i++
		case '`':
			return i
		case '$':
			if i+1 < len(src) && src[i+1] == '{' {
				if j := matchClose(src, i+1); j > 0 {
					i = j
				}
			}
		}
	}
	return len(src) - 1
}

// lineIndex maps byte offsets to 1-based lines.
type lineIndex []int

func newLineIndex(s string) lineIndex {
	li := lineIndex{0}
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			li = append(li, i+1)
		}
	}
	return li
}

// line is the 1-based line holding offset off.
func (li lineIndex) line(off int) int {
	lo, hi := 0, len(li)-1
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if li[mid] <= off {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	return lo + 1
}

// offset is the byte offset where 1-based line ln starts (clamped).
func (li lineIndex) offset(ln int) int {
	if ln < 1 {
		return 0
	}
	if ln > len(li) {
		return li[len(li)-1]
	}
	return li[ln-1]
}

// --- single-file components (.vue / .svelte) --------------------------------

// SFC is a single-file component split into its parts. Script and Markup are
// the WHOLE file with every byte outside that part blanked to a space
// (newlines kept), so an offset or line found in either is the file's own.
type SFC struct {
	Script     string // <script> block bodies (all of them: Vue's <script> + <script setup>)
	ScriptLang string // "ts", "tsx" or "js"
	Markup     string // the template: Vue's <template> block; Svelte's markup outside script/style
	HasScript  bool
}

var sfcBlockOpen = regexp.MustCompile(`(?i)<(script|style|template)(\s[^>]*)?>`)
var sfcLangAttr = regexp.MustCompile(`\blang\s*=\s*["']?(\w+)`)

// IsSFCPath reports whether path is a Vue or Svelte single-file component.
func IsSFCPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".vue", ".svelte":
		return true
	}
	return false
}

// SplitSFC splits a .vue/.svelte file into script and markup views. A Vue
// file's markup is its top-level <template> block; a Svelte file's is
// everything outside <script> and <style>.
func SplitSFC(p, src string) SFC {
	vue := strings.EqualFold(path.Ext(p), ".vue")
	script := blankAll(src)
	markup := blankAll(src)
	out := SFC{ScriptLang: "js"}
	type span struct{ from, to int }
	var excluded []span
	lower := strings.ToLower(src)
	for pos := 0; pos < len(src); {
		m := sfcBlockOpen.FindStringSubmatchIndex(src[pos:])
		if m == nil {
			break
		}
		tag := strings.ToLower(src[pos+m[2] : pos+m[3]])
		attrs := ""
		if m[4] >= 0 {
			attrs = src[pos+m[4] : pos+m[5]]
		}
		bodyFrom := pos + m[1]
		if tag == "template" && !vue {
			pos = bodyFrom // Svelte markup may hold a <template> element: it is markup
			continue
		}
		closeTag := "</" + tag
		bodyTo := len(src)
		if tag == "template" {
			// Vue nests <template v-if> inside the top-level block: the block
			// ends at the LAST </template>.
			if j := strings.LastIndex(lower, closeTag); j >= bodyFrom {
				bodyTo = j
			}
		} else if j := strings.Index(lower[bodyFrom:], closeTag); j >= 0 {
			bodyTo = bodyFrom + j
		}
		switch tag {
		case "script":
			copyRange(&script, src, bodyFrom, bodyTo)
			out.HasScript = true
			if lm := sfcLangAttr.FindStringSubmatch(attrs); lm != nil {
				switch strings.ToLower(lm[1]) {
				case "ts", "typescript":
					out.ScriptLang = "ts"
				case "tsx":
					out.ScriptLang = "tsx"
				}
			}
			excluded = append(excluded, span{pos + m[0], bodyTo})
		case "style":
			excluded = append(excluded, span{pos + m[0], bodyTo})
		case "template":
			copyRange(&markup, src, bodyFrom, bodyTo)
		}
		pos = bodyTo + len(closeTag)
	}
	if !vue {
		b := []byte(src)
		for _, sp := range excluded {
			for i := sp.from; i < sp.to && i < len(b); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
		}
		markup = string(b)
	}
	out.Script = script
	out.Markup = markup
	return out
}

func blankAll(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] != '\n' {
			b[i] = ' '
		}
	}
	return string(b)
}

func copyRange(dst *string, src string, from, to int) {
	if from < 0 || to > len(src) || from >= to {
		return
	}
	b := []byte(*dst)
	copy(b[from:to], src[from:to])
	*dst = string(b)
}

// SFCComponentName is the name a single-file component is known by: its file
// stem, with a kebab/snake stem in PascalCase (Vue resolves `<cart-item>` and
// `<CartItem>` to the same component, and an import binds it under the
// PascalCase name by convention). A stem that is not an identifier
// (SvelteKit's `+page`) is kept as written — it is never imported by name.
func SFCComponentName(p string) string {
	stem := strings.TrimSuffix(path.Base(p), path.Ext(p))
	if strings.HasPrefix(stem, "+") {
		return stem
	}
	return pascal(stem)
}

// pascal turns cart-item / cart_item / cartItem into CartItem.
func pascal(s string) string {
	var b strings.Builder
	up := true
	for _, r := range s {
		if r == '-' || r == '_' || r == '.' || r == ' ' {
			up = true
			continue
		}
		if up {
			b.WriteRune(unicode.ToUpper(r))
			up = false
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// --- imports ----------------------------------------------------------------

// uiImport is one local binding introduced by an import.
type uiImport struct {
	Spec string // module specifier as written ('./CartItem', '@/components/Button', 'react')
	Name string // the exported name bound: "default", "*" (namespace) or the export's name
	Line int
}

var (
	esImport  = regexp.MustCompile(`(?s)\bimport\s+(?:type\s+)?([^;'"]*?)\s*\bfrom\s*['"]([^'"\n]+)['"]`)
	lazyBind  = regexp.MustCompile(`(?:const|let|var)\s+([A-Za-z_$][\w$]*)\s*(?::[^=]+)?=\s*(?:React\.)?(?:lazy|loadable|defineAsyncComponent|dynamic|lazyWithRetry|asyncComponent)\s*\(\s*(?:async\s*)?\(\s*\)\s*=>\s*(?:\{\s*return\s+)?import\(\s*(?:/\*[^*]*\*/\s*)?['"]([^'"]+)['"]`)
	reExport  = regexp.MustCompile(`(?s)\bexport\s+(?:type\s+)?(\*(?:\s+as\s+[\w$]+)?|\{[^}]*\})\s*from\s*['"]([^'"\n]+)['"]`)
	dynImport = regexp.MustCompile(`import\(\s*(?:/\*[^*]*\*/\s*)?['"]([^'"]+)['"]\s*\)`)
)

// parseImports returns the file's import bindings by local name.
func parseImports(code string) map[string]uiImport {
	out := map[string]uiImport{}
	li := newLineIndex(code)
	for _, m := range esImport.FindAllStringSubmatchIndex(code, -1) {
		clause := strings.TrimSpace(code[m[2]:m[3]])
		spec := code[m[4]:m[5]]
		ln := li.line(m[0])
		if strings.HasPrefix(clause, "type ") {
			clause = strings.TrimPrefix(clause, "type ")
		}
		// default, { a, b as c }, * as ns — in any combination.
		if i := strings.Index(clause, "{"); i >= 0 {
			j := strings.LastIndex(clause, "}")
			if j > i {
				for _, part := range strings.Split(clause[i+1:j], ",") {
					part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
					if part == "" {
						continue
					}
					name, local := part, part
					if a, b, ok := strings.Cut(part, " as "); ok {
						name, local = strings.TrimSpace(a), strings.TrimSpace(b)
					}
					out[local] = uiImport{Spec: spec, Name: name, Line: ln}
				}
				clause = clause[:i] + clause[j+1:]
			}
		}
		for _, part := range strings.Split(clause, ",") {
			part = strings.TrimSpace(part)
			switch {
			case part == "":
			case strings.HasPrefix(part, "*"):
				if _, ns, ok := strings.Cut(part, " as "); ok {
					out[strings.TrimSpace(ns)] = uiImport{Spec: spec, Name: "*", Line: ln}
				}
			case isIdent(part):
				out[part] = uiImport{Spec: spec, Name: "default", Line: ln}
			}
		}
	}
	for _, m := range lazyBind.FindAllStringSubmatchIndex(code, -1) {
		out[code[m[2]:m[3]]] = uiImport{Spec: code[m[4]:m[5]], Name: "default", Line: li.line(m[0])}
	}
	return out
}

// reExports returns `export { a as b } from './x'` bindings (exported name ->
// import) and the specifiers of `export * from './x'`.
func reExports(code string) (named map[string]uiImport, star []string) {
	named = map[string]uiImport{}
	for _, m := range reExport.FindAllStringSubmatch(code, -1) {
		spec := m[2]
		if strings.HasPrefix(m[1], "*") {
			if _, ns, ok := strings.Cut(m[1], " as "); ok {
				named[strings.TrimSpace(ns)] = uiImport{Spec: spec, Name: "*"}
			} else {
				star = append(star, spec)
			}
			continue
		}
		for _, part := range strings.Split(strings.Trim(m[1], "{}"), ",") {
			part = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(part), "type "))
			if part == "" {
				continue
			}
			name, exported := part, part
			if a, b, ok := strings.Cut(part, " as "); ok {
				name, exported = strings.TrimSpace(a), strings.TrimSpace(b)
			}
			named[exported] = uiImport{Spec: spec, Name: name}
		}
	}
	return named, star
}

func isIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || r == '$' || unicode.IsLetter(r) || i > 0 && unicode.IsDigit(r) {
			continue
		}
		return false
	}
	return true
}

func isUpperIdent(s string) bool { return s != "" && s[0] >= 'A' && s[0] <= 'Z' && isIdent(s) }

// --- tags (JSX and templates) ------------------------------------------------

// uiTag is one element opening tag: `<button type="submit" onClick={save}>Save`.
type uiTag struct {
	Name        string // as written: CartItem, Form.Field, button, cart-item
	Off         int    // offset of '<'
	Attrs       string // the attribute text between the name and '>'
	SelfClosing bool
	End         int    // offset just past '>'
	Label       string // the element's leading text, when it is plain ("Save")
}

var tagStart = regexp.MustCompile(`<([A-Za-z][\w$]*(?:[.:-][\w$]+)*)`)

// scanTags returns the element opening tags in s. jsx selects JSX rules: a '<'
// directly after an identifier, ')' or ']' is a comparison or a type argument
// (`useState<Order>(`, `a < b`), not an element.
func scanTags(s string, jsx bool) []uiTag {
	var out []uiTag
	for _, m := range tagStart.FindAllStringSubmatchIndex(s, -1) {
		start := m[0]
		if jsx && !jsxPosition(s, start) {
			continue
		}
		name := s[m[2]:m[3]]
		end, self := tagEnd(s, m[1])
		if end < 0 {
			continue
		}
		t := uiTag{Name: name, Off: start, Attrs: s[m[1] : end-1], End: end, SelfClosing: self}
		if self {
			t.Attrs = strings.TrimSuffix(t.Attrs, "/")
		} else {
			t.Label = leadingText(s[end:])
		}
		out = append(out, t)
	}
	return out
}

// jsxKeywords may precede an element: `return <div/>`, `yield <X/>`.
var jsxKeywords = map[string]bool{"return": true, "yield": true, "case": true, "default": true, "else": true, "await": true, "in": true, "of": true, "do": true}

// jsxPosition reports whether a '<' at i can open a JSX element: not after
// an identifier, ')' or ']' (a comparison or type argument), unless that
// identifier is a keyword such as return.
func jsxPosition(s string, i int) bool {
	p := i - 1
	for p >= 0 && (s[p] == ' ' || s[p] == '\t') {
		p--
	}
	if p < 0 {
		return true
	}
	c := s[p]
	if c == ')' || c == ']' || c == '.' {
		return false
	}
	if c == '_' || c == '$' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' {
		q := p
		for q >= 0 && (s[q] == '_' || s[q] == '$' || s[q] >= '0' && s[q] <= '9' || s[q] >= 'a' && s[q] <= 'z' || s[q] >= 'A' && s[q] <= 'Z') {
			q--
		}
		return jsxKeywords[s[q+1:p+1]] && (q < 0 || s[q] != '.')
	}
	return true
}

// tagEnd finds the '>' closing an opening tag whose attributes start at i,
// stepping over {…} expressions and quoted values. self is true for '/>'.
func tagEnd(s string, i int) (int, bool) {
	for ; i < len(s); i++ {
		switch c := s[i]; c {
		case '{':
			j := matchClose(s, i)
			if j < 0 {
				return -1, false
			}
			i = j
		case '"', '\'':
			k := strings.IndexByte(s[i+1:], c)
			if k < 0 {
				return -1, false
			}
			i += k + 1
		case '>':
			return i + 1, i > 0 && s[i-1] == '/'
		case '<':
			return -1, false // not a tag after all
		}
	}
	return -1, false
}

var labelExpr = regexp.MustCompile(`^\{\s*(?:\w+\.)?t\(\s*['"]([^'"]+)['"]`)

// leadingText is an element's text content when it starts with plain text or
// a translation call ({t('orders.save')}).
func leadingText(s string) string {
	s = strings.TrimLeft(s, " \t\r\n")
	if m := labelExpr.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	if i := strings.IndexAny(s, "<{"); i >= 0 {
		s = s[:i]
	}
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 40 {
		s = s[:40] + "…"
	}
	return s
}

// tagAttr is one attribute of a tag.
type tagAttr struct {
	Name, Value string // Value without its quotes/braces
	Expr        bool   // {…} (JSX/Svelte) — Vue directive values are expressions too
	Off         int    // offset within the tag's Attrs
}

var attrName = regexp.MustCompile(`([@:]?[A-Za-z_][\w.:|-]*)\s*(=)?`)

// attrs parses a tag's attribute text.
func attrs(a string) []tagAttr {
	var out []tagAttr
	for i := 0; i < len(a); {
		c := a[i]
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '/' {
			i++
			continue
		}
		if c == '{' { // {...spread}
			j := matchClose(a, i)
			if j < 0 {
				return out
			}
			i = j + 1
			continue
		}
		m := attrName.FindStringSubmatchIndex(a[i:])
		if m == nil || m[0] != 0 {
			i++
			continue
		}
		at := tagAttr{Name: a[i+m[2] : i+m[3]], Off: i}
		i += m[1]
		if m[4] >= 0 { // has '='
			for i < len(a) && (a[i] == ' ' || a[i] == '\t' || a[i] == '\n') {
				i++
			}
			if i < len(a) {
				switch a[i] {
				case '{':
					j := matchClose(a, i)
					if j < 0 {
						return out
					}
					at.Value, at.Expr = strings.TrimSpace(a[i+1:j]), true
					i = j + 1
				case '"', '\'':
					k := strings.IndexByte(a[i+1:], a[i])
					if k < 0 {
						return out
					}
					at.Value = a[i+1 : i+1+k]
					i += k + 2
				default:
					j := i
					for j < len(a) && a[j] != ' ' && a[j] != '\n' {
						j++
					}
					at.Value = a[i:j]
					i = j
				}
			}
		}
		out = append(out, at)
	}
	return out
}

// vueBuiltins are template tags Vue/Svelte/router provide, never repo components.
var vueBuiltins = map[string]bool{"Template": true, "Slot": true, "Component": true, "Transition": true,
	"TransitionGroup": true, "KeepAlive": true, "Teleport": true, "Suspense": true, "RouterView": true, "RouterLink": true,
	"NuxtPage": true, "NuxtLink": true, "NuxtLayout": true, "ClientOnly": true}

// componentTagName is the component a tag renders, or "" for an intrinsic
// element. Vue kebab-case tags (<cart-item>) resolve to their PascalCase name.
func componentTagName(t string, sfc bool) string {
	switch {
	case t == "":
		return ""
	case strings.HasPrefix(t, "svelte:"):
		return ""
	case t[0] >= 'A' && t[0] <= 'Z':
		if sfc && vueBuiltins[t] {
			return ""
		}
		return t
	case sfc && strings.Contains(t, "-"):
		p := pascal(t)
		if vueBuiltins[p] {
			return ""
		}
		return p
	}
	return ""
}
