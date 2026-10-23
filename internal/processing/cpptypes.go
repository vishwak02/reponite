// cpptypes.go reads what C++ declares about types — each class's member
// fields and base classes, namespace-level typedef/using aliases, and each
// function's parameter and local variable types — so a member call can be
// resolved by the receiver's declared type instead of by its bare name.
// `pub_.publish(msg)` on a `ros::Publisher pub_` member is a library call, not
// the repo's one `publish`; `worker_->step()` on a `std::unique_ptr<Worker>`
// is Worker::step (or a subclass's override). Declared types are not a
// compiler's proof — overloads, templates and macros stay out of reach — but
// they are far stronger than a name. Pure over content.AST (ADR-018).
package processing

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// TypeFacts is what one C++ file declares about its types.
type TypeFacts struct {
	Classes map[string]*ClassFact // class/struct name -> facts (definitions with a body only)
	Aliases map[string]string     // namespace-level typedef/using alias -> target type
	Returns map[string]string     // "Class.method" -> declared return type (in- or out-of-class)
	// Includes are the file's #include paths as written ("pkg/x.hpp").
	Includes []string
	// Internal are the names this file gives internal linkage: functions and
	// classes in an anonymous namespace, and static namespace-scope
	// functions. No other file can call them.
	Internal map[string]bool
	// Declared are the free functions this file declares or defines at
	// namespace scope (an unqualified name) — what an includer may call.
	Declared map[string]bool
	// AliasArgs are an alias's template arguments (using P = X<A, B>).
	AliasArgs map[string][]string
	// Nested are the classes this file defines inside another class's
	// body, as Outer::Inner
	Nested map[string]bool
	// Enums are the enumeration types this file defines
	Enums map[string]bool
	// Top are the classes this file defines at namespace scope (not nested
	// in another class): what a bare class name written outside any class
	// names
	Top map[string]bool
	// FuncReturns are the free functions this file declares or defines at
	// namespace scope, by name, with their declared return type ("" unknown,
	// or overloads returning different types)
	FuncReturns map[string]string
	// Members are the methods each class declares or defines ("Cls.m")
	Members map[string]bool
	// C: the facts of a C file (declarations and includes only)
	C bool
	// Opened are the namespaces ("a", "a::b") the file's code is written
	// in or brings in with a using-directive
	Opened map[string]bool
	// NSAliases are the file's namespace aliases (namespace fm = a::b;)
	NSAliases map[string]string
	// FuncNS are the namespaces ("a::b", "" global) each free function this
	// file declares or defines is in.
	FuncNS map[string][]string
	// Defaults are the function and method names this file declares with a
	// defaulted parameter (f(int a, int b = 0);), each with the fewest
	// arguments such a declaration takes: their out-of-line definitions take
	// fewer than they list, down to that.
	Defaults map[string]int
}

// ClassFact is a class's member fields (name -> declared type) and bases.
type ClassFact struct {
	Fields map[string]string
	Bases  []string
	// TParams are a class template's type parameters, in order; BaseArgs a
	// base's template arguments as written (Base<A, B> -> [A B]). A base that
	// is a type parameter (template <class B> class X : public B) is the
	// argument the instantiation passes.
	TParams   []string
	TDefaults []string // each type parameter's default ("" none)
	// TDefaultArgs are each default's own template arguments
	TDefaultArgs [][]string
	BaseArgs     map[string][]string
	// Aliases are the class's own type aliases (using Batch = std::map<…>;):
	// what its members' and methods' locals of that type are
	Aliases map[string]string
}

// A type is a string: a class ("Worker"), a type in an external namespace
// ("ros::Publisher", "std::string"), a sequence of T ("[]T": vector, list,
// set, deque, array, queue …), a map ("map[K]V"), or an element of a map
// ("pair[K]V"). "" is unknown or a primitive (nothing to call a member on).

// smartPointers wrap the type whose members a call through them reaches.
var smartPointers = map[string]bool{
	"shared_ptr": true, "unique_ptr": true, "weak_ptr": true, "scoped_ptr": true, "intrusive_ptr": true,
	"optional": true, "reference_wrapper": true, "observer_ptr": true, "atomic": true,
}

var seqContainers = map[string]bool{
	"vector": true, "list": true, "deque": true, "set": true, "unordered_set": true, "multiset": true,
	"unordered_multiset": true, "array": true, "queue": true, "priority_queue": true, "stack": true,
	"forward_list": true, "span": true, "circular_buffer": true,
}

var mapContainers = map[string]bool{"map": true, "unordered_map": true, "multimap": true, "unordered_multimap": true}

// ptrMembers are the ROS/boost idiom `Foo::Ptr` (a typedef'd smart pointer to Foo).
var ptrMembers = map[string]bool{"Ptr": true, "ConstPtr": true, "SharedPtr": true, "UniquePtr": true, "WeakPtr": true}

var typeNodeTypes = []string{"type_identifier", "qualified_identifier", "template_type", "primitive_type",
	"sized_type_specifier", "placeholder_type_specifier", "struct_specifier", "class_specifier"}

// typeFactsFor is a file's type facts, for the languages that have them.
func typeFactsFor(root content.AST, r LangRules) *TypeFacts {
	if r.Name == "rust" && root != nil {
		return RustTypeFacts(root)
	}
	if r.Name == "java" && root != nil {
		return JavaTypeFacts(root)
	}
	if r.Name == "c" && root != nil {
		return cDeclFacts(root)
	}
	return CppTypeFacts(root, r)
}

// cDeclFacts is what a C file tells C++ code about the functions it can
// call: its includes and the functions it declares (a C library's header
// included by a C++ source), with their linkage. Its types stay out of the
// C++ type table.
func cDeclFacts(root content.AST) *TypeFacts {
	full := CppTypeFacts(root, CppRules)
	if full == nil {
		return nil
	}
	tf := &TypeFacts{Includes: full.Includes, Declared: full.Declared, Internal: full.Internal, FuncNS: full.FuncNS,
		C: true}
	if len(tf.Includes) == 0 && len(tf.Declared) == 0 {
		return nil
	}
	return tf
}

