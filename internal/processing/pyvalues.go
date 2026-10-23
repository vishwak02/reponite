package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// Python values whose class the code does not write next to them: what a
// call of an annotated callable returns (a factory parameter typed
// Callable[[], Order], a type alias of one, a Protocol's __call__), what a
// method or classmethod returns, a module-level instance another module
// imports, an attribute path through fields and modules. pyTyper.eval reads
// the encoded var types of pytypes.go as small expressions, in the context
// (function, class or module) that wrote them.

// More encoded var types (see pytypes.go for the others).
const (
	pyCallTag  = "c:" // c:<value>: a callable; calling it gives <value>
	pyClassObj = "o:" // o:<dotted class>: the class object itself (Model = Order)
	pyPathTag  = "f:" // f:<dotted path>: the value a name or attribute path holds (x = y, x = a.b)
	pySelfRet  = "s:" // a method's return: an instance of the class it is called on (return cls(...), return self)
	pyLocalTag = "l:" // a local name of unknown type: it shadows a global of that name
	// pyNestedTag marks a name only a function nested in this one binds (its
	// parameter, say): calls on it are not typed, a bare call of it in the
	// function itself is the module's
	pyNestedTag = "h:"
)

// pyMaxDepth bounds how far one value is followed (aliases, returns of
// returns, fields of fields).
const pyMaxDepth = 12

// PyModuleFacts are a Python module's top-level names: the values it assigns
// (as encoded var types; pyLocalTag when unknown, set twice or rebound by a
// function's `global`) and the type aliases it declares (encoded as what a
// value annotated with the alias is).
type PyModuleFacts struct {
	Globals map[string]string
	Aliases map[string]string
	// Plugins are the modules a conftest names in pytest_plugins: their
	// fixtures are visible to the tests below it
	Plugins []string
}

// pyModuleFacts reads a Python module's top-level assignments and aliases;
// nil for other languages.
func pyModuleFacts(root content.AST, r LangRules) *PyModuleFacts {
	if r.Name != "python" || root == nil {
		return nil
	}
	mf := &PyModuleFacts{Globals: map[string]string{}, Aliases: map[string]string{}}
	set := func(name, v string) {
		if v == "" {
			v = pyLocalTag
		}
		if old, ok := mf.Globals[name]; ok && old != v {
			v = pyLocalTag // two different values: unknown
		}
		mf.Globals[name] = v
	}
	unknown := func(target content.AST) {
		for _, id := range pyTargetNames(target) {
			mf.Globals[id] = pyLocalTag
		}
	}
	var stmts func(n content.AST)
	stmts = func(n content.AST) {
		for _, st := range n.Children() {
			switch st.Type() {
			case "expression_statement":
				for _, a := range st.Children() {
					switch a.Type() {
					case "assignment":
						pyModuleAssign(mf, a, set, unknown)
					case "augmented_assignment":
						if k := namedKids(a); len(k) > 0 {
							unknown(k[0])
						}
					}
				}
			case "type_alias_statement":
				var types []content.AST
				for _, c := range st.Children() {
					if c.Type() == "type" && c.IsNamed() { // not the `type` keyword
						types = append(types, c)
					}
				}
				if len(types) == 2 {
					if parts := dottedRef(firstNamed(types[0])); len(parts) == 1 {
						if enc := pyTypeEnc(types[1]); enc != "" {
							mf.Aliases[parts[0]] = enc
						}
					}
				}
			case "if_statement", "elif_clause", "else_clause", "try_statement", "except_clause", "finally_clause", "block", "with_statement":
				if st.Type() == "with_statement" {
					for _, t := range descendantsAny(st, []string{"as_pattern_target"}) {
						unknown(t)
					}
				}
				stmts(st)
			case "for_statement":
				if left, _ := pyLoopParts(st); left != nil {
					unknown(left)
				}
				stmts(st)
			}
		}
	}
	stmts(root)
	// a function's `global x` rebinds x at run time: its value is unknown
	for _, g := range descendantsAny(root, []string{"global_statement"}) {
		for _, id := range namedKids(g) {
			if id.Type() == "identifier" {
				mf.Globals[id.Text()] = pyLocalTag
			}
		}
	}
	return mf
}

