//go:build treesitter

// index_ts.go is the thin tree-sitter layer over the pure IndexFiles: it walks a
// repo's *.go files, parses each, extracts symbols, computes line spans (from
// tree-sitter positions), and hands everything to IndexFiles. CGO (tree-sitter),
// so it is behind the treesitter build tag and verified in CI (ADR-018).
package processing

import (
	"os"
	"path/filepath"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
)

// IndexDir indexes every supported source file under dir as one repo ref. Each
// file is dispatched by extension to its LangRules (lang.go); unknown extensions
// are skipped. Go edges are refined by the type checker; other languages use
// name-based resolution.
func IndexDir(w Indexer, repo, ref, dir string, normVer int) error {
	var files []ParsedFile
	hasGo := false
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
		rules, ok := RulesForExt(filepath.Ext(path))
		if !ok {
			return nil
		}
		src, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		root, spans, parseErr := parseFileRules(src, filepath.Ext(path), rules)
		if parseErr != nil {
			return parseErr
		}
		if root == nil {
			return nil // no grammar bound for this extension; skip
		}
		if rules.Name == "go" {
			hasGo = true
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		files = append(files, ParsedFile{Path: rel, Content: string(src), Lang: rules.Name, Symbols: Extract(root, rules, normVer), Spans: spans})
		return nil
	})
	if err != nil {
		return err
	}
	// Refine Go edges with the type checker where it can prove a concrete target
	// (best-effort; only worth loading go/packages when the repo contains Go —
	// other languages fall back to name-based resolution).
	var precise map[string]map[string]string
	if hasGo {
		precise = TypeResolvedEdges(dir)
	}
	return indexFiles(w, repo, ref, normVer, files, precise)
}

// parseFileRules parses source with the grammar for ext, returning the
// content.AST (for Extract) and the symbol line spans (for grep fusion).
func parseFileRules(src []byte, ext string, r LangRules) (content.AST, []query.SymbolSpan, error) {
	lang := grammarForExt(ext)
	if lang == nil {
		return nil, nil, nil // no grammar bound; caller skips
	}
	root, err := parseRoot(src, lang)
	if err != nil {
		return nil, nil, err
	}
	return tsNode{n: root, src: src}, spansFor(root, src, r), nil
}

// spansFor returns 1-based line ranges for each declaration, mirroring Extract:
// functions/methods produce a span; a type/class produces a span and is
// descended into so its nested methods also get spans (grep fusion picks the
// innermost enclosing span, so a nested method wins over its class).
func spansFor(root *sitter.Node, src []byte, r LangRules) []query.SymbolSpan {
	var spans []query.SymbolSpan
	var walk func(n *sitter.Node)
	walk = func(n *sitter.Node) {
		for i := 0; i < int(n.ChildCount()); i++ {
			ch := n.Child(i)
			w := tsNode{n: ch, src: src}
			start := int(ch.StartPoint().Row) + 1
			end := int(ch.EndPoint().Row) + 1
			t := ch.Type()
			switch {
			case containsStr(r.FuncDecl, t), containsStr(r.MethodDecl, t):
				spans = append(spans, query.SymbolSpan{Name: nameOf(w, r), StartLine: start, EndLine: end})
			case containsStr(r.TypeDecl, t):
				if len(r.TypeSpec) > 0 {
					for _, spec := range descendantsAny(w, r.TypeSpec) {
						spans = append(spans, query.SymbolSpan{Name: nameOfNode(spec, r.NameTypes, false), StartLine: start, EndLine: end})
					}
				} else {
					spans = append(spans, query.SymbolSpan{Name: nameOf(w, r), StartLine: start, EndLine: end})
				}
				walk(ch) // descend for nested methods (class/type bodies)
			default:
				walk(ch)
			}
		}
	}
	walk(root)
	return spans
}
