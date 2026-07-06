// imports.go extracts import bindings — the local identifier a file binds to
// an external module (Go "import bar \"path\"", Python "from x import y",
// JS/TS "import {a} from 'mod'", Java "import com.foo.Bar") — so a qualified
// or from-imported call site can be traced back to the module it depends on
// (feeds §8B cross-repo impact / external_refs, ADR-016). Pure over
// content.AST like extract.go, so it is unit-tested against fake ASTs here
// and against real grammars under the treesitter tag (ADR-018).
package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// ImportBinding is one locally-bound name introduced by an import: the
// identifier used at call sites (Local), the module/package path it resolves
// to (Module), and — when the import names one specific symbol rather than a
// whole module/namespace/class (a Python from-import, a JS named import, a
// Java static import) — that symbol's real, un-aliased name (Symbol). Symbol
// is empty when Local itself refers to the whole module (Go package import,
// JS default/namespace import, Java class import): the call's own trailing
// identifier supplies the member name in that case.
type ImportBinding struct {
	Local  string
	Module string
	Symbol string
}

// importsByLocal indexes import bindings by their local name for call-site
// resolution. On a collision (two imports binding the same local name — rare,
// and a code smell) the first wins.
func importsByLocal(bindings []ImportBinding) map[string]ImportBinding {
	if len(bindings) == 0 {
		return nil
	}
	m := make(map[string]ImportBinding, len(bindings))
	for _, b := range bindings {
		if b.Local == "" {
			continue
		}
		if _, ok := m[b.Local]; !ok {
			m[b.Local] = b
		}
	}
	return m
}

// Imports extracts a file's import bindings per its language rules. Relative
// or intra-package imports (Python "from . import x", a JS "./x" or "/x"
// specifier) are skipped: they resolve inside the same repo, not to an
// external module, so they carry no fleet-impact signal (§8B.2).
func Imports(root content.AST, r LangRules) []ImportBinding {
	switch r.Name {
	case "go":
		return goImports(root)
	case "python":
		return pyImports(root)
	case "javascript", "typescript":
		return jsImports(root)
	case "java":
		return javaImports(root)
	}
	return nil
}

// --- Go: import_declaration -> import_spec | import_spec_list{import_spec} ---

func goImports(root content.AST) []ImportBinding {
	var out []ImportBinding
	for _, decl := range descendantsAny(root, []string{"import_declaration"}) {
		for _, spec := range goImportSpecs(decl) {
			if b, ok := goImportSpec(spec); ok {
				out = append(out, b)
			}
		}
	}
	return out
}

func goImportSpecs(decl content.AST) []content.AST {
	var specs []content.AST
	for _, c := range decl.Children() {
		switch c.Type() {
		case "import_spec":
			specs = append(specs, c)
		case "import_spec_list":
			for _, gc := range c.Children() {
				if gc.Type() == "import_spec" {
					specs = append(specs, gc)
				}
			}
		}
	}
	return specs
}

func goImportSpec(spec content.AST) (ImportBinding, bool) {
	var path, alias string
	skip := false // blank ("_") or dot (".") imports bind no usable call-site name
	for _, c := range spec.Children() {
		switch c.Type() {
		case "interpreted_string_literal", "raw_string_literal":
			path = strings.Trim(c.Text(), "`\"")
		case "package_identifier":
			alias = c.Text()
		case "blank_identifier", "dot":
			skip = true
		}
	}
	if path == "" || skip {
		return ImportBinding{}, false
	}
	local := alias
	if local == "" {
		local = goPackageNameOf(path)
	}
	return ImportBinding{Local: local, Module: path}, true
}

// goPackageNameOf heuristically derives a Go import path's default package
// identifier: its last path segment, skipping a trailing major-version
// suffix ("/v2", "/v3", ...) per Go module convention.
func goPackageNameOf(path string) string {
	segs := strings.Split(strings.TrimRight(path, "/"), "/")
	name := segs[len(segs)-1]
	if len(segs) >= 2 && isVersionSuffix(name) {
		name = segs[len(segs)-2]
	}
	return name
}

