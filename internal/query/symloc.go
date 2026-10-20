// symloc.go locates a symbol's source and reads what the source itself says
// about it: its kind (function, method, class, ...), its signature, its
// decorators and its doc comment. Shared by `map` (hub locations) and
// `explain` (the "what is it" section). Lexical and language-aware by
// keyword, never by guess: a line no rule recognizes yields kind "symbol"
// and no doc, rather than an invented description. Pure (ADR-018).
package query

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// symLoc is one symbol span in one file.
type symLoc struct {
	Path string
	Span SymbolSpan
}

// spanIndex maps reponite's qualified ids back to file spans. A qid is the
// file's directory, ".", then the local name (Class.method for a method); the
// span table stores bare names, so a method is found as a span of its name
// nested inside a span of its class.
type spanIndex struct {
	files  []File
	byPath map[string]*File
	dirs   map[string]bool
	byName map[string][]symLoc // dir + "\x00" + bare name
}

func newSpanIndex(files []File) *spanIndex {
	x := &spanIndex{files: files, byPath: map[string]*File{}, dirs: map[string]bool{}, byName: map[string][]symLoc{}}
	for i := range files {
		f := &files[i]
		x.byPath[f.Path] = f
		d := pkgFromPath(f.Path)
		x.dirs[d] = true
		for _, sp := range f.Symbols {
			k := d + "\x00" + sp.Name
			x.byName[k] = append(x.byName[k], symLoc{Path: f.Path, Span: sp})
		}
	}
	for k := range x.byName {
		locs := x.byName[k]
		sort.Slice(locs, func(i, j int) bool {
			if locs[i].Path != locs[j].Path {
				return locs[i].Path < locs[j].Path
			}
			return locs[i].Span.StartLine < locs[j].Span.StartLine
		})
	}
	return x
}

// splitQID splits a qid into its directory and local name, choosing the
// longest directory prefix that is a real directory at this ref (a directory
// name may itself contain a ".").
func (x *spanIndex) splitQID(qid string) (dir, local string) {
	for i := len(qid) - 1; i > 0; i-- {
		if qid[i] == '.' && x.dirs[qid[:i]] {
			dir, local = qid[:i], qid[i+1:]
			break // scanning right to left: the first hit is the longest prefix
		}
	}
	if dir == "" {
		return "", qid
	}
	return dir, local
}

// locate finds the span defining qid. ok is false when the source of qid is
// not indexed (an external callee, or a symbol from a stored-only file).
func (x *spanIndex) locate(qid string) (symLoc, bool) {
	c, ok := x.locate0(qid)
	if !ok || !strings.HasSuffix(c.Path, ".py") {
		return c, ok
	}
	// Python: a later definition of the name in the same scope replaces the
	// earlier one (typing.overload stubs, then the implementation)
	_, local := x.splitQID(qid)
	parts := strings.Split(local, ".")
	f := x.byPath[c.Path]
	owner := enclosingSpanName(*f, c.Span)
	for _, o := range x.byName[pkgFromPath(c.Path)+"\x00"+parts[len(parts)-1]] {
		if o.Path == c.Path && o.Span.StartLine > c.Span.StartLine && enclosingSpanName(*f, o.Span) == owner {
			c = o
		}
	}
	return c, true
}

// enclosingSpanName is the name of the innermost span strictly enclosing sp.
func enclosingSpanName(f File, sp SymbolSpan) string {
	best, size := "", 1<<30
	for _, o := range f.Symbols {
		if o != sp && o.StartLine <= sp.StartLine && sp.EndLine <= o.EndLine && o.EndLine-o.StartLine < size {
			best, size = o.Name, o.EndLine-o.StartLine
		}
	}
	return best
}

