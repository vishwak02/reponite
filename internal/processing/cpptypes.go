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
	// FuncNS are the namespaces ("a::b", "" global) each free function this
	// file declares or defines is in.
	FuncNS map[string][]string
	// Defaults are the function and method names this file declares with a
	// defaulted parameter (f(int a, int b = 0);): their out-of-line
	// definitions take fewer arguments than they list.
	Defaults map[string]bool
}

// ClassFact is a class's member fields (name -> declared type) and bases.
type ClassFact struct {
	Fields map[string]string
	Bases  []string
	// TParams are a class template's type parameters, in order; BaseArgs a
	// base's template arguments as written (Base<A, B> -> [A B]). A base that
	// is a type parameter (template <class B> class X : public B) is the
	// argument the instantiation passes.
	TParams  []string
	BaseArgs map[string][]string
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
	return CppTypeFacts(root, r)
}

// CppTypeFacts collects a C++ file's class, field, base, alias and method
// return-type facts; nil for other languages.
func CppTypeFacts(root content.AST, r LangRules) *TypeFacts {
	if r.Name != "cpp" || root == nil {
		return nil
	}
	tf := &TypeFacts{Classes: map[string]*ClassFact{}, Aliases: map[string]string{}, Returns: map[string]string{},
		Internal: map[string]bool{}, Declared: map[string]bool{}, AliasArgs: map[string][]string{}, FuncNS: map[string][]string{}, Defaults: map[string]bool{}}
	var nsStack []string // the named namespaces around the walk
	inNS := func(name string, extra ...string) {
		ns := strings.Join(append(append([]string(nil), nsStack...), extra...), "::")
		if !containsStr(tf.FuncNS[name], ns) {
			tf.FuncNS[name] = append(tf.FuncNS[name], ns)
		}
	}
	anon := 0                     // depth of anonymous namespaces around the walk
	var tparams []string          // the template parameters of the declaration being walked
	external := map[string]bool{} // names this file also declares with external linkage
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
			case "template_declaration":
				if pl := firstChildAny(c, []string{"template_parameter_list"}); pl != nil {
					var ps []string
					for _, p := range pl.Children() {
						if p.Type() == "type_parameter_declaration" || p.Type() == "optional_type_parameter_declaration" || p.Type() == "variadic_type_parameter_declaration" {
							if id := firstChildAny(p, []string{"type_identifier"}); id != nil {
								ps = append(ps, id.Text())
							}
						}
					}
					outer := tparams
					tparams = ps
					walk(c, class)
					tparams = outer
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
				}
				walk(c, class)
				nsStack = outer
				continue
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
				name := ""
				if id := firstChildAny(c, []string{"type_identifier"}); id != nil {
					name = id.Text()
				} else if q := firstChildAny(c, []string{"qualified_identifier"}); q != nil {
					name = TypeName(q) // class Outer::Impl { … }: the nested class Impl
					if i := strings.LastIndex(name, "::"); i >= 0 {
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
				if anon > 0 && class == "" {
					tf.Internal[name] = true
				}
				cf := tf.Classes[name]
				if cf == nil {
					cf = &ClassFact{Fields: map[string]string{}}
					tf.Classes[name] = cf
				}
				if n.Type() == "template_declaration" && len(tparams) > 0 && len(cf.TParams) == 0 {
					cf.TParams = tparams
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
				for _, ad := range body.Children() {
					if ad.Type() != "alias_declaration" {
						continue
					}
					id := firstChildAny(ad, []string{"type_identifier"})
					if td := firstChildAny(ad, []string{"type_descriptor"}); id != nil && td != nil {
						if t := TypeName(td); t != "" && t != id.Text() {
							local[id.Text()] = t
						}
					}
				}
				for _, fd := range body.Children() {
					if fd.Type() != "field_declaration" {
						continue
					}
					t := TypeName(firstChildAny(fd, typeNodeTypes))
					if a, ok := local[t]; ok {
						t = a
					}
					for _, d := range fd.Children() {
						if f := declaredName(d, "field_identifier"); f != "" {
							cf.Fields[f] = t
						} else if m := methodDeclName(d); m != "" && t != "" {
							tf.Returns[name+"."+m] = t // in-class method declaration
						}
					}
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
						if anon > 0 || hasStatic(c) {
							tf.Internal[n] = true
						} else {
							external[n] = true
						}
					}
				}
				if d := declaratorOf(c, CppRules); d != nil && t != "" {
					cls, m := class, ""
					if q := firstDescBefore(d, "qualified_identifier", "parameter"); q != nil {
						kids := q.Children()
						cls, m = scopeName(kids[0]), declaratorName(d, CppRules)
					} else if class != "" {
						m = declaratorName(d, CppRules)
					}
					if cls != "" && m != "" {
						tf.Returns[cls+"."+m] = t
					}
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
			tf.Defaults[n] = true
		}
	}
	for n := range external {
		delete(tf.Internal, n) // overloads of one name, some internal: the name is not
	}
	if len(tf.Classes) == 0 && len(tf.Aliases) == 0 && len(tf.Returns) == 0 && len(tf.Includes) == 0 && len(tf.Declared) == 0 {
		return nil
	}
	return tf
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
		return ""
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
				if w := id.Text(); smartPointers[w] || seqContainers[w] || mapContainers[w] {
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
	passMembers = map[string]bool{"get": true, "lock": true, "value": true}
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
		if len(k) > 0 && k[0].Type() == "qualified_identifier" {
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
			if name == "" {
				continue
			}
			switch {
			case !isAuto:
				set(name, typed(t))
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
}

func newTypeTable(files []ParsedFile) *typeTable {
	tt := &typeTable{classes: map[string]*ClassFact{}, aliases: map[string]string{}, returns: map[string]string{}, derived: map[string][]string{}, aliasArgs: map[string][]string{}}
	for _, f := range files {
		if f.Types == nil {
			continue
		}
		for name, cf := range f.Types.Classes {
			m := tt.classes[name]
			if m == nil {
				m = &ClassFact{Fields: map[string]string{}}
				tt.classes[name] = m
			}
			for k, v := range cf.Fields {
				if old, seen := m.Fields[k]; seen && old != v {
					// two classes of one name (nested in different outer
					// classes) giving the field different types: unknown
					v = ""
				}
				m.Fields[k] = v
			}
			m.Bases = append(m.Bases, cf.Bases...)
			if len(m.TParams) == 0 {
				m.TParams = cf.TParams
			}
			for b, a := range cf.BaseArgs {
				if m.BaseArgs == nil {
					m.BaseArgs = map[string][]string{}
				}
				m.BaseArgs[b] = a
			}
		}
		for k, v := range f.Types.Aliases {
			tt.aliases[k] = v
		}
		for k, v := range f.Types.AliasArgs {
			tt.aliasArgs[k] = v
		}
		for k, v := range f.Types.Returns {
			tt.returns[k] = v
		}
	}
	names := make([]string, 0, len(tt.classes))
	for n := range tt.classes {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		seen := map[string]bool{}
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
	return tt
}

// canon follows aliases and the FooPtr naming idiom to a class name.
func (tt *typeTable) canon(t string) string {
	for i := 0; i < 8 && t != ""; i++ {
		if _, ok := tt.classes[t]; ok {
			return t
		}
		a, ok := tt.aliases[t]
		if !ok {
			break
		}
		t = a
	}
	if _, ok := tt.classes[t]; !ok {
		// ns::Cls written with the repo's own namespace: the class Cls
		if i := strings.LastIndex(t, "::"); i > 0 {
			root, _, _ := strings.Cut(t, "::")
			if leaf := t[i+2:]; !externalScopes[root] && tt.classes[leaf] != nil {
				return leaf
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

// lineage is cls and its bases, nearest first. A class template's base that
// is one of its type parameters is the argument its instantiation passes
// (using P = X<Real>; X<B> : B — P's base is Real).
func (tt *typeTable) lineage(cls string) []string {
	type inst struct {
		name string
		args []string
	}
	var out []string
	seen := map[string]bool{}
	queue := []inst{{name: cls}}
	for len(queue) > 0 {
		n := queue[0]
		queue = queue[1:]
		name, args := n.name, n.args
		for i := 0; i < 8 && tt.classes[name] == nil; i++ {
			a, ok := tt.aliases[name]
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
		cf := tt.classes[c]
		if cf == nil {
			continue
		}
		bind := func(t string) string {
			if i := indexStr(cf.TParams, t); i >= 0 && i < len(args) {
				return args[i]
			}
			return t
		}
		for _, b := range cf.Bases {
			var bargs []string
			for _, a := range cf.BaseArgs[b] {
				bargs = append(bargs, bind(a))
			}
			if bound := bind(b); bound != b {
				queue = append(queue, inst{name: bound})
				continue
			}
			if containsStr(cf.TParams, b) {
				continue // a type parameter the instantiation does not tell
			}
			queue = append(queue, inst{name: b, args: bargs})
		}
	}
	return out
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
	for _, c := range tt.lineage(cls) {
		if cf := tt.classes[c]; cf != nil {
			if t, ok := cf.Fields[field]; ok {
				return t, true
			}
		}
	}
	return "", false
}

// methodReturn is the declared return type of cls.m on cls or a base (an
// auto method returning a field: that field's type).
func (tt *typeTable) methodReturn(cls, m string) string {
	for _, c := range tt.lineage(cls) {
		if t, ok := tt.returns[c+"."+m]; ok {
			if f, isField := strings.CutPrefix(t, "f:"); isField {
				t, _ = tt.fieldType(c, f)
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
		t = head[2:]
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
		if c := tt.canon(scope); tt.classes[c] != nil {
			t = tt.methodReturn(c, name)
		} else if c := tt.canon(name); tt.classes[c] != nil {
			t = c
		}
	case strings.HasPrefix(head, "t:"):
		name := head[2:]
		if c := tt.canon(name); tt.classes[c] != nil {
			t = c
		} else if callerRecv != "" {
			t = tt.methodReturn(callerRecv, name)
		}
	}
	for _, op := range toks[1:] {
		if t = tt.canon(t); t == "" {
			return ""
		}
		switch {
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
