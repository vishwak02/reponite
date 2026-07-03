//go:build treesitter

// resolve_go.go is the Go-specific precise edge resolver: it type-checks the
// module and, per caller, records the exact qualified id each call resolves to
// when the target is a concrete in-repo function/method. This upgrades the
// name-based heuristic (resolve.go) — a call the type checker pins down becomes
// a go-types edge at full confidence; interface/dynamic and external calls are
// left to name resolution. Best-effort: if the module can't be loaded it returns
// an empty map and indexing falls back entirely to name-based edges. Extraction
// stays language-agnostic (tree-sitter); this only refines Go edge confidence.
package processing

import (
	"go/ast"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

// TypeResolvedEdges returns callerQID -> (base callee name -> precise callee QID)
// for calls the type checker resolves to a concrete in-repo target. QIDs match
// IndexFiles' scheme (package directory relative to dir, plus receiver).
func TypeResolvedEdges(dir string) map[string]map[string]string {
	out := map[string]map[string]string{}
	// Absolute base: go/packages reports absolute GoFiles, and IndexFiles keys
	// packages by their directory relative to the indexed root — relate the two
	// against an absolute dir (Rel against a relative "." would fail silently).
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return out
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir: absDir,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return out
	}

	// Directory (relative to absDir) of each type-checked package, so callee
	// packages map onto the same qids IndexFiles derives from file paths.
	pkgDir := map[*types.Package]string{}
	for _, p := range pkgs {
		if p.Types == nil || len(p.GoFiles) == 0 {
			continue
		}
		rel, err := filepath.Rel(absDir, filepath.Dir(p.GoFiles[0]))
		if err != nil || rel == "." {
			rel = ""
		}
		pkgDir[p.Types] = rel
	}

	// qidOf maps a resolved function/method to its qualified id, or ("",false)
	// for external packages and interface (abstract) methods.
	qidOf := func(fn *types.Func) (string, bool) {
		if fn == nil || fn.Pkg() == nil {
			return "", false
		}
		d, ok := pkgDir[fn.Pkg()]
		if !ok {
			return "", false // external / stdlib
		}
		name := fn.Name()
		if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
			if isInterfaceType(sig.Recv().Type()) {
				return "", false // dynamic dispatch — can't pin the concrete body
			}
			name = namedTypeName(sig.Recv().Type()) + "." + name
		}
		return qualify(d, name), true
	}

	for _, p := range pkgs {
		if p.TypesInfo == nil {
			continue
		}
		dRel := pkgDir[p.Types]
		for _, file := range p.Syntax {
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok {
					continue
				}
				caller := callerQID(dRel, fd)
				ast.Inspect(fd, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					fn := calleeFunc(p.TypesInfo, call.Fun)
					if fn == nil {
						return true
					}
					if qid, ok := qidOf(fn); ok {
						if out[caller] == nil {
							out[caller] = map[string]string{}
						}
						out[caller][fn.Name()] = qid
					}
					return true
				})
			}
		}
	}
	return out
}

// calleeFunc resolves a call's function expression to the *types.Func it targets.
func calleeFunc(info *types.Info, fun ast.Expr) *types.Func {
	switch e := fun.(type) {
	case *ast.Ident: // f()
		if fn, ok := info.Uses[e].(*types.Func); ok {
			return fn
		}
	case *ast.SelectorExpr: // x.M() or pkg.F()
		if sel, ok := info.Selections[e]; ok {
			if fn, ok := sel.Obj().(*types.Func); ok {
				return fn
			}
		}
		if fn, ok := info.Uses[e.Sel].(*types.Func); ok {
			return fn
		}
	}
	return nil
}

// callerQID builds the qualified id of a declared func/method (matching qualify).
func callerQID(dRel string, fd *ast.FuncDecl) string {
	name := fd.Name.Name
	if fd.Recv != nil && len(fd.Recv.List) > 0 {
		name = astRecvName(fd.Recv.List[0].Type) + "." + name
	}
	return qualify(dRel, name)
}

// astRecvName is the receiver type name from a receiver expr (strips pointer and
// generic type parameters): *Store -> Store, Store[T] -> Store.
func astRecvName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return astRecvName(t.X)
	case *ast.IndexExpr:
		return astRecvName(t.X)
	case *ast.IndexListExpr:
		return astRecvName(t.X)
	case *ast.Ident:
		return t.Name
	}
	return ""
}

func namedTypeName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := t.(*types.Named); ok {
		return n.Obj().Name()
	}
	return ""
}

func isInterfaceType(t types.Type) bool {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	_, ok := t.Underlying().(*types.Interface)
	return ok
}