func (x *spanIndex) locate0(qid string) (symLoc, bool) {
	dir, local := x.splitQID(qid)
	parts := strings.Split(local, ".")
	base := parts[len(parts)-1]
	cands := x.byName[dir+"\x00"+base]
	if len(cands) == 0 {
		return symLoc{}, false
	}
	if len(parts) >= 2 {
		// dir.module.Class.f: only the module's own file (two scripts in one
		// directory may each define a GwmClient)
		var own []symLoc
		for _, c := range cands {
			if fileModuleStem(c.Path) == parts[0] {
				own = append(own, c)
			}
		}
		if len(own) > 0 && len(own) < len(cands) {
			cands = own
		}
	}
	var recv *regexp.Regexp
	if len(parts) >= 2 {
		class := parts[len(parts)-2]
		// a fn in a Rust impl block for the type
		for _, c := range cands {
			if rustImplOwner(*x.byPath[c.Path], c.Span) == class {
				return c, true
			}
		}
		// dir.module.func in Python/JavaScript: the module is the file
		for _, c := range cands {
			if fileModuleStem(c.Path) == class && !nestedInClass(*x.byPath[c.Path], c.Span) {
				return c, true
			}
		}
		// A method declared inside its class's span.
		for _, c := range cands {
			f := x.byPath[c.Path]
			for _, sp := range f.Symbols {
				if sp.Name == class && sp.StartLine <= c.Span.StartLine && c.Span.EndLine <= sp.EndLine && sp != c.Span {
					return c, true
				}
			}
		}
		// An out-of-class definition (C++ Class::method, Go receiver).
		recv = regexp.MustCompile(`\*?\b` + regexp.QuoteMeta(class) + `(?:\[[^\]]*\])?\)\s*` + regexp.QuoteMeta(base) + `\b`)
		for _, c := range cands {
			f := x.byPath[c.Path]
			line := lineAt(f.Content, c.Span.StartLine)
			if scopedDef(line, class, base) || recv.MatchString(line) {
				return c, true
			}
		}
	}
	if len(parts) == 1 {
		// dir.main is main.py's main() (the id names its file), and dir.f is
		// the directory's own module's f (index.ts, __init__.py) — not another
		// module's of the same directory
		for _, c := range cands {
			if stem := fileModuleStem(c.Path); (stem == base || stem == "") && !looksLikeMethod(*x.byPath[c.Path], c.Span, base) {
				return c, true
			}
		}
		// a free function's id: not a method that shares its name
		for _, c := range cands {
			if !looksLikeMethod(*x.byPath[c.Path], c.Span, base) {
				return c, true
			}
		}
	}
	return cands[0], true
}

// qidAt is the qid of the innermost span enclosing line in path, qualified by
// its enclosing class span when it has one; "" when no span encloses it.
func (x *spanIndex) qidAt(s Store, repo, ref, path string, line int) (string, SymbolSpan) {
	f := x.byPath[path]
	if f == nil {
		return "", SymbolSpan{}
	}
	var inner SymbolSpan
	found := false
	for _, sp := range f.Symbols {
		if sp.StartLine <= line && line <= sp.EndLine {
			if !found || sp.EndLine-sp.StartLine < inner.EndLine-inner.StartLine {
				inner, found = sp, true
			}
		}
	}
	if !found {
		return "", SymbolSpan{}
	}
	syms := s.SymbolsAt(repo, ref)
	dir := pkgFromPath(path)
	// The enclosing class (smallest span strictly containing inner).
	var outer SymbolSpan
	hasOuter := false
	for _, sp := range f.Symbols {
		if sp != inner && sp.StartLine <= inner.StartLine && inner.EndLine <= sp.EndLine {
			if !hasOuter || sp.EndLine-sp.StartLine < outer.EndLine-outer.StartLine {
				outer, hasOuter = sp, true
			}
		}
	}
	try := []string{}
	if stem := fileModuleStem(path); stem != "" {
		// a Python/JS/TS module is its file: dir.card.f for card.tsx's f,
		// never index.tsx's dir.f; a symbol named like its file is dir.name
		mod := qualifyQID(dir, stem)
		if hasOuter {
			try = append(try, qualifyQID(mod, outer.Name+"."+inner.Name))
			if outer.Name == stem {
				try = append(try, qualifyQID(dir, outer.Name+"."+inner.Name))
			}
		}
		try = append(try, qualifyQID(mod, inner.Name))
		if inner.Name == stem {
			try = append(try, qualifyQID(dir, inner.Name))
		}
	}
	if hasOuter {
		try = append(try, qualifyQID(dir, outer.Name+"."+inner.Name))
	}
	try = append(try, qualifyQID(dir, inner.Name))
	for _, q := range try {
		if _, ok := syms[q]; ok {
			return q, inner
		}
	}
	// Fall back to any stored id of this name in this directory.
	for q := range syms {
		if baseName(q) == inner.Name && strings.HasPrefix(q, dir) {
			return q, inner
		}
	}
	return try[len(try)-1], inner
}

func qualifyQID(dir, local string) string {
	if dir == "" {
		return local
	}
	return dir + "." + local
}

