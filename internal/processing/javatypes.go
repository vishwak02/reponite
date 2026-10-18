package processing

import "github.com/vishwak02/reponite/internal/content"

// Java receiver typing: a method call's receiver typed from the class's
// fields, the method's parameters and local variables, then resolved on that
// type and its bases (interface calls also reach the implementations, as
// overrides). A receiver of a type the repo does not define is a library
// object: its methods are the library's.

var javaClassNodes = []string{"class_declaration", "interface_declaration", "enum_declaration", "record_declaration"}

// JavaTypeFacts collects a Java file's classes with their field types and
// bases, and their methods' return types.
func JavaTypeFacts(root content.AST) *TypeFacts {
	tf := &TypeFacts{Classes: map[string]*ClassFact{}, Aliases: map[string]string{}, Returns: map[string]string{}}
	var walk func(n content.AST)
	walk = func(n content.AST) {
		for _, c := range n.Children() {
			if !containsStr(javaClassNodes, c.Type()) {
				walk(c)
				continue
			}
			name := ""
			if id := firstChildAny(c, []string{"identifier"}); id != nil {
				name = id.Text()
			}
			if name == "" {
				continue
			}
			cf := tf.Classes[name]
			if cf == nil {
				cf = &ClassFact{Fields: map[string]string{}}
				tf.Classes[name] = cf
			}
			for _, k := range c.Children() {
				switch k.Type() {
				case "superclass", "super_interfaces", "extends_interfaces":
					for _, t := range descendantsAny(k, []string{"type_identifier", "generic_type", "scoped_type_identifier"}) {
						if b := javaTypeName(t); b != "" && !containsStr(cf.Bases, b) {
							cf.Bases = append(cf.Bases, b)
						}
					}
				case "class_body", "interface_body", "enum_body", "record_declaration_body":
					for _, m := range k.Children() {
						switch m.Type() {
						case "field_declaration", "constant_declaration":
							t := javaDeclType(m)
							for _, d := range m.Children() {
								if d.Type() == "variable_declarator" {
									if id := firstChildAny(d, []string{"identifier"}); id != nil && t != "" {
										cf.Fields[id.Text()] = t
									}
								}
							}
						case "method_declaration":
							if mn := firstChildAny(m, []string{"identifier"}); mn != nil {
								if t := javaDeclType(m); t != "" {
									tf.Returns[name+"."+mn.Text()] = t
								}
							}
						}
					}
					walk(k) // nested classes
				}
			}
		}
	}
	walk(root)
	return tf
}

// javaDeclType is the declared type of a field, local, parameter or method's
// return: the first type child, reduced to its class name.
func javaDeclType(n content.AST) string {
	for _, c := range n.Children() {
		switch c.Type() {
		case "type_identifier", "generic_type", "scoped_type_identifier":
			return javaTypeName(c)
		case "integral_type", "floating_point_type", "boolean_type", "void_type", "array_type":
			return ""
		}
	}
	return ""
}

// javaTypeName is a type's class name: Foo for Foo, List<Foo> (List), a.b.Foo.
func javaTypeName(t content.AST) string {
	switch t.Type() {
	case "type_identifier", "identifier":
		return t.Text()
	case "generic_type":
		for _, c := range t.Children() {
			if c.Type() == "type_identifier" || c.Type() == "scoped_type_identifier" {
				return javaTypeName(c)
			}
		}
	case "scoped_type_identifier":
		kids := t.Children()
		for i := len(kids) - 1; i >= 0; i-- {
			if kids[i].Type() == "type_identifier" {
				return kids[i].Text()
			}
		}
	}
	return ""
}

// javaVarTypes are a method's parameters and local variables with their
// declared types (var x = new T() too).
func javaVarTypes(fn content.AST) map[string]string {
	vars := map[string]string{}
	for _, p := range descendantsAny(fn, []string{"formal_parameter", "spread_parameter", "catch_formal_parameter", "local_variable_declaration", "enhanced_for_statement", "resource"}) {
		t := javaDeclType(p)
		if t == "" {
			continue
		}
		if p.Type() == "local_variable_declaration" || p.Type() == "resource" {
			for _, d := range p.Children() {
				if d.Type() != "variable_declarator" {
					continue
				}
				id := firstChildAny(d, []string{"identifier"})
				if id == nil {
					continue
				}
				vt := t
				if t == "var" {
					vt = ""
					if oc := firstChildAny(d, []string{"object_creation_expression"}); oc != nil {
						vt = javaDeclType(oc)
					}
				}
				if vt != "" {
					vars[id.Text()] = vt
				}
			}
			if p.Type() == "resource" {
				if id := firstChildAny(p, []string{"identifier"}); id != nil {
					vars[id.Text()] = t
				}
			}
			continue
		}
		if id := firstChildAny(p, []string{"identifier"}); id != nil {
			vars[id.Text()] = t
		}
	}
	return vars
}
