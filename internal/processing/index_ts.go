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
// name-based resolution. Vendored trees are excluded via the default ignore
// set + the repo's .reponiteignore (see IndexDirWith for --exclude globs).
func IndexDir(w Indexer, repo, ref, dir string, normVer int) error {
	return IndexDirWith(w, repo, ref, dir, normVer, IndexOptions{})
}

// IndexDirWith is IndexDir with caller-supplied filters (CLI --exclude).
func IndexDirWith(w Indexer, repo, ref, dir string, normVer int, opt IndexOptions) error {
	ig := loadIgnore(dir, opt.Excludes)
	var files []ParsedFile
	hasGo := false
	manifests := map[string][]byte{} // module-manifest files (go.mod, package.json, ...) by rel path
	err := filepath.Walk(dir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		if info.IsDir() {
			if path != dir {
				// Dot-dirs (.git, .reponite, …) are always skipped; the ignore
				// set adds vendor/third_party/node_modules/… + .reponiteignore
				// + --exclude.
				if strings.HasPrefix(info.Name(), ".") || ig.Excluded(rel, true) {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if ig.Excluded(rel, false) {
			return nil
		}
		// Module manifests carry the repo's identity, not symbols; collect for
		// module_path detection (§8B.2). They are never source, so return after.
		if IsManifestFile(path) {
			if src, readErr := os.ReadFile(path); readErr == nil {
				manifests[rel] = src
			}
			return nil
		}
		// ROS interface files (.msg/.srv/.action) are pure text, not tree-sitter.
		if IsROSFile(path) {
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			if pf, ok := rosFile(rel, string(src)); ok {
				files = append(files, pf)
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
		files = append(files, ParsedFile{
			Path: rel, Content: string(src), Lang: rules.Name,
			Symbols: Extract(root, rules, normVer), Spans: spans, Imports: Imports(root, rules),
		})
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
	if err := indexFiles(w, repo, ref, normVer, files, precise); err != nil {
		return err
	}
	if mod, ok := DetectModulePath(manifests); ok {
		return w.SetModulePath(repo, mod)
	}
	return nil
}

// loadIgnore builds the index-time exclusion set: defaults + the repo's
// .reponiteignore (root-level, gitignore syntax; absent is fine) + --exclude.
func loadIgnore(dir string, excludes []string) *Ignore {
	content := ""
	if b, err := os.ReadFile(filepath.Join(dir, ".reponiteignore")); err == nil {
		content = string(b)
	}
	return NewIgnore(content, excludes)
}

// ParseEditedSymbols parses proposed file content and returns its symbols as
// query.EditedSymbol (name + receiver + body-independent signature hash), for
// reponite_verify_edit's diff against the indexed version. Repo is left empty so
// an old-vs-new parse of the same file yields comparable signature hashes.
// Returns nil for an unsupported/unparseable extension.
func ParseEditedSymbols(path, src string, normVer int) []query.EditedSymbol {
	rules, ok := RulesForExt(filepath.Ext(path))
	if !ok {
		return nil
	}
	root, _, err := parseFileRules([]byte(src), filepath.Ext(path), rules)
	if err != nil || root == nil {
		return nil
	}
	var out []query.EditedSymbol
	for _, s := range Extract(root, rules, normVer) {
		id := content.SymbolIdentity{Lang: rules.Name, Kind: s.Kind, Signature: s.Signature, CanonBody: s.CanonBody}
		out = append(out, query.EditedSymbol{Name: s.Name, Recv: s.Recv, SignatureHash: content.SignatureHash(normVer, id)})
	}
	return out
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
// innermost enclosing span, so a nested method wins over its class). Mirrors
// Extract's honesty rules: an anonymous callable produces NO span (a ""-named
// inner span would occlude the real enclosing symbol) and a bare type
// reference (`struct Foo x;`) is not a declaration.
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
				if name := nameOf(w, r); name != "" {
					spans = append(spans, query.SymbolSpan{Name: name, StartLine: start, EndLine: end})
				}
			case containsStr(r.TypeDecl, t) && isTypeReference(w, r):
				walk(ch) // a use, not a definition — no span
			case containsStr(r.TypeDecl, t):
				if len(r.TypeSpec) > 0 {
					for _, spec := range descendantsAny(w, r.TypeSpec) {
						spans = append(spans, query.SymbolSpan{Name: nameOfNode(spec, r.NameTypes, false), StartLine: start, EndLine: end})
					}
				} else if name := nameOf(w, r); name != "" {
					spans = append(spans, query.SymbolSpan{Name: name, StartLine: start, EndLine: end})
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
