package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// JavaScript/TypeScript receiver typing: x.f() resolved on the class x holds
// when the code says which — a parameter's or variable's annotation (b:
// Bin, slots: Array<Slot>), a constructor (const p = new Parser()), the
// caller's own instance under another name (const scope = this), a field of
// a class (this.index.get(), with index declared, initialized or assigned in
// the class), a module-level instance (export const store = new Store()),
// an element of a typed collection (for (const s of this.slots),
// this.slots.forEach((s) => …)), or what a function returns. The class is
// found through the file's imports and the call resolves in its lineage.
//
// At extraction a receiver is kept as a small expression (QualifiedCall.Recv)
// and each binding as an encoded value (Symbol.Vars):
//
//	expr:  head ( "." name | "[]" )*
//	head:  this | name | name"()" | "(" value ")" | "[" expr (";" expr)* "]"
//	value: "t:"Cls (declared) | "n:"Cls (constructed: exactly Cls) |
//	       "e:"Cls (a collection of Cls) | "x:"expr; alternatives joined by "|"
//
// A binding whose value is not one of these is jsLocalTag (unknown).

// jsSkip is the value of null/undefined: it says nothing about the class a
// variable holds when it holds one.
const jsSkip = "\x00skip"

// jsBindsThis: the nodes inside which `this` is not the enclosing method's.
func jsBindsThis(t string) bool {
	switch t {
	case "function_expression", "function", "function_declaration", "generator_function",
		"generator_function_declaration", "method_definition", "class_declaration", "class",
		"abstract_class_declaration":
		return true
	}
	return false
}

// jsFunctionNode: a function whose parameters bind names.
func jsFunctionNode(t string) bool {
	return t == "arrow_function" || jsBindsThis(t) && t != "class_declaration" && t != "class" && t != "abstract_class_declaration"
}

// jsCallRecvs is the receiver expression of each call under body, in the
// order descendantsAny lists them ("" where the call is not a member call
// on a typeable object).
func jsCallRecvs(body content.AST, callTypes []string) []string {
	var out []string
	var walk func(n content.AST, thisOK bool)
	walk = func(n content.AST, thisOK bool) {
		for _, c := range n.Children() {
			if containsStr(callTypes, c.Type()) {
				out = append(out, jsCallRecv(c, thisOK))
			}
			walk(c, thisOK && !jsBindsThis(c.Type()))
		}
	}
	walk(body, true)
	return out
}

func jsCallRecv(call content.AST, thisOK bool) string {
	if call.Type() != "call_expression" {
		return ""
	}
	k := namedKids(call)
	if len(k) == 0 || k[0].Type() != "member_expression" {
		return ""
	}
	obj := namedKids(k[0])
	if len(obj) < 2 {
		return ""
	}
	return jsExpr(obj[0], thisOK, 0)
}

// jsExpr is n as a receiver expression; "" when it is not one.
func jsExpr(n content.AST, thisOK bool, depth int) string {
	if n == nil || depth > 12 {
		return ""
	}
	k := namedKids(n)
	switch n.Type() {
	case "this":
		if thisOK {
			return "this"
		}
	case "identifier":
		if t := n.Text(); t != "undefined" && reJSIdent.MatchString(t) {
			return t
		}
	case "member_expression":
		if len(k) >= 2 {
			p := k[len(k)-1]
			if p.Type() == "property_identifier" || p.Type() == "private_property_identifier" {
				if o := jsExpr(k[0], thisOK, depth+1); o != "" {
					return o + "." + p.Text()
				}
			}
		}
	case "subscript_expression":
		if len(k) >= 1 {
			if o := jsExpr(k[0], thisOK, depth+1); o != "" {
				return o + "[]"
			}
		}
	case "non_null_expression", "parenthesized_expression":
		if len(k) == 1 {
			return jsExpr(k[0], thisOK, depth+1)
		}
	case "as_expression":
		if len(k) == 2 {
			if e := jsTypeEnc(k[1]); e != "" {
				return "(" + e + ")"
			}
		}
	case "call_expression":
		if len(k) == 2 && k[1].Type() == "arguments" {
			switch k[0].Type() {
			case "identifier":
				if reJSIdent.MatchString(k[0].Text()) {
					return k[0].Text() + "()"
				}
			case "member_expression":
				// Object.values(record): its elements, as the record holds them
				if args := namedKids(k[1]); strings.ReplaceAll(k[0].Text(), " ", "") == "Object.values" && len(args) == 1 {
					return jsExpr(args[0], thisOK, depth+1)
				}
			}
		}
	case "new_expression":
		if len(k) >= 1 && (k[0].Type() == "identifier" || k[0].Type() == "member_expression") {
			if d := dottedRef(k[0]); len(d) > 0 {
				return "(n:" + strings.Join(d, ".") + ")"
			}
		}
	case "array":
		if len(k) == 0 || len(k) > 8 {
			return ""
		}
		parts := make([]string, 0, len(k))
		for _, e := range k {
			p := jsExpr(e, thisOK, depth+1)
			if p == "" {
				return ""
			}
			parts = append(parts, p)
		}
		return "[" + strings.Join(parts, ";") + "]"
	}
	return ""
}