// pyModuleAssign records one top-level assignment.
func pyModuleAssign(mf *PyModuleFacts, a content.AST, set func(name, v string), unknown func(content.AST)) {
	left, typ, right := pyAssignParts(a)
	if left == nil {
		return
	}
	if left.Type() != "identifier" {
		unknown(left)
		return
	}
	name := left.Text()
	if name == "pytest_plugins" && right != nil {
		mf.Plugins = append(mf.Plugins, pyStrings(right)...)
	}
	if typ != nil {
		if parts := dottedRef(firstNamed(typ)); len(parts) > 0 && parts[len(parts)-1] == "TypeAlias" {
			if enc := pyTypeEnc(right); enc != "" {
				mf.Aliases[name] = enc
			}
			return
		}
		set(name, pyTypeEnc(typ))
		return
	}
	if right == nil || right.Type() == "none" {
		return // x = None: no class's method runs on it
	}
	switch right.Type() {
	case "subscript", "generic_type", "binary_operator":
		// X = Callable[..., Order], X = Optional[Order], X = Order | None
		if enc := pyTypeEnc(right); enc != "" {
			mf.Aliases[name] = enc
		}
		set(name, "")
		return
	case "identifier", "attribute":
		if parts := dottedRef(right); len(parts) > 0 && isTypeName(parts[len(parts)-1]) {
			mf.Aliases[name] = pyTypeTag + strings.Join(parts, ".") // Model = Order: an alias of the class
		}
	}
	v := pyExprEnc(right)
	if strings.HasPrefix(v, pyTypeTag) {
		v = pyNewTag + v[len(pyTypeTag):] // x = Cls(...): exactly Cls
	}
	set(name, v)
}

// pyStrings are the string literals of a list, tuple or single string.
func pyStrings(n content.AST) []string {
	switch n.Type() {
	case "string":
		if s := pyStringValue(n); s != "" {
			return []string{s}
		}
	case "list", "tuple", "parenthesized_expression", "expression_list":
		var out []string
		for _, c := range namedKids(n) {
			out = append(out, pyStrings(c)...)
		}
		return out
	}
	return nil
}

// pyStringValue is a plain string literal's text ("" for an f-string or a
// concatenation).
func pyStringValue(n content.AST) string {
	out := ""
	for _, c := range n.Children() {
		switch c.Type() {
		case "string_content":
			out += c.Text()
		case "interpolation":
			return ""
		}
	}
	return out
}

// pyTargetNames are the identifiers an assignment target binds (x, a, b of
// `a, b = ...`, (x, *rest)).
func pyTargetNames(t content.AST) []string {
	if t == nil {
		return nil
	}
	switch t.Type() {
	case "identifier":
		return []string{t.Text()}
	case "pattern_list", "tuple_pattern", "list_pattern", "expression_list", "tuple", "list", "list_splat_pattern",
		"as_pattern_target", "parenthesized_expression":
		var out []string
		for _, c := range namedKids(t) {
			out = append(out, pyTargetNames(c)...)
		}
		return out
	}
	return nil // an attribute or subscript target binds no name
}

func firstNamed(n content.AST) content.AST {
	if n == nil {
		return nil
	}
	if n.Type() != "type" {
		return n
	}
	for _, c := range n.Children() {
		if c.IsNamed() {
			return c
		}
	}
	return nil
}

// pyTypeEnc encodes what a value annotated with t is: an instance of a class
// (t:), a list of one (e:) or a callable (c:<what calling it gives>).
func pyTypeEnc(t content.AST) string {
	if t == nil {
		return ""
	}
	if r := pyTypeRef(t); r != "" {
		return pyTypeTag + r
	}
	if e := pyElemRef(t); e != "" {
		return pyElemTag + e
	}
	return pyCallableRef(t)
}