func lineAt(content string, n int) string {
	if n <= 0 {
		return ""
	}
	i := 0
	for k := 1; k < n; k++ {
		j := strings.IndexByte(content[i:], '\n')
		if j < 0 {
			return ""
		}
		i += j + 1
	}
	if j := strings.IndexByte(content[i:], '\n'); j >= 0 {
		return content[i : i+j]
	}
	return content[i:]
}

// langOfPath names a file's language by extension (or project-file kind).
func langOfPath(path string) string {
	if k := ProjectFileKind(path); k != "" {
		return k
	}
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.HasSuffix(base, ".launch") || strings.HasSuffix(base, ".launch.xml"):
		return "ros-launch"
	case strings.HasSuffix(base, ".launch.py"):
		return "python"
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".java":
		return "java"
	case ".c":
		return "c"
	case ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return "cpp"
	case ".rs":
		return "rust"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".msg", ".srv", ".action":
		return "ros-interface"
	case ".yaml", ".yml":
		return "yaml"
	case "":
		return "script"
	}
	return "other"
}

// isCodeLang reports whether a langOfPath result is a programming language
// (the files that carry symbols), as opposed to config and project files.
func isCodeLang(l string) bool {
	switch l {
	case "go", "python", "javascript", "typescript", "java", "c", "cpp", "rust", "shell", "script":
		return true
	}
	return false
}

// SymbolFacts read from source.
type symbolSourceFacts struct {
	Kind       string
	Signature  string
	Decorators []string
	Doc        string
	DocLine    int
}

var (
	reClass      = regexp.MustCompile(`^(?:export\s+)?(?:default\s+)?(?:public\s+|private\s+|protected\s+)?(?:abstract\s+|final\s+|static\s+|sealed\s+)*(?:data\s+)?class\b`)
	reStruct     = regexp.MustCompile(`^(?:pub(?:\([\w:]+\))?\s+)?(?:typedef\s+)?struct\b|^type\s+\w+(?:\[[^\]]*\])?\s+struct\b`)
	reInterface  = regexp.MustCompile(`^(?:export\s+)?(?:public\s+)?interface\b|^type\s+\w+(?:\[[^\]]*\])?\s+interface\b|^(?:pub(?:\([\w:]+\))?\s+)?trait\b`)
	reEnum       = regexp.MustCompile(`^(?:export\s+)?(?:pub(?:\([\w:]+\))?\s+)?(?:const\s+)?enum\b`)
	reTypeAlias  = regexp.MustCompile(`^(?:export\s+)?type\s+\w+`)
	reGoMethod   = regexp.MustCompile(`^func\s*\(`)
	reGoFuncDecl = regexp.MustCompile(`^func\b`)
	rePyDef      = regexp.MustCompile(`^(?:async\s+)?def\b`)
	reRustFn     = regexp.MustCompile(`^(?:pub(?:\([\w:]+\))?\s+)?(?:const\s+)?(?:async\s+)?(?:unsafe\s+)?(?:extern\s+"\w+"\s+)?fn\b`)
	reJSFunc     = regexp.MustCompile(`\bfunction\b|=>`)
	reShellFunc  = regexp.MustCompile(`^(?:function\s+)?[\w-]+\s*\(\)\s*\{?`)
	reImplBlock  = regexp.MustCompile(`^impl\b`)
)

// sourceFacts reads kind, signature, decorators and doc for the span starting
// at startLine (1-based) in content. inClass says whether the symbol is
// qualified by a class (a method), which a def/fn line cannot say by itself.
func sourceFacts(content, lang string, startLine, endLine int, inClass bool) symbolSourceFacts {
	lines := strings.Split(content, "\n")
	var f symbolSourceFacts
	if startLine <= 0 || startLine > len(lines) {
		return f
	}
	i := startLine - 1
	// Decorators/attributes may sit inside the span (Python decorated
	// definitions) or just above it.
	for k := i - 1; k >= 0; k-- {
		t := strings.TrimSpace(lines[k])
		if isDecorator(t) {
			f.Decorators = append([]string{t}, f.Decorators...)
			continue
		}
		break
	}
	for i < len(lines) && i < endLine && isDecorator(strings.TrimSpace(lines[i])) {
		f.Decorators = append(f.Decorators, strings.TrimSpace(lines[i]))
		i++
	}
	if i >= len(lines) {
		return f
	}
	declLine := i
	first := strings.TrimSpace(lines[i])
	f.Kind = kindOf(first, lang, inClass)
	f.Signature = signatureFrom(lines, i, endLine, lang)

	// Doc: a docstring right after a Python/shell-free signature, else the
	// contiguous comment block above the declaration (and its decorators).
	if lang == "python" || strings.HasSuffix(strings.TrimSpace(f.Signature), ":") {
		if d, at := pyDocstring(lines, declLine, endLine); d != "" {
			f.Doc, f.DocLine = d, at
			return f
		}
	}
	above := startLine - 1 - len(f.Decorators)
	if d, at := commentAbove(lines, above-1); d != "" {
		f.Doc, f.DocLine = d, at
	}
	return f
}

