package processing

import "github.com/vishwak02/reponite/internal/content"

// hasClassBases reports whether a language's class bases are read, so
// self.f() / this.f() resolves to the method a base class defines.
func hasClassBases(lang string) bool {
	f := langFamily(lang)
	return f == "python" || f == "js"
}

// classBases reads the bases a class declaration names, each as the dotted
// reference it is written with (Base, module.Base): Python's
// `class A(Base, metaclass=M)` and JS/TS `class A extends Base`. A base that
// is not a plain or dotted name (a call, a mixin expression) is left out.
func classBases(decl content.AST, lang string) []QualifiedCall {
	if !hasClassBases(lang) {
		return nil
	}
	var refs [][]string
	for _, c := range decl.Children() {
		switch c.Type() {
		case "argument_list": // python superclasses
			for _, a := range c.Children() {
				if a.IsNamed() && a.Type() != "keyword_argument" && !isCommentType(a.Type()) {
					refs = append(refs, dottedRef(a))
				}
			}
		case "class_heritage": // javascript / typescript
			for _, h := range c.Children() {
				switch {
				case h.Type() == "extends_clause":
					for _, v := range h.Children() {
						if v.IsNamed() && v.Type() != "type_arguments" {
							refs = append(refs, dottedRef(v))
							break
						}
					}
				case h.IsNamed() && h.Type() != "implements_clause" && !isCommentType(h.Type()):
					refs = append(refs, dottedRef(h)) // javascript: extends <expr>
				}
			}
		}
	}
	var out []QualifiedCall
	for _, parts := range refs {
		if len(parts) == 0 {
			continue
		}
		qc := QualifiedCall{Name: parts[len(parts)-1], Chain: len(parts)}
		if len(parts) > 1 {
			qc.Qualifier = parts[0]
		}
		out = append(out, qc)
	}
	return out
}

// dottedRef is the identifiers of a name or attribute chain (a.b.C); a
// generic's arguments are dropped (Generic[T], Base<T>). nil when the node is
// anything else.
func dottedRef(n content.AST) []string {
	switch n.Type() {
	case "identifier", "type_identifier", "property_identifier":
		return []string{n.Text()}
	case "attribute", "member_expression", "nested_type_identifier", "nested_identifier":
		var out []string
		for _, c := range n.Children() {
			if !c.IsNamed() {
				continue
			}
			p := dottedRef(c)
			if p == nil {
				return nil
			}
			out = append(out, p...)
		}
		return out
	case "subscript", "generic_type":
		for _, c := range n.Children() {
			if c.IsNamed() {
				return dottedRef(c)
			}
		}
	}
	return nil
}

// classGraph is the repo's classes (Python, JS/TS) by module and name, with
// the bases each names resolved through the defining file's imports.
type classGraph struct {
	bases   map[string][]string // class key -> its bases' keys, in declaration order
	derived map[string][]string // class key -> classes naming it as a direct base
	pkg     map[string]string   // class key -> module (package) id
	name    map[string]string   // class key -> class name
}

// inertBases are the bases that only mark a class (abstract, generic, a
// protocol): they define no method a subclass calls through self.
var inertBases = map[string]bool{"object": true, "ABC": true, "Generic": true, "Protocol": true}

// superScope marks a CallSelf site written as super().f() / super.f().
const superScope = "\x00super"

func classKey(pkg, name string) string { return pkg + "\x00" + name }

func newClassGraph() *classGraph {
	return &classGraph{bases: map[string][]string{}, derived: map[string][]string{}, pkg: map[string]string{}, name: map[string]string{}}
}

func (g *classGraph) define(pkg, name string) string {
	k := classKey(pkg, name)
	g.pkg[k], g.name[k] = pkg, name
	if _, ok := g.bases[k]; !ok {
		g.bases[k] = nil
	}
	return k
}