// CppTypeFacts collects a C++ file's class, field, base, alias and method
// return-type facts; nil for other languages.
func CppTypeFacts(root content.AST, r LangRules) *TypeFacts {
	if r.Name != "cpp" || root == nil {
		return nil
	}
	tf := &TypeFacts{Classes: map[string]*ClassFact{}, Aliases: map[string]string{}, Returns: map[string]string{},
		Internal: map[string]bool{}, Declared: map[string]bool{}, AliasArgs: map[string][]string{}, FuncNS: map[string][]string{}, Defaults: map[string]int{}, Nested: map[string]bool{}, Enums: map[string]bool{}, Top: map[string]bool{},
		FuncReturns: map[string]string{}, Members: map[string]bool{}, Opened: map[string]bool{}, NSAliases: map[string]string{}}
	var nsStack []string // the named namespaces around the walk
	inNS := func(name string, extra ...string) {
		ns := strings.Join(append(append([]string(nil), nsStack...), extra...), "::")
		if !containsStr(tf.FuncNS[name], ns) {
			tf.FuncNS[name] = append(tf.FuncNS[name], ns)
		}
	}
	anon := 0                     // depth of anonymous namespaces around the walk
	var tparams []string          // the template parameters of the declaration being walked
	var tdefaults []string        // the defaults of those parameters ("" none), in order
	var tdefaultArgs [][]string   // each default's template arguments
	external := map[string]bool{} // names this file also declares with external linkage
	funcReturn := func(name, t string) {
		if strings.HasPrefix(t, "f:") || strings.HasPrefix(t, "x:") || containsStr(tparams, t) {
			t = "" // a template's parameter, or what auto deduces
		}
		if prev, ok := tf.FuncReturns[name]; ok && prev != t {
			t = "" // overloads returning different types
		}
		tf.FuncReturns[name] = t
	}
	var walk func(n content.AST, class string)
	walk = func(n content.AST, class string) {
		for _, c := range n.Children() {
			switch c.Type() {
			case "preproc_include":
				if p := firstChildAny(c, []string{"string_literal", "system_lib_string"}); p != nil {
					if inc := strings.Trim(p.Text(), "\"<> "); inc != "" {
						tf.Includes = append(tf.Includes, inc)
					}
				}
				continue
			case "enum_specifier":
				if id := firstChildAny(c, []string{"type_identifier"}); id != nil && firstChildAny(c, []string{"enumerator_list"}) != nil {
					tf.Enums[id.Text()] = true
				}
				continue
			case "template_declaration":
				if pl := firstChildAny(c, []string{"template_parameter_list"}); pl != nil {
					var ps, defs []string
					var defArgs [][]string
					for _, p := range pl.Children() {
						if p.Type() == "type_parameter_declaration" || p.Type() == "optional_type_parameter_declaration" || p.Type() == "variadic_type_parameter_declaration" {
							if id := firstChildAny(p, []string{"type_identifier"}); id != nil {
								ps = append(ps, id.Text())
								def := ""
								var dargs []string
								if p.Type() == "optional_type_parameter_declaration" {
									// template <typename B = Default>: the default
									if d := firstChildAny(p, []string{"template_type", "qualified_identifier", "primitive_type"}); d != nil {
										def, dargs = TypeName(d), writtenArgs(d)
									} else if ids := namedKids(p); len(ids) > 1 && ids[len(ids)-1].Type() == "type_identifier" {
										def = ids[len(ids)-1].Text()
									}
								}
								defs = append(defs, def)
								defArgs = append(defArgs, dargs)
							}
						}
					}
					outer, outerDefs, outerArgs := tparams, tdefaults, tdefaultArgs
					tparams, tdefaults, tdefaultArgs = ps, defs, defArgs
					walk(c, class)
					tparams, tdefaults, tdefaultArgs = outer, outerDefs, outerArgs
					continue
				}
			case "namespace_definition":
				if firstChildAny(c, []string{"namespace_identifier", "nested_namespace_specifier"}) == nil {
					anon++
					walk(c, class)
					anon--
					continue
				}
				outer := nsStack
				if id := firstChildAny(c, []string{"namespace_identifier", "nested_namespace_specifier"}); id != nil {
					nsStack = append(append([]string(nil), nsStack...), strings.Split(strings.ReplaceAll(id.Text(), " ", ""), "::")...)
					for k := range nsStack {
						tf.Opened[strings.Join(nsStack[:k+1], "::")] = true
					}
				}
				walk(c, class)
				nsStack = outer
				continue
			case "namespace_alias_definition":
				kids := namedKids(c)
				if len(kids) == 2 && kids[0].Type() == "namespace_identifier" {
					tf.NSAliases[kids[0].Text()] = strings.TrimPrefix(strings.ReplaceAll(kids[1].Text(), " ", ""), "::")
				}
				continue
			case "using_declaration":
				// using namespace a::b;: a::b's names are found from here
				if firstChildAny(c, []string{"namespace"}) != nil {
					if id := firstChildAny(c, []string{"identifier", "qualified_identifier", "namespace_identifier"}); id != nil {
						tf.Opened[strings.ReplaceAll(id.Text(), " ", "")] = true
					}
				}
			case "declaration":
				if class == "" {
					if d := firstChildAny(c, []string{"function_declarator", "pointer_declarator", "reference_declarator"}); d != nil {
						if fd := firstDescAny(d, []string{"function_declarator"}); fd != nil || d.Type() == "function_declarator" {
							if fd == nil {
								fd = d
							}
							if id := firstChildAny(fd, []string{"identifier", "operator_name"}); id != nil {
								tf.Declared[id.Text()] = true
								inNS(id.Text())
								funcReturn(id.Text(), TypeName(firstChildAny(c, typeNodeTypes)))
								if anon > 0 || hasStatic(c) {
									tf.Internal[id.Text()] = true
								} else {
									external[id.Text()] = true
								}
							}
						}
					}
				}
			}
			switch c.Type() {
			case "class_specifier", "struct_specifier":
				body := firstChildAny(c, []string{"field_declaration_list"})
				name, outer := "", class
				if id := firstChildAny(c, []string{"type_identifier"}); id != nil {
					name = id.Text()
				} else if q := firstChildAny(c, []string{"qualified_identifier"}); q != nil {
					name = TypeName(q) // class Outer::Impl { … }: the nested class Impl
					if i := strings.LastIndex(name, "::"); i >= 0 {
						if outer == "" && body != nil && !externalScopes[name[:i]] {
							// defined out of its class's body (a pimpl's
							// class Widget::Impl in the source file): nested
							// in Widget all the same
							outer = name[:i]
							if j := strings.LastIndex(outer, "::"); j >= 0 {
								outer = outer[j+2:]
							}
						}
						name = name[i+2:]
					}
				} else if tt := firstChildAny(c, []string{"template_type"}); tt != nil {
					// struct Trait<ros::Duration> { … }: a specialization of Trait
					if id := firstChildAny(tt, []string{"type_identifier"}); id != nil {
						name = id.Text()
					}
				}
				if body == nil || name == "" {
					walk(c, class)
					continue
				}
				if anon > 0 && outer == "" {
					tf.Internal[name] = true
				}
				if outer != "" {
					tf.Nested[outer+"::"+name] = true
				} else {
					tf.Top[name] = true
				}
				// this body's own facts; merged into the class of its name, and kept
				// apart as Outer::Inner for a nested class (two classes may each
				// nest one of that name)
				cf := &ClassFact{Fields: map[string]string{}}
				if n.Type() == "template_declaration" && len(tparams) > 0 {
					cf.TParams, cf.TDefaults, cf.TDefaultArgs = tparams, tdefaults, tdefaultArgs
				}
				if bc := firstChildAny(c, []string{"base_class_clause"}); bc != nil {
					for _, b := range bc.Children() {
						if t := TypeName(b); t != "" {
							cf.Bases = append(cf.Bases, t)
							if args := writtenArgs(b); len(args) > 0 {
								if cf.BaseArgs == nil {
									cf.BaseArgs = map[string][]string{}
								}
								cf.BaseArgs[t] = args
							}
						}
					}
				}
				// using Impl = X<…>; inside the class: its members' Impl is X
				local := map[string]string{}
				for _, ad := range classMembers(body) {
					if ad.Type() != "alias_declaration" {
						continue
					}
					id := firstChildAny(ad, []string{"type_identifier"})
					if td := firstChildAny(ad, []string{"type_descriptor"}); id != nil && td != nil {
						if t := TypeName(td); t != "" && t != id.Text() {
							if prev, seen := local[id.Text()]; seen && prev != t {
								t = "" // #if A / #else: the build's configuration picks one
							}
							local[id.Text()] = t
						}
					}
				}
				for a, t := range local {
					if cf.Aliases == nil {
						cf.Aliases = map[string]string{}
					}
					cf.Aliases[a] = t
					tf.Aliases[name+"::"+a] = t // Cls::Alias named from outside
				}
				for _, fd := range classMembers(body) {
					if fd.Type() != "field_declaration" {
						continue
					}
					t := TypeName(firstChildAny(fd, typeNodeTypes))
					if a, ok := local[t]; ok {
						t = a
					}
					for _, d := range fd.Children() {
						if f := declaredName(d, "field_identifier"); f != "" {
							cf.Fields[f] = arrayOf(d, t)
						} else if m := methodDeclName(d); m != "" {
							tf.Members[name+"."+m] = true
							if t != "" {
								tf.Returns[name+"."+m] = t // in-class method declaration
							}
						}
					}
				}
				if leaf := tf.Classes[name]; leaf == nil {
					tf.Classes[name] = cf.clone()
				} else {
					leaf.merge(cf)
				}
				if outer != "" {
					tf.Classes[outer+"::"+name] = cf
				}
				walk(body, name)
			case "function_definition":
				// A definition's return type: in-class (class set) or out-of-class
				// (void Cls::f()). The body holds no class-level facts.
				t := TypeName(firstChildAny(c, typeNodeTypes))
				if t == "" && firstChildAny(c, []string{"placeholder_type_specifier"}) != nil {
					t = returnedField(c) // auto& get() { return _x; }: the field's type
				}
				if d := declaratorOf(c, CppRules); d != nil && class == "" {
					if n := declaratorName(d, CppRules); n == "" {
					} else if q := firstDescBefore(d, "qualified_identifier", "parameter"); q != nil {
						external[n] = true // void ns::f() {…}: defines a declared f
						if sc := strings.TrimSuffix(strings.ReplaceAll(q.Text(), " ", ""), "::"+n); sc != q.Text() {
							inNS(n, strings.Split(sc, "::")...)
						}
					} else {
						tf.Declared[n] = true
						inNS(n)
						funcReturn(n, t)
						if anon > 0 || hasStatic(c) {
							tf.Internal[n] = true
						} else {
							external[n] = true
						}
					}
				}
				if d := declaratorOf(c, CppRules); d != nil {
					cls, m := class, ""
					if q := firstDescBefore(d, "qualified_identifier", "parameter"); q != nil {
						// void A::B::f(): a method of B (A only scopes it)
						cls, m = lastScope(q), declaratorName(d, CppRules)
					} else if class != "" {
						m = declaratorName(d, CppRules)
					}
					if cls != "" && m != "" {
						tf.Members[cls+"."+m] = true
						if t != "" {
							tf.Returns[cls+"."+m] = t
						}
					}
				}
				if lcs := localClasses(c, CppRules); len(lcs) > 0 {
					walk(kidsNode{AST: c, kids: lcs}, "") // classes the body defines
				}
			case "alias_declaration":
				if class == "" {
					id := firstChildAny(c, []string{"type_identifier"})
					if td := firstChildAny(c, []string{"type_descriptor"}); id != nil && td != nil {
						if t := TypeName(td); t != "" {
							tf.Aliases[id.Text()] = t
							if args := writtenArgs(td); len(args) > 0 {
								tf.AliasArgs[id.Text()] = args
							}
						}
					}
				}
			case "type_definition":
				if class == "" {
					kids := c.Children()
					t := TypeName(firstChildAny(c, typeNodeTypes))
					for i := len(kids) - 1; i > 0; i-- {
						if kids[i].Type() == "type_identifier" {
							if t != "" && t != kids[i].Text() {
								tf.Aliases[kids[i].Text()] = t
							}
							break
						}
					}
				}
			case "compound_statement":
			default:
				walk(c, class)
			}
		}
	}
	walk(root, "")
	for _, fd := range descendantsAny(root, []string{"function_declarator"}) {
		pl := firstChildAny(fd, []string{"parameter_list"})
		if pl == nil || firstChildAny(pl, []string{"optional_parameter_declaration"}) == nil {
			continue
		}
		if id := firstChildAny(fd, []string{"identifier", "field_identifier", "qualified_identifier", "operator_name"}); id != nil {
			n := id.Text()
			if i := strings.LastIndex(n, "::"); i >= 0 {
				n = n[i+2:]
			}
			need := 0
			for _, p := range pl.Children() {
				if p.Type() == "parameter_declaration" {
					need++
				}
			}
			if prev, ok := tf.Defaults[n]; !ok || need < prev {
				tf.Defaults[n] = need
			}
		}
	}
	for n := range external {
		delete(tf.Internal, n) // overloads of one name, some internal: the name is not
	}
	if len(tf.Classes) == 0 && len(tf.Aliases) == 0 && len(tf.Returns) == 0 && len(tf.Includes) == 0 && len(tf.Declared) == 0 && len(tf.Enums) == 0 && len(tf.Members) == 0 && len(tf.NSAliases) == 0 {
		return nil
	}
	return tf
}

