// gmock.go reads googletest's mocking macros the way they expand, since their
// definitions live in the framework's headers, never in the repository:
//
//   - MOCK_METHOD(R, f, (A a, B b), (const, override)) — and the older
//     MOCK_METHODn / MOCK_CONST_METHODn(f, R(A, B)) — declare on the mock
//     class the override f and two overloads of gmock_f: one taking a matcher
//     per parameter, one taking (WithoutMatchers, Function*) that calls the
//     first.
//   - EXPECT_CALL(obj, f(m1, m2)) and ON_CALL(…) are ((obj).gmock_f(m1, m2))
//     (…): a call of gmock_f on obj, not of f. EXPECT_CALL(obj, f) without
//     matchers calls the (WithoutMatchers, Function*) overload.
//   - NiceMock<T>, NaggyMock<T> and StrictMock<T> derive from T: a call on one
//     reaches T's members.
//   - MATCHER(name, desc) { … } and MATCHER_Pn(name, p1, …, pn, desc) { … }
//     define a function name(p1, …, pn) returning the matcher, whose
//     MatchAndExplain body is the block written after the macro; inside it,
//     arg is the matched value.
//
// Pure over content.AST (ADR-018).
package processing

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

var reGmockOldMock = regexp.MustCompile(`^MOCK_(CONST_)?METHOD(\d+)(_T)?(_WITH_CALLTYPE)?$`)

// gmockMockMacro reports one of googletest's mock-method macros.
func gmockMockMacro(name string) bool {
	return name == "MOCK_METHOD" || reGmockOldMock.MatchString(name)
}

// gmockMembers is the class-member text a mock-method macro's arguments
// declare (as written by macroArgs), false when they are not its shape.
func gmockMembers(macro string, args []string) (string, bool) {
	var ret, name string
	var params []string
	isConst := false
	if macro == "MOCK_METHOD" {
		if len(args) < 3 || len(args) > 4 {
			return "", false
		}
		ret, name = unparen(args[0]), strings.TrimSpace(args[1])
		p := strings.TrimSpace(args[2])
		if !strings.HasPrefix(p, "(") || !strings.HasSuffix(p, ")") {
			return "", false
		}
		params = splitTopLevel(p[1 : len(p)-1])
		if len(args) == 4 {
			for _, s := range splitTopLevel(unparen(args[3])) {
				if strings.TrimSpace(s) == "const" {
					isConst = true
				}
			}
		}
	} else {
		m := reGmockOldMock.FindStringSubmatch(macro)
		if m == nil {
			return "", false
		}
		isConst = m[1] != ""
		if m[4] != "" { // (calltype, name, signature)
			if len(args) == 0 {
				return "", false
			}
			args = args[1:]
		}
		if len(args) != 2 {
			return "", false
		}
		name = strings.TrimSpace(args[0])
		r, plist, ok := splitSignature(args[1])
		if !ok {
			return "", false
		}
		ret, params = r, splitTopLevel(plist)
		if n, err := strconv.Atoi(m[2]); err == nil && n == 0 {
			params = nil
		}
	}
	if !isPlainIdent(name) || ret == "" {
		return "", false
	}
	if len(params) == 1 && (strings.TrimSpace(params[0]) == "" || strings.TrimSpace(params[0]) == "void") {
		params = nil
	}
	for i, p := range params {
		params[i] = unparenType(strings.TrimSpace(p))
	}
	cq := ""
	if isConst {
		cq = " const"
	}
	plist := strings.Join(params, ", ")
	under := make([]string, len(params))
	for i := range under {
		under[i] = "_"
	}
	var b strings.Builder
	b.WriteString(ret + " " + name + "(" + plist + ")" + cq + " {}\n")
	b.WriteString("::testing::MockSpec<void()> gmock_" + name + "(" + plist + ")" + cq + " { return {}; }\n")
	b.WriteString("::testing::MockSpec<void()> gmock_" + name + "(const ::testing::internal::WithoutMatchers&, ::testing::internal::Function<void()>*) const { return this->gmock_" +
		name + "(" + strings.Join(under, ", ") + "); }\n")
	return b.String(), true
}

