// index.go is the indexer core: given a repo ref's parsed files, it computes the
// three hashes for every symbol, resolves callees to confidence-tagged CALLS
// edges (resolve.go), runs the behavior-hash pass over the whole-ref graph, and
// writes the resulting records + files to a Store. It is PURE (backend-agnostic),
// so it is unit-tested in-sandbox against storage.Mem (ADR-018); the thin
// treesitter-tagged layer (ParseGo + ExtractGo + IndexFiles) supplies real files.
package processing

import (
	"fmt"
	"path"
	"strings"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/scip"
	"github.com/vishwak02/reponite/internal/storage"
)

// Indexer is the write surface the indexer needs; satisfied by storage.Mem and
// the SQLite adapter (storage/sqlite).
type Indexer interface {
	// ClearRef drops a ref's existing symbols/files/external-refs so a reindex
	// replaces rather than accumulates (vanished rows must not linger).
	ClearRef(repo, ref string) error
	Put(repo, ref, name string, rec storage.SymbolRecord) error
	PutFile(repo, ref string, f query.File) error
	// PutExternalRefs records a ref's cross-repo dependency edges (§8B).
	PutExternalRefs(repo, ref string, refs []query.ExternalRef) error
	// PutMonikers records each symbol's SCIP moniker at a ref (§8B.4).
	PutMonikers(repo, ref string, monikers map[string]string) error
	// SetModulePath records repo's module/package identity (idempotent per repo).
	SetModulePath(repo, modulePath string) error
}

var _ Indexer = (*storage.Mem)(nil)

// ParsedFile is a file's extracted symbols plus optional line spans (for grep
// fusion), produced by the tree-sitter layer.
type ParsedFile struct {
	Path    string
	Content string
	Lang    string // language name (lang.go); empty defaults to "go" for back-compat
	Symbols []Symbol
	Spans   []query.SymbolSpan
	// Imports are the file's external import bindings (imports.go), used to
	// resolve qualified calls to (module, name) external references (§8B).
	Imports []ImportBinding
	// IsTest marks this file as test code (§9A.1). Captured here because a
	// symbol's stored id keeps the file's directory but not its basename, so
	// `test_foo.py` cannot be recognized downstream.
	IsTest bool
	// SCIP is this file's contribution from a SCIP index, when the repo has
	// one: each locally defined symbol's globally unique moniker, and its
	// references to monikers defined elsewhere (§8B.4, Phase 6b). Zero value =
	// no SCIP index, and every downstream tier behaves exactly as before.
	SCIP scip.FileMonikers
	// Types are the file's C++ class/field/base/alias facts (cpptypes.go), for
	// resolving member calls by the receiver's declared type. nil elsewhere.
	Types *TypeFacts
	// Load, when set, re-reads the file's content for storage; Content is
	// then empty. A parsed source file's text is needed only when it is
	// stored, so a large repo does not hold all of it until then.
	Load func() (string, error)
}

// IndexFiles indexes all files of one repo ref with name-based edge resolution.
// Symbols are keyed by a package-qualified id (pkg.name, pkg = the file's
// directory) so distinct definitions sharing a bare name (e.g. storage.Mem.Put
// vs sqlite.Store.Put) are distinct nodes and never conflated (correctness).
func IndexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile) error {
	return indexFiles(w, repo, ref, normVer, files, nil, nil, nil)
}