// pyTypeHead splits a generic type or subscript into its head's dotted name
// and its arguments: Callable[[int], Order] -> [Callable], ([int], Order).
func pyTypeHead(t content.AST) ([]string, []content.AST) {
	t = firstNamed(t)
	if t == nil || t.Type() != "subscript" && t.Type() != "generic_type" {
		return nil, nil
	}
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
	if head == nil {
		return nil, nil
	}
	return dottedRef(head), args
}

// pyCallableRef encodes a Callable[..., R] annotation as c:<R's encoding>;
// an awaitable R (Callable[..., Awaitable[Order]]) gives what awaiting it
// gives. Optional[Callable[...]] is the callable.
func pyCallableRef(t content.AST) string {
	h, args := pyTypeHead(t)
	if len(h) == 0 {
		if b := firstNamed(t); b != nil && b.Type() == "binary_operator" {
			// Callable[..., R] | None
			if k := namedKids(b); len(k) == 2 && k[1].Type() == "none" {
				return pyCallableRef(k[0])
			}
		}
		return ""
	}
	switch h[len(h)-1] {
	case "Optional":
		if len(args) == 1 {
			return pyCallableRef(args[0])
		}
	case "Callable":
		if len(args) == 2 {
			if r := pyTypeEnc(pyAwaited(args[1])); r != "" {
				return pyCallTag + r
			}
		}
	}
	return ""
}

// pyAwaited is the type awaiting a value of type t gives: Awaitable[X] and
// Coroutine[A, B, X] give X; anything else is itself.
func pyAwaited(t content.AST) content.AST {
	h, args := pyTypeHead(t)
	if len(h) == 0 || len(args) == 0 {
		return t
	}
	switch h[len(h)-1] {
	case "Awaitable", "Coroutine":
		return args[len(args)-1]
	}
	return t
}

// pyExprEnc encodes what an expression evaluates to, where it is written:
// a call (pyValueType), an awaited call (the same: what the coroutine
// returns), a name or attribute path (f:).
func pyExprEnc(v content.AST) string {
	if v == nil {
		return ""
	}
	switch v.Type() {
	case "await", "parenthesized_expression":
		if k := namedKids(v); len(k) == 1 {
			return pyExprEnc(k[0])
		}
	case "call":
		return pyValueType(v)
	case "identifier":
		switch v.Text() {
		case "None", "True", "False":
			return ""
		}
		return pyPathTag + v.Text()
	case "attribute":
		if parts := dottedRef(v); len(parts) > 0 {
			return pyPathTag + strings.Join(parts, ".")
		}
	case "conditional_expression":
		// a if c else b: one value when both branches are
		if k := namedKids(v); len(k) == 3 {
			if a, b := pyExprEnc(k[0]), pyExprEnc(k[2]); a == b {
				return a
			}
		}
	}
	return ""
}

// pyModule is a module's facts with what reading them needs.
type pyModule struct {
	path    string
	facts   *PyModuleFacts
	imports map[string]ImportBinding // its module-level imports
}

// pyVal is what a Python expression evaluates to.
type pyVal struct {
	kind  byte   // pyInst, pyClassV, pyModuleV, pyCallableV, pyFuncV; 0: unknown
	cls   string // class key (pyInst, pyClassV)
	exact bool   // pyInst: exactly that class; pyClassV: the class itself, not cls of a subclass
	mod   string // pyModuleV: the module's package id
	name  string // pyModuleV: its dotted name as imported, from file
	from  string
	ret   string // pyCallableV: what calling it gives, in rctx
	rctx  *pyCtx
	fn    string // pyFuncV: the function's qid
}

const (
	pyInst      byte = 'i'
	pyClassV    byte = 'c'
	pyModuleV   byte = 'm'
	pyCallableV byte = 'f'
	pyFuncV     byte = 'q'
)