// jsTypeEnc encodes a type annotation as a value: Cls -> t:Cls, Cls[] /
// Array<Cls> / Set<Cls> / Record<K, Cls> -> e:Cls, Cls | null -> t:Cls;
// "" for anything else (a primitive, a function or object type, a union of
// classes).
func jsTypeEnc(t content.AST) string {
	if t == nil {
		return ""
	}
	k := namedKids(t)
	switch t.Type() {
	case "type_annotation", "parenthesized_type", "readonly_type":
		if len(k) == 1 {
			return jsTypeEnc(k[0])
		}
	case "type_identifier":
		return "t:" + t.Text()
	case "nested_type_identifier":
		if d := dottedRef(t); len(d) > 0 {
			return "t:" + strings.Join(d, ".")
		}
	case "generic_type":
		if len(k) == 0 {
			return ""
		}
		head := dottedRef(k[0])
		if len(head) == 0 {
			return ""
		}
		var args []content.AST
		for _, a := range k[1:] {
			if a.Type() == "type_arguments" {
				args = namedKids(a)
			}
		}
		switch name := strings.Join(head, "."); name {
		case "Array", "ReadonlyArray", "Set", "ReadonlySet", "Iterable", "IterableIterator":
			if len(args) == 1 {
				return jsElemEnc(args[0])
			}
			return ""
		case "Record":
			if len(args) == 2 {
				return jsElemEnc(args[1])
			}
			return ""
		case "Readonly", "Required", "NonNullable":
			if len(args) == 1 {
				return jsTypeEnc(args[0])
			}
			return ""
		default:
			return "t:" + name
		}
	case "array_type":
		if len(k) == 1 {
			return jsElemEnc(k[0])
		}
	case "union_type":
		var kept []content.AST
		var flat func(u content.AST)
		flat = func(u content.AST) {
			for _, m := range namedKids(u) {
				switch {
				case m.Type() == "union_type":
					flat(m)
				case m.Type() == "literal_type" && (m.Text() == "null" || m.Text() == "undefined"),
					m.Type() == "predefined_type" && m.Text() == "undefined":
				default:
					kept = append(kept, m)
				}
			}
		}
		flat(t)
		if len(kept) == 1 {
			return jsTypeEnc(kept[0])
		}
	}
	return ""
}

func jsElemEnc(t content.AST) string {
	if e := jsTypeEnc(t); strings.HasPrefix(e, "t:") {
		return "e:" + e[2:]
	}
	return ""
}

// jsValueEnc encodes the value an expression binds; "" when unknown.
func jsValueEnc(v content.AST, thisOK bool) string {
	switch v.Type() {
	case "null", "undefined":
		return jsSkip
	case "this":
		if thisOK {
			return "x:this"
		}
		return ""
	case "new_expression":
		if e := jsExpr(v, thisOK, 0); strings.HasPrefix(e, "(n:") {
			return e[1 : len(e)-1]
		}
		return ""
	case "as_expression":
		if k := namedKids(v); len(k) == 2 {
			return jsTypeEnc(k[1])
		}
		return ""
	}
	if e := jsExpr(v, thisOK, 0); e != "" {
		return "x:" + e
	}
	return ""
}

// jsIterMethods hand each element of the collection they are called on to
// their callback's first parameter.
var jsIterMethods = map[string]bool{"forEach": true, "map": true, "filter": true, "some": true, "every": true,
	"find": true, "findLast": true, "findIndex": true, "findLastIndex": true, "flatMap": true}

