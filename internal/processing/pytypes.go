package processing

import (
	"path"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// Python receiver typing: x.f() resolved on the class x holds when the code
// says which — an annotation (x: Order, def f(o: Order), -> Order), a
// constructor (x = Order(...)), a function whose return is annotated or is
// a constructor (x = make_order()), or a field the class sets on self
// (self.repo = Repo(...); self.repo.save()). The class is then found through
// the file's imports and the call resolves in its method resolution order.

// Encoded var types: "t:<dotted class>", "r:<dotted function>" (the value
// that function returns) or "x:<name>" (a test's parameter: the pytest
// fixture of that name, what it returns).
const (
	pyTypeTag    = "t:"
	pyReturnTag  = "r:"
	pyFixtureTag = "x:"
	pyMethodTag  = "m:" // m:<dotted class>#<method>: what Cls(...).method(...) returns
	pyElemTag    = "e:" // e:<dotted class>: a list (set, sequence, iterable) of that class
	pyIterTag    = "i:" // i:<value>: an element of that value (a for/comprehension variable)
	pyNewTag     = "n:" // n:<dotted class>: an instance this function constructs (exactly that class)
	// pyFactoryTag is a fixture's Returns when it hands the test a function it
	// defines itself (return _factory): calling the parameter runs the
	// fixture's own code
	pyFactoryTag = "k:"
)

// pyContainers are the generic types whose first argument is the type of
// what iterating them yields.
var pyContainers = map[string]bool{
	"list": true, "List": true, "set": true, "Set": true, "frozenset": true, "FrozenSet": true,
	"Sequence": true, "MutableSequence": true, "Iterable": true, "Iterator": true, "Collection": true,
	"tuple": true, "Tuple": true, "Generator": true, "AbstractSet": true,
}

// pyElemRef is the dotted class a container annotation holds: list[Order],
// List["Order"], Optional[Sequence[Order]], tuple[Order, ...]; "" otherwise.
func pyElemRef(t content.AST) string {
	if t == nil {
		return ""
	}
	switch t.Type() {
	case "type":
		for _, c := range t.Children() {
			if c.IsNamed() {
				return pyElemRef(c)
			}
		}
	case "subscript", "generic_type":
		var head content.AST
		var args []content.AST
		for _, c := range t.Children() {
			switch {
			case !c.IsNamed():
			case head == nil:
				head = c
			case c.Type() == "type_parameter":
				for _, a := range c.Children() {
					if a.IsNamed() {
						args = append(args, a)
					}
				}
			default:
				args = append(args, c)
			}
		}
		h := dottedRef(head)
		if len(h) == 0 || len(args) == 0 {
			return ""
		}
		switch last := h[len(h)-1]; {
		case last == "Optional" && len(args) == 1:
			return pyElemRef(args[0])
		case pyContainers[last]:
			if last == "tuple" || last == "Tuple" {
				// tuple[X, ...] is a sequence of X; tuple[X, Y] is not
				if len(args) != 2 || strings.TrimSpace(args[1].Text()) != "..." && args[1].Type() != "ellipsis" {
					return ""
				}
			}
			return pyTypeRef(args[0])
		}
	case "binary_operator": // list[X] | None
		var named []content.AST
		for _, c := range t.Children() {
			if c.IsNamed() {
				named = append(named, c)
			}
		}
		if len(named) == 2 && named[1].Type() == "none" {
			return pyElemRef(named[0])
		}
	}
	return ""
}

// pyLoopParts is a for statement's or comprehension clause's variable and
// what it iterates.
func pyLoopParts(n content.AST) (left, right content.AST) {
	seenIn := false
	for _, c := range n.Children() {
		switch {
		case c.Type() == "in":
			seenIn = true
		case !c.IsNamed():
		case !seenIn && left == nil:
			left = c
		case seenIn && right == nil:
			right = c
		}
	}
	return left, right
}

// pyTypeRef is the dotted class a type annotation names: Order, models.Order,
// Optional[Order] / Order | None (the Order), "Order" (a forward reference).
// "" for builtins' generics (list[Order]) and anything else.
func pyTypeRef(t content.AST) string {
	if t == nil {
		return ""
	}
	switch t.Type() {
	case "type":
		for _, c := range t.Children() {
			if c.IsNamed() {
				return pyTypeRef(c)
			}
		}
	case "identifier", "attribute":
		if parts := dottedRef(t); len(parts) > 0 {
			return strings.Join(parts, ".")
		}
	case "string":
		s := strings.Trim(t.Text(), `"'`)
		if s == "" {
			for _, c := range t.Children() {
				if c.Type() == "string_content" {
					s = c.Text()
				}
			}
		}
		if isDottedName(s) {
			return s
		}
	case "subscript", "generic_type":
		// Optional[X] is X; other generics are containers of X, not X
		var head content.AST
		var args []content.AST
		for _, c := range t.Children() {
			switch {
			case !c.IsNamed():
			case head == nil:
				head = c
			case c.Type() == "type_parameter":
				for _, a := range c.Children() {
					if a.IsNamed() {
						args = append(args, a)
					}
				}
			default:
				args = append(args, c)
			}
		}
		if head != nil && len(args) == 1 {
			if h := dottedRef(head); len(h) > 0 && h[len(h)-1] == "Optional" {
				return pyTypeRef(args[0])
			}
		}
	case "binary_operator": // X | None
		var named []content.AST
		for _, c := range t.Children() {
			if c.IsNamed() {
				named = append(named, c)
			}
		}
		if len(named) == 2 && named[1].Type() == "none" {
			return pyTypeRef(named[0])
		}
		if len(named) == 2 && named[0].Type() == "none" {
			return pyTypeRef(named[1])
		}
	case "none":
	}
	return ""
}

func isDottedName(s string) bool {
	if s == "" {
		return false
	}
	for _, part := range strings.Split(s, ".") {
		if part == "" || !(part[0] == '_' || part[0] >= 'A' && part[0] <= 'Z' || part[0] >= 'a' && part[0] <= 'z') {
			return false
		}
		for i := 1; i < len(part); i++ {
			c := part[i]
			if !(c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
				return false
			}
		}
	}
	return true
}

// pyValueType encodes what an assigned value is: a constructor call of a
// capitalized name (t:), another call (r:, its return), "" otherwise.
func pyValueType(v content.AST) string {
	if v == nil || v.Type() != "call" {
		return ""
	}
	var fn content.AST
	for _, c := range v.Children() {
		if c.IsNamed() {
			fn = c
			break
		}
	}
	if fn == nil || fn.Type() != "identifier" && fn.Type() != "attribute" {
		return ""
	}
	if fn.Type() == "attribute" {
		// Cls(...).method(...): the method's return, on an instance of Cls
		var obj, attr content.AST
		for _, c := range fn.Children() {
			switch {
			case !c.IsNamed():
			case obj == nil:
				obj = c
			default:
				attr = c
			}
		}
		if obj != nil && obj.Type() == "call" && attr != nil {
			if t := pyValueType(obj); strings.HasPrefix(t, pyTypeTag) {
				return pyMethodTag + t[len(pyTypeTag):] + "#" + attr.Text()
			}
			return ""
		}
	}
	parts := dottedRef(fn)
	if len(parts) == 0 {
		return ""
	}
	name := strings.Join(parts, ".")
	if last := parts[len(parts)-1]; isTypeName(last) {
		return pyTypeTag + name
	}
	return pyReturnTag + name
}

// pyVarTypes are a function's parameters and local names with their types:
// annotations first, else what they are assigned. A test's plain parameters
// (fixtures) are the pytest fixtures of their names.
func pyVarTypes(fn content.AST, test bool) map[string]string {
	vars := map[string]string{}
	if ps := firstChildAny(fn, []string{"parameters"}); ps != nil {
		for _, p := range ps.Children() {
			if test && p.Type() == "identifier" && p.Text() != "self" {
				vars[p.Text()] = pyFixtureTag + p.Text()
				continue
			}
			if p.Type() != "typed_parameter" && p.Type() != "typed_default_parameter" {
				continue
			}
			var name string
			var typ content.AST
			for _, c := range p.Children() {
				switch {
				case c.Type() == "identifier" && name == "":
					name = c.Text()
				case c.Type() == "type":
					typ = c
				}
			}
			if t := pyTypeRef(typ); name != "" && t != "" {
				vars[name] = pyTypeTag + t
			} else if e := pyElemRef(typ); name != "" && e != "" {
				vars[name] = pyElemTag + e
			}
		}
	}
	body := firstChildAny(fn, []string{"block"})
	if body == nil {
		return vars
	}
	for _, a := range descendantsAny(body, []string{"assignment"}) {
		left, typ, right := pyAssignParts(a)
		if left == nil || left.Type() != "identifier" {
			continue
		}
		name := left.Text()
		if t := pyTypeRef(typ); t != "" {
			vars[name] = pyTypeTag + t
		} else if e := pyElemRef(typ); e != "" {
			vars[name] = pyElemTag + e
		} else if v := pyValueType(right); v != "" {
			if _, annotated := vars[name]; !annotated || !strings.HasPrefix(vars[name], pyTypeTag) {
				if strings.HasPrefix(v, pyTypeTag) {
					v = pyNewTag + v[len(pyTypeTag):] // x = Cls(...): exactly Cls
				}
				vars[name] = v
			}
		}
	}
	// for x in xs / [f(x) for x in xs]: x is an element of what xs holds
	for _, l := range descendantsAny(body, []string{"for_statement", "for_in_clause"}) {
		left, right := pyLoopParts(l)
		if left == nil || left.Type() != "identifier" || right == nil {
			continue
		}
		if _, typed := vars[left.Text()]; typed {
			continue
		}
		switch {
		case right.Type() == "identifier":
			vars[left.Text()] = pyIterTag + "v:" + right.Text()
		case right.Type() == "call":
			if v := pyValueType(right); strings.HasPrefix(v, pyReturnTag) {
				vars[left.Text()] = pyIterTag + v
			}
		}
	}
	return vars
}

// pyAssignParts splits `left [: type] = right`.
func pyAssignParts(a content.AST) (left, typ, right content.AST) {
	seenEq := false
	for _, c := range a.Children() {
		switch {
		case c.Type() == "=":
			seenEq = true
		case c.Type() == "type":
			typ = c
		case !c.IsNamed():
		case !seenEq && left == nil:
			left = c
		case seenEq && right == nil:
			right = c
		}
	}
	return left, typ, right
}

// pyReturnType is the class a function returns: its return annotation, else
// the class every `return Cls(...)` of its body constructs.
func pyReturnType(fn content.AST, fixture bool) string {
	if fixture && pyFixtureFactory(fn) {
		return pyFactoryTag // -> Callable[...]: the function it defines
	}
	afterArrow := false
	for _, c := range fn.Children() {
		if c.Type() == "->" {
			afterArrow = true
			continue
		}
		if afterArrow && c.Type() == "type" {
			if t := pyTypeRef(c); t != "" {
				return t
			}
			if e := pyElemRef(c); e != "" {
				return pyElemTag + e // -> list[Order]
			}
			return ""
		}
	}
	body := firstChildAny(fn, []string{"block"})
	if body == nil {
		return ""
	}
	// a pytest fixture hands the test what it yields, too
	kinds := []string{"return_statement"}
	if fixture {
		kinds = append(kinds, "yield")
	}
	cls := ""
	var locals map[string]string
	var inner map[string]bool // functions and lambdas the fixture defines
	if fixture {
		inner = map[string]bool{}
		for _, d := range descendantsAny(body, []string{"function_definition"}) {
			if id := firstChildAny(d, []string{"identifier"}); id != nil {
				inner[id.Text()] = true
			}
		}
		for _, a := range descendantsAny(body, []string{"assignment"}) {
			if k := namedKids(a); len(k) == 2 && k[0].Type() == "identifier" && k[1].Type() == "lambda" {
				inner[k[0].Text()] = true
			}
		}
	}
	factory := false
	for _, r := range ownDescendants(body, kinds) {
		var v content.AST
		for _, c := range r.Children() {
			if c.IsNamed() {
				v = c
			}
		}
		if v == nil && r.Type() == "yield" {
			continue // a bare yield: the fixture only sets up and tears down
		}
		if fixture && v != nil && (v.Type() == "lambda" || v.Type() == "identifier" && inner[v.Text()]) {
			factory = true
			continue
		}
		t := pyValueType(v)
		if v != nil && v.Type() == "identifier" {
			// app = Flask(...); ...; return app
			if locals == nil {
				locals = pyVarTypes(fn, fixture)
			}
			t = locals[v.Text()]
			if strings.HasPrefix(t, pyNewTag) {
				t = pyTypeTag + t[len(pyNewTag):]
			}
		}
		if strings.HasPrefix(t, pyFixtureTag) && fixture && (cls == "" || cls == t) {
			cls = t // a fixture handing on another fixture it receives
			continue
		}
		if !strings.HasPrefix(t, pyTypeTag) || cls != "" && cls != t[len(pyTypeTag):] {
			return "" // a return of something else, or of two classes
		}
		cls = t[len(pyTypeTag):]
	}
	if factory {
		if cls != "" {
			return ""
		}
		return pyFactoryTag
	}
	return cls
}

// pyClassFields are the attributes a class's methods set on self (self.x =
// Repo(...), self.x: Repo = ...), and its class-level annotations (x: Repo).
func pyClassFields(class content.AST) map[string]string {
	fields := map[string]string{}
	body := firstChildAny(class, []string{"block"})
	if body == nil {
		return fields
	}
	for _, st := range body.Children() {
		if st.Type() != "expression_statement" {
			continue
		}
		for _, a := range st.Children() {
			if a.Type() != "assignment" {
				continue
			}
			left, typ, right := pyAssignParts(a)
			if left == nil || left.Type() != "identifier" {
				continue
			}
			if t := pyTypeRef(typ); t != "" {
				fields[left.Text()] = pyTypeTag + t
			} else if v := pyValueType(right); strings.HasPrefix(v, pyTypeTag) {
				fields[left.Text()] = v
			}
		}
	}
	for _, a := range descendantsAny(body, []string{"assignment"}) {
		left, typ, right := pyAssignParts(a)
		if left == nil || left.Type() != "attribute" {
			continue
		}
		parts := dottedRef(left)
		if len(parts) != 2 || parts[0] != "self" {
			continue
		}
		v := ""
		if t := pyTypeRef(typ); t != "" {
			v = pyTypeTag + t
		} else {
			v = pyValueType(right)
		}
		if v == "" {
			continue
		}
		if old, ok := fields[parts[1]]; ok && old != v {
			fields[parts[1]] = "" // set to two different things: unknown
			continue
		}
		fields[parts[1]] = v
	}
	for k, v := range fields {
		if v == "" {
			delete(fields, k)
		}
	}
	return fields
}

// pyClassScope prefixes a CallTyped scope that is a class key of the
// classGraph (Python), not a bare class name.
const pyClassScope = "\x00pycls:"

// pyClassExact prefixes a CallTyped scope for a call on the class itself
// (Cls.f()): the method its MRO reaches, without subclass overrides.
const pyClassExact = "\x00pyclsx:"

// pyClassNew prefixes a CallTyped scope for a call on an instance of exactly
// that class (x = Cls(...); x.f()): the method its MRO reaches, typed.
const pyClassNew = "\x00pyclsn:"

// pyCtx is what typing needs from one definition: where it is, its typed
// names, and how its file's imports read a dotted name.
type pyCtx struct {
	pkg, recv string
	path      string
	vars      map[string]string
	returns   string
	sites     func([]QualifiedCall) []CallSite
}

// pyTyper resolves Python type names to classes of the repo, at index time.
type pyTyper struct {
	classes     *classGraph
	allDefs     map[string]bool
	isMethod    map[string]bool
	ctx         func(qid string) (pyCtx, bool)
	classByName map[string][]string // class name -> class keys
	funcByName  map[string][]string // module-level function name -> qids
	reexport    func(pkg, name string) (string, string, bool)
	fields      map[string]map[string]string
	returns     map[string]string
	// imported is the qid a module (by file path) binds name to with a
	// from-import of a repo function ("" when none): a conftest gathering
	// fixtures from other modules
	imported func(path, name string) string
}

// dotted turns "a.b.C" into the call shape import resolution reads.
func dottedCall(d string) QualifiedCall {
	parts := strings.Split(d, ".")
	qc := QualifiedCall{Name: parts[len(parts)-1], Chain: len(parts)}
	if len(parts) > 1 {
		qc.Qualifier = parts[0]
	}
	return qc
}

// class is the class key a dotted name written in ctx names; "" when it is
// not the repo's (or two classes share the name and the imports do not
// say which).
func (p *pyTyper) class(c pyCtx, dotted string) string {
	for _, s := range c.sites([]QualifiedCall{dottedCall(dotted)}) {
		switch s.Kind {
		case CallModule:
			if k, ok := p.classes.has(s.Scope, s.Name); ok {
				return k
			}
			if p.reexport != nil {
				if rp, rn, ok := p.reexport(s.Scope, s.Name); ok {
					if k, ok := p.classes.has(rp, rn); ok {
						return k
					}
				}
			}
		case CallPlain:
			if k, ok := p.classes.has(c.pkg, s.Name); ok {
				return k
			}
		case CallExternal:
			return ""
		}
		// re-exported through a package __init__, or imported in a way not
		// followed: the one class of that name, if only one
		if ks := p.classByName[s.Name]; len(ks) == 1 {
			return ks[0]
		}
	}
	return ""
}

// pyBuiltinTypes are the builtin classes an annotation or constructor names
// bare: their methods are the language's, never a repo method of that name.
var pyBuiltinTypes = map[string]bool{
	"str": true, "bytes": true, "bytearray": true, "int": true, "float": true, "complex": true, "bool": true,
	"dict": true, "list": true, "set": true, "frozenset": true, "tuple": true,
}

// pyTypingModules name types that say nothing about the object (Any, a
// Callable, a Protocol or ABC a repo class may implement).
var pyTypingModules = map[string]bool{"typing": true, "typing_extensions": true, "collections.abc": true, "abc": true}

// externalClass is the library class a dotted name written in ctx names: one
// imported from outside the repo, or a builtin; "" when it is the repo's or
// not known.
func (p *pyTyper) externalClass(c pyCtx, dotted string) string {
	if p.class(c, dotted) != "" {
		return ""
	}
	for _, s := range c.sites([]QualifiedCall{dottedCall(dotted)}) {
		switch {
		case s.Kind == CallExternal && !pyTypingModules[s.Scope]:
			return dotted
		case s.Kind == CallPlain && pyBuiltinTypes[dotted] && len(p.classByName[dotted]) == 0:
			return dotted
		}
	}
	return ""
}

// externalValue is the library class an encoded var type is, in ctx: a
// library class annotated or constructed, or what a repo function annotated
// to return one returns.
func (p *pyTyper) externalValue(c pyCtx, v string) string {
	switch {
	case strings.HasPrefix(v, pyTypeTag):
		return p.externalClass(c, v[len(pyTypeTag):])
	case strings.HasPrefix(v, pyNewTag):
		return p.externalClass(c, v[len(pyNewTag):])
	case strings.HasPrefix(v, pyReturnTag), strings.HasPrefix(v, pyFixtureTag):
		var q string
		if strings.HasPrefix(v, pyFixtureTag) {
			q = p.fixture(c, v[len(pyFixtureTag):])
		} else {
			q = p.function(c, v[len(pyReturnTag):])
		}
		if q == "" {
			return ""
		}
		if fc, ok := p.ctx(q); ok && fc.returns != "" && !strings.HasPrefix(fc.returns, pyElemTag) {
			return p.externalClass(fc, fc.returns)
		}
	}
	return ""
}

// function is the qid of the module-level function a dotted name written
// in ctx calls.
func (p *pyTyper) function(c pyCtx, dotted string) string {
	for _, s := range c.sites([]QualifiedCall{dottedCall(dotted)}) {
		switch s.Kind {
		case CallModule:
			if q := qualifyModule(s.Scope, s.Name); p.allDefs[q] {
				return q
			}
		case CallPlain:
			if q := qualifyModule(c.pkg, s.Name); p.allDefs[q] {
				return q
			}
		case CallExternal:
			return ""
		}
		if qs := p.funcByName[s.Name]; len(qs) == 1 {
			return qs[0]
		}
	}
	return ""
}

// returnClass is the class key a function's return value is.
func (p *pyTyper) returnClass(q string) string {
	if k, ok := p.returns[q]; ok {
		return k
	}
	p.returns[q] = "" // a cycle reads as unknown
	k := ""
	if c, ok := p.ctx(q); ok && strings.HasPrefix(c.returns, pyFixtureTag) {
		k = p.valueClass(c, c.returns)
	} else if ok && c.returns != "" && !strings.HasPrefix(c.returns, pyElemTag) {
		if c.returns == "Self" && c.recv != "" {
			// -> Self (typing.Self): the class the method is called on; its own
			// class stands for it
			k, _ = p.classes.has(c.pkg, c.recv)
		} else {
			k = p.class(c, c.returns)
		}
	}
	p.returns[q] = k
	return k
}

// valueClass is the class key an encoded var type is, in ctx.
func (p *pyTyper) valueClass(c pyCtx, v string) string {
	switch {
	case strings.HasPrefix(v, pyTypeTag):
		return p.class(c, v[len(pyTypeTag):])
	case strings.HasPrefix(v, pyNewTag):
		return p.class(c, v[len(pyNewTag):])
	case strings.HasPrefix(v, pyReturnTag):
		if q := p.function(c, v[len(pyReturnTag):]); q != "" {
			return p.returnClass(q)
		}
	case strings.HasPrefix(v, pyFixtureTag):
		if q := p.fixture(c, v[len(pyFixtureTag):]); q != "" {
			return p.returnClass(q)
		}
	case strings.HasPrefix(v, pyIterTag):
		return p.elemClass(c, v[len(pyIterTag):])
	case strings.HasPrefix(v, pyMethodTag):
		cls, m, _ := strings.Cut(v[len(pyMethodTag):], "#")
		if k := p.class(c, cls); k != "" {
			if q, ok := p.classes.method(k, m, false, p.allDefs); ok {
				return p.returnClass(q)
			}
		}
	}
	return ""
}

// elemClass is the class key of what iterating an encoded value yields: a
// variable annotated list[X], or what a function or fixture annotated to
// return one returns.
func (p *pyTyper) elemClass(c pyCtx, v string) string {
	if name, ok := strings.CutPrefix(v, "v:"); ok {
		if v = c.vars[name]; strings.HasPrefix(v, pyIterTag) {
			return "" // an element of an element: not followed
		}
	}
	var q string
	switch {
	case strings.HasPrefix(v, pyElemTag):
		return p.class(c, v[len(pyElemTag):])
	case strings.HasPrefix(v, pyReturnTag):
		q = p.function(c, v[len(pyReturnTag):])
	case strings.HasPrefix(v, pyFixtureTag):
		q = p.fixture(c, v[len(pyFixtureTag):])
	}
	if q == "" {
		return ""
	}
	if fc, ok := p.ctx(q); ok && strings.HasPrefix(fc.returns, pyElemTag) {
		return p.class(fc, fc.returns[len(pyElemTag):])
	}
	return ""
}

// fixtureOf is the fixture the parameter name of function q receives.
func (p *pyTyper) fixtureOf(q, name string) string {
	c, ok := p.ctx(q)
	if !ok {
		return ""
	}
	return p.fixture(c, name)
}

// fixture is the qid of the pytest fixture a test's parameter name receives:
// a function of that name in the test's own module, else in the nearest
// conftest.py above it.
func (p *pyTyper) fixture(c pyCtx, name string) string {
	if q := qualifyModule(c.pkg, name); p.allDefs[q] && !p.isMethod[q] {
		return q
	}
	if c.path == "" {
		return ""
	}
	for dir := path.Dir(c.path); ; dir = path.Dir(dir) {
		cf := path.Join(dir, "conftest.py")
		if q := qualifyModule(pkgOfFile(cf, "python"), name); p.allDefs[q] && !p.isMethod[q] {
			return q
		}
		if p.imported != nil {
			if q := p.imported(cf, name); q != "" {
				return q
			}
		}
		if dir == "." || dir == "/" || dir == "" {
			return ""
		}
	}
}

// fieldClass is the class key of attribute attr on an instance of cls: set
// on self by cls or a base (MRO order).
func (p *pyTyper) fieldClass(cls, attr string) string {
	k, _ := p.fieldType(cls, attr)
	return k
}

// pyExtField marks a field holding a library class's instance in
// pyTyper.fields.
const pyExtField = "\x00ext:"

// fieldType is what attribute attr on an instance of cls holds: a repo class
// key, or the library class it is constructed or annotated as.
func (p *pyTyper) fieldType(cls, attr string) (key, ext string) {
	order, ok := p.classes.mro(cls)
	if !ok {
		return "", ""
	}
	for _, k := range order {
		if isExtBase(k) {
			return "", ""
		}
		fs, done := p.fields[k]
		if !done {
			fs = map[string]string{}
			cq := qualifyModule(p.classes.pkg[k], p.classes.name[k])
			if c, ok := p.ctx(cq); ok {
				for f, v := range c.vars {
					if fk := p.valueClass(c, v); fk != "" {
						fs[f] = fk
					} else if e := p.externalValue(c, v); e != "" {
						fs[f] = pyExtField + e
					}
				}
			}
			p.fields[k] = fs
		}
		if fk, ok := fs[attr]; ok {
			if strings.HasPrefix(fk, pyExtField) {
				return "", fk[len(pyExtField):]
			}
			return fk, ""
		}
	}
	return "", ""
}

// typedCalls splits a Python definition's calls into those whose receiver
// has a known class (returned as typed sites) and the rest.
func (p *pyTyper) typedCalls(qid string, qcs []QualifiedCall) ([]QualifiedCall, []CallSite) {
	c, ok := p.ctx(qid)
	if !ok {
		return qcs, nil
	}
	var rest []QualifiedCall
	var typed []CallSite
	for _, qc := range qcs {
		cls, chain, exact := "", qc.Chain, false
		switch {
		case qc.Chain >= 2 && qc.Qualifier != "self" && qc.Qualifier != "cls":
			v, ok := c.vars[qc.Qualifier]
			if qc.Result && qc.Chain == 4 && qc.Attr != "" {
				// User.lookup(x).validate(): the method lookup returns, on the
				// class (Cls.make()) or on a typed value (repo.find())
				owner := ""
				if ok {
					owner = p.valueClass(c, v)
				} else if isTypeName(qc.Qualifier) {
					owner = p.class(c, qc.Qualifier)
				}
				if owner != "" {
					if q, found := p.classes.method(owner, qc.Attr, false, p.allDefs); found {
						cls = p.returnClass(q)
					}
				}
				break
			}
			if !ok && qc.Chain == 2 && isTypeName(qc.Qualifier) {
				if k := p.class(c, qc.Qualifier); k != "" {
					// Cls.f(): the method Cls's MRO reaches — a classmethod or
					// staticmethod inherited from a base — never a subclass's
					typed = append(typed, CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassExact + k})
					continue
				}
			}
			if !ok && qc.Chain == 3 && qc.Attr == "" {
				// make_repo().save(): what the function returns; User(...).save():
				// the instance the class constructs
				v, ok, chain = pyReturnTag+qc.Qualifier, true, 2
				if isTypeName(qc.Qualifier) {
					v = pyNewTag + qc.Qualifier
				}
			}
			if !ok {
				break
			}
			if ext := p.externalValue(c, v); ext != "" {
				// a library object's method (or its attribute's: spinner.green.ok()),
				// whatever the repo names alike
				typed = append(typed, CallSite{Name: qc.Name, Kind: CallExternal, Scope: ext})
				continue
			}
			exact = strings.HasPrefix(v, pyNewTag)
			switch cls = p.valueClass(c, v); {
			case cls == "" || chain == 2:
			case chain == 3 && qc.Attr != "":
				cls = p.fieldClass(cls, qc.Attr) // x.repo.save(): the field's class
			default:
				cls = ""
			}
		case qc.Chain == 3 && qc.Qualifier == "self" && qc.Attr != "" && c.recv != "":
			if own, ok := p.classes.has(c.pkg, c.recv); ok {
				var ext string
				if cls, ext = p.fieldType(own, qc.Attr); ext != "" {
					// self._event = threading.Event(); self._event.set()
					typed = append(typed, CallSite{Name: qc.Name, Kind: CallExternal, Scope: ext})
					continue
				}
			}
		}
		if cls == "" {
			rest = append(rest, qc)
			continue
		}
		if exact && chain == 2 {
			// an instance the function constructs is that class, never a
			// subclass: no override can run in its place
			typed = append(typed, CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassNew + cls})
			continue
		}
		typed = append(typed, CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassScope + cls})
	}
	return rest, typed
}