// kidsNode is a node seen with only the given children.
type kidsNode struct {
	content.AST
	kids []content.AST
}

func (k kidsNode) Children() []content.AST { return k.kids }

// lastScope is the innermost scope a qualified name is written in:
// A::B::f -> B, ns::Cls<T>::f -> Cls.
func lastScope(q content.AST) string {
	scope := ""
	for q != nil && q.Type() == "qualified_identifier" {
		kids := q.Children()
		if len(kids) == 0 {
			break
		}
		if s := scopeName(kids[0]); s != "" {
			scope = s
		}
		q = kids[len(kids)-1]
	}
	return scope
}

// classMembers are a class body's member declarations, those inside
// #if/#ifdef/#else blocks included.
func classMembers(body content.AST) []content.AST {
	var out []content.AST
	for _, c := range body.Children() {
		switch c.Type() {
		case "preproc_if", "preproc_ifdef", "preproc_else", "preproc_elif", "preproc_elifdef":
			out = append(out, classMembers(c)...)
		default:
			out = append(out, c)
		}
	}
	return out
}

// returnedField is "f:<field>" when a function's body returns a member
// field (return _x; return this->_x; return *_x;), else "".
func returnedField(fn content.AST) string {
	body := firstChildAny(fn, []string{"compound_statement"})
	if body == nil {
		return ""
	}
	var ret content.AST
	for _, st := range body.Children() {
		if st.Type() == "return_statement" {
			if ret != nil {
				return "" // several returns: no single answer
			}
			ret = st
		}
	}
	if ret == nil {
		return ""
	}
	for _, e := range ret.Children() {
		if !e.IsNamed() {
			continue
		}
		for e.Type() == "pointer_expression" || e.Type() == "parenthesized_expression" {
			var inner content.AST
			for _, k := range e.Children() {
				if k.IsNamed() {
					inner = k
				}
			}
			if inner == nil {
				return ""
			}
			e = inner
		}
		switch e.Type() {
		case "identifier":
			return "f:" + e.Text()
		case "field_expression":
			if k := e.Children(); len(k) > 0 && isThis(k[0]) {
				return "f:" + k[len(k)-1].Text()
			}
		}
		// any other expression over the class's members (*std::get<0>(_t),
		// _x.front()): evaluated in the class at index time
		if x := recvExpr(e, nil); x != "" {
			return "x:" + x
		}
		return ""
	}
	return ""
}

// tupleGet is the receiver expression of std::get<N>(t) / std::get<T>(t):
// t's element N, or its element of type T.
func tupleGet(fn content.AST, call []content.AST, vars map[string]string) string {
	args := firstChildAny(fn, []string{"template_argument_list"})
	var al content.AST
	for _, c := range call {
		if c.Type() == "argument_list" {
			al = c
		}
	}
	if args == nil || al == nil {
		return ""
	}
	var arg content.AST
	for _, c := range al.Children() {
		if c.IsNamed() {
			arg = c
			break
		}
	}
	if arg == nil {
		return ""
	}
	base := recvExpr(arg, vars)
	if base == "" {
		return ""
	}
	if n := firstChildAny(args, []string{"number_literal"}); n != nil {
		return then(base, "get#"+n.Text())
	}
	if t := TypeName(firstChildAny(args, []string{"type_descriptor"})); t != "" {
		return then(base, "get:"+t)
	}
	return ""
}

// writtenArgs are the template arguments of the class type a node names
// (X<A, B>, ns::X<A>), or nil.
func writtenArgs(n content.AST) []string {
	if n == nil {
		return nil
	}
	if n.Type() == "template_type" {
		if id := firstChildAny(n, []string{"type_identifier"}); id != nil {
			if w := id.Text(); smartPointers[w] || seqContainers[w] || mapContainers[w] {
				return nil
			}
		}
		return templateArgs(n)
	}
	switch n.Type() {
	case "type_descriptor", "qualified_identifier":
		for _, c := range n.Children() {
			if c.Type() == "template_type" || c.Type() == "qualified_identifier" {
				return writtenArgs(c)
			}
		}
	}
	return nil
}

func (cf *ClassFact) clone() *ClassFact {
	out := &ClassFact{Fields: map[string]string{}}
	out.merge(cf)
	return out
}

// merge adds src's facts (another body of a class of the same name).
func (cf *ClassFact) merge(src *ClassFact) {
	for k, v := range src.Fields {
		if old, seen := cf.Fields[k]; seen && old != v {
			v = "" // two classes of one name typing a field differently: unknown
		}
		cf.Fields[k] = v
	}
	cf.Bases = append(cf.Bases, src.Bases...)
	if len(cf.TParams) == 0 {
		cf.TParams, cf.TDefaults, cf.TDefaultArgs = src.TParams, src.TDefaults, src.TDefaultArgs
	}
	for b, a := range src.BaseArgs {
		if cf.BaseArgs == nil {
			cf.BaseArgs = map[string][]string{}
		}
		cf.BaseArgs[b] = a
	}
	for a, t := range src.Aliases {
		if cf.Aliases == nil {
			cf.Aliases = map[string]string{}
		}
		cf.Aliases[a] = t
	}
}

// hasStatic: a declaration or definition written with the static storage
// class (at namespace scope: internal linkage).
func hasStatic(n content.AST) bool {
	for _, c := range n.Children() {
		if c.Type() == "storage_class_specifier" && c.Text() == "static" {
			return true
		}
	}
	return false
}

// declaredName is the name a declarator introduces (the leaf of type leaf
// inside pointer/reference/array/init declarators); "" for a function
// declarator (an in-class method declaration is not a field).
func declaredName(d content.AST, leaf string) string {
	switch d.Type() {
	case leaf:
		return d.Text()
	case "pointer_declarator", "reference_declarator", "array_declarator", "init_declarator", "bitfield_clause":
		for _, c := range d.Children() {
			if c.Type() == "function_declarator" {
				return ""
			}
			if n := declaredName(c, leaf); n != "" {
				return n
			}
		}
	}
	return ""
}

