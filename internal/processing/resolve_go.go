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
	"regexp"
	"runtime"
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
// same resolver as a working tree. paths are every tracked file — the Go
// compiler needs the ones the index skips too (a large generated mock, an
// assembly file, a //go:embed asset), or their packages do not build; read
// returns a tracked file's content.
func typeCheckTree(paths []string, manifests map[string][]byte, read func(string) (string, bool)) map[string]map[string][]string {
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
	goDirs := map[string]bool{}
	var embeds []string // dir-relative patterns, as "dir\x00pattern"
	n := 0
	for _, p := range paths {
		if !strings.HasSuffix(p, ".go") {
			continue
		}
		c, ok := read(p)
		if !ok || !write(p, c) {
			continue
		}
		n++
		goDirs[path.Dir(p)] = true
		for _, m := range goEmbedRe.FindAllStringSubmatch(c, -1) {
			for _, pat := range strings.Fields(m[1]) {
				embeds = append(embeds, path.Dir(p)+"\x00"+strings.Trim(pat, "\"`"))
			}
		}
	}
	if n == 0 {
		return nil
	}
	for _, p := range paths {
		dir := path.Dir(p)
		keep := false
		switch strings.ToLower(path.Ext(p)) {
		case ".s", ".c", ".h", ".cc", ".cpp", ".hh", ".hpp", ".m", ".syso":
			keep = goDirs[dir] // cgo and assembly beside the package's Go files
		}
		for _, e := range embeds {
			d, pat, _ := strings.Cut(e, "\x00")
			rel := strings.TrimPrefix(p, d+"/")
			if d == "." {
				rel = p
			}
			if rel == p && d != "." {
				continue
			}
			pat = strings.TrimPrefix(pat, "all:")
			if ok, _ := path.Match(pat, rel); ok || strings.HasPrefix(rel, strings.TrimSuffix(pat, "/")+"/") {
				keep = true
				break
			}
		}
		if keep {
			if c, ok := read(p); ok {
				write(p, c)
			}
		}
	}
	// as written first: a go directive newer than this toolchain is fine when
	// a matching toolchain is cached. When packages then fail to build, the
	// directive capped at this toolchain may do better; the run that resolves
	// more calls is kept.
	out, notes, bad := typeResolvedEdges(tmp)
	if bad > 0 {
		capped := false
		for p, b := range manifests {
			if path.Base(p) == "go.mod" {
				if c := capGoDirective(string(b)); c != string(b) {
					write(p, c)
					capped = true
				}
			}
		}
		if capped {
			if out2, notes2, _ := typeResolvedEdges(tmp); countEdges(out2) > countEdges(out) {
				out, notes = out2, notes2
			}
		}
	}
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, n)
	}
	return out
}

func countEdges(m map[string]map[string][]string) int {
	n := 0
	for _, byName := range m {
		for _, qs := range byName {
			n += len(qs)
		}
	}
	return n
}

// goEmbedRe reads a //go:embed directive's patterns.
var goEmbedRe = regexp.MustCompile(`(?m)^\s*//go:embed\s+(.+)$`)

// TypeResolvedEdges returns callerQID -> (base callee name -> precise callee QIDs)
// for calls the type checker resolves to a concrete in-repo target. QIDs match
// IndexFiles' scheme (package directory relative to dir, plus receiver). Every
// function of a package that type-checked without errors has an entry, empty
// when it calls nothing in the repo: its other calls are proven to go to a
// library or through an interface, and are never guessed by name. Test files
// are checked too.
func TypeResolvedEdges(dir string) map[string]map[string][]string {
	out, notes, _ := typeResolvedEdges(dir)
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, n)
	}
	return out
}

