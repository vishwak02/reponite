package processing

import (
	"path"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// rustImplType is the type an impl block adds methods to: Foo in
// `impl Foo`, `impl<T> Foo<T>`, `impl Trait for Foo`, `impl fmt::Display
// for a::Foo`.
func rustImplType(impl content.AST) string {
	var named []content.AST
	afterFor := -1
	for _, c := range impl.Children() {
		if c.Type() == "for" {
			afterFor = len(named)
			continue
		}
		if c.IsNamed() && c.Type() != "type_parameters" && c.Type() != "where_clause" && c.Type() != "declaration_list" && !isCommentType(c.Type()) {
			named = append(named, c)
		}
	}
	i := 0
	if afterFor >= 0 {
		i = afterFor
	}
	if i >= len(named) {
		return ""
	}
	return rustTypeName(named[i])
}

// rustTypeName is a type's own name: Foo for Foo, Foo<T>, a::b::Foo, &Foo.
func rustTypeName(t content.AST) string {
	switch t.Type() {
	case "type_identifier", "identifier":
		return t.Text()
	case "generic_type", "reference_type", "pointer_type":
		for _, c := range t.Children() {
			if c.IsNamed() && c.Type() != "type_arguments" && c.Type() != "lifetime" && c.Type() != "mutable_specifier" {
				return rustTypeName(c)
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

// rustCallSites reads each call's shape in a Rust body: f(), path::f(),
// Type::f(), Self::f(), self.f(), value.f().
func rustCallSites(body content.AST, vars map[string]string) []CallSite {
	seen := map[CallSite]bool{}
	var out []CallSite
	for _, call := range descendantsAny(body, []string{"call_expression"}) {
		kids := call.Children()
		if len(kids) == 0 {
			continue
		}
		cs, ok := rustCallee(kids[0], vars)
		if !ok || cs.Name == "" || seen[cs] {
			continue
		}
		seen[cs] = true
		out = append(out, cs)
	}
	return out
}

func rustCallee(fn content.AST, vars map[string]string) (CallSite, bool) {
	switch fn.Type() {
	case "identifier":
		return CallSite{Name: fn.Text(), Kind: CallPlain}, true
	case "generic_function": // f::<T>(), Type::f::<T>()
		for _, c := range fn.Children() {
			if c.IsNamed() {
				return rustCallee(c, vars)
			}
		}
	case "field_expression":
		var value, field content.AST
		for _, c := range fn.Children() {
			switch {
			case c.Type() == "field_identifier":
				field = c
			case c.IsNamed() && value == nil:
				value = c
			}
		}
		if field == nil {
			return CallSite{}, false
		}
		if value != nil && value.Type() == "self" {
			return CallSite{Name: field.Text(), Kind: CallSelf}, true
		}
		cs := CallSite{Name: field.Text(), Kind: CallMember}
		if value != nil {
			cs.Recv = rustRecvExpr(value, vars)
		}
		return cs, true
	case "scoped_identifier":
		segs := rustPath(fn)
		if len(segs) < 2 {
			return CallSite{}, false
		}
		name, scope, root := segs[len(segs)-1], segs[len(segs)-2], segs[0]
		if scope == "Self" || isTypeName(scope) {
			return CallSite{Name: name, Kind: CallScoped, Scope: scope, Root: root}, true
		}
		// a module path: crate::a::f, super::f, a_module::f, std::mem::swap
		return CallSite{Name: name, Kind: CallPlain, Root: root, Scope: strings.Join(segs[:len(segs)-1], "::")}, true
	}
	return CallSite{}, false
}

// rustPath is a scoped identifier's segments; a generic segment
// (Vec::<T>::new) keeps its type's name.
func rustPath(n content.AST) []string {
	var out []string
	for _, c := range n.Children() {
		switch c.Type() {
		case "scoped_identifier", "scoped_type_identifier":
			out = append(out, rustPath(c)...)
		case "identifier", "type_identifier", "self", "super", "crate":
			out = append(out, c.Text())
		case "generic_type":
			if t := rustTypeName(c); t != "" {
				out = append(out, t)
			}
		}
	}
	return out
}

// rustCrate is the crate's module tree, read from its file layout: a.rs and
// a/mod.rs are module a of their directory's module; lib.rs and main.rs are
// a crate root.
type rustCrate struct {
	pkgOf  map[string]string // module path ("src/walk") -> package id of its file
	modOf  map[string]string // package id -> module path
	roots  map[string]bool   // module paths that are a crate root
	firsts map[string]bool   // names a path's first segment takes for the crate's own code
}

func newRustCrate(paths []string) *rustCrate {
	rc := &rustCrate{pkgOf: map[string]string{}, modOf: map[string]string{}, roots: map[string]bool{},
		firsts: map[string]bool{"crate": true, "self": true, "super": true}}
	for _, p := range paths {
		if !strings.HasSuffix(p, ".rs") {
			continue
		}
		dir := path.Dir(p)
		if dir == "." {
			dir = ""
		}
		stem := strings.TrimSuffix(path.Base(p), ".rs")
		mod := dir
		switch stem {
		case "lib", "main":
			rc.roots[dir] = true
		case "mod":
		default:
			mod = path.Join(dir, stem)
		}
		pkg := pkgOfFile(p, "rust")
		rc.pkgOf[mod], rc.modOf[pkg] = pkg, mod
		rc.firsts[stem] = true
		for d := dir; d != "" && d != "."; d = path.Dir(d) {
			b := path.Base(d)
			rc.firsts[b] = true
			rc.firsts[strings.ReplaceAll(b, "-", "_")] = true
			if path.Dir(d) == d {
				break
			}
		}
	}
	return rc
}

// resolveModule is the package of the module a path names from the caller's
// module: crate::a::b from the crate root, super:: from the parent, self::
// from the caller, a::b first as the caller's child, then (a `use`d module)
// from the crate root.
func (rc *rustCrate) resolveModule(callerPkg, modPath string) (string, bool) {
	if rc == nil {
		return "", false
	}
	cur, ok := rc.modOf[callerPkg]
	if !ok {
		return "", false
	}
	segs := strings.Split(modPath, "::")
	root := cur
	for !rc.roots[root] && root != "" && root != "." {
		root = path.Dir(root)
	}
	var bases []string
	switch segs[0] {
	case "crate":
		bases, segs = []string{root}, segs[1:]
	case "self":
		bases, segs = []string{cur}, segs[1:]
	case "super":
		base := cur
		for len(segs) > 0 && segs[0] == "super" {
			base, segs = path.Dir(base), segs[1:]
		}
		bases = []string{base}
	default:
		bases = []string{cur, root}
	}
	for _, b := range bases {
		m := path.Join(append([]string{b}, segs...)...)
		if pkg, ok := rc.pkgOf[m]; ok {
			return pkg, true
		}
	}
	return "", false
}