// link records cls's bases once every class is defined: a base written as a
// bare name is a class of the same module or one imported by name; a dotted
// one is a class of an imported module. Anything else (a library class) ends
// the lineage there.
func (g *classGraph) link(cls string, sites []CallSite) {
	pkg := g.pkg[cls]
	for _, s := range sites {
		if inertBases[s.Name] {
			continue // defines no method a subclass's self.f() could reach
		}
		var b string
		switch s.Kind {
		case CallModule:
			b = classKey(s.Scope, s.Name)
		case CallPlain:
			b = classKey(pkg, s.Name)
		}
		if _, ok := g.bases[b]; !ok || b == cls {
			b = "" // not a class of the repo: the lineage stops here
		}
		g.bases[cls] = append(g.bases[cls], b)
		if b != "" {
			g.derived[b] = append(g.derived[b], cls)
		}
	}
}

// mro is cls's method resolution order (C3, as Python computes it; for single
// inheritance — all JS/TS has — the chain of bases). A base outside the repo
// appears as "". ok is false when the bases have no consistent order.
func (g *classGraph) mro(cls string) ([]string, bool) {
	var memo = map[string][]string{}
	var visiting = map[string]bool{}
	var lin func(c string) ([]string, bool)
	lin = func(c string) ([]string, bool) {
		if l, ok := memo[c]; ok {
			return l, true
		}
		if visiting[c] {
			return nil, false // a cycle
		}
		visiting[c] = true
		defer delete(visiting, c)
		var seqs [][]string
		var direct []string
		for _, b := range g.bases[c] {
			if b == "" {
				// an external base: its methods are unknown, so anything after
				// it in the order is not proven to be the one that runs
				seqs = append(seqs, []string{""})
				direct = append(direct, "")
				continue
			}
			l, ok := lin(b)
			if !ok {
				return nil, false
			}
			seqs = append(seqs, l)
			direct = append(direct, b)
		}
		seqs = append(seqs, direct)
		out := []string{c}
		for {
			var nonEmpty [][]string
			for _, s := range seqs {
				if len(s) > 0 {
					nonEmpty = append(nonEmpty, s)
				}
			}
			if len(nonEmpty) == 0 {
				break
			}
			var head string
			found := false
			for _, s := range nonEmpty {
				h := s[0]
				inTail := false
				for _, t := range nonEmpty {
					for _, x := range t[1:] {
						if x == h {
							inTail = true
						}
					}
				}
				if !inTail {
					head, found = h, true
					break
				}
			}
			if !found {
				return nil, false
			}
			out = append(out, head)
			for i, s := range nonEmpty {
				if s[0] == head {
					nonEmpty[i] = s[1:]
				}
			}
			seqs = nonEmpty
		}
		memo[c] = out
		return out, true
	}
	return lin(cls)
}

// method resolves name looked up on an instance of cls (on its bases for
// super): the definition the MRO reaches first. found is false when no repo
// class in the order defines it or an external base comes first (its own
// method may be the one that runs).
func (g *classGraph) method(cls, name string, super bool, nodeSet map[string]bool) (string, bool) {
	order, ok := g.mro(cls)
	if !ok {
		return "", false
	}
	if super {
		order = order[1:]
	}
	for _, c := range order {
		if c == "" {
			return "", false
		}
		if q := qualifyModule(g.pkg[c], g.name[c]+"."+name); nodeSet[q] {
			return q, true
		}
	}
	return "", false
}

// overriders are the definitions of name on classes deriving from cls, at
// any depth: on an instance of a subclass, self.name() runs one of these.
func (g *classGraph) overriders(cls, name string, nodeSet map[string]bool) []string {
	var out []string
	seen := map[string]bool{cls: true}
	queue := append([]string(nil), g.derived[cls]...)
	for len(queue) > 0 {
		c := queue[0]
		queue = queue[1:]
		if seen[c] {
			continue
		}
		seen[c] = true
		if q := qualifyModule(g.pkg[c], g.name[c]+"."+name); nodeSet[q] {
			out = append(out, q)
		}
		queue = append(queue, g.derived[c]...)
	}
	return out
}

// has reports whether the repo defines a class at (pkg, name).
func (g *classGraph) has(pkg, name string) (string, bool) {
	if g == nil {
		return "", false
	}
	k := classKey(pkg, name)
	_, ok := g.bases[k]
	return k, ok
}