// indexFiles is IndexFiles with an optional precise edge map (callerQID -> base
// callee name -> type-checker-proven callee QID) that upgrades matching edges to
// go-types confidence; nil precise means pure name-based resolution.
// goMods maps each Go module's directory to its module path (goModules), so
// a Go call through an import resolves to the imported package's directory;
// nil when the tree has no go.mod.
func indexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile, precise map[string]map[string][]string, peers query.Store, goMods map[string]string) error {
	type computed struct {
		sym        Symbol
		pkg        string
		lang       string
		isTest     bool
		symbolHash content.Hash
		sigHash    content.Hash
		imports    map[string]ImportBinding // the defining file's import bindings
		path       string                   // the defining file
	}
	var order []string              // qualified ids, first-seen order
	arity := map[string][2]int{}    // C/C++ qid -> parameter count range
	byQID := map[string]computed{}  // qid -> facts
	byBase := map[string][]string{} // bare name -> defining qids (for edge resolution)
	var extRefs []query.ExternalRef // cross-repo dependency edges (§8B)
	monikers := map[string]string{} // qid -> SCIP moniker (§8B.4)
	linkRobotEmbedded(files)
	nameGoInits(files)
	mods := newModuleIndex(files)
	classes := newClassGraph()
	type classDecl struct {
		key, path, lang string
		bases           []QualifiedCall
		byLocal         map[string]ImportBinding
	}
	var classDecls []classDecl
	for _, f := range files {
		lang := f.Lang
		if lang == "" {
			lang = "go" // back-compat: pure callers/tests predate the Lang field
		}
		pkg := pkgOfFile(f.Path, lang)
		byLocal := importsByLocal(moduleLevelImports(f.Imports, f.Symbols))
		if langFamily(lang) == "js" {
			// import X from './m' binds X to what m exports as default
			for k, b := range byLocal {
				if b.Default && b.Symbol == "" {
					if mp, ok := mods.resolve(lang, f.Path, b.Module); ok && mods.defaults[mp] != "" {
						b.Symbol = mods.defaults[mp]
						byLocal[k] = b
					}
				}
			}
		}
		if lang == "java" {
			// import a.b.C binds C to class C of package a.b
			for k, b := range byLocal {
				if b.Symbol == "" && isTypeName(b.Local) {
					b.Symbol = b.Local
					byLocal[k] = b
				}
			}
		}
		for _, s := range f.Symbols {
			// Methods qualify by receiver too (pkg.Recv.name) so same-named methods
			// on different types are distinct; edge resolution still keys off the
			// bare name (byBase).
			local := s.Name
			if s.Recv != "" {
				local = s.Recv + "." + s.Name
			}
			qid := qualifyModule(pkg, local)
			if prev, dup := byQID[qid]; dup && s.Recv != "" && prev.path != f.Path && isCSource(f.Path) && isCSource(prev.path) {
				// Cls::f defined in two source files of one directory: two classes
				// of one name (each test file its own fixture), not one class —
				// the later one is Cls@file
				s.Recv += "@" + strings.TrimSuffix(path.Base(f.Path), path.Ext(f.Path))
				local = s.Recv + "." + s.Name
				qid = qualifyModule(pkg, local)
			}
			id := content.SymbolIdentity{Repo: repo, Lang: lang, Kind: s.Kind, Signature: s.Signature, CanonBody: s.CanonBody}
			if _, dup := byQID[qid]; !dup {
				order = append(order, qid)
				byBase[s.Name] = append(byBase[s.Name], qid)
			}
			sh := s.symHash
			if sh == "" {
				sh = content.SymbolHash(normVer, id)
			}
			imports := withLocalImports(byLocal, s.LocalImports)
			own := s // this definition's own parameter count, before any merge
			if prev, dup := byQID[qid]; dup && lang != "python" && prev.path == f.Path {
				// Overloads (Java, C++, C#, TypeScript signatures) share one id:
				// the id calls what any of them calls. (Python: a later def
				// replaces the earlier one, so the last body stands.)
				s = mergeOverload(prev.sym, s)
			}
			byQID[qid] = computed{sym: s, pkg: pkg, lang: lang, isTest: f.IsTest, symbolHash: sh, sigHash: content.SignatureHash(normVer, id), imports: imports, path: f.Path}
			if own.HasParams {
				// overloads sharing the id take any of their parameter counts
				a, seen := arity[qid]
				switch {
				case !seen:
					a = [2]int{own.ParamMin, own.ParamMax}
				default:
					a[0] = min(a[0], own.ParamMin)
					if a[1] >= 0 && (own.ParamMax < 0 || own.ParamMax > a[1]) {
						a[1] = own.ParamMax
					}
				}
				arity[qid] = a
			}
			if len(imports) > 0 {
				extRefs = append(extRefs, resolveExternalRefs(qid, s.QualifiedCalls, imports)...)
			}
			if mon := f.SCIP.Defs[s.Name]; mon != "" {
				monikers[qid] = mon
			}
			if s.Kind == "type" && s.Recv == "" && hasClassBases(lang) {
				k := classes.define(pkg, s.Name)
				classDecls = append(classDecls, classDecl{key: k, path: f.Path, lang: lang, bases: s.Bases, byLocal: byLocal})
			}
		}
		// SCIP references are symbol-resolved: the moniker IS the target's
		// global identity, so these need no module/name guessing (§8B.4).
		for _, r := range f.SCIP.Refs {
			extRefs = append(extRefs, query.ExternalRef{
				From: qualifyModule(pkg, r.From), TargetSymbol: r.Symbol,
				ResolutionMethod: MethodSCIP, Confidence: ConfSCIP,
			})
		}
	}

	nodeSet := make(map[string]bool, len(order))
	x := siteIndex{nodeSet: nodeSet, byBase: byBase, byRecv: map[string][]string{},
		methods: map[string][]string{}, pkgOfQID: map[string]string{}, isTest: map[string]bool{}, isMethod: map[string]bool{}, types: newTypeTable(files)}
	x.arity = arity
	x.vis = newCppVisibility(files)
	x.jsModule = func(pkg string) bool {
		jm := mods.jsMods[pkg]
		return jm != nil && langFamily(jm.lang) == "js"
	}
	if x.vis != nil {
		x.defPath, x.recvOf = map[string]string{}, map[string]string{}
	}
	for _, qid := range order {
		nodeSet[qid] = true
		c := byQID[qid]
		if x.vis != nil {
			x.defPath[qid], x.recvOf[qid] = c.path, c.sym.Recv
		}
		x.pkgOfQID[qid] = c.pkg
		x.isTest[qid] = testish(c.isTest, c.pkg, c.sym.Recv)
		if c.sym.Recv != "" && c.sym.Kind != "type" {
			key := c.sym.Recv + "." + c.sym.Name
			x.byRecv[key] = append(x.byRecv[key], qid)
			x.methods[c.sym.Name] = append(x.methods[c.sym.Name], qid)
			x.isMethod[qid] = true
		}
	}

	dirs := map[string]bool{}
	for _, c := range byQID {
		dirs[pkgOf(c.path)] = true
	}
	inRepo := func(module string) bool { return moduleInRepo(module, dirs) }
	// Python resolves absolute imports for real: one it cannot place in the
	// repo is outside it (`import json` beside a package dir named json)
	inRepoFor := func(lang, path string) func(string) bool {
		if lang != "python" {
			return inRepo
		}
		return func(module string) bool {
			_, ok := mods.resolve(lang, path, module)
			return ok || isRelativeModule(module)
		}
	}
	linkReexport := func(pkg, name string) (string, string, bool) {
		return mods.resolveExport(pkg, name, func(p, n string) bool { _, ok := classes.has(p, n); return ok })
	}
	for _, d := range classDecls {
		d := d
		modPkg := func(module string) (string, bool) { return mods.resolve(d.lang, d.path, module) }
		classes.link(d.key, sitesFromQualified(d.bases, d.byLocal, d.lang, inRepoFor(d.lang, d.path), modPkg), linkReexport)
	}
	x.classes = classes
	var rsPaths []string
	for _, f := range files {
		if f.Lang == "rust" {
			rsPaths = append(rsPaths, f.Path)
		}
	}
	if len(rsPaths) > 0 {
		x.rust = newRustCrate(rsPaths)
	}

	// A call resolves only to a definition its language can call: a Python
	// logger.info() must never land on a shell function info() elsewhere in the
	// repo just because the bare name is unique. Each caller language gets its
	// own view of the definitions (built once per language present).
	type langView struct {
		nodeSet map[string]bool
		byBase  map[string][]string
	}
	views := map[string]langView{}
	viewFor := func(lang string) langView {
		fam := langFamily(lang)
		if v, ok := views[fam]; ok {
			return v
		}
		v := langView{nodeSet: map[string]bool{}, byBase: map[string][]string{}}
		for _, q := range order {
			if canCall(fam, langFamily(byQID[q].lang)) {
				v.nodeSet[q] = true
			}
		}
		for base, qs := range byBase {
			for _, q := range qs {
				if v.nodeSet[q] {
					v.byBase[base] = append(v.byBase[base], q)
				}
			}
		}
		views[fam] = v
		return v
	}

	allDefs := map[string]bool{}
	for _, q := range order {
		allDefs[q] = true
	}
	defined := func(pkg, name string) bool { return allDefs[qualifyModule(pkg, name)] }

	// Python receiver typing (pytypes.go): a dotted type or function name is
	// resolved in the context of the file that writes it
	py := &pyTyper{classes: classes, allDefs: allDefs, isMethod: x.isMethod, fields: map[string]map[string]string{}, returns: map[string]string{}}
	py.ctx = func(q string) (pyCtx, bool) {
		c, ok := byQID[q]
		if !ok {
			return pyCtx{}, false
		}
		return pyCtx{pkg: c.pkg, recv: c.sym.Recv, path: c.path, vars: c.sym.Vars, returns: c.sym.Returns, sites: func(qcs []QualifiedCall) []CallSite {
			modPkg := func(module string) (string, bool) { return mods.resolve(c.lang, c.path, module) }
			return sitesFromQualified(qcs, c.imports, c.lang, inRepoFor(c.lang, c.path), modPkg)
		}}, true
	}
	py.reexport = func(pkg, name string) (string, string, bool) { return mods.resolveExport(pkg, name, defined) }
	py.classByName = map[string][]string{}
	for k, n := range classes.name {
		py.classByName[n] = append(py.classByName[n], k)
	}
	py.funcByName = map[string][]string{}
	for _, q := range order {
		if c := byQID[q]; c.lang == "python" && c.sym.Kind != "type" && c.sym.Recv == "" {
			py.funcByName[c.sym.Name] = append(py.funcByName[c.sym.Name], q)
		}
	}

	nodes := make([]Node, 0, len(order))
	var edges []Edge
	resolved := make(map[string][]query.Callee, len(order))
	for _, qid := range order {
		c := byQID[qid]
		nodes = append(nodes, Node{ID: qid, SymbolHash: c.symbolHash})
		var callees []query.Callee
		x.callerTest, x.caller, x.callerPath = x.isTest[qid], qid, c.path
		v := viewFor(c.lang)
		x.nodeSet, x.byBase = v.nodeSet, v.byBase
		nodeSet, byBase := v.nodeSet, v.byBase
		if c.lang == "go" && len(c.sym.QualifiedCalls) > 0 {
			callees = resolveGoCalls(c.pkg, c.sym.Recv, c.sym.QualifiedCalls, c.imports, goMods, x, precise[qid])
		} else if len(c.sym.CallSites) > 0 && c.lang == "rust" {
			callees = resolveSiteEdgesLang(c.pkg, c.sym.Recv, c.lang, c.sym.CallSites, x)
		} else if len(c.sym.CallSites) > 0 {
			callees = resolveSiteEdges(c.pkg, c.sym.Recv, c.sym.CallSites, x)
		} else if qualifiedSiteLangs[c.lang] && len(c.sym.QualifiedCalls) > 0 {
			modPkg := func(module string) (string, bool) { return mods.resolve(c.lang, c.path, module) }
			qcalls := c.sym.QualifiedCalls
			var typed []CallSite
			if c.lang == "python" {
				qcalls, typed = py.typedCalls(qid, qcalls)
			}
			sites := sitesFromQualified(selfAsPlain(qcalls), c.imports, c.lang, inRepoFor(c.lang, c.path), modPkg)
			sites = append(sites, typed...)
			if c.lang == "python" {
				// a name a package __init__ re-exports: the module defining it
				for i, s := range sites {
					if s.Kind == CallModule && !nodeSet[qualifyModule(s.Scope, s.Name)] {
						if p, n, ok := mods.resolveExport(s.Scope, s.Name, defined); ok {
							sites[i].Scope, sites[i].Name = p, n
						}
					}
				}
			}
			if langFamily(c.lang) == "js" {
				// x.f() on a default import that is an instance of a class:
				// that class's method
				for _, qc := range c.sym.QualifiedCalls {
					b, ok := c.imports[qc.Qualifier]
					if !ok || !b.Default || qc.Chain != 2 {
						continue
					}
					if mp, ok := mods.resolve(c.lang, c.path, b.Module); ok && mods.defaultClass[mp] != "" {
						sites = append(sites, CallSite{Name: qc.Name, Kind: CallTyped, Scope: mods.defaultClass[mp]})
					}
				}
				// x.f() on a named import that is a module-level value: an
				// instance (export const api = new Client()) calls its class's
				// method; an object literal (export const Logger = { error() {} })
				// its own method, a function of the defining module
				isValue := func(pkg, name string) bool { return mods.instances[pkg][name] != "" || mods.objects[pkg][name] }
				receivers := map[string]int{} // method name -> receivers it is called on
				for _, qc := range c.sym.QualifiedCalls {
					receivers[qc.Name]++
				}
				for _, qc := range c.sym.QualifiedCalls {
					b, ok := c.imports[qc.Qualifier]
					if !ok || b.Default || b.Symbol == "" || qc.Chain != 2 {
						continue
					}
					mp, ok := mods.resolve(c.lang, c.path, b.Module)
					if !ok {
						continue
					}
					vp, vn, ok := mods.resolveExport(mp, b.Symbol, isValue)
					if !ok {
						continue
					}
					var site CallSite
					switch {
					case mods.instances[vp][vn] != "":
						site = CallSite{Name: qc.Name, Kind: CallTyped, Scope: mods.instances[vp][vn]}
					case nodeSet[qualifyModule(vp, qc.Name)]:
						site = CallSite{Name: qc.Name, Kind: CallModule, Scope: vp}
					default:
						continue
					}
					// the value's own site replaces the guess made from its name
					kept := sites[:0]
					for _, s := range sites {
						if !(s.Name == qc.Name && (s.Kind == CallScoped && s.Scope == b.Symbol || s.Kind == CallMember && receivers[qc.Name] == 1)) {
							kept = append(kept, s)
						}
					}
					sites = append(kept, site)
				}
				// f().m() on a function whose return class is known
				hasReturn := func(pkg, name string) bool { return mods.returns[pkg][name] != "" }
				for _, qc := range c.sym.QualifiedCalls {
					if qc.Chain != 3 || qc.Attr != "" || qc.Qualifier == "" {
						continue
					}
					fp, fn := pkgOfFile(c.path, c.lang), qc.Qualifier
					if b, ok := c.imports[qc.Qualifier]; ok {
						mp, ok := mods.resolve(c.lang, c.path, b.Module)
						if !ok || b.Symbol == "" {
							continue
						}
						if fp, fn, ok = mods.resolveExport(mp, b.Symbol, hasReturn); !ok {
							continue
						}
					}
					cls := mods.returns[fp][fn]
					if cls == "" {
						continue
					}
					kept := sites[:0]
					for _, s := range sites {
						if !(s.Name == qc.Name && s.Kind == CallMember && receivers[qc.Name] == 1) {
							kept = append(kept, s)
						}
					}
					sites = append(kept, CallSite{Name: qc.Name, Kind: CallTyped, Scope: cls})
				}
				// a name a module passes on (barrel index files, export default
				// of an import): the module that defines it
				for i, s := range sites {
					if s.Kind == CallModule && !nodeSet[qualifyModule(s.Scope, s.Name)] {
						if p, n, ok := mods.resolveExport(s.Scope, s.Name, defined); ok {
							sites[i].Scope, sites[i].Name = p, n
						}
					}
				}
			}
			if c.lang == "java" {
				for i, s := range sites {
					if s.Kind == CallMember && s.Recv != "" {
						if t := c.sym.Vars[s.Recv]; t != "" {
							sites[i].Recv = "v:" + t
						} else {
							sites[i].Recv = "f:" + s.Recv // a field of the method's class
						}
					}
				}
			}
			callees = resolveSiteEdgesLang(c.pkg, c.sym.Recv, c.lang, sites, x)
		} else {
			callees = resolveEdges(c.pkg, c.sym.Callees, nodeSet, byBase, nil)
		}
		// An unresolved target is a NAME, not a symbol: when it equals a real
		// symbol id (a function in a root-level file is just "save"), mark it
		// so no query mistakes it for that symbol.
		for i, ce := range callees {
			if (ce.ResolutionMethod == MethodExternal || ce.ResolutionMethod == MethodAmbiguous) && byQID[ce.Name].sym.Name != "" {
				callees[i].Name = "(" + unresolvedLabel(ce.ResolutionMethod) + ")." + ce.Name
			}
		}
		resolved[qid] = callees
		for _, ce := range callees {
			edges = append(edges, Edge{From: qid, To: ce.Name, Confidence: ce.Confidence})
		}
	}
	beh := ComputeBehavior(nodes, edges, normVer)

	// Replace, don't accumulate: drop the ref's prior records before rewriting.
	if err := w.ClearRef(repo, ref); err != nil {
		return err
	}
	for _, qid := range order {
		c := byQID[qid]
		directConf := 1.0 // min confidence over this symbol's own direct edges
		for _, ce := range resolved[qid] {
			if ce.Confidence < directConf {
				directConf = ce.Confidence
			}
		}
		if err := w.Put(repo, ref, qid, storage.SymbolRecord{
			Lang:          c.lang,
			IsTest:        c.isTest,
			SymbolHash:    c.symbolHash,
			SignatureHash: c.sigHash,
			BehaviorHash:  beh.BehaviorHash[qid],
			BehaviorConf:  beh.BehaviorConf[qid],
			DirectConf:    directConf,
			Callees:       resolved[qid],
		}); err != nil {
			return err
		}
	}
	for _, f := range files {
		src := f.Content
		if f.Load != nil {
			var err error
			if src, err = f.Load(); err != nil {
				return fmt.Errorf("re-read %s: %w", f.Path, err)
			}
		}
		if err := w.PutFile(repo, ref, query.File{Path: f.Path, Content: src, Symbols: f.Spans}); err != nil {
			return err
		}
	}
	captureTargetSignatures(w, peers, repo, extRefs)
	if err := w.PutExternalRefs(repo, ref, extRefs); err != nil {
		return err
	}
	return w.PutMonikers(repo, ref, monikers)
}

