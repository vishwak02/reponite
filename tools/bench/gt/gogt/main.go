//go:build bench

// gogt prints the ground truth of a Go module for the reponite benchmark: every
// function and method definition with its span, and every call from one to
// another that the type checker resolves statically. Calls through an
// interface or a function value are listed as "dynamic" (their target is not
// known statically) and are not counted as ground-truth edges.
//
//	go run -tags bench ./tools/bench/gt/gogt <module dir> > truth.json
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type def struct {
	ID   string `json:"id"` // path:line of the declaration
	Path string `json:"path"`
	Line int    `json:"line"`
	End  int    `json:"end"`
	Name string `json:"name"` // Func or Recv.Method
	Test bool   `json:"test"`
}

type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"` // static | dynamic
	Line int    `json:"line"` // the call's line
}

type truth struct {
	Lang  string `json:"lang"`
	Defs  []def  `json:"defs"`
	Edges []edge `json:"edges"`
	Note  string `json:"note,omitempty"`
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: gogt <module dir>")
		os.Exit(2)
	}
	root, _ := filepath.Abs(os.Args[1])
	cfg := &packages.Config{
		Mode:  packages.NeedName | packages.NeedFiles | packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo,
		Dir:   root,
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	t := truth{Lang: "go"}
	var loadErrs int
	rel := func(fset *token.FileSet, p token.Pos) (string, int) {
		pos := fset.Position(p)
		r, err := filepath.Rel(root, pos.Filename)
		if err != nil || strings.HasPrefix(r, "..") {
			return "", 0
		}
		return filepath.ToSlash(r), pos.Line
	}
	seenDef := map[string]bool{}
	seenEdge := map[string]bool{}
	// a declaration position for every function object defined in the module
	declOf := map[types.Object]string{}
	modulePkg := map[string]bool{} // import paths of the module's own packages
	for _, p := range pkgs {
		modulePkg[p.PkgPath] = true
	}
	for _, p := range pkgs {
		loadErrs += len(p.Errors)
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok {
					continue
				}
				path, line := rel(p.Fset, fd.Name.Pos())
				if path == "" {
					continue
				}
				_, end := rel(p.Fset, fd.End())
				name := fd.Name.Name
				if fd.Recv != nil && len(fd.Recv.List) > 0 {
					name = recvName(fd.Recv.List[0].Type) + "." + name
				}
				id := fmt.Sprintf("%s:%d", path, line)
				if obj := p.TypesInfo.Defs[fd.Name]; obj != nil {
					declOf[obj] = id
				}
				if !seenDef[id] {
					seenDef[id] = true
					t.Defs = append(t.Defs, def{ID: id, Path: path, Line: line, End: end, Name: name, Test: strings.HasSuffix(path, "_test.go")})
				}
			}
		}
	}
	// generic instantiations and test variants share declarations: map by position
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				fpath, fline := rel(p.Fset, fd.Name.Pos())
				if fpath == "" {
					continue
				}
				from := fmt.Sprintf("%s:%d", fpath, fline)
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					var id *ast.Ident
					switch fn := ast.Unparen(call.Fun).(type) {
					case *ast.Ident:
						id = fn
					case *ast.SelectorExpr:
						id = fn.Sel
					case *ast.IndexExpr: // generic call f[T](x)
						if x, ok := fn.X.(*ast.Ident); ok {
							id = x
						} else if s, ok := fn.X.(*ast.SelectorExpr); ok {
							id = s.Sel
						}
					}
					if id == nil {
						return true
					}
					obj := p.TypesInfo.Uses[id]
					fn, ok := obj.(*types.Func)
					if !ok {
						return true
					}
					// pkg.F(): F of that package. With the package's import failed
					// the checker can bind the name to a same-named F in scope
					if sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr); ok {
						if x, ok := sel.X.(*ast.Ident); ok {
							switch o := p.TypesInfo.Uses[x].(type) {
							case nil:
								return true // an unresolved qualifier (a failed import)
							case *types.PkgName:
								if fn.Pkg() != o.Imported() {
									return true
								}
							}
						}
					}
					fn = fn.Origin()
					to := declOf[fn]
					if to == "" {
						// declared in another package of the module (loaded separately);
						// a package outside it whose import failed can carry a
						// position borrowed from the module's own code
						if fn.Pkg() == nil || !modulePkg[fn.Pkg().Path()] {
							return true
						}
						tp, tl := rel(p.Fset, fn.Pos())
						if tp == "" {
							return true // outside the module
						}
						to = fmt.Sprintf("%s:%d", tp, tl)
						if !seenDef[to] {
							return true
						}
					}
					kind := "static"
					if sig, ok := fn.Type().(*types.Signature); ok && sig.Recv() != nil {
						if types.IsInterface(sig.Recv().Type()) {
							kind = "dynamic"
						}
					}
					_, cl := rel(p.Fset, call.Pos())
					k := from + ">" + to
					if !seenEdge[k] {
						seenEdge[k] = true
						t.Edges = append(t.Edges, edge{From: from, To: to, Kind: kind, Line: cl})
					}
					return true
				})
			}
		}
	}
	sort.Slice(t.Defs, func(i, j int) bool { return t.Defs[i].ID < t.Defs[j].ID })
	sort.Slice(t.Edges, func(i, j int) bool {
		return t.Edges[i].From+t.Edges[i].To < t.Edges[j].From+t.Edges[j].To
	})
	if loadErrs > 0 {
		t.Note = fmt.Sprintf("%d package load errors (types may be partial)", loadErrs)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", " ")
	if err := enc.Encode(t); err != nil {
		os.Exit(1)
	}
}

func recvName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.StarExpr:
		return recvName(x.X)
	case *ast.IndexExpr:
		return recvName(x.X)
	case *ast.IndexListExpr:
		return recvName(x.X)
	case *ast.Ident:
		return x.Name
	}
	return "?"
}
