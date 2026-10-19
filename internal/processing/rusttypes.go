package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// Rust receiver typing: enough to tell which type a method call's receiver
// has in the common cases — self and its fields, parameters, let bindings
// with a type or a constructor (Type::new(..), Type { .. }), enum variants,
// and the declared return types of the crate's own methods. A receiver of a
// type the crate does not define is a library value: its methods are the
// library's, never a crate method of the same name.

// rustExt marks a type the crate does not define.
const rustExt = "\x00ext"

// RustTypeFacts collects a Rust file's struct field types, its types (structs,
// enums, traits) and its methods' return types.
func RustTypeFacts(root content.AST) *TypeFacts {
	tf := &TypeFacts{Classes: map[string]*ClassFact{}, Aliases: map[string]string{}, Returns: map[string]string{}}
	var walk func(n content.AST, impl string)
	walk = func(n content.AST, impl string) {
		for _, c := range n.Children() {
			switch c.Type() {
			case "struct_item", "enum_item", "union_item", "trait_item":
				name := ""
				for _, k := range c.Children() {
					if k.Type() == "type_identifier" {
						name = k.Text()
						break
					}
				}
				if name == "" {
					continue
				}
				cf := tf.Classes[name]
				if cf == nil {
					cf = &ClassFact{Fields: map[string]string{}}
					tf.Classes[name] = cf
				}
				if c.Type() == "struct_item" {
					for _, fl := range descendantsAny(c, []string{"field_declaration"}) {
						var fname string
						var ftype content.AST
						for _, k := range fl.Children() {
							switch {
							case k.Type() == "field_identifier":
								fname = k.Text()
							case k.IsNamed() && k.Type() != "visibility_modifier" && fname != "":
								ftype = k
							}
						}
						if fname != "" && ftype != nil {
							cf.Fields[fname] = rustNormType(ftype, name)
						}
					}
				}
				if c.Type() == "trait_item" {
					walk(c, name)
				}
			case "impl_item":
				walk(c, rustImplType(c))
			case "declaration_list":
				walk(c, impl)
			case "function_item", "function_signature_item":
				if impl == "" {
					continue
				}
				name, ret := "", content.AST(nil)
				seenParams := false
				for _, k := range c.Children() {
					switch {
					case k.Type() == "identifier" && name == "":
						name = k.Text()
					case k.Type() == "parameters":
						seenParams = true
					case seenParams && k.IsNamed() && k.Type() != "block" && k.Type() != "where_clause" && ret == nil:
						ret = k
					}
				}
				if name != "" && ret != nil {
					if t := rustNormType(ret, impl); t != "" {
						tf.Returns[impl+"."+name] = t
					}
				}
			case "mod_item":
				walk(c, "")
			}
		}
	}
	walk(root, "")
	return tf
}

// rustUnwraps take the value out of a Result or Option.
var rustUnwraps = map[string]bool{"unwrap": true, "expect": true, "unwrap_or": true, "unwrap_or_else": true,
	"unwrap_or_default": true, "unwrap_unchecked": true}

// rustSmartPointers deref to the type they hold: a method call through one
// reaches the held type's method.
var rustSmartPointers = map[string]bool{"Box": true, "Arc": true, "Rc": true, "Cow": true}

// rustNormType reduces a written type to what method lookup needs: the
// crate type's name (Self as the impl's), Result<T>/Option<T> kept as a
// wrapper (? unwraps it), any other library type as itself, "" when unknown.
func rustNormType(t content.AST, self string) string {
	switch t.Type() {
	case "type_identifier", "identifier":
		if t.Text() == "Self" {
			return self
		}
		return t.Text()
	case "reference_type", "pointer_type":
		for _, c := range t.Children() {
			if c.IsNamed() && c.Type() != "lifetime" && c.Type() != "mutable_specifier" {
				return rustNormType(c, self)
			}
		}
	case "scoped_type_identifier":
		return rustTypeName(t)
	case "generic_type":
		var base string
		var args []content.AST
		for _, c := range t.Children() {
			switch {
			case c.Type() == "type_arguments":
				for _, a := range c.Children() {
					if a.IsNamed() && a.Type() != "lifetime" {
						args = append(args, a)
					}
				}
			case c.IsNamed() && base == "":
				base = rustTypeName(c)
			}
		}
		switch {
		case rustSmartPointers[base] && len(args) > 0:
			return rustNormType(args[len(args)-1], self)
		case (base == "Result" || base == "Option") && len(args) > 0:
			if inner := rustNormType(args[0], self); inner != "" {
				return base + "<" + inner + ">"
			}
			return base
		}
		return base
	}
	return ""
}

