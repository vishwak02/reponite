// canon.go implements canonicalization (architecture §5): a language-aware,
// versioned transform over the AST that erases meaningless differences
// (formatting, comments) while preserving every meaningful one (identifiers,
// literals, operators, structure). Its output is the CanonBody fed to
// SymbolHash and the input to FileHash; getting it right is the correctness
// surface behind every hash.
//
// Conservative by default (invariant 2): when a construct's treatment is
// unproven it is KEPT verbatim. canon() may under-normalize (miss some dedup)
// but must never merge distinct code. The Go (Tier-1, norm_ver=1) rules here:
// drop comments (they feed doc_hash instead, §5.5); sort import specs
// (reordering imports is not a change); keep every other token verbatim; and
// preserve structure via type-tagged, parenthesized recursion so distinct trees
// never collide. Further normalizations (trailing commas, redundant parens) are
// deliberately deferred until the golden corpus proves them safe.
package content

import (
	"bytes"
	"sort"
	"strings"
)

// AST is the language-agnostic syntax node that canon() consumes. It never
// operates on raw source text. The tree-sitter adapter implements this on a
// real machine (ADR-018); tests drive it with an in-memory fake.
type AST interface {
	Type() string    // grammar node type: "function_declaration", "identifier", "comment", "<=", ...
	Text() string    // literal source text of a leaf token
	Children() []AST // child nodes in source order; empty for a leaf
	IsNamed() bool   // named grammar node vs anonymous punctuation/operator token
}

// unitSep separates sibling canonical forms so adjacent tokens cannot merge
// ambiguously. 0x1f (ASCII Unit Separator) effectively never occurs in source.
const unitSep = 0x1f

func isComment(t string) bool {
	// Match any grammar comment node type (comment, line_comment, block_comment,
	// doc_comment, …) so canon drops docs regardless of the language label.
	return strings.Contains(t, "comment")
}

// Canon returns the canonical byte string for a node under ruleset norm_ver.
// norm_ver selects the rule table (v1 today); it is folded into hashes by the
// hash functions, not into these bytes.
func Canon(node AST, normVer int) []byte {
	if node == nil {
		return nil
	}
	return canonNode(node)
}

func canonNode(n AST) []byte {
	t := n.Type()
	if isComment(t) {
		return nil // dropped from identity; captured by DocText for doc_hash (§5.5)
	}
	kids := n.Children()
	if len(kids) == 0 {
		return []byte(n.Text()) // leaf: identifiers, literals, operators, keywords — kept verbatim
	}
	if t == "import_spec_list" {
		return canonChildren(t, kids, true) // sort: import order is not a change
	}
	return canonChildren(t, kids, false)
}

func canonChildren(t string, kids []AST, sortKids bool) []byte {
	subs := make([][]byte, 0, len(kids))
	for _, c := range kids {
		if sub := canonNode(c); len(sub) > 0 {
			subs = append(subs, sub)
		}
	}
	if sortKids {
		sort.Slice(subs, func(i, j int) bool { return bytes.Compare(subs[i], subs[j]) < 0 })
	}
	var b bytes.Buffer
	b.WriteString(t)
	b.WriteByte('(')
	for i, s := range subs {
		if i > 0 {
			b.WriteByte(unitSep)
		}
		b.Write(s)
	}
	b.WriteByte(')')
	return b.Bytes()
}

// DocText concatenates the comment/docstring text under a node in source order.
// It feeds doc_hash (§5.5), kept OUT of Canon so a documentation-only edit
// updates the intent layer without churning symbol identity or triggering a
// spurious behavior-change verdict.
func DocText(node AST) []byte {
	if node == nil {
		return nil
	}
	var b bytes.Buffer
	collectDoc(&b, node)
	return b.Bytes()
}

func collectDoc(b *bytes.Buffer, n AST) {
	if isComment(n.Type()) {
		b.WriteString(n.Text())
		b.WriteByte('\n')
		return
	}
	for _, c := range n.Children() {
		collectDoc(b, c)
	}
}