// arrayOf is t for a declarator of a T, "[]t" for one of an array of T
// (T x[N]: x[i] is a T).
func arrayOf(d content.AST, t string) string {
	for d != nil && t != "" {
		switch d.Type() {
		case "array_declarator":
			return "[]" + t
		case "init_declarator", "bitfield_clause":
			d = firstChildAny(d, []string{"array_declarator", "identifier", "field_identifier"})
			continue
		}
		break
	}
	return t
}

// methodDeclName is the method an in-class declaration declares
// (`const Req& getReq() const;`), or "".
func methodDeclName(d content.AST) string {
	switch d.Type() {
	case "function_declarator":
		if id := firstChildAny(d, []string{"field_identifier", "identifier"}); id != nil {
			return id.Text()
		}
	case "pointer_declarator", "reference_declarator":
		for _, c := range d.Children() {
			if n := methodDeclName(c); n != "" {
				return n
			}
		}
	}
	return ""
}

// TypeName reduces a C++ type node to the type a member call on it reaches:
// Worker, const Worker&, Worker* -> "Worker"; std::shared_ptr<Worker> /
// boost::shared_ptr<Worker> / Worker::Ptr -> "Worker"; std::vector<W> ->
// "[]W"; std::map<K, V> -> "map[K]V"; a type in an external namespace keeps
// it (ros::Publisher -> "ros::Publisher"); primitives and `auto` are "".
func TypeName(n content.AST) string {
	if n == nil {
		return ""
	}
	switch n.Type() {
	case "type_identifier":
		return n.Text()
	case "primitive_type", "sized_type_specifier":
		// double, unsigned int: a number (no members, an overload's argument)
		if f := strings.Fields(n.Text()); len(f) > 0 {
			return f[len(f)-1]
		}
		return ""
	case "type_descriptor":
		return TypeName(firstChildAny(n, typeNodeTypes))
	case "template_type":
		id := firstChildAny(n, []string{"type_identifier"})
		if id == nil {
			return ""
		}
		args := templateArgs(n)
		switch name := id.Text(); {
		case smartPointers[name]:
			if len(args) > 0 {
				return args[0]
			}
			return ""
		case seqContainers[name]:
			if len(args) > 0 {
				return "[]" + args[0]
			}
			return "[]"
		case mapContainers[name]:
			if len(args) > 1 {
				return "map[" + args[0] + "]" + args[1]
			}
			return "map[]"
		case name == "pair":
			if len(args) > 1 {
				return "pair[" + args[0] + "]" + args[1]
			}
			return ""
		case name == "tuple":
			return "tuple[" + strings.Join(args, ";") + "]"
		default:
			return name
		}
	case "qualified_identifier":
		var scopes []string
		cur := n
		for cur != nil && cur.Type() == "qualified_identifier" {
			kids := cur.Children()
			if len(kids) == 0 {
				return ""
			}
			if s := scopeName(kids[0]); s != "" {
				scopes = append(scopes, s)
			}
			cur = kids[len(kids)-1]
		}
		if cur == nil {
			return ""
		}
		if cur.Type() == "template_type" {
			if id := firstChildAny(cur, []string{"type_identifier"}); id != nil {
				if w := id.Text(); smartPointers[w] || seqContainers[w] || mapContainers[w] || w == "pair" || w == "tuple" {
					return TypeName(cur)
				}
			}
		}
		name := scopeName(cur)
		if name == "" {
			return ""
		}
		if ptrMembers[name] && len(scopes) > 0 {
			return scopes[len(scopes)-1]
		}
		if len(scopes) > 0 && externalScopes[scopes[0]] {
			return scopes[0] + "::" + name
		}
		if len(scopes) > 0 {
			// Outer::Inner, Cls::Alias, ns::Cls: kept scoped — canon strips a
			// namespace, follows a class's alias, and a nested class keeps the
			// class it is nested in
			return scopes[len(scopes)-1] + "::" + name
		}
		return name
	case "struct_specifier", "class_specifier":
		if id := firstChildAny(n, []string{"type_identifier"}); id != nil {
			return id.Text()
		}
	}
	return ""
}

func templateArgs(n content.AST) []string {
	list := firstChildAny(n, []string{"template_argument_list"})
	if list == nil {
		return nil
	}
	var out []string
	for _, c := range list.Children() {
		if c.Type() == "type_descriptor" {
			out = append(out, TypeName(c))
		}
	}
	return out
}

// Receiver expressions. A member call's object is recorded as the path from
// something whose type is known to the object, so the type can be evaluated
// once every file's class facts are in (index time):
//
//	"v:<type>"  a parameter/local with that declared type
//	"f:<name>"  a member field of the caller's class (x_, this->x_)
//	"c:"        the caller's class itself (this->getX()->…)
//	"t:<name>"  X(…) — a temporary of class X, else getX() on the implicit this
//	" .<name>"  a field of the current type          (a.b)
//	" ()<name>" the return type of a method on it    (a.get_b())
//	" []"       an element                           (v[i], v.at(i), v.front())
//	" it"       what iterating it yields             (for (auto& x : v), v.find(k)->)
//
// e.g. `auto it = works_.find(id); it->second.canForget()` -> "f:works_ it .second".

// iterMembers return an iterator; elemMembers an element; passMembers the
// object a smart pointer or wrapper holds.
var (
	iterMembers = map[string]bool{"find": true, "begin": true, "end": true, "cbegin": true, "cend": true,
		"rbegin": true, "rend": true, "lower_bound": true, "upper_bound": true}
	elemMembers = map[string]bool{"at": true, "front": true, "back": true, "top": true}
	passMembers = map[string]bool{"get": true, "lock": true, "value": true, "value_or": true}
	// stdPassThrough: std:: functions whose result is reached through their
	// first argument — an iterator over it ("it"), or the argument itself
	stdPassThrough = map[string]string{"begin": "it", "end": "it", "cbegin": "it", "cend": "it", "rbegin": "it",
		"rend": "it", "next": "=", "prev": "=", "move": "=", "ref": "=", "cref": "=", "as_const": "=", "addressof": "="}
)

// recvExpr is the receiver expression of an object node, or "".
func recvExpr(n content.AST, vars map[string]string) string {
	switch n.Type() {
	case "identifier":
		if v, ok := vars[n.Text()]; ok {
			return v // "" for a local whose type is unknown: never a field
		}
		return "f:" + n.Text()
	case "parenthesized_expression", "pointer_expression":
		for _, c := range n.Children() {
			if c.IsNamed() {
				return recvExpr(c, vars)
			}
		}
	case "field_expression":
		k := n.Children()
		if len(k) == 0 {
			return ""
		}
		f := memberName(k[len(k)-1])
		if f == "" {
			return ""
		}
		if isThis(k[0]) {
			return "f:" + f
		}
		if base := recvExpr(k[0], vars); base != "" {
			return then(base, "."+f)
		}
	case "subscript_expression":
		if k := n.Children(); len(k) > 0 {
			if base := recvExpr(k[0], vars); base != "" {
				return then(base, "[]")
			}
		}
	case "call_expression":
		k := n.Children()
		if len(k) > 0 && k[0].Type() == "template_function" {
			// static_cast<T*>(p)->m(), const_cast<T&>(*this).m(): the cast's type
			if id := firstChildAny(k[0], []string{"identifier"}); id != nil && castNames[id.Text()] {
				if args := firstChildAny(k[0], []string{"template_argument_list"}); args != nil {
					if t := TypeName(firstChildAny(args, []string{"type_descriptor"})); t != "" {
						return "v:" + t
					}
				}
				return ""
			}
			// std::get<N>(t), std::get<T>(t): an element of a tuple
			if id := firstChildAny(k[0], []string{"identifier"}); id != nil && id.Text() == "get" {
				return tupleGet(k[0], k, vars)
			}
		}
		if len(k) > 0 && k[0].Type() == "qualified_identifier" {
			if scope, name := scopeAndLeaf(k[0]); scope == "std" && stdPassThrough[name] != "" {
				// std::begin(v) iterates v; std::next(it), std::move(x): their argument
				for _, c := range k {
					if c.Type() != "argument_list" {
						continue
					}
					for _, a := range c.Children() {
						if a.IsNamed() {
							if base := recvExpr(a, vars); base != "" {
								if stdPassThrough[name] == "it" {
									return then(base, "it")
								}
								return base
							}
							return ""
						}
					}
				}
				return ""
			}
			if tf := firstChildAny(k[0], []string{"template_function"}); tf != nil {
				if id := firstChildAny(tf, []string{"identifier"}); id != nil && id.Text() == "get" {
					return tupleGet(tf, k, vars) // std::get<…>(t)
				}
			}
			// Cls::instance().m(): what Cls's static method returns — or, when
			// Cls::instance names no method (ns::make()), as below
			if scope, name := scopeAndLeaf(k[0]); scope != "" && name != "" {
				return "q:" + scope + "::" + name
			}
		}
		if len(k) > 0 && (k[0].Type() == "identifier" || k[0].Type() == "qualified_identifier") {
			// X(…).m(): a temporary of class X, or getX()->… on the implicit
			// this — "t:" decides at index time (a free function is neither).
			if name := calleeLeaf(k[0]); name != "" {
				return "t:" + name
			}
			return ""
		}
		if len(k) == 0 || k[0].Type() != "field_expression" {
			return ""
		}
		fk := k[0].Children()
		if len(fk) == 0 {
			return ""
		}
		m := memberName(fk[len(fk)-1])
		if m == "" {
			return ""
		}
		base := "c:" // this->m(): a method of the caller's own class
		if !isThis(fk[0]) {
			if base = recvExpr(fk[0], vars); base == "" {
				return ""
			}
		}
		if fk[len(fk)-1].Type() == "template_method" {
			// x.get<Tag>(): a template member of x's class, never a smart
			// pointer's get() or a container's front()
			return then(base, "()"+m)
		}
		switch {
		case iterMembers[m]:
			return then(base, "it")
		case elemMembers[m]:
			return then(base, "[]")
		case passMembers[m]:
			return base
		}
		return then(base, "()"+m)
	}
	return ""
}