// eval is the value an encoded var type stands for, in ctx c.
func (p *pyTyper) eval(c pyCtx, v string, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	if len(v) < 2 || v[1] != ':' {
		return pyVal{}
	}
	rest := v[2:]
	switch v[:2] {
	case pyTypeTag:
		return p.typeVal(c, rest, depth+1)
	case pyNewTag:
		if k := p.class(c, rest); k != "" {
			return pyVal{kind: pyInst, cls: k, exact: true}
		}
	case pyReturnTag:
		return p.callVal(c, rest, depth+1)
	case pyFixtureTag:
		if q := p.fixture(c, rest); q != "" {
			return p.returnVal(q, "", false, depth+1)
		}
	case pyIterTag:
		if k := p.elemClass(c, rest); k != "" {
			return pyVal{kind: pyInst, cls: k}
		}
	case pyMethodTag:
		cls, m, _ := strings.Cut(rest, "#")
		if k := p.class(c, cls); k != "" {
			if q, ok := p.classes.method(k, m, false, p.allDefs); ok {
				return p.returnVal(q, k, true, depth+1) // on Cls(...): exactly Cls
			}
		}
	case pyCallTag, pyFactoryTag:
		cc := c
		return pyVal{kind: pyCallableV, ret: rest, rctx: &cc}
	case pyClassObj:
		if k := p.class(c, rest); k != "" {
			return pyVal{kind: pyClassV, cls: k, exact: true}
		}
	case pyPathTag:
		return p.pathVal(c, rest, depth+1)
	}
	return pyVal{}
}

// typeVal is a value annotated with dotted: an instance of that class, or
// what the type alias it names says.
func (p *pyTyper) typeVal(c pyCtx, dotted string, depth int) pyVal {
	if k := p.class(c, dotted); k != "" {
		return pyVal{kind: pyInst, cls: k}
	}
	if enc, ac, ok := p.alias(c, dotted); ok {
		return p.eval(ac, enc, depth+1)
	}
	return pyVal{}
}

// alias finds the type alias a dotted name written in c names: its encoding
// and the module context it is written in.
func (p *pyTyper) alias(c pyCtx, dotted string) (string, pyCtx, bool) {
	has := func(pkg, name string) bool {
		m := p.modules[pkg]
		return m != nil && m.facts != nil && m.facts.Aliases[name] != ""
	}
	for _, s := range c.sites([]QualifiedCall{dottedCall(dotted)}) {
		pkg, name := "", s.Name
		switch s.Kind {
		case CallModule:
			pkg = s.Scope
		case CallPlain:
			pkg = c.pkg
		default:
			continue
		}
		if !has(pkg, name) && p.modExport != nil {
			if rp, rn, ok := p.modExport(pkg, name, has); ok {
				pkg, name = rp, rn
			}
		}
		if has(pkg, name) {
			return p.modules[pkg].facts.Aliases[name], p.modCtx(pkg), true
		}
	}
	return "", pyCtx{}, false
}

// modCtx is the context of a module's own top level.
func (p *pyTyper) modCtx(pkg string) pyCtx {
	m := p.modules[pkg]
	if m == nil {
		return pyCtx{pkg: pkg, module: true, sites: func([]QualifiedCall) []CallSite { return nil }}
	}
	var vars map[string]string
	if m.facts != nil {
		vars = m.facts.Globals
	}
	return pyCtx{pkg: pkg, path: m.path, vars: vars, imports: m.imports, module: true, sites: p.siteFn(m.path, m.imports)}
}

