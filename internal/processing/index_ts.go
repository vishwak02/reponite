//go:build treesitter

// index_ts.go is the thin tree-sitter layer over the pure IndexFiles: it walks a
// repo's *.go files, parses each, extracts symbols, computes line spans (from
// tree-sitter positions), and hands everything to IndexFiles. CGO (tree-sitter),
// so it is behind the treesitter build tag and verified in CI (ADR-018).
package processing

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	golang "github.com/smacker/go-tree-sitter/golang"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

// IndexDir indexes every .go file under dir as one repo ref.
func IndexDir(w Indexer, repo, ref, dir string, normVer int) error {
	var files []ParsedFile
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			if path != dir {
				if b := info.Name(); strings.HasPrefix(b, ".") || b == "vendor" || b == "node_modules" {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		root, spans, parseErr := parseFile(src)
		if parseErr != nil {
			return parseErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		files = append(files, ParsedFile{Path: rel, Content: string(src), Symbols: ExtractGo(root, normVer), Spans: spans})
		return nil
	})
	if err != nil {
		return err
	}
	return IndexFiles(w, repo, ref, normVer, files)
}

// parseFile parses Go source, returning the content.AST (for ExtractGo) and the
// top-level symbol line spans (for grep fusion).
func parseFile(src []byte) (content.AST, []query.SymbolSpan, error) {
	p := sitter.NewParser()
	p.SetLanguage(golang.GetLanguage())
	tree, err := p.ParseCtx(context.Background(), nil, src)
	if err != nil {
		return nil, nil, err
	}
	root := tree.RootNode()
	return tsNode{n: root, src: src}, topLevelSpans(root, src), nil
}

// topLevelSpans returns 1-based line ranges for each top-level declaration.
func topLevelSpans(root *sitter.Node, src []byte) []query.SymbolSpan {
	var spans []query.SymbolSpan
	for i := 0; i < int(root.ChildCount()); i++ {
		ch := root.Child(i)
		w := tsNode{n: ch, src: src}
		start := int(ch.StartPoint().Row) + 1
		end := int(ch.EndPoint().Row) + 1
		switch ch.Type() {
		case "function_declaration":
			spans = append(spans, query.SymbolSpan{Name: nameOfNode(w, []string{"identifier"}, false), StartLine: start, EndLine: end})
		case "method_declaration":
			spans = append(spans, query.SymbolSpan{Name: nameOfNode(w, []string{"field_identifier"}, false), StartLine: start, EndLine: end})
		case "type_declaration":
			for _, spec := range descendantsAny(w, []string{"type_spec"}) {
				spans = append(spans, query.SymbolSpan{Name: nameOfNode(spec, []string{"type_identifier"}, false), StartLine: start, EndLine: end})
			}
		}
	}
	return spans
}