// scopeAndLeaf splits a qualified callee Cls::f (ns::Cls::f) into its last
// scope and its name; "" when the scope is not a plain name.
func scopeAndLeaf(n content.AST) (string, string) {
	k := n.Children()
	if len(k) == 0 {
		return "", ""
	}
	var scope string
	switch k[0].Type() {
	case "namespace_identifier", "type_identifier", "identifier":
		scope = k[0].Text()
	default:
		return "", ""
	}
	rest := k[len(k)-1]
	if rest.Type() == "qualified_identifier" {
		return scopeAndLeaf(rest)
	}
	return scope, calleeLeaf(rest)
}

// calleeLeaf is the last name of an identifier or qualified callee.
func calleeLeaf(n content.AST) string {
	for n.Type() == "qualified_identifier" {
		k := n.Children()
		if len(k) == 0 {
			return ""
		}
		n = k[len(k)-1]
	}
	if n.Type() == "identifier" || n.Type() == "type_identifier" {
		return n.Text()
	}
	return ""
}

// then appends one step to every alternative of a receiver expression.
func then(expr, op string) string {
	alts := strings.Split(expr, "|")
	for i := range alts {
		alts[i] += " " + op
	}
	return strings.Join(alts, "|")
}

// cppVarTypes maps a function's parameters and locals to receiver
// expressions. `auto x = std::make_shared<T>(…)` / `new T(…)` are T; `auto x
// = expr` is expr's path; range-for variables iterate their range. A name
// declared two different ways in one function (shadowing in separate blocks)
// keeps both ("a|b"): it is typed only when both evaluate to the same type,
// and is never mistaken for a member field.
func cppVarTypes(fn content.AST, r LangRules) map[string]string {
	vars := map[string]string{}
	set := func(name, t string) {
		if name == "" {
			return
		}
		old, ok := vars[name]
		switch {
		case !ok:
			vars[name] = t
		case old == "" || t == "":
			vars[name] = "" // one declaration of unknown type: the name is unknown
		case !containsStr(strings.Split(old, "|"), t):
			vars[name] = old + "|" + t // declared twice: typed only if both agree (eval)
		}
	}
	typed := func(t string) string {
		if t == "" {
			return ""
		}
		return "v:" + t
	}
	declare := func(decl content.AST) {
		t := TypeName(firstChildAny(decl, typeNodeTypes))
		isAuto := firstChildAny(decl, []string{"placeholder_type_specifier"}) != nil
		if decl.Type() == "for_range_loop" {
			declareRange(decl, t, isAuto, vars, set)
			return
		}
		kids := decl.Children()
		for i, d := range kids {
			name := declaredName(d, "identifier")
			if name == "" && d.Type() == "function_declarator" && decl.Type() == "declaration" && !isAuto {
				// T x(args); in a body: a variable constructed with arguments
				// (the parse cannot tell it from a local function declaration)
				if id := firstChildAny(d, []string{"identifier"}); id != nil {
					name = id.Text()
				}
			}
			if name == "" {
				continue
			}
			switch {
			case !isAuto:
				set(name, typed(arrayOf(d, t)))
			case d.Type() == "identifier":
				// if (auto x = f()) — a condition's declaration has no
				// init_declarator: the value is the next named sibling.
				v := ""
				for _, n := range kids[i+1:] {
					if n.IsNamed() {
						v = autoValueExpr(n, vars)
						break
					}
				}
				set(name, v)
			default:
				set(name, autoInitExpr(d, vars))
			}
		}
	}
	if d := declaratorOf(fn, r); d != nil {
		if pl := firstDescAny(d, []string{"parameter_list"}); pl != nil {
			for _, p := range pl.Children() {
				if p.Type() == "parameter_declaration" || p.Type() == "optional_parameter_declaration" {
					declare(p)
				}
			}
		}
	}
	if body := firstChildAny(fn, r.BodyTypes); body != nil {
		// Lambda parameters are parameter_declarations inside the body.
		for _, decl := range descendantsAny(body, []string{"declaration", "for_range_loop", "parameter_declaration", "optional_parameter_declaration"}) {
			declare(decl)
		}
	}
	return vars
}

// declareRange: `for (T x : r)`, `for (auto& x : r)`, `for (auto& [k, v] : r)`.
func declareRange(loop content.AST, t string, isAuto bool, vars map[string]string, set func(string, string)) {
	var decl, rng content.AST
	for _, c := range loop.Children() {
		if !c.IsNamed() || c.Type() == "type_qualifier" || containsStr(typeNodeTypes, c.Type()) {
			continue
		}
		if decl == nil {
			decl = c
			continue
		}
		if c.Type() != "compound_statement" {
			rng = c
		}
		break
	}
	if decl == nil {
		return
	}
	rangeExpr := ""
	if rng != nil {
		rangeExpr = recvExpr(rng, vars)
	}
	if sb := structuredBinding(decl); sb != nil {
		var names []string
		for _, id := range sb.Children() {
			if id.Type() == "identifier" {
				names = append(names, id.Text())
			}
		}
		for i, n := range names {
			switch {
			case rangeExpr == "" || i > 1:
				set(n, "")
			case i == 0:
				set(n, then(then(rangeExpr, "it"), ".first"))
			default:
				set(n, then(then(rangeExpr, "it"), ".second"))
			}
		}
		return
	}
	name := declaredName(decl, "identifier")
	switch {
	case name == "":
	case !isAuto && t != "":
		set(name, "v:"+t)
	case isAuto && rangeExpr != "":
		set(name, then(rangeExpr, "it"))
	default:
		set(name, "")
	}
}

func structuredBinding(n content.AST) content.AST {
	if n.Type() == "structured_binding_declarator" {
		return n
	}
	if n.Type() == "reference_declarator" {
		for _, c := range n.Children() {
			if sb := structuredBinding(c); sb != nil {
				return sb
			}
		}
	}
	return nil
}

// autoInitExpr is the receiver expression an `auto` declarator's initializer
// gives it: new T / make_shared<T> -> T; an object path -> that path.
func autoInitExpr(d content.AST, vars map[string]string) string {
	if d.Type() != "init_declarator" {
		for _, c := range d.Children() { // auto& x = …: the init_declarator sits in a reference_declarator
			if c.Type() == "init_declarator" {
				return autoInitExpr(c, vars)
			}
		}
		return ""
	}
	kids := d.Children()
	for _, v := range kids[1:] {
		if v.IsNamed() {
			return autoValueExpr(v, vars)
		}
	}
	return ""
}

// autoValueExpr is the receiver expression of an `auto` variable's value.
func autoValueExpr(v content.AST, vars map[string]string) string {
	switch v.Type() {
	case "new_expression":
		if t := TypeName(firstChildAny(v, typeNodeTypes)); t != "" {
			return "v:" + t
		}
		return ""
	case "call_expression":
		ck := v.Children()
		if len(ck) == 0 {
			return ""
		}
		fn := ck[0]
		for fn.Type() == "qualified_identifier" {
			k := fn.Children()
			fn = k[len(k)-1]
		}
		if fn.Type() == "template_function" {
			if id := firstChildAny(fn, []string{"identifier"}); id != nil {
				switch id.Text() {
				case "make_shared", "make_unique", "allocate_shared", "make_optional":
					if args := templateArgs(fn); len(args) > 0 && args[0] != "" {
						return "v:" + args[0]
					}
				}
			}
			return ""
		}
	}
	return recvExpr(v, vars)
}