// returnVal is what calling function q gives; recv is the class key it is
// called on (for a return of cls(...) or self), "" for its own class, and
// recvExact says it is called on exactly that class (Cls.make(), not cls of
// a subclass), so an instance of it is exactly that class too.
func (p *pyTyper) returnVal(q, recv string, recvExact bool, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	fc, ok := p.ctx(q)
	if !ok {
		return pyVal{}
	}
	r := fc.returns
	switch {
	case r == "" || strings.HasPrefix(r, pyElemTag):
		return pyVal{}
	case r == "Self" || r == pySelfRet:
		k := recv
		if k == "" && fc.recv != "" {
			k, _ = p.classes.has(fc.pkg, fc.recv)
			recvExact = false
		}
		if k == "" {
			return pyVal{}
		}
		return pyVal{kind: pyInst, cls: k, exact: recvExact}
	}
	if v, ok := p.vals[q]; ok {
		return v
	}
	if p.busy[q] {
		p.cut = true // a cycle reads as unknown, and nothing on it is kept
		return pyVal{}
	}
	p.busy[q] = true
	cut := p.cut
	p.cut = false
	var v pyVal
	if strings.Contains(r, ":") {
		v = p.eval(fc, r, depth+1)
	} else {
		v = p.typeVal(fc, r, depth+1)
	}
	if v.kind == pyInst {
		v.exact = false // a function's return: its class or a subclass
	}
	delete(p.busy, q)
	if !p.cut {
		p.vals[q] = v
	}
	p.cut = p.cut || cut
	return v
}

// callVal is what calling dotted (a name or attribute path written in c)
// gives.
func (p *pyTyper) callVal(c pyCtx, dotted string, depth int) pyVal {
	parts := strings.Split(dotted, ".")
	base, decided := p.headVal(c, parts[0], depth+1)
	if !decided {
		if q := p.function(c, dotted); q != "" {
			return p.returnVal(q, "", false, depth+1)
		}
		return pyVal{}
	}
	if len(parts) == 1 {
		return p.callOn(base, depth+1)
	}
	for _, a := range parts[1 : len(parts)-1] {
		if base = p.attrVal(base, a, depth+1); base.kind == 0 {
			return pyVal{}
		}
	}
	return p.callMember(base, parts[len(parts)-1], depth+1)
}

// pathVal is the value of a name or attribute path written in c.
func (p *pyTyper) pathVal(c pyCtx, dotted string, depth int) pyVal {
	parts := strings.Split(dotted, ".")
	base, _ := p.headVal(c, parts[0], depth+1)
	for _, a := range parts[1:] {
		if base.kind == 0 {
			break
		}
		base = p.attrVal(base, a, depth+1)
	}
	return base
}

// headVal is what a bare name written in c is bound to: a local or
// parameter, self/cls, an import, a module global, a class or a function of
// the module. decided is false when the name is none of those (a builtin,
// or bound in a way not read).
func (p *pyTyper) headVal(c pyCtx, name string, depth int) (pyVal, bool) {
	v, local := c.vars[name]
	if !c.module && c.recv != "" && (name == "self" || name == "cls") && (!local || v == pyParamTag) {
		k, ok := p.classes.has(c.pkg, c.recv)
		if !ok {
			return pyVal{}, true
		}
		if name == "cls" {
			return pyVal{kind: pyClassV, cls: k}, true
		}
		return pyVal{kind: pyInst, cls: k}, true
	}
	if local {
		if v == pyParamTag || v == pyLocalTag || v == pyNestedTag || v == "" {
			return pyVal{}, true
		}
		return p.eval(c, v, depth+1), true
	}
	if b, ok := c.imports[name]; ok {
		return p.importVal(c, b, depth+1), true
	}
	if !c.module {
		if m := p.modules[c.pkg]; m != nil && m.facts != nil {
			if g, ok := m.facts.Globals[name]; ok {
				if _, def := m.imports[name]; def {
					return pyVal{}, true // imported and assigned: which one is unknown
				}
				return p.eval(p.modCtx(c.pkg), g, depth+1), true
			}
		}
	}
	if k, ok := p.classes.has(c.pkg, name); ok {
		return pyVal{kind: pyClassV, cls: k, exact: true}, true
	}
	if q := qualifyModule(c.pkg, name); p.allDefs[q] && !p.isMethod[q] {
		return pyVal{kind: pyFuncV, fn: q}, true
	}
	return pyVal{}, false
}