// captureTargetSignatures stamps each external ref with the target's signature
// hash AS SEEN NOW (§8B.3 per-caller skew: comparing this captured contract to
// the target's future signature answers "does this caller still expect the old
// shape"). Resolvable only when the target's repo lives in the SAME store being
// written (shared/fleet store, monorepo) — capture-early, query-later (§8B.6).
// Per-repo stores, an unindexed target, or an ambiguous name leave it "" —
// unknown is reported, never guessed (invariant 5).
func captureTargetSignatures(w Indexer, peers query.Store, callerRepo string, extRefs []query.ExternalRef) {
	if len(extRefs) == 0 {
		return
	}
	// Prefer the fleet view (other repos indexed on this machine) — a lone
	// per-repo store contains only its own symbols, so without peers the
	// contract is simply unknown.
	s := peers
	if s == nil {
		var ok bool
		if s, ok = w.(query.Store); !ok {
			return
		}
	}
	repoByModule := map[string]string{}
	for _, r := range s.Repos() {
		if r == callerRepo {
			continue
		}
		if m := s.ModulePath(r); m != "" {
			repoByModule[m] = r
		}
	}
	if len(repoByModule) == 0 {
		return
	}
	type key struct{ module, name string }
	cache := map[key]string{}
	for i := range extRefs {
		k := key{extRefs[i].Module, extRefs[i].Name}
		sig, seen := cache[k]
		if !seen {
			sig = targetSignature(s, repoForModule(repoByModule, k.module), k.name)
			cache[k] = sig
		}
		extRefs[i].TargetSignatureHash = sig
	}
}