// cppLike reports whether a .h header is C++ rather than C. Most ROS packages
// name their C++ headers .h; parsed with the C grammar, their classes and
// inline methods were lost.
var cppLike = regexp.MustCompile(`(?m)\b(class|namespace)\s+[A-Za-z_]\w*|template\s*<|\bstd::|\w::\w|^\s*(public|private|protected)\s*:|#include\s*<[a-z_]+>`)

// RulesForSource is RulesForExt refined by content: a C++ .h header gets the
// C++ rules and grammar. It returns the extension the grammar is keyed on.
func RulesForSource(ext string, src []byte) (LangRules, string, bool) {
	if ext == ".h" && cppLike.Match(src) {
		return CppRules, ".hpp", true
	}
	r, ok := RulesForExt(ext)
	return r, ext, ok
}

// typeTable is a ref's merged C++ type facts, for receiver-typed resolution.
type typeTable struct {
	classes map[string]*ClassFact
	aliases map[string]string
	returns map[string]string   // "Class.method" -> return type
	derived map[string][]string // class -> classes naming it as a direct base
	// aliasArgs: an alias's template arguments (using P = X<A>)
	aliasArgs map[string][]string
	// nested: Outer::Inner for each class defined inside another; nestedIn:
	// how many classes nest one of that name (two: the bare name is ambiguous)
	nested   map[string]bool
	nestedIn map[string]int
	nestedBy map[string][]string // Inner -> each Outer::Inner
	enums    map[string]bool     // enumeration type names
	top      map[string]bool     // classes some file defines at namespace scope
	// funcReturns: a free function's return type by name ("" unknown or
	// conflicting); funcNS the namespaces it is declared in; members every
	// class's methods ("Cls.m")
	funcReturns map[string]string
	funcNS      map[string][]string
	members     map[string]bool
	// fileAliases are the namespace-level aliases each source file declares
	// (its own, seen by no other file); file is the source file whose calls
	// are being resolved
	fileAliases map[string]map[string]string
	file        string
	depth       int // nesting of method returns being evaluated
}

// oneReturn is the return type the declarations of one Cls.m agree on: a
// declaration and its definition (ns::T and T, auto returning a field),
// else "" — two classes of one name (each test file's fixture) whose
// methods return different types.
func (tt *typeTable) oneReturn(vs []string) string {
	if len(vs) == 1 {
		return vs[0]
	}
	sort.Strings(vs)
	out, deduced := "", ""
	for _, v := range vs {
		if strings.HasPrefix(v, "f:") || strings.HasPrefix(v, "x:") {
			deduced = v // auto: what the body returns
			continue
		}
		switch {
		case out == "":
			out = v
		case tt.canon(tt.unscope(v)) == tt.canon(tt.unscope(out)):
		case reScope.ReplaceAllString(v, "") == reScope.ReplaceAllString(out, ""):
			// Item in the class body, Outer::Item out of it ([]Item,
			// []Outer::Item): one type, the scoped spelling kept
			if len(v) > len(out) {
				out = v
			}
		default:
			return ""
		}
	}
	if out == "" {
		return deduced
	}
	return out
}

func newTypeTable(files []ParsedFile) *typeTable {
	tt := &typeTable{classes: map[string]*ClassFact{}, aliases: map[string]string{}, returns: map[string]string{}, derived: map[string][]string{}, aliasArgs: map[string][]string{},
		nested: map[string]bool{}, nestedIn: map[string]int{}, nestedBy: map[string][]string{}, enums: map[string]bool{}, top: map[string]bool{},
		funcReturns: map[string]string{}, funcNS: map[string][]string{}, members: map[string]bool{},
		fileAliases: map[string]map[string]string{}}
	returns := map[string][]string{}
	srcOf := map[string]string{} // class -> the source file defining it (one with aliases)
	for _, f := range files {
		if f.Types == nil {
			continue
		}
		for name, cf := range f.Types.Classes {
			m := tt.classes[name]
			if m == nil {
				tt.classes[name] = cf.clone()
				continue
			}
			m.merge(cf)
		}
		for k, v := range f.Types.Aliases {
			if isCSource(f.Path) && !strings.Contains(k, "::") {
				continue // a source file's own alias: that file's (fileAliases)
			}
			tt.aliases[k] = v
		}
		for k, v := range f.Types.AliasArgs {
			tt.aliasArgs[k] = v
		}
		for k := range f.Types.Enums {
			tt.enums[k] = true
		}
		for k := range f.Types.Top {
			tt.top[k] = true
		}
		for k, v := range f.Types.FuncReturns {
			if prev, ok := tt.funcReturns[k]; ok && prev != v {
				v = "" // two files' functions of one name, typed differently
			}
			tt.funcReturns[k] = v
		}
		for k, nss := range f.Types.FuncNS {
			for _, ns := range nss {
				if !containsStr(tt.funcNS[k], ns) {
					tt.funcNS[k] = append(tt.funcNS[k], ns)
				}
			}
		}
		for k := range f.Types.Members {
			tt.members[k] = true
		}
		if isCSource(f.Path) && len(f.Types.Aliases) > 0 {
			tt.fileAliases[f.Path] = f.Types.Aliases
			for name := range f.Types.Classes {
				if _, ok := srcOf[name]; !ok {
					srcOf[name] = f.Path
				}
			}
		}
		for k := range f.Types.Nested {
			if !tt.nested[k] {
				tt.nested[k] = true
				leaf := k[strings.LastIndex(k, "::")+2:]
				tt.nestedIn[leaf]++
				tt.nestedBy[leaf] = append(tt.nestedBy[leaf], k)
			}
		}
		for k, v := range f.Types.Returns {
			if !containsStr(returns[k], v) {
				returns[k] = append(returns[k], v)
			}
		}
	}
	for k, vs := range returns {
		tt.returns[k] = tt.oneReturn(vs)
	}
	names := make([]string, 0, len(tt.classes))
	for n := range tt.classes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		seen := map[string]bool{}
		tt.file = srcOf[n] // a source file's class may name its base by that file's alias
		for _, b := range tt.classes[n].Bases {
			if containsStr(tt.classes[n].TParams, b) {
				continue // a type parameter: whatever the instantiation passes
			}
			b = tt.canon(b)
			if b != n && !seen[b] {
				seen[b] = true
				tt.derived[b] = append(tt.derived[b], n)
			}
		}
	}
	tt.file = ""
	return tt
}

// alias is what name aliases: the source file being resolved's own alias
// first, then the headers' (and class members') ones.
func (tt *typeTable) alias(name string) (string, bool) {
	if a, ok := tt.fileAliases[tt.file][name]; ok {
		return a, true
	}
	a, ok := tt.aliases[name]
	return a, ok
}

// canon follows aliases and the FooPtr naming idiom to a class name.
func (tt *typeTable) canon(t string) string {
	return tt.canonN(t, 0)
}