// importVal is what an import binding of ctx c binds its local name to.
func (p *pyTyper) importVal(c pyCtx, b ImportBinding, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	if b.Symbol == "" {
		// import a.b.c binds a (the package); import a.b.c as x binds x to a.b.c
		mod := b.Module
		if b.Local == pyFirstSegment(b.Module) {
			mod = b.Local
		}
		if pkg, ok := p.resolveMod(c.path, mod); ok {
			return pyVal{kind: pyModuleV, mod: pkg, name: mod, from: c.path}
		}
		return pyVal{}
	}
	if b.Symbol == "*" {
		return pyVal{}
	}
	mp, ok := p.resolveMod(c.path, b.Module)
	if !ok {
		return pyVal{}
	}
	if v := p.memberOfModule(mp, b.Symbol, depth+1); v.kind != 0 {
		return v
	}
	sub := b.Module + "." + b.Symbol
	if strings.HasSuffix(b.Module, ".") {
		sub = b.Module + b.Symbol // from . import users
	}
	if pkg, ok := p.resolveMod(c.path, sub); ok {
		return pyVal{kind: pyModuleV, mod: pkg, name: sub, from: c.path}
	}
	return pyVal{}
}

// memberOfModule is the value of name in module pkg: a class, a function, a
// value it assigns, or a name it imports from elsewhere.
func (p *pyTyper) memberOfModule(pkg, name string, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	if k, ok := p.classes.has(pkg, name); ok {
		return pyVal{kind: pyClassV, cls: k, exact: true}
	}
	if q := qualifyModule(pkg, name); p.allDefs[q] && !p.isMethod[q] {
		return pyVal{kind: pyFuncV, fn: q}
	}
	m := p.modules[pkg]
	if m == nil {
		return pyVal{}
	}
	if m.facts != nil {
		if g, ok := m.facts.Globals[name]; ok {
			if _, imp := m.imports[name]; imp {
				return pyVal{}
			}
			return p.eval(p.modCtx(pkg), g, depth+1)
		}
	}
	if b, ok := m.imports[name]; ok {
		return p.importVal(p.modCtx(pkg), b, depth+1)
	}
	return pyVal{}
}

// attrVal is the value of attribute a of base.
func (p *pyTyper) attrVal(base pyVal, a string, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	switch base.kind {
	case pyInst, pyClassV:
		return p.fieldVal(base.cls, a, depth+1)
	case pyModuleV:
		if v := p.memberOfModule(base.mod, a, depth+1); v.kind != 0 {
			return v
		}
		if base.name != "" {
			sub := base.name + "." + a
			if strings.HasSuffix(base.name, ".") {
				sub = base.name + a
			}
			if pkg, ok := p.resolveMod(base.from, sub); ok {
				return pyVal{kind: pyModuleV, mod: pkg, name: sub, from: base.from}
			}
		}
	}
	return pyVal{}
}

// fieldVal is what attribute attr of an instance (or the class object) of
// cls holds: a field a class of its MRO sets on self or declares.
func (p *pyTyper) fieldVal(cls, attr string, depth int) pyVal {
	order, ok := p.classes.mro(cls)
	if !ok {
		return pyVal{}
	}
	for _, k := range order {
		if isExtBase(k) {
			return pyVal{}
		}
		cq := qualifyModule(p.classes.pkg[k], p.classes.name[k])
		c, ok := p.ctx(cq)
		if !ok {
			continue
		}
		if v, ok := c.vars[attr]; ok {
			return p.eval(c, v, depth+1)
		}
	}
	return pyVal{}
}