func isVersionSuffix(s string) bool {
	if len(s) < 2 || s[0] != 'v' {
		return false
	}
	for _, c := range s[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// --- Python: import_statement | import_from_statement ---

func pyImports(root content.AST) []ImportBinding {
	var out []ImportBinding
	for _, stmt := range descendantsAny(root, []string{"import_statement"}) {
		out = append(out, pyImportStatement(stmt)...)
	}
	for _, stmt := range descendantsAny(root, []string{"import_from_statement"}) {
		out = append(out, pyImportFromStatement(stmt)...)
	}
	return out
}

func pyImportStatement(stmt content.AST) []ImportBinding {
	var out []ImportBinding
	for _, c := range stmt.Children() {
		switch c.Type() {
		case "dotted_name":
			mod := c.Text()
			out = append(out, ImportBinding{Local: pyFirstSegment(mod), Module: mod})
		case "aliased_import":
			mod, alias := pyAliasParts(c)
			if mod != "" && alias != "" {
				out = append(out, ImportBinding{Local: alias, Module: mod})
			}
		}
	}
	return out
}

// pyImportFromStatement handles "from <module> import a, b as c". Relative
// imports ("from . import x", "from ..pkg import y") resolve inside the
// repo, not to an external module — skipped entirely (§8B.2).
func pyImportFromStatement(stmt content.AST) []ImportBinding {
	if firstChildAny(stmt, []string{"relative_import"}) != nil {
		return nil
	}
	mod := nameOfNode(stmt, []string{"dotted_name"}, false)
	if mod == "" {
		return nil
	}
	var out []ImportBinding
	seenModule := false
	for _, c := range stmt.Children() {
		switch c.Type() {
		case "dotted_name":
			if !seenModule {
				seenModule = true // the first dotted_name child is the module itself
				continue
			}
			name := c.Text()
			out = append(out, ImportBinding{Local: name, Module: mod, Symbol: name})
		case "aliased_import":
			orig, alias := pyAliasParts(c)
			if orig != "" && alias != "" {
				out = append(out, ImportBinding{Local: alias, Module: mod, Symbol: orig})
			}
		}
	}
	return out
}

// pyAliasParts reads an "aliased_import" node's (dotted_name, "as",
// identifier); used by both "import X as Y" and "from M import X as Y".
func pyAliasParts(n content.AST) (name, alias string) {
	for _, c := range n.Children() {
		switch c.Type() {
		case "dotted_name":
			name = c.Text()
		case "identifier":
			alias = c.Text()
		}
	}
	return name, alias
}

func pyFirstSegment(dotted string) string {
	if i := strings.IndexByte(dotted, '.'); i >= 0 {
		return dotted[:i]
	}
	return dotted
}

// --- JavaScript/TypeScript: import_statement ---

func jsImports(root content.AST) []ImportBinding {
	var out []ImportBinding
	for _, stmt := range descendantsAny(root, []string{"import_statement"}) {
		out = append(out, jsImportStatement(stmt)...)
	}
	return out
}

func jsImportStatement(stmt content.AST) []ImportBinding {
	mod := jsModulePath(stmt)
	if mod == "" || isRelativeModule(mod) {
		return nil
	}
	clause := firstChildAny(stmt, []string{"import_clause"})
	if clause == nil {
		return nil // side-effect-only import, e.g. import 'polyfill';
	}
	var out []ImportBinding
	for _, c := range clause.Children() {
		switch c.Type() {
		case "identifier": // default import: `import foo from 'mod'`
			out = append(out, ImportBinding{Local: c.Text(), Module: mod})
		case "namespace_import": // `* as ns`
			if id := lastIdentLeaf(c); id != "" {
				out = append(out, ImportBinding{Local: id, Module: mod})
			}
		case "named_imports": // `{ a, b as c }`
			for _, spec := range c.Children() {
				if spec.Type() == "import_specifier" {
					out = append(out, jsImportSpecifier(spec, mod))
				}
			}
		}
	}
	return out
}

func jsImportSpecifier(spec content.AST, mod string) ImportBinding {
	ids := identLeaves(spec, []string{"identifier"})
	if len(ids) == 0 {
		return ImportBinding{}
	}
	if len(ids) == 1 {
		return ImportBinding{Local: ids[0], Module: mod, Symbol: ids[0]}
	}
	return ImportBinding{Local: ids[len(ids)-1], Module: mod, Symbol: ids[0]} // "orig as alias"
}

func jsModulePath(stmt content.AST) string {
	s := firstChildAny(stmt, []string{"string"})
	if s == nil {
		return ""
	}
	return strings.Trim(s.Text(), `'"`)
}

func lastIdentLeaf(n content.AST) string {
	ids := identLeaves(n, []string{"identifier"})
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

func isRelativeModule(p string) bool {
	return strings.HasPrefix(p, ".") || strings.HasPrefix(p, "/")
}

// --- Java: import_declaration ---

func javaImports(root content.AST) []ImportBinding {
	var out []ImportBinding
	for _, decl := range descendantsAny(root, []string{"import_declaration"}) {
		if b, ok := javaImportDecl(decl); ok {
			out = append(out, b)
		}
	}
	return out
}

func javaImportDecl(decl content.AST) (ImportBinding, bool) {
	isStatic := false
	qualified := ""
	wildcard := false
	for _, c := range decl.Children() {
		switch c.Type() {
		case "static":
			isStatic = true
		case "scoped_identifier", "identifier":
			qualified = c.Text()
		case "asterisk":
			wildcard = true
		}
	}
	if qualified == "" || wildcard {
		return ImportBinding{}, false
	}
	i := strings.LastIndexByte(qualified, '.')
	if i < 0 {
		return ImportBinding{}, false
	}
	module, name := qualified[:i], qualified[i+1:]
	if isStatic {
		return ImportBinding{Local: name, Module: module, Symbol: name}, true
	}
	return ImportBinding{Local: name, Module: module}, true
}