// repoForModule maps an import path to the repo owning it, by longest module
// ROOT match — an import path is a module path plus a package path, so exact
// equality alone would miss every multi-package repo.
func repoForModule(repoByModule map[string]string, importPath string) string {
	best, bestLen := "", -1
	for root, repo := range repoByModule {
		if query.ModuleMatches(root, importPath) && len(root) > bestLen {
			best, bestLen = repo, len(root)
		}
	}
	return best
}

// targetSignature returns the signature hash of the UNIQUE symbol named name in
// repo's preferred ref (HEAD if indexed, else the lexically-newest ref), or ""
// when the repo is unknown, the name is absent, or several symbols share it.
func targetSignature(s query.Store, repo, name string) string {
	if repo == "" {
		return ""
	}
	ref := preferredRef(s.Refs(repo))
	if ref == "" {
		return ""
	}
	sig, count := "", 0
	for qid, facts := range s.SymbolsAt(repo, ref) {
		if baseName(qid) == name {
			sig = string(facts.SignatureHash)
			count++
		}
	}
	if count != 1 {
		return "" // ambiguous or absent: unknown, never guessed
	}
	return sig
}

func preferredRef(refs []string) string {
	best := ""
	for _, r := range refs {
		if r == "HEAD" {
			return r
		}
		if r > best {
			best = r
		}
	}
	return best
}

