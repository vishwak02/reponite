package pyright

import (
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// site is one call written in a Python file: the called name's position
// (0-based line, UTF-16 column, as the language server counts) and the name.
type site struct {
	Line   int
	Col    int
	Name   string
	Member bool // called on a value (x.f()), not by a name alone
	// Self: called on self or cls (self.f()), whose type is the class's
	Self bool
}

// token kinds of the scanner
const (
	tName = iota
	tOp
	tString
	tNewline // the end of a logical line
)

type token struct {
	kind int
	text string
	line int // 0-based
	col  int // UTF-16 column
}

// keywords are never a called name (the soft ones, match/case/type, are
// names unless the server says otherwise).
var keywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true, "assert": true, "async": true, "await": true,
	"break": true, "class": true, "continue": true, "def": true, "del": true, "elif": true, "else": true, "except": true,
	"finally": true, "for": true, "from": true, "global": true, "if": true, "import": true, "in": true, "is": true,
	"lambda": true, "nonlocal": true, "not": true, "or": true, "pass": true, "raise": true, "return": true, "try": true,
	"while": true, "with": true, "yield": true,
}

// scanner tokenizes Python source: names, operators, strings, and the ends
// of logical lines. Calls inside an f-string's replacement fields are
// tokenized as code.
type scanner struct {
	src       string
	pos       int
	line      int
	lineStart int // byte offset of the current line's start
	toks      []token
}

func (s *scanner) col(off int) int {
	n := 0
	for _, r := range s.src[s.lineStart:off] {
		n += len(utf16.Encode([]rune{r}))
	}
	return n
}

func (s *scanner) emit(kind int, text string, off int) {
	s.toks = append(s.toks, token{kind: kind, text: text, line: s.line, col: s.col(off)})
}

func (s *scanner) newline() {
	s.line++
	s.lineStart = s.pos
}

// code scans until the byte stop (0: end of input) at bracket depth 0 —
// an f-string's replacement field ends at its closing brace.
func (s *scanner) code(stop byte) {
	depth := 0
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		switch {
		case stop != 0 && depth == 0 && (c == stop || (stop == '}' && (c == '!' || c == ':') && !s.walrusOrNe())):
			return
		case c == '\n':
			s.pos++
			if depth == 0 && stop == 0 {
				s.emit(tNewline, "", s.pos-1)
			}
			s.newline()
		case c == '\\' && s.pos+1 < len(s.src) && (s.src[s.pos+1] == '\n' || s.src[s.pos+1] == '\r'):
			s.pos++ // a line continued: no logical line ends
			if s.src[s.pos] == '\r' && s.pos+1 < len(s.src) && s.src[s.pos+1] == '\n' {
				s.pos++
			}
			s.pos++
			s.newline()
		case c == '#':
			for s.pos < len(s.src) && s.src[s.pos] != '\n' {
				s.pos++
			}
		case c == ' ' || c == '\t' || c == '\r' || c == '\f':
			s.pos++
		case c == '"' || c == '\'':
			s.str("")
		case isNameStart(s.src, s.pos):
			start := s.pos
			for s.pos < len(s.src) && isNamePart(s.src, s.pos) {
				_, n := utf8.DecodeRuneInString(s.src[s.pos:])
				s.pos += n
			}
			word := s.src[start:s.pos]
			if s.pos < len(s.src) && (s.src[s.pos] == '"' || s.src[s.pos] == '\'') && isPrefix(word) {
				s.str(strings.ToLower(word))
				continue
			}
			s.emit(tName, word, start)
		default:
			switch c {
			case '(', '[', '{':
				depth++
			case ')', ']', '}':
				if depth > 0 {
					depth--
				}
			}
			s.emit(tOp, string(c), s.pos)
			s.pos++
		}
	}
}

// walrusOrNe: at a ':' or '!' inside a replacement field, whether it is an
// operator (:=, !=) rather than the field's conversion or format spec.
func (s *scanner) walrusOrNe() bool {
	return s.pos+1 < len(s.src) && s.src[s.pos+1] == '='
}

func isPrefix(w string) bool {
	if len(w) > 2 {
		return false
	}
	for _, r := range strings.ToLower(w) {
		if !strings.ContainsRune("rbuft", r) {
			return false
		}
	}
	return true
}