// jsBinder collects what each name of a function (or each field of a class)
// is bound to.
type jsBinder struct {
	order     []string
	declared  map[string]bool
	plain     map[string]bool     // has a declaration without an annotation
	unknown   map[string]bool     // bound to something not encodable
	alts      map[string][]string // values of its unannotated declarations
	annotated map[string][]string // its annotations
	assigns   map[string][]string // values assigned to it later ("" unknown)
	inConst   bool                // walking a const declaration's declarators
}

func newJSBinder() *jsBinder {
	return &jsBinder{declared: map[string]bool{}, plain: map[string]bool{}, unknown: map[string]bool{},
		alts: map[string][]string{}, annotated: map[string][]string{}, assigns: map[string][]string{}}
}

func (b *jsBinder) declare(name string) {
	if !b.declared[name] {
		b.declared[name] = true
		b.order = append(b.order, name)
	}
}

// bind records an unannotated declaration of name with value enc.
func (b *jsBinder) bind(name, enc string) {
	if name == "" {
		return
	}
	b.declare(name)
	b.plain[name] = true
	switch enc {
	case "":
		b.unknown[name] = true
	case jsSkip:
	default:
		b.alts[name] = append(b.alts[name], enc)
	}
}

func (b *jsBinder) annotate(name, enc string) {
	if name == "" {
		return
	}
	b.declare(name)
	b.annotated[name] = append(b.annotated[name], enc)
}

func (b *jsBinder) assign(name, enc string) {
	if enc != jsSkip {
		b.assigns[name] = append(b.assigns[name], enc)
	}
}

// pattern binds every name a destructuring pattern declares, as unknown.
func (b *jsBinder) pattern(n content.AST) {
	switch n.Type() {
	case "identifier", "shorthand_property_identifier_pattern":
		b.bind(n.Text(), "")
	case "assignment_pattern", "object_assignment_pattern":
		if k := namedKids(n); len(k) > 0 {
			b.pattern(k[0]) // the default value is not a binding
		}
	case "pair_pattern":
		if k := namedKids(n); len(k) > 1 {
			b.pattern(k[len(k)-1])
		}
	case "object_pattern", "array_pattern", "rest_pattern", "required_parameter", "optional_parameter", "formal_parameters":
		for _, k := range namedKids(n) {
			if k.Type() == "type_annotation" || k.Type() == "accessibility_modifier" {
				continue
			}
			b.pattern(k)
		}
	}
}