// unparen strips one pair of parentheses around the whole of s: a macro
// argument holding a comma is written (std::map<K, V>).
func unparen(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "(") && closingParen(s, 0) == len(s)-1 {
		return strings.TrimSpace(s[1 : len(s)-1])
	}
	return s
}

// unparenType strips the parentheses of a parameter whose type is written in
// them: ((std::map<K, V>) m) -> std::map<K, V> m.
func unparenType(p string) string {
	if strings.HasPrefix(p, "(") {
		if end := closingParen(p, 0); end > 0 {
			return strings.TrimSpace(strings.TrimSpace(p[1:end]) + " " + strings.TrimSpace(p[end+1:]))
		}
	}
	return p
}

// closingParen is the index of the parenthesis closing s[open], -1 none.
func closingParen(s string, open int) int {
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// splitTopLevel splits s on the commas outside (), [], {} and <>.
func splitTopLevel(s string) []string {
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[', '{', '<':
			depth++
		case ')', ']', '}', '>':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(s[start:i]))
				start = i + 1
			}
		}
	}
	if rest := strings.TrimSpace(s[start:]); rest != "" || len(out) > 0 {
		out = append(out, rest)
	}
	return out
}

// splitSignature splits a function type R(A, B) into R and "A, B".
func splitSignature(sig string) (ret, params string, ok bool) {
	sig = strings.TrimSpace(sig)
	if !strings.HasSuffix(sig, ")") {
		return "", "", false
	}
	depth := 0
	for i := len(sig) - 1; i >= 0; i-- {
		switch sig[i] {
		case ')':
			depth++
		case '(':
			depth--
			if depth == 0 {
				return strings.TrimSpace(sig[:i]), sig[i+1 : len(sig)-1], strings.TrimSpace(sig[:i]) != ""
			}
		}
	}
	return "", "", false
}

// gmockWrappers derive from their argument: a call on one is a call on it.
var gmockWrappers = map[string]bool{"NiceMock": true, "NaggyMock": true, "StrictMock": true}

// gmockWrapped is T of a NiceMock<T>, NaggyMock<T> or StrictMock<T> type
// node, written bare or in googletest's namespace.
func gmockWrapped(n content.AST) (string, bool) {
	t := n
	if t != nil && t.Type() == "qualified_identifier" {
		kids := t.Children()
		if len(kids) == 0 || strings.TrimPrefix(strings.TrimSpace(kids[0].Text()), "::") != "testing" {
			return "", false
		}
		t = kids[len(kids)-1]
	}
	if t == nil || t.Type() != "template_type" {
		return "", false
	}
	id := firstChildAny(t, []string{"type_identifier"})
	if id == nil || !gmockWrappers[id.Text()] {
		return "", false
	}
	if args := templateArgs(t); len(args) > 0 && args[0] != "" {
		return args[0], true
	}
	return "", false
}

// gmockExpectation reads EXPECT_CALL(obj, f(m…)) / ON_CALL(…): the object,
// the expected call (a call_expression, or the bare name), the method's name
// and how many matchers it passes (-1: none written, the
// (WithoutMatchers, Function*) overload).
func gmockExpectation(call content.AST) (obj, spec content.AST, name string, matchers int, ok bool) {
	kids := call.Children()
	if len(kids) < 2 || kids[0].Type() != "identifier" {
		return nil, nil, "", 0, false
	}
	if m := kids[0].Text(); m != "EXPECT_CALL" && m != "ON_CALL" {
		return nil, nil, "", 0, false
	}
	var args []content.AST
	for _, a := range kids[1].Children() {
		if a.IsNamed() && !isCommentType(a.Type()) {
			args = append(args, a)
		}
	}
	if kids[1].Type() != "argument_list" || len(args) != 2 {
		return nil, nil, "", 0, false
	}
	obj, spec = args[0], args[1]
	switch spec.Type() {
	case "identifier":
		return obj, spec, spec.Text(), -1, true
	case "call_expression":
		sk := spec.Children()
		if len(sk) < 2 || sk[0].Type() != "identifier" {
			return nil, nil, "", 0, false
		}
		n := 0
		for _, a := range sk[len(sk)-1].Children() {
			if a.IsNamed() && !isCommentType(a.Type()) {
				n++
			}
		}
		return obj, spec, sk[0].Text(), n, true
	}
	return nil, nil, "", 0, false
}

