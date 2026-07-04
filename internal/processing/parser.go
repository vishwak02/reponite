//go:build treesitter

// parser.go adapts tree-sitter parse trees to content.AST (per
// docs/adapters/treesitter-ast-contract.md), so the sandbox-verified canon()
// logic runs on real Go source. Uses CGO (tree-sitter), so it lives behind the
// `treesitter` build tag; the default build stays dependency- and CGO-free
// (ADR-018). The adapter performs ZERO normalization — it faithfully exposes the
// parse tree (all children incl. anonymous tokens, exact leaf text); canon owns
// all identity policy.
package processing

import (
	"context"

	sitter "github.com/smacker/go-tree-sitter"
	golang "github.com/smacker/go-tree-sitter/golang"
	java "github.com/smacker/go-tree-sitter/java"
	javascript "github.com/smacker/go-tree-sitter/javascript"
	python "github.com/smacker/go-tree-sitter/python"
	tsx "github.com/smacker/go-tree-sitter/typescript/tsx"
	typescript "github.com/smacker/go-tree-sitter/typescript/typescript"

	"github.com/vishwak02/reponite/internal/content"
)

// tsNode wraps a tree-sitter node + source to satisfy content.AST.
type tsNode struct {
	n   *sitter.Node
	src []byte
}

func (t tsNode) Type() string  { return t.n.Type() }
func (t tsNode) Text() string  { return t.n.Content(t.src) }
func (t tsNode) IsNamed() bool { return t.n.IsNamed() }

func (t tsNode) Children() []content.AST {
	count := int(t.n.ChildCount()) // ALL children, incl. anonymous operators/punctuation
	out := make([]content.AST, 0, count)
	for i := 0; i < count; i++ {
		out = append(out, tsNode{n: t.n.Child(i), src: t.src})
	}
	return out
}

// grammarForExt returns the tree-sitter grammar for a file extension, or nil if
// the extension is unsupported. TypeScript needs two grammars — the tsx grammar
// is a superset that also accepts JSX in .tsx files — so the choice is keyed on
// the extension, not just the LangRules name.
func grammarForExt(ext string) *sitter.Language {
	switch ext {
	case ".go":
		return golang.GetLanguage()
	case ".py":
		return python.GetLanguage()
	case ".js", ".jsx", ".mjs", ".cjs":
		return javascript.GetLanguage()
	case ".ts":
		return typescript.GetLanguage()
	case ".tsx":
		return tsx.GetLanguage()
	case ".java":
		return java.GetLanguage()
	}
	return nil
}

// parseRoot parses source with a specific grammar, returning the raw root node
// (callers wrap it in tsNode for content.AST or read positions directly).
func parseRoot(src []byte, lang *sitter.Language) (*sitter.Node, error) {
	p := sitter.NewParser()
	p.SetLanguage(lang)
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, err
	}
	return tree.RootNode(), nil
}

// ParseGo parses Go source into a content.AST tree rooted at source_file.
func ParseGo(src []byte) (content.AST, error) {
	root, err := parseRoot(src, golang.GetLanguage())
	if err != nil {
		return nil, err
	}
	return tsNode{n: root, src: src}, nil
}