// str scans a string literal starting at s.pos (its quote), with prefix
// (lower-cased) already consumed.
func (s *scanner) str(prefix string) {
	startTok := token{kind: tString, line: s.line, col: s.col(s.pos)}
	q := s.src[s.pos]
	triple := strings.HasPrefix(s.src[s.pos:], strings.Repeat(string(q), 3))
	if triple {
		s.pos += 3
	} else {
		s.pos++
	}
	raw := strings.ContainsRune(prefix, 'r')
	fmtd := strings.ContainsRune(prefix, 'f') || strings.ContainsRune(prefix, 't')
	for s.pos < len(s.src) {
		c := s.src[s.pos]
		switch {
		case c == '\\' && !raw:
			if s.pos+1 < len(s.src) && s.src[s.pos+1] == '\n' {
				s.pos += 2
				s.newline()
				continue
			}
			s.pos += 2
		case c == '\\' && raw:
			s.pos += 2 // a raw string still cannot end at an escaped quote
		case c == '\n':
			s.pos++
			s.newline()
			if !triple {
				return // an unterminated string ends with its line
			}
		case c == q && (!triple || strings.HasPrefix(s.src[s.pos:], strings.Repeat(string(q), 3))):
			if triple {
				s.pos += 3
			} else {
				s.pos++
			}
			s.toks = append(s.toks, startTok)
			return
		case fmtd && c == '{':
			if s.pos+1 < len(s.src) && s.src[s.pos+1] == '{' {
				s.pos += 2
				continue
			}
			s.pos++
			s.code('}')
			// a format spec (:>{width}) may hold fields of its own
			for s.pos < len(s.src) && s.src[s.pos] != '}' && s.src[s.pos] != '\n' {
				if s.src[s.pos] == '{' {
					s.pos++
					s.code('}')
				}
				if s.pos < len(s.src) && s.src[s.pos] != '}' {
					s.pos++
				}
			}
			if s.pos < len(s.src) && s.src[s.pos] == '}' {
				s.pos++
			}
		default:
			s.pos++
		}
	}
}

func isNameStart(src string, i int) bool {
	c := src[i]
	if c < utf8.RuneSelf {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
	}
	r, _ := utf8.DecodeRuneInString(src[i:])
	return unicode.IsLetter(r)
}

func isNamePart(src string, i int) bool {
	c := src[i]
	if c < utf8.RuneSelf {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	r, _ := utf8.DecodeRuneInString(src[i:])
	return unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Mc, r)
}

// scanCalls lists every call of a name in src — f(…), a.b.f(…) — that runs
// inside a body: a decorator's calls and those of a def's or class's
// header (default values, bases, annotations) run where the definition is,
// not in it, and are left out.
func scanCalls(src string) []site {
	s := &scanner{src: src}
	s.code(0)
	toks := s.toks
	var out []site
	lineStart := true // the next token starts a logical line
	header := false   // inside a def, class or case header, until its ':' at depth 0
	decorator := false
	depth := 0
	for i, t := range toks {
		if t.kind == tNewline {
			lineStart, header, decorator, depth = true, false, false, 0
			continue
		}
		first := lineStart
		lineStart = false
		if t.kind == tOp {
			switch t.text {
			case "(", "[", "{":
				depth++
			case ")", "]", "}":
				if depth > 0 {
					depth--
				}
			case "@":
				if first {
					decorator = true
				}
			case ":":
				if header && depth == 0 {
					header = false
				}
			}
			continue
		}
		if t.kind != tName {
			continue
		}
		if t.text == "def" || t.text == "class" {
			header = true
			continue
		}
		if first && t.text == "case" && i+1 < len(toks) && caseClause(toks[i+1]) {
			// a match statement's case: its class patterns (case Point(x=0):)
			// match, they do not call
			header = true
			continue
		}
		if header || decorator || keywords[t.text] {
			continue
		}
		if i+1 >= len(toks) || toks[i+1].kind != tOp || toks[i+1].text != "(" {
			continue
		}
		if i > 0 && toks[i-1].kind == tName && (toks[i-1].text == "def" || toks[i-1].text == "class") {
			continue
		}
		member := i > 0 && toks[i-1].kind == tOp && toks[i-1].text == "."
		self := member && i > 1 && toks[i-2].kind == tName && (toks[i-2].text == "self" || toks[i-2].text == "cls") &&
			(i < 3 || toks[i-3].kind != tOp || toks[i-3].text != ".")
		out = append(out, site{Line: t.line, Col: t.col, Name: t.text, Member: member, Self: self})
	}
	return out
}

// partlyUnknown reports a hover's type that is a union with Unknown (or
// Any, as pyright prints Unknown outside strict mode) at its top level
// ("(method) save: Any | (() -> None)"): the value may be something the
// checker could not type, whose method the call may run.
func partlyUnknown(hover string) bool {
	i := strings.Index(hover, ": ")
	if i < 0 || strings.HasPrefix(strings.TrimSpace(hover), "(method) def") || strings.HasPrefix(strings.TrimSpace(hover), "(function) def") {
		return false
	}
	t := hover[i+2:]
	if j := strings.IndexByte(t, '\n'); j >= 0 {
		t = t[:j]
	}
	depth, start := 0, 0
	part := func(end int) bool {
		x := strings.TrimSpace(t[start:end])
		return x == "Unknown" || x == "Any"
	}
	for k := 0; k < len(t); k++ {
		switch t[k] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '|':
			if depth == 0 {
				if part(k) {
					return true
				}
				start = k + 1
			}
		}
	}
	return part(len(t))
}