// clikeCalls is descendantsAny(body, r.CallTypes) for a C/C++ body with
// googletest's expectations read as what they expand to: EXPECT_CALL(obj,
// f(m)) is not a call of EXPECT_CALL or of f — its object's and matchers'
// own calls are kept — and comes back in gm.
func clikeCalls(body content.AST, r LangRules) (calls []content.AST, gm []content.AST) {
	var walk func(content.AST)
	walk = func(x content.AST) {
		if containsStr(r.CallTypes, x.Type()) {
			if obj, spec, _, _, ok := gmockExpectation(x); ok {
				gm = append(gm, x)
				walk(obj)
				for _, c := range spec.Children() {
					walk(c) // the matchers' own calls; f(…) itself is no call
				}
				return
			}
			calls = append(calls, x)
		}
		for _, c := range x.Children() {
			walk(c)
		}
	}
	for _, c := range body.Children() {
		walk(c)
	}
	return calls, gm
}

// gmockSite is the call an expectation makes: gmock_f on its object.
func gmockSite(call content.AST, vars map[string]string, cpp bool) (CallSite, bool) {
	obj, _, name, matchers, ok := gmockExpectation(call)
	if !ok || !isPlainIdent(name) {
		return CallSite{}, false
	}
	cs := CallSite{Name: "gmock_" + name, Kind: CallMember, NArgs: matchers + 1}
	if matchers < 0 {
		cs.NArgs = 3 // (WithoutMatchers, nullptr)
	}
	if cpp {
		cs.Recv = recvExpr(obj, vars)
	}
	return cs, true
}

var reGmockMatcherLine = regexp.MustCompile(`^\s*MATCHER(_P(\d*))?\s*\(`)

// gmockMatcherDef is one matcher a file defines: the function it names,
// its parameters, its lines (the macro's to the body's last), and the
// function as C++ (name(params) { body }) for the extractor.
type gmockMatcherDef struct {
	name       string
	params     []string
	start, end int
	text       string
}

// gmockMatchers reads the matchers src defines. The parser reads
// MATCHER(name, desc) { … } as a statement and a block, no function.
func gmockMatchers(src string) []gmockMatcherDef {
	if !strings.Contains(src, "MATCHER") {
		return nil
	}
	var out []gmockMatcherDef
	pos := 0
	for ln, text := range strings.Split(src, "\n") {
		lineStart := pos
		pos += len(text) + 1
		m := reGmockMatcherLine.FindStringSubmatch(text)
		if m == nil {
			continue
		}
		n := 0
		if m[1] != "" {
			n = 1
			if m[2] != "" {
				n, _ = strconv.Atoi(m[2])
			}
		}
		args, end := macroArgs(src, lineStart+strings.IndexByte(text, '('))
		if end < 0 || len(args) != n+2 || !isPlainIdent(args[0]) {
			continue
		}
		ok := true
		for _, p := range args[1 : n+1] {
			ok = ok && isPlainIdent(p)
		}
		k := end
		for k < len(src) && (src[k] == ' ' || src[k] == '\t' || src[k] == '\n' || src[k] == '\r') {
			k++
		}
		if !ok || k >= len(src) || src[k] != '{' {
			continue
		}
		close := closingBrace(src, k)
		if close < 0 {
			continue
		}
		params := make([]string, n)
		for i, p := range args[1 : n+1] {
			params[i] = "gmock_param " + p // of the type the call passes: unknown here
		}
		out = append(out, gmockMatcherDef{name: args[0], params: args[1 : n+1], start: ln + 1,
			end:  ln + 1 + strings.Count(src[lineStart:close], "\n"),
			text: "bool " + args[0] + "(" + strings.Join(params, ", ") + ") " + src[k:close+1] + "\n"})
	}
	return out
}

// closingBrace is the index of the brace closing src[open], skipping
// literals and comments; -1 when unclosed.
func closingBrace(src string, open int) int {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; {
		case c == '"' || c == '\'':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			if j := strings.Index(src[i+2:], "*/"); j >= 0 {
				i += j + 3
			} else {
				return -1
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}