func (tt *typeTable) canonN(t string, depth int) string {
	if strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") || strings.HasPrefix(t, "pair[") || strings.HasPrefix(t, "tuple[") {
		return t // a container: its element type is canonical where it is read
	}
	for i := 0; i < 8 && t != ""; i++ {
		if a, ok := tt.fileAliases[tt.file][t]; ok && a != t {
			// an alias the source file being resolved declares: what the
			// name means there, whatever another file defines under it
			t = a
			continue
		}
		if _, ok := tt.classes[t]; ok {
			return t
		}
		a, ok := tt.alias(t)
		if !ok {
			break
		}
		t = a
	}
	if _, ok := tt.classes[t]; !ok {
		// Alias::Member: the member alias of the class Alias names
		if i := strings.LastIndex(t, "::"); i > 0 && depth < 4 {
			if sc := tt.canonN(t[:i], depth+1); sc != t[:i] {
				if a, ok := tt.alias(sc + "::" + t[i+2:]); ok {
					if tt.nested[sc+"::"+a] {
						return sc + "::" + a // a member alias of the class's own nested class
					}
					return tt.canonN(a, depth+1)
				}
				if tt.nested[sc+"::"+t[i+2:]] {
					return sc + "::" + t[i+2:] // Alias::Nested: that class's nested class
				}
				if tt.classes[t[i+2:]] != nil {
					return t[i+2:] // Alias::Nested: the nested class
				}
			}
		}
		// ns::Cls written with the repo's own namespace: the class (or the
		// alias) Cls
		if i := strings.LastIndex(t, "::"); i > 0 {
			root, _, _ := strings.Cut(t, "::")
			if leaf := t[i+2:]; !externalScopes[root] {
				if tt.classes[leaf] != nil {
					return leaf
				}
				if _, ok := tt.alias(leaf); ok && depth < 4 {
					return tt.canonN(leaf, depth+1) // using X = ns::X: not followed in circles
				}
			}
		}
		for _, suf := range []string{"ConstPtr", "Ptr"} {
			if base := strings.TrimSuffix(t, suf); base != t {
				if _, ok := tt.classes[base]; ok {
					return base
				}
			}
		}
	}
	return t
}

// open reports whether cls's lineage has a base it cannot name: a class
// template's type parameter its instantiation does not tell — a method
// missing from the known lineage may then be that base's.
func (tt *typeTable) open(cls string) bool {
	for _, c := range tt.lineage(cls) {
		cf := tt.classes[c]
		if cf == nil {
			continue
		}
		for _, b := range cf.Bases {
			if containsStr(cf.TParams, b) {
				return true
			}
		}
	}
	return false
}

// nestedName is t as Outer::Inner when it names a class nested in another
// (written Outer::Inner, or Outer::Alias for one), else "".
func (tt *typeTable) nestedName(t, callerRecv string) string {
	if tt.nested[t] {
		return t
	}
	i := strings.LastIndex(t, "::")
	if i <= 0 {
		return ""
	}
	// the scope may be an alias the caller's class declares
	sc, leaf := tt.canon(tt.inClass(t[:i], callerRecv)), t[i+2:]
	if tt.nested[sc+"::"+leaf] {
		return sc + "::" + leaf
	}
	if a, ok := tt.alias(sc + "::" + leaf); ok {
		if tt.nested[sc+"::"+a] {
			return sc + "::" + a
		}
	}
	return ""
}

// ambiguousNested: name is a class nested in two different classes, and no
// class of that name is defined at namespace scope, so the bare name does
// not say which.
func (tt *typeTable) ambiguousNested(name string) bool {
	return tt.nestedIn[name] > 1 && !tt.top[name]
}

// unscope drops the repo namespace from ns::Name when Name is a class or an
// alias the type table knows and ns::Name itself is not.
func (tt *typeTable) unscope(t string) string {
	if tt.classes[t] != nil {
		return t
	}
	if _, ok := tt.alias(t); ok {
		return t
	}
	i := strings.LastIndex(t, "::")
	if i <= 0 {
		return t
	}
	root, _, _ := strings.Cut(t, "::")
	leaf := t[i+2:]
	if externalScopes[root] {
		return t
	}
	if _, ok := tt.alias(leaf); ok || tt.classes[leaf] != nil {
		return leaf
	}
	return t
}

// ownNested is t written inside class c: c's own nested class of that name
// (std::unique_ptr<Impl> _impl in Foo is Foo::Impl), or that of a class c
// is nested in (Foo::Handle's Impl is Foo::Impl), else t.
func (tt *typeTable) ownNested(c, t string) string {
	for i := 0; i < 4 && c != ""; i++ {
		if tt.nested[c+"::"+t] {
			return c + "::" + t
		}
		c = tt.enclosing(c)
	}
	return t
}

// enclosing is the class c is nested in ("" when c is not a nested class,
// or the bare name nests in several classes).
func (tt *typeTable) enclosing(c string) string {
	if i := strings.LastIndex(c, "::"); i > 0 {
		if tt.nested[c] {
			return c[:i]
		}
		return ""
	}
	if ns := tt.nestedBy[c]; len(ns) == 1 && !tt.top[c] {
		return ns[0][:strings.LastIndex(ns[0], "::")]
	}
	return ""
}

// inClass resolves t as written in a method of callerRecv: an alias the
// class (or a base) declares, else t itself.
func (tt *typeTable) inClass(t, callerRecv string) string {
	if callerRecv != "" {
		if n := tt.ownNested(strings.SplitN(callerRecv, "@", 2)[0], t); n != t {
			return n
		}
	}
	if callerRecv == "" {
		return t
	}
	if _, ok := tt.classAlias(t, callerRecv); ok {
		return tt.inScope(t, callerRecv, 0) // aliases inside it too (std::array<MotorDriver, N>)
	}
	if head, rest, scoped := strings.Cut(t, "::"); scoped {
		// typename Alias::Member with the class's own Alias: through it
		if a, ok := tt.classAlias(head, callerRecv); ok {
			if a == "" {
				return "" // Alias is a template parameter: the instantiation's
			}
			return a + "::" + rest
		}
	}
	return t
}

// inScope is type t as written in class c: its aliases followed, inside a
// container's element type too (std::array<MotorDriver, N> with the class's
// using MotorDriver = …); "" when an alias is the build's to pick.
func (tt *typeTable) inScope(t, c string, depth int) string {
	if depth > 4 || t == "" {
		return t
	}
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		if e := tt.inScope(elem, c, depth+1); e != "" {
			return "[]" + e
		}
		return ""
	}
	if a, ok := tt.classAlias(t, c); ok {
		if a == "" {
			return ""
		}
		return tt.inScope(a, c, depth+1)
	}
	return t
}

// classAlias is the type alias t names in cls (or a base): it shadows a
// class of the same name outside (using MotorDriver = Traits::MotorDriver;
// is not the class template MotorDriver). An alias of a template
// parameter's member type is the instantiation's: "".
func (tt *typeTable) classAlias(t, cls string) (string, bool) {
	cs, binds := tt.lineageBound(cls)
	for i, c := range cs {
		cf := tt.classes[c]
		if cf == nil || len(cf.Aliases) == 0 {
			continue
		}
		if a, ok := cf.Aliases[t]; ok {
			if head, _, _ := strings.Cut(a, "::"); containsStr(cf.TParams, head) {
				if b := binds[i][head]; b != "" && !strings.Contains(a, "::") {
					return b, true // using B = Param; in a base the class instantiates
				}
				return "", true // using B = Param; (or Param::X): the instantiation's
			}
			return a, true
		}
	}
	return "", false
}

// lineage is cls and its bases, nearest first. A class template's base that
// is one of its type parameters is the argument its instantiation passes
// (using P = X<Real>; X<B> : B — P's base is Real).
func (tt *typeTable) lineage(cls string) []string {
	out, _ := tt.lineageBound(cls)
	return out
}

// lineageBound is lineage with, for each class template on it, what its
// type parameters are bound to there (nil: none known): CRTP's
// Base<Derived> has T = Derived.
func (tt *typeTable) lineageBound(cls string) ([]string, []map[string]string) {
	type inst struct {
		name string
		args []string
	}
	var out []string
	var binds []map[string]string
	seen := map[string]bool{}
	queue := []inst{{name: cls}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		name, args := tt.unscope(n.name), n.args
		for i := 0; i < 8 && tt.classes[name] == nil; i++ {
			a, ok := tt.alias(name)
			if !ok {
				break
			}
			if aa := tt.aliasArgs[name]; len(aa) > 0 {
				args = aa
			}
			name = a
		}
		c := tt.canon(name)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		binds = append(binds, nil)
		cf := tt.classes[c]
		if cf == nil {
			continue
		}
		outer := ""
		if i := strings.LastIndex(c, "::"); i > 0 && tt.nested[c] {
			outer = c[:i] // a nested class's bases are first its siblings
		}
		sibling := func(t string) string {
			if outer != "" && tt.nested[outer+"::"+t] {
				return outer + "::" + t
			}
			return t
		}
		bind := func(t string) (string, []string) {
			i := indexStr(cf.TParams, t)
			if i >= 0 && i < len(args) && args[i] != "" {
				return args[i], nil
			}
			if i >= 0 && i < len(cf.TDefaults) && cf.TDefaults[i] != "" {
				var dargs []string // X<> takes the parameter's default
				if i < len(cf.TDefaultArgs) {
					dargs = cf.TDefaultArgs[i]
				}
				return sibling(cf.TDefaults[i]), dargs
			}
			return t, nil
		}
		for _, p := range cf.TParams {
			if b, _ := bind(p); b != p && b != "" {
				if binds[len(binds)-1] == nil {
					binds[len(binds)-1] = map[string]string{}
				}
				binds[len(binds)-1][p] = b
			}
		}
		for _, b := range cf.Bases {
			var bargs []string
			for _, a := range cf.BaseArgs[b] {
				ba, _ := bind(a)
				bargs = append(bargs, ba)
			}
			if bound, dargs := bind(b); bound != b {
				queue = append(queue, inst{name: bound, args: dargs})
				continue
			}
			if containsStr(cf.TParams, b) {
				continue // a type parameter the instantiation does not tell
			}
			queue = append(queue, inst{name: sibling(b), args: bargs})
		}
	}
	return out, binds
}