// rustVarTypes are a function's parameter and let-bound names with the
// receiver expression (recvExpr tokens) each holds.
func rustVarTypes(fn content.AST, self string) map[string]string {
	vars := map[string]string{}
	if ps := firstChildAny(fn, []string{"parameters"}); ps != nil {
		for _, p := range ps.Children() {
			if p.Type() != "parameter" {
				continue
			}
			var name string
			var typ content.AST
			for _, k := range p.Children() {
				switch {
				case k.Type() == "identifier" && name == "":
					name = k.Text()
				case k.Type() == "mut_pattern" && name == "":
					if id := firstChildAny(k, []string{"identifier"}); id != nil {
						name = id.Text()
					}
				case k.IsNamed() && name != "" && typ == nil:
					typ = k
				}
			}
			if name != "" && typ != nil {
				if t := rustNormType(typ, self); t != "" {
					vars[name] = "v:" + t
				}
			}
		}
	}
	body := firstChildAny(fn, []string{"block"})
	if body == nil {
		return vars
	}
	for _, let := range descendantsAny(body, []string{"let_declaration"}) {
		var name string
		var typ, value content.AST
		afterEq := false
		for _, k := range let.Children() {
			switch {
			case k.Type() == "=":
				afterEq = true
			case k.Type() == "identifier" && name == "" && !afterEq:
				name = k.Text()
			case k.Type() == "mut_pattern" && name == "" && !afterEq:
				if id := firstChildAny(k, []string{"identifier"}); id != nil {
					name = id.Text()
				}
			case k.IsNamed() && !afterEq && name != "" && typ == nil && k.Type() != "mutable_specifier":
				typ = k
			case k.IsNamed() && afterEq && value == nil:
				value = k
			}
		}
		if name == "" {
			continue
		}
		switch {
		case typ != nil:
			if t := rustNormType(typ, self); t != "" {
				vars[name] = "v:" + t
			} else {
				delete(vars, name)
			}
		case value != nil:
			if e := rustRecvExpr(value, vars); e != "" {
				vars[name] = e
			} else {
				delete(vars, name) // shadowed by a value of unknown type
			}
		}
	}
	return vars
}

// rustRecvExpr encodes an expression as receiver tokens for typeTable
// evaluation: "c:" self, "f:x" self.x, "v:T" a value of type T, "s:T.f" the
// result of T::f(..), then ".x" (field), "()m" (method result), "?" (unwrap).
// "" when the expression's type cannot be followed.
func rustRecvExpr(n content.AST, vars map[string]string) string {
	switch n.Type() {
	case "self":
		return "c:"
	case "identifier":
		return vars[n.Text()]
	case "reference_expression", "unary_expression", "parenthesized_expression":
		for _, c := range n.Children() {
			if c.IsNamed() && c.Type() != "mutable_specifier" {
				return rustRecvExpr(c, vars)
			}
		}
	case "try_expression":
		for _, c := range n.Children() {
			if c.IsNamed() {
				if b := rustRecvExpr(c, vars); b != "" {
					return b + " ?"
				}
				return ""
			}
		}
	case "field_expression":
		var value, field content.AST
		for _, c := range n.Children() {
			switch {
			case c.Type() == "field_identifier":
				field = c
			case c.IsNamed() && value == nil:
				value = c
			}
		}
		if value == nil || field == nil {
			return ""
		}
		b := rustRecvExpr(value, vars)
		switch b {
		case "":
			return ""
		case "c:":
			return "f:" + field.Text()
		}
		return b + " ." + field.Text()
	case "call_expression":
		kids := n.Children()
		if len(kids) == 0 {
			return ""
		}
		fn := kids[0]
		if fn.Type() == "generic_function" {
			for _, c := range fn.Children() {
				if c.IsNamed() {
					fn = c
					break
				}
			}
		}
		switch fn.Type() {
		case "field_expression":
			var value, m content.AST
			for _, c := range fn.Children() {
				switch {
				case c.Type() == "field_identifier":
					m = c
				case c.IsNamed() && value == nil:
					value = c
				}
			}
			if value == nil || m == nil {
				return ""
			}
			if b := rustRecvExpr(value, vars); b != "" {
				return b + " ()" + m.Text()
			}
		case "scoped_identifier":
			segs := rustPath(fn)
			if len(segs) >= 2 && (segs[len(segs)-2] == "Self" || isTypeName(segs[len(segs)-2])) {
				return "s:" + segs[len(segs)-2] + "." + segs[len(segs)-1]
			}
		}
	case "scoped_identifier": // Enum::Variant
		segs := rustPath(n)
		if len(segs) >= 2 && isTypeName(segs[len(segs)-2]) {
			return "v:" + segs[len(segs)-2]
		}
	case "struct_expression":
		for _, c := range n.Children() {
			if c.Type() == "type_identifier" || c.Type() == "scoped_type_identifier" || c.Type() == "generic_type_with_turbofish" {
				if t := rustTypeName(c); t != "" {
					return "v:" + t
				}
			}
		}
	}
	return ""
}

