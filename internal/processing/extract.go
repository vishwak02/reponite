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
				out = append(out, extractCallable(child, "function", r, normVer, joinDoc(doc), enclosing))
			case containsStr(r.MethodDecl, t):
				out = append(out, extractCallable(child, "method", r, normVer, joinDoc(doc), enclosing))
			case containsStr(r.TypeDecl, t):
				out = append(out, typeSymbols(child, r, normVer, joinDoc(doc))...)
				// Descend for nested methods, qualified by this type's name. Go-style
				// type blocks (TypeSpec set) never nest methods, so they don't qualify.
				nested := enclosing
				if len(r.TypeSpec) == 0 {
					nested = nameOf(child, r)
				}
				walk(child, nested)
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

func extractCallable(fn content.AST, kind string, r LangRules, normVer int, doc []byte, enclosing string) Symbol {
	var canonBody []byte
	var callees []string
	if body := firstChildAny(fn, r.BodyTypes); body != nil {
		canonBody = content.Canon(body, normVer)
		callees = calleesWithRules(body, r)
	}
	recv := ""
	if kind == "method" {
		recv = receiverType(fn, r)
	}
	if recv == "" {
		recv = enclosing // class-based languages: qualify by the enclosing class
	}
	return Symbol{
		Name:      nameOf(fn, r),
		Recv:      recv,
		Kind:      kind,
		Signature: string(content.Canon(withoutChildTypes(fn, r.BodyTypes), normVer)),
		CanonBody: canonBody,
		Doc:       doc,
		Callees:   callees,
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
	return []Symbol{{
		Name: nameOf(decl, r), Kind: "type",
		Signature: string(content.Canon(withoutChildTypes(decl, r.BodyTypes), normVer)), Doc: doc,
	}}
}

// calleesWithRules returns deduped callee names invoked in a body (name-based).
func calleesWithRules(body content.AST, r LangRules) []string {
	seen := map[string]bool{}
	var out []string
	for _, call := range descendantsAny(body, r.CallTypes) {
		kids := call.Children()
		if len(kids) == 0 {
			continue
		}
		if name := trailingIdent(kids[0], r.NameTypes); name != "" && !r.Builtins[name] && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

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

func nameOf(n content.AST, r LangRules) string { return nameOfNode(n, r.NameTypes, r.NameByDesc) }

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

func trailingIdent(n content.AST, nameTypes []string) string {
	ids := identLeaves(n, nameTypes)
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
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