// typeResolvedEdges is TypeResolvedEdges with its diagnostics returned, and
// how many module loads failed or packages could not build.
func typeResolvedEdges(dir string) (map[string]map[string][]string, []string, int) {
	out := map[string]map[string][]string{}
	var notes []string
	problems := 0
	// Absolute base: go/packages reports absolute GoFiles, and IndexFiles keys
	// packages by their directory relative to the indexed root — relate the two
	// against an absolute dir (Rel against a relative "." would fail silently).
	absDir, err := filepath.Abs(dir)
	if err != nil {
		return out, nil, 1
	}
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo | packages.NeedDeps | packages.NeedImports,
		Dir:   absDir,
		Env:   offlineGoEnv(),
		Tests: true,
	}
	// every module of the tree: the root's, and nested ones (a repo whose Go
	// code is a module in a subdirectory, or several modules side by side)
	var pkgs []*packages.Package
	for _, mod := range moduleDirs(absDir) {
		c := *cfg
		c.Dir = mod
		ps, err := packages.Load(&c, "./...")
		if err != nil {
			notes = append(notes, fmt.Sprintf("reponite: Go type check unavailable (%v); Go calls are resolved by name", err))
			problems++
			continue
		}
		pkgs = append(pkgs, ps...)
	}
	if len(pkgs) == 0 {
		return out, notes, problems
	}
	// a package whose only error is a go directive newer than this build of
	// the checker is checked all the same: the checker carries on past it
	broken := map[*packages.Package]bool{}
	failed, firstErr := 0, ""
	for _, p := range pkgs {
		for _, e := range p.Errors {
			if strings.Contains(e.Msg, "requires newer Go version") {
				continue
			}
			broken[p] = true
			failed++
			if firstErr == "" {
				firstErr = e.Error()
			}
			break
		}
	}
	if failed > 0 {
		notes = append(notes, fmt.Sprintf("reponite: Go type check incomplete in %d of %d packages (%s); their calls are resolved by name", failed, len(pkgs), firstErr))
		problems += failed
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
				if fd.Recv == nil && fd.Name.Name == "init" {
					caller = qualify(dRel, goInitName(p.Fset.File(fd.Pos()).Name())) // nameGoInits
				}
				if !broken[p] {
					if out[caller] == nil {
						out[caller] = map[string][]string{}
					}
					out[caller][goCheckedKey] = nil // checked: no guessing for this caller
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
					qid, ok := qidOf(fn)
					if !ok {
						if !broken[p] {
							return true // the checked marker already says so
						}
						if f := p.Fset.Position(fn.Pos()).Filename; f == "" || strings.HasPrefix(f, absDir+string(filepath.Separator)) {
							return true // the repo's own, in a package the checker could not place
						}
						// a package that did not fully build: this call is
						// proven to reach no repo function, even if others are not
						qid = goExternalMark
					}
					if out[caller] == nil {
						out[caller] = map[string][]string{}
					}
					// one name may reach several targets (a.F and b.F)
					if !containsStr(out[caller][fn.Name()], qid) {
						out[caller][fn.Name()] = append(out[caller][fn.Name()], qid)
					}
					return true
				})
			}
		}
	}
	return out, notes, problems
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

// moduleDirs are the directories under root holding a go.mod (root itself
// when it has none: a GOPATH-style tree is still loaded from there).
func moduleDirs(root string) []string {
	var out []string
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && p != root && (strings.HasPrefix(d.Name(), ".") || d.Name() == "vendor" || d.Name() == "testdata" || d.Name() == "node_modules") {
			return filepath.SkipDir
		}
		if !d.IsDir() && d.Name() == "go.mod" {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	if len(out) == 0 {
		out = []string{root}
	}
	return out
}

var reGoDirective = regexp.MustCompile(`(?m)^go\s+(\d+)\.(\d+)(?:\.\d+)?\s*$`)
var reToolchain = regexp.MustCompile(`(?m)^toolchain\s+\S+\s*$`)

// capGoDirective lowers a go.mod's language version to the one this binary's
// type checker knows (go/types refuses newer code outright) and drops a
// toolchain line: the check is of names and types, not of new language
// features, and with the proxy off no other toolchain could be fetched.
func capGoDirective(gomod string) string {
	var maj, min int
	if _, err := fmt.Sscanf(strings.TrimPrefix(runtime.Version(), "go"), "%d.%d", &maj, &min); err != nil {
		return gomod
	}
	gomod = reToolchain.ReplaceAllString(gomod, "")
	return reGoDirective.ReplaceAllStringFunc(gomod, func(m string) string {
		sm := reGoDirective.FindStringSubmatch(m)
		var a, b int
		fmt.Sscanf(sm[1]+" "+sm[2], "%d %d", &a, &b)
		if a > maj || a == maj && b > min {
			return fmt.Sprintf("go %d.%d", maj, min)
		}
		return m
	})
}