// callMember is what calling attribute name of base gives.
func (p *pyTyper) callMember(base pyVal, name string, depth int) pyVal {
	switch base.kind {
	case pyInst, pyClassV:
		if q, ok := p.classes.method(base.cls, name, false, p.allDefs); ok {
			return p.returnVal(q, base.cls, base.exact, depth+1)
		}
		return p.callOn(p.fieldVal(base.cls, name, depth+1), depth+1)
	case pyModuleV:
		return p.callOn(p.memberOfModule(base.mod, name, depth+1), depth+1)
	}
	return pyVal{}
}

// callOn is what calling the value v gives.
func (p *pyTyper) callOn(v pyVal, depth int) pyVal {
	if depth > pyMaxDepth {
		p.cut = true
		return pyVal{}
	}
	switch v.kind {
	case pyClassV:
		return pyVal{kind: pyInst, cls: v.cls, exact: v.exact}
	case pyCallableV:
		if v.rctx != nil && v.ret != "" {
			r := p.eval(*v.rctx, v.ret, depth+1)
			if r.kind == pyInst {
				r.exact = false
			}
			return r
		}
	case pyFuncV:
		return p.returnVal(v.fn, "", false, depth+1)
	case pyInst:
		if q, ok := p.classes.method(v.cls, "__call__", false, p.allDefs); ok {
			return p.returnVal(q, v.cls, v.exact, depth+1)
		}
	}
	return pyVal{}
}

// inferredSite types a call the receiver typing of typedCalls left alone,
// by evaluating its receiver.
func (p *pyTyper) inferredSite(c pyCtx, qc QualifiedCall) (CallSite, bool) {
	var recv pyVal
	switch {
	case qc.Result && qc.Chain == 3 && qc.Attr == "" && qc.Qualifier != "":
		recv = p.callVal(c, qc.Qualifier, 0) // f().g()
	case qc.Result && qc.Chain == 4 && qc.Attr != "":
		recv = p.callVal(c, qc.Qualifier+"."+qc.Attr, 0) // a.f().g()
	case qc.Result || qc.Chain < 2:
		return CallSite{}, false
	default:
		path := qc.Path
		switch {
		case path != "":
		case qc.Chain == 2:
			path = qc.Qualifier
		case qc.Chain == 3 && qc.Attr != "":
			path = qc.Qualifier + "." + qc.Attr
		default:
			return CallSite{}, false
		}
		if head := pyFirstSegment(path); (head == "self" || head == "cls") && c.recv != "" && qc.Chain == 2 {
			return CallSite{}, false // self.f(): the class's own method (CallSelf)
		}
		recv = p.pathVal(c, path, 0)
	}
	p.cut = false
	switch recv.kind {
	case pyInst:
		if _, ok := p.classes.method(recv.cls, qc.Name, false, p.allDefs); !ok {
			// a field holding a class: calling it constructs that class
			if f := p.fieldVal(recv.cls, qc.Name, 0); f.kind == pyClassV {
				return CallSite{Name: p.classes.name[f.cls], Kind: CallModule, Scope: p.classes.pkg[f.cls]}, true
			}
		}
		if recv.exact {
			return CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassNew + recv.cls}, true
		}
		return CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassScope + recv.cls}, true
	case pyClassV:
		if recv.exact {
			return CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassExact + recv.cls}, true
		}
		return CallSite{Name: qc.Name, Kind: CallTyped, Scope: pyClassScope + recv.cls}, true
	case pyModuleV:
		if qc.Chain == 2 {
			return CallSite{}, false // m.f(): import resolution reads it
		}
		return CallSite{Name: qc.Name, Kind: CallModule, Scope: recv.mod}, true
	}
	return CallSite{}, false
}

// top evaluates v in c as a query of its own: a cycle or the depth bound met
// inside it does not mark an enclosing evaluation.
func (p *pyTyper) top(c pyCtx, v string) pyVal {
	saved := p.cut
	p.cut = false
	val := p.eval(c, v, 0)
	p.cut = saved
	return val
}