// baseName mirrors query.baseName for target matching (bare name of a qid).
func baseName(qid string) string {
	if i := strings.LastIndex(qid, "."); i >= 0 {
		return qid[i+1:]
	}
	return qid
}

// mergeOverload folds overload b into a (the first definition): a keeps its
// identity and signature; the calls of both bodies count.
func mergeOverload(a, b Symbol) Symbol {
	a.Callees = append(append([]string(nil), a.Callees...), b.Callees...)
	a.QualifiedCalls = append(append([]QualifiedCall(nil), a.QualifiedCalls...), b.QualifiedCalls...)
	a.CallSites = append(append([]CallSite(nil), a.CallSites...), b.CallSites...)
	a.LocalImports = append(append([]ImportBinding(nil), a.LocalImports...), b.LocalImports...)
	if len(b.Vars) > 0 {
		vars := make(map[string]string, len(a.Vars)+len(b.Vars))
		for k, v := range a.Vars {
			vars[k] = v
		}
		for k, v := range b.Vars {
			if _, ok := vars[k]; !ok {
				vars[k] = v
			}
		}
		a.Vars = vars
	}
	return a
}

// nameGoInits gives each Go init function its file's name (init@config for
// config.go's): a package may have one per file, and under one id all but
// the last would be lost.
func nameGoInits(files []ParsedFile) {
	for i := range files {
		f := &files[i]
		if f.Lang != "go" && !(f.Lang == "" && strings.HasSuffix(f.Path, ".go")) {
			continue
		}
		name := goInitName(f.Path)
		for j := range f.Symbols {
			if s := &f.Symbols[j]; s.Name == "init" && s.Recv == "" {
				s.Name = name
			}
		}
		for j := range f.Spans {
			if f.Spans[j].Name == "init" {
				f.Spans[j].Name = name
			}
		}
	}
}

// goInitName is the id name of a Go file's init function.
func goInitName(p string) string {
	return "init@" + strings.TrimSuffix(path.Base(p), ".go")
}

// isCSource: a C/C++ translation unit (not a header).
func isCSource(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".c", ".cc", ".cpp", ".cxx":
		return true
	}
	return false
}