var (
	reTypeWord = regexp.MustCompile(`[A-Za-z_]\w*`)
	reScope    = regexp.MustCompile(`[A-Za-z_]\w*::`)
)

// substParams is type t with the template parameters m binds replaced
// (T& with T = Derived is Derived; []T is []Derived).
func substParams(t string, m map[string]string) string {
	if len(m) == 0 || t == "" {
		return t
	}
	return reTypeWord.ReplaceAllStringFunc(t, func(w string) string {
		if b, ok := m[w]; ok {
			return b
		}
		return w
	})
}

func indexStr(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

// descendants is every class deriving (transitively) from cls.
func (tt *typeTable) descendants(cls string) []string {
	var out []string
	seen := map[string]bool{cls: true}
	queue := append([]string(nil), tt.derived[cls]...)
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
		queue = append(queue, tt.derived[c]...)
	}
	return out
}

// fieldType is the declared type of member field on cls or a base.
func (tt *typeTable) fieldType(cls, field string) (string, bool) {
	cs, binds := tt.lineageBound(cls)
	for i, c := range cs {
		if cf := tt.classes[c]; cf != nil {
			if t, ok := cf.Fields[field]; ok {
				if b := binds[i]; b != nil {
					if s := substParams(t, b); s != t {
						return s, true // a template base's field of a parameter's type
					}
				}
				return tt.inScope(tt.ownNested(c, t), c, 0), true
			}
		}
	}
	return "", false
}

// hasMember reports whether cls's lineage may define a method m: it
// declares one, or a base is a class the repo does not define (a library's,
// a template parameter) whose members are not known.
func (tt *typeTable) hasMember(cls, m string) bool {
	if tt.open(cls) {
		return true
	}
	for _, c := range tt.lineage(cls) {
		if tt.classes[c] == nil {
			return true
		}
		if i := strings.LastIndex(c, "::"); i > 0 && tt.nested[c] {
			c = c[i+2:]
		}
		if tt.members[c+"."+m] {
			return true
		}
	}
	return false
}

// funcReturn is the return type of the repo's free function name, when one
// is declared; with scope set (ns::name), only a function declared in a
// namespace ending in scope.
func (tt *typeTable) funcReturn(name, scope string) string {
	if scope != "" {
		found := false
		for _, ns := range tt.funcNS[name] {
			if ns == scope || strings.HasSuffix(ns, "::"+scope) {
				found = true
			}
		}
		if !found {
			return ""
		}
	}
	return tt.funcReturns[name]
}

// methodReturn is the declared return type of cls.m on cls or a base (an
// auto method returning a field: that field's type).
func (tt *typeTable) methodReturn(cls, m string) string {
	cs, binds := tt.lineageBound(cls)
	for k, c := range cs {
		if i := strings.LastIndex(c, "::"); i > 0 && tt.nested[c] {
			c = c[i+2:] // a nested class's methods are recorded by its name
		}
		if t, ok := tt.returns[c+"."+m]; ok {
			if b := binds[k]; b != nil && !strings.HasPrefix(t, "f:") && !strings.HasPrefix(t, "x:") {
				if s := substParams(t, b); s != t {
					return s // T& with() in a CRTP base Base<Derived>: Derived
				}
			}
			if f, isField := strings.CutPrefix(t, "f:"); isField {
				t, _ = tt.fieldType(c, f)
				t = substParams(t, binds[k])
			} else if x, isExpr := strings.CutPrefix(t, "x:"); isExpr {
				if tt.depth > 4 {
					return "" // a method's return evaluated through itself
				}
				tt.depth++
				t = tt.eval(x, c)
				tt.depth--
			} else {
				t = tt.inScope(t, c, 0) // the class's aliases, as its declaration reads them
			}
			return t
		}
	}
	return ""
}

// eval evaluates a receiver expression (see recvExpr) in a method of
// callerRecv to a type; "" when any step is unknown.
func (tt *typeTable) eval(expr, callerRecv string) string {
	if strings.Contains(expr, "|") {
		t := ""
		for i, alt := range strings.Split(expr, "|") {
			at := tt.eval(alt, callerRecv)
			if at == "" || i > 0 && at != t {
				return ""
			}
			t = at
		}
		return t
	}
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return ""
	}
	t := ""
	switch head := toks[0]; {
	case strings.HasPrefix(head, "v:"):
		t = tt.inClass(head[2:], callerRecv)
	case strings.HasPrefix(head, "f:"):
		if callerRecv == "" {
			return ""
		}
		t, _ = tt.fieldType(callerRecv, head[2:])
	case head == "c:":
		t = callerRecv
	case strings.HasPrefix(head, "q:"):
		// Cls::f(): Cls's static method's return; ns::X(): a temporary of X
		scope, name, _ := strings.Cut(head[2:], "::")
		if n := tt.nestedName(head[2:], callerRecv); n != "" {
			t = n // Outer::Alias(…): a temporary of Outer's nested class
		} else if c := tt.canon(head[2:]); tt.classes[c] != nil {
			t = c // Cls::Alias(…): a temporary of the class the alias names
		} else if c := tt.canon(scope); tt.classes[c] != nil {
			t = tt.methodReturn(c, name)
		} else if c := tt.canon(name); tt.classes[c] != nil {
			t = c
		} else if !externalScopes[scope] {
			t = tt.funcReturn(name, scope) // ns::make(): what the repo's ns::make returns
		}
	case strings.HasPrefix(head, "t:"):
		name := head[2:]
		if c := tt.canon(name); tt.classes[c] != nil {
			t = c
		} else if callerRecv != "" {
			t = tt.methodReturn(callerRecv, name)
			if t == "" && !tt.hasMember(callerRecv, name) {
				t = tt.funcReturn(name, "") // make() in a method: a free function
			}
		} else {
			t = tt.funcReturn(name, "")
		}
	}
	for _, op := range toks[1:] {
		if t = tt.canon(t); t == "" {
			return ""
		}
		switch {
		case strings.HasPrefix(op, "get") && strings.HasPrefix(t, "tuple["):
			elems := strings.Split(strings.TrimSuffix(strings.TrimPrefix(t, "tuple["), "]"), ";")
			t = ""
			if i, err := strconv.Atoi(strings.TrimPrefix(op, "get#")); err == nil && i < len(elems) {
				t = elems[i]
			} else if want := strings.TrimPrefix(op, "get:"); want != op {
				for _, e := range elems {
					if e == want || typeLeaf(e) == typeLeaf(want) {
						t = e
					}
				}
			}
		case op == "[]" || op == "it":
			t = elemOf(t, op == "it")
		case op == ".first" || op == ".second":
			if k, v, ok := splitKV(t, "pair["); ok {
				if op == ".first" {
					t = k
				} else {
					t = v
				}
				continue
			}
			t, _ = tt.fieldType(t, op[1:])
		case strings.HasPrefix(op, "."):
			if tt.classes[t] == nil {
				return ""
			}
			t, _ = tt.fieldType(t, op[1:])
		case strings.HasPrefix(op, "()"):
			if tt.classes[t] == nil {
				return ""
			}
			t = tt.methodReturn(t, op[2:])
		default:
			return ""
		}
	}
	if n := tt.nestedName(t, callerRecv); n != "" {
		return n // Outer::Inner: which of two same-named nested classes
	}
	return tt.canon(t)
}

// elemOf is what indexing (or iterating) a container type yields.
func elemOf(t string, iterate bool) string {
	if strings.HasPrefix(t, "[]") {
		return t[2:]
	}
	if k, v, ok := splitKV(t, "map["); ok {
		if iterate {
			return "pair[" + k + "]" + v
		}
		return v
	}
	return ""
}

// splitKV splits "map[K]V" / "pair[K]V" (K may itself hold brackets).
func splitKV(t, prefix string) (k, v string, ok bool) {
	if !strings.HasPrefix(t, prefix) {
		return "", "", false
	}
	depth := 1
	for i := len(prefix); i < len(t); i++ {
		switch t[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return t[len(prefix):i], t[i+1:], true
			}
		}
	}
	return "", "", false
}

var castNames = map[string]bool{"static_cast": true, "const_cast": true, "dynamic_cast": true, "reinterpret_cast": true,
	"static_pointer_cast": true, "dynamic_pointer_cast": true, "const_pointer_cast": true}
