// extract.go walks a parsed file's AST to produce top-level symbols with their
// canonical signature/body forms and heuristic (name-based) callees — the input
// to hashing and the behavior pass. It is PURE over the content.AST interface,
// so it is unit-tested in-sandbox against a fake AST (ADR-018); the real tree is
// produced by ParseGo (build tag treesitter). Name-based callee resolution is
// the tree-sitter tier (medium confidence, §7); SCIP upgrades it later.
package processing

import (
	"bytes"
	"strings"

	"github.com/reponite/reponite/internal/content"
)

// Symbol is a top-level symbol extracted from a file, pre-hashing.
type Symbol struct {
	Name      string
	Kind      string // function|method|type
	Signature string // canonical, body-independent shape
	CanonBody []byte // canonical body (nil for types)
	Doc       []byte // associated doc comment(s)
	Callees   []string
}

func isCommentType(t string) bool {
	return strings.Contains(t, "comment")
}

// ExtractGo returns the top-level symbols of a Go source_file AST. Doc comments
// are associated with the following declaration; a non-comment sibling resets
// the pending doc (standard Go doc association).
func ExtractGo(root content.AST, normVer int) []Symbol {
	var out []Symbol
	var doc [][]byte
	for _, child := range root.Children() {
		switch t := child.Type(); {
		case isCommentType(t):
			doc = append(doc, []byte(child.Text()))
			continue
		case t == "function_declaration":
			out = append(out, extractFunc(child, "function", "identifier", normVer, joinDoc(doc)))
		case t == "method_declaration":
			out = append(out, extractFunc(child, "method", "field_identifier", normVer, joinDoc(doc)))
		case t == "type_declaration":
			out = append(out, extractTypes(child, normVer, joinDoc(doc))...)
		}
		doc = nil
	}
	return out
}

func joinDoc(doc [][]byte) []byte {
	if len(doc) == 0 {
		return nil
	}
	return bytes.Join(doc, []byte("\n"))
}

func extractFunc(fn content.AST, kind, nameType string, normVer int, doc []byte) Symbol {
	var canonBody []byte
	var callees []string
	if body := firstChild(fn, "block"); body != nil {
		canonBody = content.Canon(body, normVer)
		callees = extractCallees(body)
	}
	return Symbol{
		Name:      firstChildText(fn, nameType),
		Kind:      kind,
		Signature: string(content.Canon(withoutChildType(fn, "block"), normVer)),
		CanonBody: canonBody,
		Doc:       doc,
		Callees:   callees,
	}
}

func extractTypes(decl content.AST, normVer int, doc []byte) []Symbol {
	var out []Symbol
	for _, spec := range descendants(decl, "type_spec") {
		out = append(out, Symbol{
			Name:      firstChildText(spec, "type_identifier"),
			Kind:      "type",
			Signature: string(content.Canon(spec, normVer)),
			Doc:       doc,
		})
	}
	return out
}

// extractCallees returns the deduped, source-ordered names invoked in a body,
// via name-based resolution (last identifier of each call's function expr).
func extractCallees(body content.AST) []string {
	seen := map[string]bool{}
	var out []string
	for _, call := range descendants(body, "call_expression") {
		kids := call.Children()
		if len(kids) == 0 {
			continue
		}
		if name := trailingIdent(kids[0]); name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// --- AST helpers (pure) ---

func firstChild(n content.AST, typ string) content.AST {
	for _, c := range n.Children() {
		if c.Type() == typ {
			return c
		}
	}
	return nil
}

func firstChildText(n content.AST, typ string) string {
	if c := firstChild(n, typ); c != nil {
		return c.Text()
	}
	return ""
}

func descendants(n content.AST, typ string) []content.AST {
	var out []content.AST
	var walk func(content.AST)
	walk = func(x content.AST) {
		for _, c := range x.Children() {
			if c.Type() == typ {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(n)
	return out
}

func trailingIdent(n content.AST) string {
	ids := identLeaves(n)
	if len(ids) == 0 {
		return ""
	}
	return ids[len(ids)-1]
}

func identLeaves(n content.AST) []string {
	var out []string
	var walk func(content.AST)
	walk = func(x content.AST) {
		if t := x.Type(); t == "identifier" || t == "field_identifier" {
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

// filteredNode is a content.AST view that drops top-level children of one type,
// used to canonicalize a declaration's signature independently of its body.
type filteredNode struct {
	content.AST
	drop string
}

func (f filteredNode) Children() []content.AST {
	var out []content.AST
	for _, c := range f.AST.Children() {
		if c.Type() != f.drop {
			out = append(out, c)
		}
	}
	return out
}

func withoutChildType(n content.AST, typ string) content.AST {
	return filteredNode{AST: n, drop: typ}
}