// caseClause: the token after a line's leading "case" starts a pattern
// (case is then the soft keyword, not a name called or assigned).
func caseClause(next token) bool {
	if next.kind != tOp {
		return true
	}
	switch next.text {
	case "(", ".", "=", "[", ",", ")", ":":
		// case(x), case.y, case = 1: a name; case (a, b): a pattern, but
		// rare enough to read as a call of case, which no def answers
		return false
	}
	return true
}

// defAt reads the definition at a location the server gave (0-based line,
// UTF-16 column of its name): the name, and whether a def or a class
// statement defines it there ("" for anything else — an assignment, a
// parameter, an import).
func defAt(src string, line, col int) (name, kind string) {
	ln := lineOf(src, line)
	if ln == "" {
		return "", ""
	}
	off := byteOffset(ln, col)
	if off < 0 || off >= len(ln) || !isNameStart(ln, off) {
		return "", ""
	}
	end := off
	for end < len(ln) && isNamePart(ln, end) {
		_, n := utf8.DecodeRuneInString(ln[end:])
		end += n
	}
	before := strings.Fields(ln[:off])
	after := strings.TrimSpace(ln[end:])
	switch {
	case len(before) == 1 && before[0] == "def", len(before) == 2 && before[0] == "async" && before[1] == "def":
		return ln[off:end], "def"
	case len(before) == 1 && before[0] == "class":
		return ln[off:end], "class"
	case isAssigned(before, after):
		return ln[off:end], "var"
	}
	return ln[off:end], ""
}

// aliasOf: at a name bound by `name = a.b.c` (a statement of its own, the
// value a name or an attribute chain alone), the position of the value's
// last name (0-based line, UTF-16 column).
func aliasOf(src string, line, col int) (int, int, bool) {
	ln := lineOf(src, line)
	off := byteOffset(ln, col)
	if off < 0 || strings.TrimSpace(ln[:off]) != "" {
		return 0, 0, false
	}
	end := off
	for end < len(ln) && isNamePart(ln, end) {
		_, n := utf8.DecodeRuneInString(ln[end:])
		end += n
	}
	rest := strings.TrimLeft(ln[end:], " \t")
	if !strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, "==") {
		return 0, 0, false
	}
	i := len(ln) - len(rest) + 1
	for i < len(ln) && (ln[i] == ' ' || ln[i] == '\t') {
		i++
	}
	last := -1
	for {
		if i >= len(ln) || !isNameStart(ln, i) {
			return 0, 0, false
		}
		last = i
		for i < len(ln) && isNamePart(ln, i) {
			_, n := utf8.DecodeRuneInString(ln[i:])
			i += n
		}
		if i < len(ln) && ln[i] == '.' {
			i++
			continue
		}
		break
	}
	tail := strings.TrimSpace(ln[i:])
	if tail != "" && !strings.HasPrefix(tail, "#") {
		return 0, 0, false
	}
	if keywords[ln[last:i]] {
		return 0, 0, false // x = None
	}
	n := 0
	for _, r := range ln[:last] {
		n += len(utf16.Encode([]rune{r}))
	}
	return line, n, true
}

// isAssigned: a name bound by an assignment statement (x = …, x: T = …,
// a, x = …) or a for loop (for x in …), from the words before it on its
// line and the text after it.
func isAssigned(before []string, after string) bool {
	if len(before) > 0 && before[0] == "for" {
		return true
	}
	if len(before) > 0 && !strings.HasSuffix(before[len(before)-1], ",") && before[len(before)-1] != "(" {
		return false // an attribute, a keyword argument, a parameter
	}
	for _, w := range before {
		if strings.ContainsAny(w, "=()[]{}:.") && w != "(" {
			return false
		}
	}
	if strings.HasPrefix(after, ",") {
		// a, x = …: the assignment's '=' follows the targets
		i := strings.Index(after, "=")
		return i > 0 && !strings.HasPrefix(after[i:], "==") && !strings.ContainsAny(after[:i], "()[]:")
	}
	if strings.HasPrefix(after, ":") && !strings.HasPrefix(after, ":=") {
		i := strings.Index(after, "=")
		return i > 0 && !strings.HasPrefix(after[i:], "==")
	}
	return strings.HasPrefix(after, "=") && !strings.HasPrefix(after, "==")
}

func lineOf(src string, line int) string {
	for i := 0; i < line; i++ {
		j := strings.IndexByte(src, '\n')
		if j < 0 {
			return ""
		}
		src = src[j+1:]
	}
	if j := strings.IndexByte(src, '\n'); j >= 0 {
		src = src[:j]
	}
	return strings.TrimSuffix(src, "\r")
}

// byteOffset is the byte offset in ln of a UTF-16 column, -1 past its end.
func byteOffset(ln string, col int) int {
	n := 0
	for i, r := range ln {
		if n == col {
			return i
		}
		if n > col {
			return -1
		}
		n += len(utf16.Encode([]rune{r}))
	}
	if n == col {
		return len(ln)
	}
	return -1
}