func isDecorator(t string) bool {
	return strings.HasPrefix(t, "@") && !strings.HasPrefix(t, "@@") || strings.HasPrefix(t, "#[")
}

func kindOf(first, lang string, inClass bool) string {
	t := strings.TrimSpace(first)
	switch {
	case reClass.MatchString(t):
		return "class"
	case reInterface.MatchString(t):
		return "interface"
	case reStruct.MatchString(t):
		return "struct"
	case reEnum.MatchString(t):
		return "enum"
	case reImplBlock.MatchString(t):
		return "impl"
	case reGoMethod.MatchString(t):
		return "method"
	case reGoFuncDecl.MatchString(t), rePyDef.MatchString(t), reRustFn.MatchString(t):
		if inClass {
			return "method"
		}
		return "function"
	case lang == "go" && reTypeAlias.MatchString(t), lang == "typescript" && reTypeAlias.MatchString(t):
		return "type"
	case reJSFunc.MatchString(t):
		if inClass {
			return "method"
		}
		return "function"
	case lang == "shell" && reShellFunc.MatchString(t):
		return "function"
	case strings.Contains(t, "("):
		if inClass {
			return "method"
		}
		return "function"
	}
	return "symbol"
}

// signatureFrom joins the declaration's lines up to where the body begins.
func signatureFrom(lines []string, i, endLine int, lang string) string {
	var parts []string
	for k := i; k < len(lines) && k < i+8 && (endLine <= 0 || k < endLine); k++ {
		t := strings.TrimSpace(lines[k])
		if idx := strings.Index(t, "{"); idx >= 0 && !(lang == "python") {
			// A brace opens the body — unless it is part of the declaration
			// (a Go/TS type literal parameter); keep it simple: stop here.
			parts = append(parts, strings.TrimSpace(t[:idx]))
			break
		}
		parts = append(parts, t)
		if lang == "python" && strings.HasSuffix(t, ":") {
			break
		}
		if strings.HasSuffix(t, ";") || strings.HasSuffix(t, "=>") || strings.HasSuffix(t, ")") && lang != "python" && k+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k+1]), "{") {
			break
		}
	}
	sig := strings.Join(strings.Fields(strings.Join(parts, " ")), " ")
	sig = strings.TrimSuffix(strings.TrimSpace(sig), "=>")
	if len(sig) > 300 {
		sig = sig[:300] + "…"
	}
	return strings.TrimSpace(sig)
}

// pyDocstring returns the docstring that opens a Python def/class body.
func pyDocstring(lines []string, declLine, endLine int) (string, int) {
	k := declLine
	for k < len(lines) && !strings.HasSuffix(strings.TrimSpace(stripPyLineComment(lines[k])), ":") && k < declLine+8 {
		k++
	}
	k++
	for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
		k++
	}
	if k >= len(lines) || endLine > 0 && k >= endLine {
		return "", 0
	}
	return docstringAt(lines, k)
}

// docstringAt reads a triple-quoted string starting on line k (0-based).
func docstringAt(lines []string, k int) (string, int) {
	t := strings.TrimSpace(lines[k])
	t = strings.TrimLeft(t, "rRuUbB")
	q := ""
	switch {
	case strings.HasPrefix(t, `"""`):
		q = `"""`
	case strings.HasPrefix(t, `'''`):
		q = `'''`
	default:
		return "", 0
	}
	body := t[3:]
	var out []string
	if j := strings.Index(body, q); j >= 0 {
		return strings.TrimSpace(body[:j]), k + 1
	}
	out = append(out, body)
	for m := k + 1; m < len(lines) && m < k+60; m++ {
		ln := lines[m]
		if j := strings.Index(ln, q); j >= 0 {
			out = append(out, ln[:j])
			break
		}
		out = append(out, ln)
	}
	return cleanDoc(out), k + 1
}

