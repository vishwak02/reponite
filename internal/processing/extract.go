// extract.go is the language-agnostic symbol/edge extractor. Extract walks a
// content.AST per a LangRules table (lang.go): it collects function/method/type
// declarations (descending into classes for nested methods), a body-independent
// signature, canonical body, associated doc comments, and heuristic name-based
// callees. Pure over content.AST, so it is unit-tested in-sandbox against fake
// ASTs for each language; real grammars are bound in the parser layer (ADR-018).
package processing

import (
	"bytes"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// Symbol is a top-level (or nested) symbol extracted from a file, pre-hashing.
type Symbol struct {
	Name      string
	Recv      string // method receiver type (empty for functions/types), for id qualification
	Kind      string // function|method|type
	Signature string
	CanonBody []byte
	Doc       []byte
	Callees   []string
	// QualifiedCalls carries each call site's leftmost qualifier + trailing name
	// (Qualifier empty for an unqualified call). It exists ONLY to resolve
	// cross-repo dependencies against import bindings (external_refs, §8B); the
	// behavior graph still keys off Callees, so this never perturbs any hash.
	QualifiedCalls []QualifiedCall
}

// QualifiedCall is one call site reduced to the two identifiers that matter for
// import resolution: the leftmost object (Qualifier — a package/namespace/class
// alias, empty when the call is unqualified) and the invoked member (Name).
// e.g. Go "bar.Do()" → {bar, Do}; Python "baz()" → {"", baz}; Java "Bar.x()" →
// {Bar, x}. "this."/"self." receivers are not identifiers, so they yield an
// empty Qualifier and are correctly ignored as intra-repo calls.
type QualifiedCall struct {
	Qualifier string
	Name      string
}

func isCommentType(t string) bool { return strings.Contains(t, "comment") }

// ExtractGo extracts Go symbols; delegates to the generic engine with GoRules.
func ExtractGo(root content.AST, normVer int) []Symbol { return Extract(root, GoRules, normVer) }

// Extract returns the symbols of an AST per the language rules. Declarations
// nested in classes/types are found by descending into them; a doc comment is
// associated with the declaration that immediately follows it.
func Extract(root content.AST, r LangRules, normVer int) []Symbol {
	var out []Symbol
	// enclosing carries the name of the class/type we're descending through, so
	// class-based languages (Python/JS/TS/Java) qualify a method by its enclosing
	// class the way Go qualifies by its receiver — keeping same-named methods on
	// different classes distinct (Invariant 2). Empty at file scope and for Go
	// (whose methods are top-level with a child receiver, not nested in the type).
	var walk func(n content.AST, enclosing string)
	walk = func(n content.AST, enclosing string) {
		var doc [][]byte
		for _, child := range n.Children() {
			t := child.Type()
			if isCommentType(t) {
				doc = append(doc, []byte(child.Text()))
				continue
			}
			switch {
			case containsStr(r.FuncDecl, t):
				appendNamed(&out, extractCallable(child, "function", r, normVer, joinDoc(doc), enclosing))
			case containsStr(r.MethodDecl, t):
				appendNamed(&out, extractCallable(child, "method", r, normVer, joinDoc(doc), enclosing))
			case containsStr(r.TypeDecl, t) && isTypeReference(child, r):
				// A bare type reference (`struct Foo x;`, forward declaration) —
				// not a definition; emit nothing, keep walking.
				walk(child, enclosing)
			case containsStr(r.TypeDecl, t):
				out = append(out, typeSymbols(child, r, normVer, joinDoc(doc))...)
				// Descend for nested methods, qualified by this type's name. Go-style
				// type blocks (TypeSpec set) never nest methods, so they don't qualify.
				nested := enclosing
				if len(r.TypeSpec) == 0 {
					nested = nameOf(child, r)
				}
				walk(child, nested)
			case containsStr(r.ScopeDecl, t):
				// A scope block (e.g. Rust `impl T`) qualifies its nested methods by
				// its own name, but is not itself a symbol.
				walk(child, nameOf(child, r))
			default:
				walk(child, enclosing)
			}
			doc = nil
		}
	}
	walk(root, "")
	return out
}

func joinDoc(doc [][]byte) []byte {
	if len(doc) == 0 {
		return nil
	}
	return bytes.Join(doc, []byte("\n"))
}

// appendNamed keeps a callable only when name resolution produced a real name.
// An anonymous callable is unaddressable; storing it under an invented or empty
// id would corrupt the graph (never lie — see nameOf).
func appendNamed(out *[]Symbol, s Symbol) {
	if s.Name != "" {
		*out = append(*out, s)
	}
}

func extractCallable(fn content.AST, kind string, r LangRules, normVer int, doc []byte, enclosing string) Symbol {
	var canonBody []byte
	var callees []string
	var qcalls []QualifiedCall
	if body := firstChildAny(fn, r.BodyTypes); body != nil {
		canonBody = content.Canon(body, normVer)
		callees = calleesWithRules(body, r)
		qcalls = qualifiedCallsWithRules(body, r)
	}
	recv := ""
	if kind == "method" {
		recv = receiverType(fn, r)
	}
	if recv == "" {
		recv = enclosing // class-based languages: qualify by the enclosing class
	}
	return Symbol{
		Name:           nameOf(fn, r),
		Recv:           recv,
		Kind:           kind,
		Signature:      string(content.Canon(withoutChildTypes(fn, r.BodyTypes), normVer)),
		CanonBody:      canonBody,
		Doc:            doc,
		Callees:        callees,
		QualifiedCalls: qcalls,
	}
}

// receiverType returns a method's receiver type name (pointer/spaces stripped),
// e.g. "Mem" for "(m *Mem)". The receiver is the first RecvTypes child of the
// method declaration; its type name is the first RecvName descendant within.
func receiverType(fn content.AST, r LangRules) string {
	if len(r.RecvTypes) == 0 {
		return ""
	}
	recv := firstChildAny(fn, r.RecvTypes)
	if recv == nil {
		return ""
	}
	return strings.TrimLeft(nameOfNode(recv, r.RecvName, true), "*& ")
}

func typeSymbols(decl content.AST, r LangRules, normVer int, doc []byte) []Symbol {
	if len(r.TypeSpec) > 0 {
		var out []Symbol
		for _, spec := range descendantsAny(decl, r.TypeSpec) {
			out = append(out, Symbol{
				Name: nameOfNode(spec, r.NameTypes, false), Kind: "type",
				Signature: string(content.Canon(spec, normVer)), Doc: doc,
			})
		}
		return out
	}
	name := nameOf(decl, r)
	if name == "" {
		// e.g. the anonymous struct inside `typedef struct { ... } Alias;` — the
		// enclosing type_definition carries the name; an unnamed symbol would
		// be unaddressable (see appendNamed).
		return nil
	}
	return []Symbol{{
		Name: name, Kind: "type",
		Signature: string(content.Canon(withoutChildTypes(decl, r.BodyTypes), normVer)), Doc: doc,
	}}
}

// calleesWithRules returns deduped callee names invoked in a body (name-based),
// derived from the same (qualifier, name) extraction qualifiedCallsWithRules uses
// so the callee is the invoked member — not the receiver. This matters for
// languages where the method name is the LAST identifier of the callee
// expression rather than the first child: Java's flat method_invocation
// (Bar.x() -> x) and C++/C member calls (obj.method()/ptr->m() -> method), whose
// method name lives in a field_identifier (see CallNameTypes).
func calleesWithRules(body content.AST, r LangRules) []string {
	seen := map[string]bool{}
	var out []string
	for _, qc := range qualifiedCallsWithRules(body, r) {
		if qc.Name == "" || seen[qc.Name] {
			continue
		}
		seen[qc.Name] = true
		out = append(out, qc.Name)
	}
	return out
}

// qualifiedCallsWithRules returns each call site's (leftmost qualifier, trailing
// name), deduped by the pair. Unlike calleesWithRules it keeps the qualifier so
// a call can be matched to an import binding for cross-repo resolution (§8B).
// It reads the identifiers appearing before the call's argument list, treating
// the first as the qualifier when there are two or more and the last as the
// invoked name — a shape that fits selector/attribute/member callee expressions
// (Go/Python/JS/TS) and Java's flat method_invocation alike.
func qualifiedCallsWithRules(body content.AST, r LangRules) []QualifiedCall {
	seen := map[QualifiedCall]bool{}
	var out []QualifiedCall
	for _, call := range descendantsAny(body, r.CallTypes) {
		ids := calleeIdents(call, r)
		if len(ids) == 0 {
			continue
		}
		qc := QualifiedCall{Name: ids[len(ids)-1]}
		if len(ids) >= 2 {
			qc.Qualifier = ids[0]
		}
		if r.Builtins[qc.Name] || seen[qc] {
			continue
		}
		seen[qc] = true
		out = append(out, qc)
	}
	return out
}

// calleeIdents returns the callee-name identifier leaves of a call's callee
// expression, in source order, stopping at the argument list (so argument
// identifiers and nested calls — handled by their own iteration — are excluded).
// Uses CallNameTypes when set (e.g. C/C++ include field_identifier so a member
// method resolves to its own name), else NameTypes.
func calleeIdents(call content.AST, r LangRules) []string {
	types := callNameTypes(r)
	var ids []string
	for _, c := range call.Children() {
		if isArgNode(c.Type()) {
			break
		}
		ids = append(ids, identLeaves(c, types)...)
	}
	return ids
}

// callNameTypes is the node-type set for callee/member names: CallNameTypes if
// the language sets it, else NameTypes.
func callNameTypes(r LangRules) []string {
	if len(r.CallNameTypes) > 0 {
		return r.CallNameTypes
	}
	return r.NameTypes
}

// isArgNode reports whether a node type is a call's argument list. tree-sitter
// names these "argument_list" (Go/Python/Java) or "arguments" (JS/TS).
func isArgNode(t string) bool { return strings.Contains(t, "argument") }

// extractCallees is the Go-specific callee helper kept for existing tests.
func extractCallees(body content.AST) []string { return calleesWithRules(body, GoRules) }

// --- generic AST helpers ---

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

func firstChildAny(n content.AST, types []string) content.AST {
	for _, c := range n.Children() {
		if containsStr(types, c.Type()) {
			return c
		}
	}
	return nil
}

func firstDescAny(n content.AST, types []string) content.AST {
	for _, c := range n.Children() {
		if containsStr(types, c.Type()) {
			return c
		}
		if d := firstDescAny(c, types); d != nil {
			return d
		}
	}
	return nil
}

func nameOf(n content.AST, r LangRules) string {
	// A callable whose name is nested in a declarator (C/C++): the name is the
	// last DeclNameTypes leaf inside the declarator BEFORE its parameter list —
	// so a return type is skipped and a qualified name reduces to its last
	// segment. The declarator is authoritative: when it yields no name, the
	// callable is anonymous — never invent one from a parameter type or body
	// identifier (that misattributed C++ endpoints to names like NodeHandle).
	if len(r.DeclNameIn) > 0 {
		if d := firstChildAny(n, r.DeclNameIn); d != nil {
			return declaratorName(d, r)
		}
	}
	return nameOfNode(n, r.NameTypes, r.NameByDesc)
}

// declaratorName returns the last DeclNameTypes (default NameTypes) leaf inside
// a declarator that appears BEFORE the parameter list. The declared name always
// precedes the parameters in C/C++ declarators, so stopping there keeps
// parameter names, member-initializer identifiers, and trailing-return types
// (`auto f() -> Widget`) from being mistaken for the name.
func declaratorName(d content.AST, r LangRules) string {
	types := r.DeclNameTypes
	if len(types) == 0 {
		types = r.NameTypes
	}
	name := ""
	var walk func(content.AST) bool // true once the parameter list is reached
	walk = func(n content.AST) bool {
		for _, c := range n.Children() {
			if strings.Contains(c.Type(), "parameter") {
				return true
			}
			if containsStr(types, c.Type()) {
				name = c.Text()
				continue
			}
			if walk(c) {
				return true
			}
		}
		return false
	}
	walk(d)
	return name
}

// isTypeReference reports whether a TypeDecl-typed node is only a type
// REFERENCE (C/C++ `struct Foo x;`, a forward declaration, an elaborated type
// in a signature) rather than a definition: the node's type requires a body
// (TypeDeclNeedsBody) and none is present. References must not become symbols
// or enclosing-symbol spans.
func isTypeReference(n content.AST, r LangRules) bool {
	return containsStr(r.TypeDeclNeedsBody, n.Type()) && firstChildAny(n, r.TypeDeclBody) == nil
}

func nameOfNode(n content.AST, types []string, byDesc bool) string {
	var node content.AST
	if byDesc {
		node = firstDescAny(n, types)
	} else {
		node = firstChildAny(n, types)
	}
	if node == nil {
		return ""
	}
	return node.Text()
}

func descendantsAny(n content.AST, types []string) []content.AST {
	var out []content.AST
	var walk func(content.AST)
	walk = func(x content.AST) {
		for _, c := range x.Children() {
			if containsStr(types, c.Type()) {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func identLeaves(n content.AST, nameTypes []string) []string {
	var out []string
	var walk func(content.AST)
	walk = func(x content.AST) {
		if containsStr(nameTypes, x.Type()) {
			out = append(out, x.Text())
			return
		}
		for _, c := range x.Children() {
			walk(c)
		}
	}
	walk(n)
	return out
}

type filteredNode struct {
	content.AST
	drop map[string]bool
}

func (f filteredNode) Children() []content.AST {
	var out []content.AST
	for _, c := range f.AST.Children() {
		if !f.drop[c.Type()] {
			out = append(out, c)
		}
	}
	return out
}

func withoutChildTypes(n content.AST, types []string) content.AST {
	drop := make(map[string]bool, len(types))
	for _, t := range types {
		drop[t] = true
	}
	return filteredNode{AST: n, drop: drop}
}