// ownDescendants are the nodes of the given types in n that belong to n's
// own function: not inside a function or lambda nested in it (a factory's
// return is not the fixture's).
func ownDescendants(n content.AST, types []string) []content.AST {
	var out []content.AST
	var walk func(n content.AST)
	walk = func(n content.AST) {
		for _, c := range n.Children() {
			if c.Type() == "function_definition" || c.Type() == "lambda" {
				continue
			}
			if containsStr(types, c.Type()) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

// pyFixtureFactory: every value a fixture returns or yields is a function it
// defines itself (def _make(...): ...; return _make) or a lambda.
func pyFixtureFactory(fn content.AST) bool {
	body := firstChildAny(fn, []string{"block"})
	if body == nil {
		return false
	}
	inner := map[string]bool{}
	for _, d := range descendantsAny(body, []string{"function_definition"}) {
		if id := firstChildAny(d, []string{"identifier"}); id != nil {
			inner[id.Text()] = true
		}
	}
	for _, a := range descendantsAny(body, []string{"assignment"}) {
		if k := namedKids(a); len(k) == 2 && k[0].Type() == "identifier" && k[1].Type() == "lambda" {
			inner[k[0].Text()] = true
		}
	}
	seen := false
	for _, r := range ownDescendants(body, []string{"return_statement", "yield"}) {
		var v content.AST
		for _, c := range r.Children() {
			if c.IsNamed() {
				v = c
			}
		}
		if v == nil {
			continue
		}
		if !(v.Type() == "lambda" || v.Type() == "identifier" && inner[v.Text()]) {
			return false
		}
		seen = true
	}
	return seen
}