func stripPyLineComment(s string) string {
	if i := strings.Index(s, "  #"); i >= 0 {
		return s[:i]
	}
	return s
}

// commentAbove collects the contiguous comment block ending at line k (0-based).
func commentAbove(lines []string, k int) (string, int) {
	var block []string
	start := -1
	inBlock := false
	for ; k >= 0; k-- {
		t := strings.TrimSpace(lines[k])
		switch {
		case inBlock:
			block = append([]string{t}, block...)
			if strings.HasPrefix(t, "/*") {
				inBlock = false
				start = k
			}
			continue
		case strings.HasSuffix(t, "*/"):
			block = append([]string{t}, block...)
			start = k
			if !strings.HasPrefix(t, "/*") {
				inBlock = true
			}
			continue
		case strings.HasPrefix(t, "//"), strings.HasPrefix(t, "#") && !isPreprocessor(t), strings.HasPrefix(t, "--"):
			block = append([]string{t}, block...)
			start = k
			continue
		}
		break
	}
	if len(block) == 0 {
		return "", 0
	}
	return cleanDoc(block), start + 1
}

func isPreprocessor(t string) bool {
	for _, p := range []string{"#include", "#define", "#if", "#endif", "#else", "#elif", "#pragma", "#undef", "#!", "#["} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// cleanDoc strips comment markers and blank edges, keeping at most 12 lines.
func cleanDoc(raw []string) string {
	var out []string
	for _, l := range raw {
		t := strings.TrimSpace(l)
		for _, p := range []string{"/**", "/*!", "/*", "*/", "///", "//!", "//", "##", "#", "--", "*"} {
			if strings.HasPrefix(t, p) {
				t = strings.TrimSpace(t[len(p):])
				break
			}
		}
		t = strings.TrimSpace(strings.TrimSuffix(t, "*/"))
		out = append(out, t)
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	if len(out) > 12 {
		out = append(out[:12], "…")
	}
	return strings.Join(out, "\n")
}

// firstSentence is the opening sentence of a doc (for one-line purposes).
func firstSentence(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for i := 0; i+1 < len(s); i++ {
		if (s[i] == '.' || s[i] == '!' || s[i] == '?') && s[i+1] == ' ' && i > 8 {
			return s[:i+1]
		}
	}
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

// rustImplOwner is the type whose impl block encloses span sp in a Rust
// file: Batch for a fn inside `impl Batch {`, Foo inside `impl<T> Trait for
// Foo<T> {`. "" for a free function (or one in a mod block).
func rustImplOwner(f File, sp SymbolSpan) string {
	if !strings.HasSuffix(f.Path, ".rs") {
		return ""
	}
	lines := strings.Split(f.Content, "\n")
	if sp.StartLine < 1 || sp.StartLine > len(lines) {
		return ""
	}
	indent := func(s string) int { return len(s) - len(strings.TrimLeft(s, " \t")) }
	fnIndent := indent(lines[sp.StartLine-1])
	for i := sp.StartLine - 2; i >= 0; i-- {
		ln := lines[i]
		t := strings.TrimSpace(ln)
		if t == "" || indent(ln) >= fnIndent {
			continue
		}
		// the first line above at a lower indentation opens the enclosing block
		if strings.HasPrefix(t, "impl") && (len(t) == 4 || t[4] == ' ' || t[4] == '<') {
			return rustImplTypeText(t)
		}
		if strings.HasPrefix(t, "}") || strings.HasPrefix(t, "#[") || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "where") {
			continue // attributes, comments and where clauses sit between impl and fn
		}
		return ""
	}
	return ""
}

// rustImplTypeText reads the implemented type from an impl header's text.
func rustImplTypeText(h string) string {
	h = strings.TrimSpace(strings.TrimPrefix(h, "impl"))
	if strings.HasPrefix(h, "<") { // impl<'a, T: X<Y>>
		depth := 0
		for i := 0; i < len(h); i++ {
			if h[i] == '<' {
				depth++
			} else if h[i] == '>' {
				depth--
				if depth == 0 {
					h = strings.TrimSpace(h[i+1:])
					break
				}
			}
		}
	}
	if i := strings.Index(h, " for "); i >= 0 {
		h = strings.TrimSpace(h[i+5:])
	}
	end := strings.IndexAny(h, "<{ \t")
	if end >= 0 {
		h = h[:end]
	}
	h = strings.TrimLeft(h, "&")
	if i := strings.LastIndex(h, "::"); i >= 0 {
		h = h[i+2:]
	}
	return h
}