// result is each name's value: its annotation when every declaration is
// annotated, else every value it is given, all of which must be known.
// assigned also keeps names only assigned (a class's this.x = …).
func (b *jsBinder) result(assigned bool) map[string]string {
	names := b.order
	if assigned {
		for n := range b.assigns {
			if !b.declared[n] {
				names = append(names, n)
			}
		}
	}
	out := map[string]string{}
	for _, name := range names {
		v := jsLocalTag
		var vals []string
		known := !b.unknown[name]
		if anns := b.annotated[name]; len(anns) > 0 && !b.plain[name] {
			vals = anns
		} else {
			vals = append(append(append(vals, b.alts[name]...), b.annotated[name]...), b.assigns[name]...)
		}
		seen := map[string]bool{}
		var uniq []string
		for _, x := range vals {
			if x == "" {
				known = false
			}
			if !seen[x] {
				seen[x] = true
				uniq = append(uniq, x)
			}
		}
		if known && len(uniq) > 0 {
			v = strings.Join(uniq, "|")
		}
		out[name] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// jsLocals are a JS/TS function's own bindings — parameters, variables,
// loop and catch variables, the parameters of the functions nested in it —
// each with the value it holds when the code says (else jsLocalTag).
func jsLocals(fn content.AST) map[string]string {
	b := newJSBinder()
	b.function(fn, true, "", true)
	return b.result(false)
}

// function binds a function's parameters (the first one to hint when it
// has no annotation) and walks its body.
func (b *jsBinder) function(f content.AST, thisOK bool, hint string, root bool) {
	inner := thisOK
	if !root && f.Type() != "arrow_function" {
		inner = false
	}
	for _, c := range f.Children() {
		switch {
		case c.Type() == "formal_parameters":
			b.params(c, hint)
			b.walk(c, inner)
		case c.Type() == "identifier" && f.Type() == "arrow_function":
			b.bind(c.Text(), hint) // x => …
		default:
			b.node(c, inner)
		}
	}
}

func (b *jsBinder) params(fp content.AST, hint string) {
	for i, p := range namedKids(fp) {
		h := ""
		if i == 0 && hint != "" {
			h = hint
		}
		switch p.Type() {
		case "required_parameter", "optional_parameter":
			var pat, ann content.AST
			for _, c := range namedKids(p) {
				switch c.Type() {
				case "accessibility_modifier", "override_modifier", "decorator":
				case "type_annotation":
					ann = c
				default:
					if pat == nil {
						pat = c
					}
				}
			}
			switch {
			case pat == nil:
			case pat.Type() == "identifier" && ann != nil:
				b.annotate(pat.Text(), jsTypeEnc(ann))
			case pat.Type() == "identifier":
				b.bind(pat.Text(), h)
			default:
				b.pattern(pat)
			}
		case "identifier":
			b.bind(p.Text(), h)
		default:
			b.pattern(p)
		}
	}
}

func (b *jsBinder) walk(n content.AST, thisOK bool) {
	for _, c := range n.Children() {
		b.node(c, thisOK)
	}
}

func (b *jsBinder) node(c content.AST, thisOK bool) {
	t := c.Type()
	switch {
	case jsFunctionNode(t):
		saved := b.inConst
		b.inConst = false // its var declarations are not the const's
		b.function(c, thisOK, "", false)
		b.inConst = saved
		return
	case t == "variable_declaration":
		saved := b.inConst
		b.inConst = false
		b.walk(c, thisOK)
		b.inConst = saved
		return
	case jsBindsThis(t): // a nested class
		b.walk(c, false)
		return
	case t == "lexical_declaration":
		saved := b.inConst
		b.inConst = jsIsConst(c)
		b.walk(c, thisOK)
		b.inConst = saved
		return
	case t == "variable_declarator":
		b.declarator(c, thisOK)
	case t == "assignment_expression":
		if k := namedKids(c); len(k) == 2 && k[0].Type() == "identifier" {
			b.assign(k[0].Text(), jsValueEnc(k[1], thisOK))
		}
	case t == "for_in_statement":
		b.forIn(c, thisOK)
	case t == "catch_clause":
		if k := namedKids(c); len(k) > 0 && k[0].Type() != "statement_block" {
			b.pattern(k[0])
		}
	case t == "call_expression":
		if b.callback(c, thisOK) {
			return
		}
	}
	b.walk(c, thisOK)
}

func (b *jsBinder) declarator(d content.AST, thisOK bool) {
	k := namedKids(d)
	if len(k) == 0 {
		return
	}
	if k[0].Type() != "identifier" {
		b.pattern(k[0])
		return
	}
	var ann, val content.AST
	for _, c := range k[1:] {
		switch {
		case c.Type() == "type_annotation":
			ann = c
		case val == nil:
			val = c
		}
	}
	switch name := k[0].Text(); {
	case ann != nil:
		enc := jsTypeEnc(ann)
		if val != nil && strings.HasPrefix(enc, "t:") && b.inConst {
			if made := jsValueEnc(val, thisOK); strings.HasPrefix(made, "n:") && made[2:] != enc[2:] {
				// const x: Base = new Impl(): exactly Impl, annotated Base
				enc = "d:" + enc[2:] + ">" + made[2:]
			}
		}
		b.annotate(name, enc)
	case val != nil:
		b.bind(name, jsValueEnc(val, thisOK))
	default:
		b.bind(name, jsSkip) // let x; — its assignments say
	}
}

// jsIsConst: a const declaration (its names bound once).
func jsIsConst(decl content.AST) bool {
	if decl.Type() != "lexical_declaration" {
		return false
	}
	for _, c := range decl.Children() {
		return c.Type() == "const"
	}
	return false
}

func (b *jsBinder) forIn(f content.AST, thisOK bool) {
	of := false
	for _, c := range f.Children() {
		if !c.IsNamed() && c.Type() == "of" {
			of = true
		}
	}
	k := namedKids(f)
	if len(k) < 2 {
		return
	}
	if k[0].Type() != "identifier" {
		b.pattern(k[0])
		return
	}
	enc := ""
	if e := jsExpr(k[1], thisOK, 0); of && e != "" {
		enc = "x:" + e + "[]"
	}
	b.bind(k[0].Text(), enc)
}

// callback binds the first parameter of a callback handed to an iterating
// method (items.forEach((it) => …)) to the collection's element, and walks
// the call; false when the call is not one.
func (b *jsBinder) callback(call content.AST, thisOK bool) bool {
	k := namedKids(call)
	if len(k) != 2 || k[0].Type() != "member_expression" || k[1].Type() != "arguments" {
		return false
	}
	m := namedKids(k[0])
	if len(m) < 2 || !jsIterMethods[m[len(m)-1].Text()] {
		return false
	}
	args := namedKids(k[1])
	if len(args) == 0 || args[0].Type() != "arrow_function" && args[0].Type() != "function_expression" && args[0].Type() != "function" {
		return false
	}
	hint := ""
	if e := jsExpr(m[0], thisOK, 0); e != "" {
		hint = "x:" + e + "[]"
	}
	b.node(k[0], thisOK)
	first := true
	for _, a := range k[1].Children() {
		if first && a.IsNamed() && !isCommentType(a.Type()) {
			first = false
			b.function(a, thisOK, hint, false) // args[0]
			continue
		}
		b.node(a, thisOK)
	}
	return true
}

// jsIsStatic: a class member declared static.
func jsIsStatic(m content.AST) bool {
	for _, c := range m.Children() {
		if c.Type() == "static" {
			return true
		}
	}
	return false
}

// jsClassFields are the instance fields of a JS/TS class (or the properties
// of an interface) whose value is known: a field's annotation, else its
// initializer and every this.f = … in the class's methods; a constructor's
// parameter properties (constructor(private api: Api)).
func jsClassFields(decl content.AST) map[string]string {
	body := firstChildAny(decl, []string{"class_body", "interface_body", "object_type"})
	if body == nil {
		return nil
	}
	b := newJSBinder()
	for _, m := range namedKids(body) {
		switch m.Type() {
		case "public_field_definition", "field_definition":
			if jsIsStatic(m) {
				continue
			}
			name := ""
			var ann, val content.AST
			for _, c := range namedKids(m) {
				switch c.Type() {
				case "property_identifier", "private_property_identifier":
					if name == "" {
						name = c.Text()
					}
				case "type_annotation":
					ann = c
				case "accessibility_modifier", "decorator", "override_modifier":
				default:
					if name != "" && val == nil {
						val = c
					}
				}
			}
			switch {
			case name == "":
			case ann != nil:
				b.annotate(name, jsTypeEnc(ann))
			case val != nil:
				b.bind(name, jsValueEnc(val, true))
			default:
				b.bind(name, jsSkip)
			}
			if val != nil && jsFunctionNode(val.Type()) {
				b.thisAssigns(val, jsLocals(val))
			}
		case "property_signature":
			name := ""
			var ann content.AST
			for _, c := range namedKids(m) {
				switch c.Type() {
				case "property_identifier":
					name = c.Text()
				case "type_annotation":
					ann = c
				}
			}
			if name != "" && ann != nil {
				b.annotate(name, jsTypeEnc(ann))
			}
		case "method_definition":
			if jsIsStatic(m) {
				continue
			}
			if id := firstChildAny(m, []string{"property_identifier"}); id != nil && id.Text() == "constructor" {
				if fp := firstChildAny(m, []string{"formal_parameters"}); fp != nil {
					for _, p := range namedKids(fp) {
						if firstChildAny(p, []string{"accessibility_modifier"}) == nil && !jsHasChild(p, "readonly") {
							continue
						}
						pid := firstChildAny(p, []string{"identifier"})
						if pid == nil {
							continue
						}
						if ann := firstChildAny(p, []string{"type_annotation"}); ann != nil {
							b.annotate(pid.Text(), jsTypeEnc(ann))
						} else {
							b.bind(pid.Text(), "")
						}
					}
				}
			}
			b.thisAssigns(m, jsLocals(m))
		}
	}
	out := b.result(true)
	for k, v := range out {
		if v == jsLocalTag {
			delete(out, k)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func jsHasChild(n content.AST, t string) bool {
	for _, c := range n.Children() {
		if c.Type() == t {
			return true
		}
	}
	return false
}

// thisAssigns records every this.f = v in a method (outside functions that
// bind their own this). A value naming one of the method's own bindings is
// known only as that binding's declared class.
func (b *jsBinder) thisAssigns(fn content.AST, locals map[string]string) {
	var walk func(n content.AST, thisOK bool)
	walk = func(n content.AST, thisOK bool) {
		for _, c := range n.Children() {
			if c.Type() == "assignment_expression" && thisOK {
				if k := namedKids(c); len(k) == 2 && k[0].Type() == "member_expression" {
					if lk := namedKids(k[0]); len(lk) == 2 && lk[0].Type() == "this" && lk[1].Type() == "property_identifier" {
						b.assign(lk[1].Text(), jsOwnValue(jsValueEnc(k[1], true), locals))
					}
				}
			}
			walk(c, thisOK && !jsBindsThis(c.Type()))
		}
	}
	walk(fn, true)
}

// jsOwnValue is a value written in a method, as its class knows it: one
// that names a local of the method is that local's declared class, or
// unknown.
func jsOwnValue(enc string, locals map[string]string) string {
	if !strings.HasPrefix(enc, "x:") {
		return enc
	}
	head, _ := jsSplitHead(enc[2:])
	if head == "" {
		return ""
	}
	name := strings.TrimSuffix(head, "()")
	lv, local := locals[name]
	switch {
	case head == "this" || head[0] == '(' || head[0] == '[' || !local:
		return enc
	case head == enc[2:] && (strings.HasPrefix(lv, "t:") || strings.HasPrefix(lv, "n:") || strings.HasPrefix(lv, "e:")) && !strings.Contains(lv, "|"):
		return lv
	}
	return ""
}

// jsSplitHead splits a receiver expression into its head and the member
// and element accesses after it.
func jsSplitHead(e string) (head, ops string) {
	if e == "" {
		return "", ""
	}
	switch e[0] {
	case '(', '[':
		depth := 0
		for i := 0; i < len(e); i++ {
			switch e[i] {
			case '(', '[':
				depth++
			case ')', ']':
				depth--
				if depth == 0 {
					return e[:i+1], e[i+1:]
				}
			}
		}
		return e, ""
	}
	i := 0
	for i < len(e) && e[i] != '.' && e[i] != '[' && e[i] != '(' {
		i++
	}
	if strings.HasPrefix(e[i:], "()") {
		i += 2
	}
	return e[:i], e[i:]
}

// jsSplitTop splits s at sep outside brackets and parentheses.
func jsSplitTop(s string, sep byte) []string {
	var out []string
	depth, last := 0, 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, s[last:i])
				last = i + 1
			}
		}
	}
	return append(out, s[last:])
}

// --- index time ---

// jsModCtx is what reading a name in a module needs: its file, language and
// module-level imports.
type jsModCtx struct {
	path, lang string
	imports    map[string]ImportBinding
}

// jsCtx is where an expression is read: a module, the class `this` is (by
// name, "" outside a class) and the reading function's bindings.
type jsCtx struct {
	pkg, recv string
	mod       jsModCtx
	vars      map[string]string
}

// jsVal is what an expression holds: an instance of a class (exactly that
// class when constructed in sight), or a collection of instances.
type jsVal struct {
	cls         string
	exact, coll bool
	decl        string // the class its annotation names when made of a subclass ("d:")
}

// jsTyper evaluates receiver expressions against the repo's classes.
type jsTyper struct {
	classes *classGraph
	mods    moduleIndex
	modCtx  map[string]jsModCtx          // module package id -> its context
	fields  map[string]map[string]string // class key -> its known fields
	inRepo  func(string) bool
}

func (t *jsTyper) moduleCtx(pkg string) jsCtx { return jsCtx{pkg: pkg, mod: t.modCtx[pkg]} }

// classIn is the class key a type name written in ctx names, through the
// file's imports; "" when it is not a class of the repo. An ES module names
// nothing it does not declare or import, so no other module's class of the
// name is taken.
func (t *jsTyper) classIn(ctx jsCtx, dotted string) string {
	if dotted == "" {
		return ""
	}
	isCls := func(p, n string) bool { _, ok := t.classes.has(p, n); return ok }
	modPkg := func(m string) (string, bool) { return t.mods.resolve(ctx.mod.lang, ctx.mod.path, m) }
	for _, s := range sitesFromQualified([]QualifiedCall{dottedCall(dotted)}, ctx.mod.imports, ctx.mod.lang, t.inRepo, modPkg) {
		switch s.Kind {
		case CallModule:
			if k, ok := t.classes.has(s.Scope, s.Name); ok {
				return k
			}
			if rp, rn, ok := t.mods.resolveExport(s.Scope, s.Name, isCls); ok {
				k, _ := t.classes.has(rp, rn)
				return k
			}
			if b, ok := ctx.mod.imports[dotted]; ok && b.Default && b.Symbol == "" {
				if rp, rn, ok := t.mods.resolveExport(s.Scope, "default", isCls); ok {
					k, _ := t.classes.has(rp, rn)
					return k
				}
			}
		case CallPlain:
			if k, ok := t.classes.has(ctx.pkg, s.Name); ok {
				return k
			}
		}
	}
	return ""
}

// eval is what a receiver expression holds in ctx.
func (t *jsTyper) eval(ctx jsCtx, expr string, depth int) (jsVal, bool) {
	if depth > 8 || expr == "" {
		return jsVal{}, false
	}
	head, ops := jsSplitHead(expr)
	v, ok := t.head(ctx, head, depth)
	for ok && ops != "" {
		switch {
		case strings.HasPrefix(ops, "[]"):
			ops = ops[2:]
			if !v.coll {
				return jsVal{}, false
			}
			v = jsVal{cls: v.cls}
		case ops[0] == '.':
			end := 1
			for end < len(ops) && ops[end] != '.' && ops[end] != '[' {
				end++
			}
			name := ops[1:end]
			ops = ops[end:]
			if v.coll {
				return jsVal{}, false
			}
			v, ok = t.field(v.cls, name, depth+1)
		default:
			return jsVal{}, false
		}
	}
	return v, ok
}

func (t *jsTyper) head(ctx jsCtx, h string, depth int) (jsVal, bool) {
	switch {
	case h == "":
		return jsVal{}, false
	case h[0] == '(':
		return t.value(ctx, h[1:len(h)-1], depth+1)
	case h[0] == '[':
		var out jsVal
		for i, item := range jsSplitTop(h[1:len(h)-1], ';') {
			v, ok := t.eval(ctx, item, depth+1)
			if !ok || v.coll || i > 0 && v.cls != out.cls {
				return jsVal{}, false
			}
			out = jsVal{cls: v.cls, coll: true}
		}
		return out, out.cls != ""
	case h == "this":
		if ctx.recv == "" {
			return jsVal{}, false
		}
		k, ok := t.classes.has(ctx.pkg, ctx.recv)
		return jsVal{cls: k}, ok
	case strings.HasSuffix(h, "()"):
		return t.returnOf(ctx, h[:len(h)-2])
	}
	if v, ok := ctx.vars[h]; ok {
		return t.value(ctx, v, depth+1)
	}
	return t.moduleValue(ctx, h)
}

// value is what an encoded binding holds: every alternative must agree.
func (t *jsTyper) value(ctx jsCtx, enc string, depth int) (jsVal, bool) {
	if depth > 8 {
		return jsVal{}, false
	}
	var out jsVal
	for i, alt := range strings.Split(enc, "|") {
		var v jsVal
		switch {
		case strings.HasPrefix(alt, "t:"):
			v = jsVal{cls: t.classIn(ctx, alt[2:])}
		case strings.HasPrefix(alt, "n:"):
			v = jsVal{cls: t.classIn(ctx, alt[2:]), exact: true}
		case strings.HasPrefix(alt, "d:"):
			decl, made, _ := strings.Cut(alt[2:], ">")
			dk, mk := t.classIn(ctx, decl), t.classIn(ctx, made)
			if mk != "" && dk != "" && mk != dk && t.classes.derives(mk, dk) {
				v = jsVal{cls: mk, exact: true, decl: dk}
			} else {
				v = jsVal{cls: dk} // not a subclass the repo knows: the annotation
			}
		case strings.HasPrefix(alt, "e:"):
			v = jsVal{cls: t.classIn(ctx, alt[2:]), coll: true}
		case strings.HasPrefix(alt, "x:"):
			var ok bool
			if v, ok = t.eval(ctx, alt[2:], depth+1); !ok {
				return jsVal{}, false
			}
		}
		if v.cls == "" || i > 0 && (v.cls != out.cls || v.coll != out.coll) {
			return jsVal{}, false
		}
		if i > 0 {
			v.exact = v.exact && out.exact
			if v.decl != out.decl {
				v.decl = ""
			}
		}
		out = v
	}
	return out, out.cls != ""
}

// returnOf is the instance function f (as ctx names it) returns.
func (t *jsTyper) returnOf(ctx jsCtx, f string) (jsVal, bool) {
	if _, local := ctx.vars[f]; local {
		return jsVal{}, false
	}
	hasReturn := func(p, n string) bool { return t.mods.returns[p][n] != "" }
	if b, ok := ctx.mod.imports[f]; ok {
		mp, ok := t.mods.resolve(ctx.mod.lang, ctx.mod.path, b.Module)
		if !ok || b.Symbol == "" {
			return jsVal{}, false
		}
		fp, fn, ok := t.mods.resolveExport(mp, b.Symbol, hasReturn)
		if !ok {
			return jsVal{}, false
		}
		k := t.classIn(t.moduleCtx(fp), t.mods.returns[fp][fn])
		return jsVal{cls: k}, k != ""
	}
	if cls := t.mods.returns[ctx.pkg][f]; cls != "" {
		k := t.classIn(ctx, cls)
		return jsVal{cls: k}, k != ""
	}
	return jsVal{}, false
}

// moduleValue is the instance a module-level name holds: one the module
// constructs (const store = new Store()), or one it imports.
func (t *jsTyper) moduleValue(ctx jsCtx, name string) (jsVal, bool) {
	if cls := t.mods.instances[ctx.pkg][name]; cls != "" {
		k := t.classIn(ctx, cls)
		return jsVal{cls: k, exact: true}, k != ""
	}
	b, ok := ctx.mod.imports[name]
	if !ok {
		return jsVal{}, false
	}
	mp, ok := t.mods.resolve(ctx.mod.lang, ctx.mod.path, b.Module)
	if !ok {
		return jsVal{}, false
	}
	if dc := t.mods.defaultClass[mp]; b.Default && dc != "" {
		k := t.classIn(t.moduleCtx(mp), dc)
		return jsVal{cls: k, exact: true}, k != ""
	}
	if b.Symbol == "" {
		return jsVal{}, false
	}
	isInst := func(p, n string) bool { return t.mods.instances[p][n] != "" }
	vp, vn, ok := t.mods.resolveExport(mp, b.Symbol, isInst)
	if !ok {
		return jsVal{}, false
	}
	k := t.classIn(t.moduleCtx(vp), t.mods.instances[vp][vn])
	return jsVal{cls: k, exact: true}, k != ""
}

// field is what field name of an instance of cls holds: the first class of
// its lineage that declares it says; a library base ahead of it hides it.
func (t *jsTyper) field(cls, name string, depth int) (jsVal, bool) {
	order, ok := t.classes.mro(cls)
	if !ok {
		return jsVal{}, false
	}
	for _, k := range order {
		if isExtBase(k) {
			return jsVal{}, false
		}
		if enc, ok := t.fields[k][name]; ok {
			ctx := t.moduleCtx(t.classes.pkg[k])
			ctx.recv = t.classes.name[k]
			return t.value(ctx, enc, depth+1)
		}
	}
	return jsVal{}, false
}

// typedSites are the call sites of a JS/TS definition whose receiver's
// class is known, and how many of its calls of each name they cover.
func (t *jsTyper) typedSites(ctx jsCtx, qcs []QualifiedCall) ([]CallSite, map[string]int) {
	var out []CallSite
	n := map[string]int{}
	for _, qc := range qcs {
		if qc.Recv == "" || qc.Recv == "this" {
			continue // this.f(): the caller's own class (CallSelf)
		}
		v, ok := t.eval(ctx, qc.Recv, 0)
		if !ok {
			continue
		}
		n[qc.Name]++
		switch {
		case v.coll:
			// an array's, set's or record's own member, whatever the repo
			// names alike
			out = append(out, CallSite{Name: qc.Name, Kind: CallExternal, Scope: "Array"})
		case v.exact:
			out = append(out, CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassNew + v.cls})
			out = append(out, declSite(v.decl, v.cls, qc.Name)...)
		default:
			out = append(out, CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassScope + v.cls})
		}
	}
	return out, n
}