// evalDeclared evaluates receiver tokens (Rust, Java) in a method of
// callerRecv: the repo type the value has (ext false), a library type (ext
// true; its name, or "" when only its being foreign is known), or ("",
// false) when unknown.
func (tt *typeTable) evalDeclared(expr, callerRecv string) (string, bool) {
	t := tt.evalDeclaredRaw(expr, callerRecv)
	if t == rustExt {
		return "", true // foreign, its type's name lost along the chain
	}
	if strings.HasPrefix(t, rustExt+":") {
		return strings.TrimPrefix(t, rustExt+":"), true
	}
	return t, false
}

func (tt *typeTable) evalDeclaredRaw(expr, callerRecv string) string {
	toks := strings.Fields(expr)
	if len(toks) == 0 {
		return ""
	}
	known := func(t string) bool { return tt.classes[t] != nil }
	var t string
	switch head := toks[0]; {
	case head == "c:":
		t = callerRecv
	case strings.HasPrefix(head, "f:"):
		if callerRecv == "" || !known(callerRecv) {
			return ""
		}
		var ok bool
		if t, ok = tt.fieldType(callerRecv, head[2:]); !ok {
			return ""
		}
	case strings.HasPrefix(head, "v:"):
		t = head[2:]
		if t == "Self" {
			t = callerRecv
		}
	case strings.HasPrefix(head, "s:"):
		typ, fn, _ := strings.Cut(head[2:], ".")
		if typ == "Self" {
			typ = callerRecv
		}
		if !known(typ) {
			t = rustExt + ":" + typ
			break
		}
		t = tt.returns[typ+"."+fn]
	}
	for _, op := range toks[1:] {
		switch {
		case t == "":
			return ""
		case strings.HasPrefix(t, rustExt):
			t = rustExt // a library value's fields and methods stay the library's
		case op == "?":
			if i := strings.Index(t, "<"); i > 0 && strings.HasSuffix(t, ">") {
				t = t[i+1 : len(t)-1]
			} else {
				return ""
			}
		case strings.HasPrefix(op, "."):
			if len(t) == 1 {
				return "" // a generic parameter's field
			}
			if !known(t) {
				t = rustExt
				continue
			}
			f, ok := tt.fieldType(t, op[1:])
			if !ok {
				return ""
			}
			t = f
		case strings.HasPrefix(op, "()"):
			if len(t) == 1 {
				return "" // a generic parameter's method: any type's
			}
			if i := strings.Index(t, "<"); i > 0 && strings.HasSuffix(t, ">") && rustUnwraps[op[2:]] {
				t = t[i+1 : len(t)-1] // Result<T>/Option<T> unwrapped
				continue
			}
			if known(t) && (op == "()clone" || op == "()to_owned") {
				continue
			}
			if !known(t) {
				t = rustExt
				continue
			}
			t = tt.returns[t+"."+op[2:]]
		}
	}
	switch {
	case t == "" || strings.HasPrefix(t, rustExt) || known(t):
		return t
	case len(t) == 1:
		return "" // a generic parameter: any type
	}
	if i := strings.Index(t, "<"); i > 0 {
		t = t[:i] // Result<T>, Option<T>
	}
	return rustExt + ":" + t
}
