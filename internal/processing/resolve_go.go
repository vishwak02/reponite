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
	"fmt"
	"go/ast"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"strings"

	"golang.org/x/tools/go/packages"
)

// offlineGoEnv keeps type-checking local: no module downloads during
// indexing. Dependencies already in the module cache are used; a missing one
// leaves its callers' calls to the name rules.
func offlineGoEnv() []string {
	// the toolchain is left to go.mod: a cached one that matches is used, and
	// with the proxy off none is downloaded
	return append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod", "GOWORK=off")
}

// typeCheckTree type-checks a git revision's Go code: it writes the
// revision's Go files and module files to a temporary directory and runs the
// same resolver as a working tree. read returns a tracked file's content.
func typeCheckTree(files []ParsedFile, manifests map[string][]byte, read func(string) (string, bool)) map[string]map[string][]string {
	tmp, err := os.MkdirTemp("", "reponite-gotypes-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(tmp)
	write := func(rel, content string) bool {
		dst := filepath.Join(tmp, filepath.FromSlash(rel))
		if os.MkdirAll(filepath.Dir(dst), 0o755) != nil {
			return false
		}
		return os.WriteFile(dst, []byte(content), 0o644) == nil
	}
	for p, b := range manifests {
		if path.Base(p) != "go.mod" {
			continue
		}
		if !write(p, string(b)) {
			return nil
		}
		if sum, ok := read(path.Join(path.Dir(p), "go.sum")); ok {
			write(path.Join(path.Dir(p), "go.sum"), sum)
		}
	}
	n := 0
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".go") {
			continue
		}
		c, ok := read(f.Path)
		if !ok || !write(f.Path, c) {
			continue
		}
		n++
	}
	if n == 0 {
		return nil
	}
	return TypeResolvedEdges(tmp)
}

// TypeResolvedEdges returns callerQID -> (base callee name -> precise callee QIDs)
// for calls the type checker resolves to a concrete in-repo target. QIDs match
// IndexFiles' scheme (package directory relative to dir, plus receiver). Every
// function of a package that type-checked without errors has an entry, empty
// when it calls nothing in the repo: its other calls are proven to go to a
// library or through an interface, and are never guessed by name. Test files
// are checked too.
func TypeResolvedEdges(dir string) map[string]map[string][]string {
	out := map[string]map[string][]string{}
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
		Dir:   absDir,
		Env:   offlineGoEnv(),
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: Go type check unavailable (%v); Go calls are resolved by name\n", err)
		return out
	}
	failed, firstErr := 0, ""
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			failed++
			if firstErr == "" {
				firstErr = p.Errors[0].Error()
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(os.Stderr, "reponite: Go type check incomplete in %d of %d packages (%s); their calls are resolved by name\n", failed, len(pkgs), firstErr)
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
				if len(p.Errors) == 0 && out[caller] == nil {
					out[caller] = map[string][]string{} // checked: no guessing for this caller
				}
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
							out[caller] = map[string][]string{}
						}
						// one name may reach several targets (a.F and b.F)
						if !containsStr(out[caller][fn.Name()], qid) {
							out[caller][fn.Name()] = append(out[caller][fn.Name()], qid)
						}
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
