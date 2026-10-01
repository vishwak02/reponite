// resolve.go is the CALLS-edge resolution policy: it maps each heuristic callee
// name to a package-qualified target symbol and assigns the edge a
// resolution_method and confidence (architecture §7, invariant 5). This is the
// single place edge confidence is decided. Pure and stdlib-only (ADR-018).
package processing

import (
	"path/filepath"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// Resolution methods label how a CALLS edge's target was resolved. The method is
// part of the edge's identity (invariant 5, content.EdgeHash) and drives its
// confidence.
const (
	// MethodResolved: the base name maps to exactly one definition in scope (the
	// caller's package, or a repo-wide unique name), so the target is known.
	MethodResolved = "name-resolved"
	// MethodAmbiguous: the base name has several definitions across packages and
	// we cannot pick one without type information — an honest low-confidence leaf.
	MethodAmbiguous = "ambiguous"
	// MethodExternal: the callee is not defined in the repo (stdlib, third-party,
	// or cross-repo) and is treated as an opaque behavior leaf.
	MethodExternal = "unresolved-external"
	// MethodTypes: proven by the Go type checker (reserved for precise
	// resolution; assigned confidence 1.0 when that path lands).
	MethodTypes = "go-types"
	// MethodImport: a cross-repo reference resolved through the caller file's
	// import bindings to a (module_path, name) — precise about *which* module,
	// but still name/path-based across the repo boundary, not type-proven
	// (§8B.4). Used for external_refs, not for in-repo CALLS edges.
	MethodImport = "import-resolved"
	// MethodSCIP: a cross-repo reference matched by SCIP moniker — a globally
	// unique symbol identity produced independently by each repo's indexer, so
	// the link is symbol-to-symbol, not a name/path match (§8B.4, Phase 6b).
	MethodSCIP = "scip-resolved"
)

// Confidence per resolution method (§7, invariant 5), monotonic with certainty:
// type-proven > uniquely name-resolved > opaque external > ambiguous.
const (
	ConfTypes = 1.0
	// ConfSCIP: cross-boundary but symbol-resolved. Just under type-proven
	// in-repo resolution — the moniker is exact, yet it still asserts a
	// SOURCE-level dependency, and SCIP indexes can lag the working tree.
	ConfSCIP      = 0.95
	ConfResolved  = 0.9
	ConfImport    = 0.75
	ConfExternal  = 0.6
	ConfAmbiguous = 0.5
)

// pkgOf returns the package qualifier for a file: its directory relative to the
// repo root. This is a language-agnostic stand-in for the package (distinct
// packages live in distinct directories), disambiguating same-named symbols
// across packages until receiver-level / type-checked qualification lands. Files
// at the repo root have no qualifier.
func pkgOf(path string) string {
	dir := filepath.Dir(path)
	if dir == "." || dir == "/" || dir == "" {
		return ""
	}
	return dir
}

// qualify joins a package qualifier and a bare symbol name into a stable id
// (pkg + "." + name); a rootless symbol keeps its bare name.
func qualify(pkg, name string) string {
	if pkg == "" {
		return name
	}
	return pkg + "." + name
}

// BaseName is the bare symbol name of a qualified id (the segment after the last
// "."). Directory qualifiers use "/" so they never contain a ".".
func BaseName(qid string) string {
	if i := strings.LastIndex(qid, "."); i >= 0 {
		return qid[i+1:]
	}
	return qid
}

// resolveEdges resolves each base callee name for a caller in package callerPkg.
// A type-checker-proven target (precise[base], supplied by the Go resolver) wins
// at full confidence. Otherwise it falls back to name scoping, as close to Go's
// rules as a heuristic allows: a definition in the caller's own package wins (an
// unqualified call resolves there); then a repo-wide unique base name; a base
// name with several definitions is honestly ambiguous (can't choose without type
// info); an unknown one is external. nodeSet holds every qualified id in the ref;
// byBase maps a base name to the qualified ids that define it; precise may be nil.
func resolveEdges(callerPkg string, callees []string, nodeSet map[string]bool, byBase map[string][]string, precise map[string]string) []query.Callee {
	out := make([]query.Callee, 0, len(callees))
	for _, base := range callees {
		if q, ok := precise[base]; ok && nodeSet[q] {
			out = append(out, query.Callee{Name: q, ResolutionMethod: MethodTypes, Confidence: ConfTypes})
			continue
		}
		if q := qualify(callerPkg, base); nodeSet[q] {
			out = append(out, query.Callee{Name: q, ResolutionMethod: MethodResolved, Confidence: ConfResolved})
			continue
		}
		switch cands := byBase[base]; len(cands) {
		case 1:
			out = append(out, query.Callee{Name: cands[0], ResolutionMethod: MethodResolved, Confidence: ConfResolved})
		case 0:
			out = append(out, query.Callee{Name: base, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
		default:
			out = append(out, query.Callee{Name: base, ResolutionMethod: MethodAmbiguous, Confidence: ConfAmbiguous})
		}
	}
	return out
}

// resolveExternalRefs maps a caller's qualified call sites to cross-repo
// external references, using the caller file's import bindings (byLocal). A
// call is external only when it resolves through an import:
//   - qualified (pkg.Do): the qualifier names a whole-module/namespace/class
//     import → (that module, the called member); or if the qualifier is itself
//     an imported symbol used as a receiver → (its module, the imported name).
//   - unqualified (baz): the bare name is a from/named/static import → (its
//     module, the imported symbol's real name).
//
// Calls whose qualifier/name is a local variable or an in-repo definition
// resolve to nothing here — they are not cross-repo dependencies. The result is
// deduped by (module, name) so N calls to the same external symbol count once.
func resolveExternalRefs(caller string, qcalls []QualifiedCall, byLocal map[string]ImportBinding) []query.ExternalRef {
	seen := map[[2]string]bool{}
	var out []query.ExternalRef
	for _, qc := range qcalls {
		module, name, ok := resolveImportedCall(qc, byLocal)
		if !ok {
			continue
		}
		key := [2]string{module, name}
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, query.ExternalRef{
			From: caller, Module: module, Name: name,
			ResolutionMethod: MethodImport, Confidence: ConfImport,
		})
	}
	return out
}

func resolveImportedCall(qc QualifiedCall, byLocal map[string]ImportBinding) (module, name string, ok bool) {
	if qc.Qualifier != "" {
		b, found := byLocal[qc.Qualifier]
		if !found {
			return "", "", false // qualifier is a local var / in-repo — not external
		}
		if b.Symbol != "" {
			return b.Module, b.Symbol, true // imported symbol used as a receiver (e.g. from foo import Bar; Bar.m())
		}
		return b.Module, qc.Name, true // whole-module handle (bar.Do, np.array, ns.thing)
	}
	// Unqualified call: external only if the name is itself an imported symbol
	// (from-import / named / static import).
	if b, found := byLocal[qc.Name]; found && b.Symbol != "" {
		return b.Module, b.Symbol, true
	}
	return "", "", false
}

// MethodMember: a member call (obj.f(), p->f()) on an object whose type is not
// known here, matched to the one class in the repo defining a method f. Likely
// but unproven — the object's type was never checked — so it sits below
// name-resolved and above an opaque external leaf.
const (
	MethodMember = "member-name-resolved"
	ConfMember   = 0.7
)

// siteIndex is what call-site resolution needs about a ref's symbols.
type siteIndex struct {
	nodeSet    map[string]bool
	byBase     map[string][]string // bare name -> qids
	byRecv     map[string][]string // "Recv.name" -> qids (methods and scoped definitions)
	methods    map[string][]string // bare name -> qids that are methods (have a receiver)
	pkgOfQID   map[string]string   // qid -> package (directory)
	isTest     map[string]bool     // qid -> defined in test code
	precise    map[string]string   // base -> type-proven qid (Go only; nil here)
	types      *typeTable          // C++ classes/fields/bases (nil: none in the ref)
	callerPkg  string
	callerTest bool
}

// MethodTyped: a member call resolved on the receiver's DECLARED type (its
// parameter, local or member-field declaration), following base classes. The
// class is read from a declaration, not proven by a compiler — overloads,
// templates and macros are not modeled — so it sits below go-types and
// name-resolved-in-own-class, above a member-name guess.
// MethodOverride: a subclass's override of the method a call reaches through
// a base-class receiver — a possible runtime target (virtual dispatch), one
// edge per override.
const (
	MethodTyped    = "receiver-typed"
	ConfTyped      = 0.85
	MethodOverride = "virtual-override"
	ConfOverride   = 0.6
)

// siteKindRank orders a name's call-site kinds when one function calls the same
// name several ways: the most specific claim about the target wins.
var siteKindRank = map[CallKind]int{CallTyped: 0, CallSelf: 0, CallPlain: 1, CallScoped: 2, CallExternal: 3, CallMember: 4}

// resolveSiteEdges resolves C/C++ call sites by their shape (callsite.go):
//   - this->f() / f() inside a method of A: A's own f first (same class);
//   - A::f(): the definition scoped to A; std::/boost::/ros::… are external;
//   - obj.f(): only a METHOD can be the target, and a standard-library member
//     name on an object of unknown type is never pinned on the repo's method.
//
// Everything else falls back to the name rules of resolveEdges.
func resolveSiteEdges(callerPkg, callerRecv string, sites []CallSite, x siteIndex) []query.Callee {
	return resolveSiteEdgesLang(callerPkg, callerRecv, "cpp", sites, x)
}

func resolveSiteEdgesLang(callerPkg, callerRecv, lang string, sites []CallSite, x siteIndex) []query.Callee {
	best := map[string]CallSite{}
	var order []string
	for _, s := range sites {
		s = typeSite(s, callerRecv, x)
		key := s.Name
		if s.Kind == CallTyped {
			key = s.Name + "\x00" + s.Scope // w.step() and p.step() on two classes: two targets
		}
		cur, ok := best[key]
		if !ok {
			order = append(order, key)
		}
		if !ok || siteKindRank[s.Kind] < siteKindRank[cur.Kind] {
			best[key] = s
		}
	}
	x.callerPkg = callerPkg
	out := make([]query.Callee, 0, len(order))
	seen := map[string]int{} // callee -> index in out
	add := func(c query.Callee) {
		i, ok := seen[c.Name]
		if !ok {
			seen[c.Name] = len(out)
			out = append(out, c)
			return
		}
		if c.Confidence > out[i].Confidence {
			out[i] = c // one call typed, another not: the typed claim stands
		}
	}
	for _, key := range order {
		s := best[key]
		name := s.Name
		switch s.Kind {
		case CallTyped:
			for _, c := range typedCallees(s.Scope, name, lang, x) {
				add(c)
			}
		case CallSelf, CallPlain:
			if callerRecv != "" {
				if q, ok := pickOne(x.byRecv[callerRecv+"."+name], x); ok {
					add(query.Callee{Name: q, ResolutionMethod: MethodResolved, Confidence: ConfResolved})
					for _, o := range overrides(callerRecv, name, x) {
						add(o) // this->f() in a base: a subclass's f may run instead
					}
					continue
				}
				if x.types != nil && x.types.classes[callerRecv] != nil {
					if cs := typedCallees(callerRecv, name, lang, x); len(cs) > 0 && cs[0].ResolutionMethod != MethodExternal {
						for _, c := range cs {
							add(c) // inherited from a repo base class
						}
						continue
					}
				}
			}
			if s.Kind == CallSelf {
				// this->f() with no f on the caller's own class: inherited, so
				// the target is a base class method — which one is unproven.
				add(memberCalleeLang(name, lang, x))
				continue
			}
			add(resolveEdges(callerPkg, []string{name}, x.nodeSet, x.byBase, x.precise)[0])
		case CallScoped:
			if externalScopes[s.Root] {
				add(query.Callee{Name: s.Root + "::" + name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
				continue
			}
			if q, ok := pickOne(x.byRecv[s.Scope+"."+name], x); ok {
				add(query.Callee{Name: q, ResolutionMethod: MethodResolved, Confidence: ConfResolved})
				continue
			}
			add(resolveEdges(callerPkg, []string{name}, x.nodeSet, x.byBase, x.precise)[0])
		case CallExternal:
			add(query.Callee{Name: s.Scope + "::" + name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
		case CallMember:
			add(memberCalleeLang(name, lang, x))
		}
	}
	return out
}

// typeSite turns an untyped member site into a typed one when its receiver's
// class is known: a parameter/local's declared type, or a field declared on
// the caller's class or one of its bases.
func typeSite(s CallSite, callerRecv string, x siteIndex) CallSite {
	if s.Kind != CallMember || x.types == nil {
		return s
	}
	t := x.types.eval(s.Recv, callerRecv)
	if t == "" {
		return s
	}
	if strings.HasPrefix(t, "[]") || strings.HasPrefix(t, "map[") || strings.HasPrefix(t, "pair[") {
		t = "std::container" // a container's own member: size, insert, push_back, …
	}
	if strings.Contains(t, "::") || x.types.classes[t] != nil {
		return CallSite{Name: s.Name, Kind: CallTyped, Scope: t}
	}
	return s // a type the ref does not define (template parameter, using-imported library type)
}

// typedCallees resolves cls.name(): the definition on cls or its nearest base
// that has one, plus every subclass override. A class in an external
// namespace, or a repo class whose lineage defines no such method (a library
// base's member), is an external leaf named cls::name.
func typedCallees(cls, name, lang string, x siteIndex) []query.Callee {
	if strings.Contains(cls, "::") {
		return []query.Callee{{Name: cls + "::" + name, ResolutionMethod: MethodExternal, Confidence: ConfExternal}}
	}
	var out []query.Callee
	for _, c := range x.types.lineage(cls) {
		if q, ok := pickOne(x.byRecv[c+"."+name], x); ok {
			out = append(out, query.Callee{Name: q, ResolutionMethod: MethodTyped, Confidence: ConfTyped})
			break
		}
	}
	out = append(out, overrides(cls, name, x)...)
	if len(out) == 0 {
		out = append(out, query.Callee{Name: cls + "::" + name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
	}
	return out
}

// overrides are the definitions of name on classes deriving from cls.
func overrides(cls, name string, x siteIndex) []query.Callee {
	if x.types == nil {
		return nil
	}
	var out []query.Callee
	for _, d := range x.types.descendants(cls) {
		if q, ok := pickOne(x.byRecv[d+"."+name], x); ok && (x.callerTest || !x.isTest[q]) {
			// A mock's override is a target only for test code.
			out = append(out, query.Callee{Name: q, ResolutionMethod: MethodOverride, Confidence: ConfOverride})
		}
	}
	return out
}

// memberCalleeLang resolves obj.name() with the object's type unknown.
func memberCalleeLang(name, lang string, x siteIndex) query.Callee {
	cands := x.methods[name]
	switch {
	case len(cands) == 0:
		// Only a method can be a member call's target, and no repo class has one:
		// a library type's member (or a function pointer field).
		return query.Callee{Name: name, ResolutionMethod: MethodExternal, Confidence: ConfExternal}
	case memberBuiltins[lang][name]:
		// The repo defines it, but so does every standard container: which one
		// this object is cannot be told without its type.
		return query.Callee{Name: name, ResolutionMethod: MethodAmbiguous, Confidence: ConfAmbiguous}
	case len(cands) == 1 && x.isTest[cands[0]] && !x.callerTest:
		// Production code never calls into a test file; the one same-named
		// method there (a mock's toJson) says nothing about this object.
		return query.Callee{Name: name, ResolutionMethod: MethodExternal, Confidence: ConfExternal}
	case len(cands) == 1:
		return query.Callee{Name: cands[0], ResolutionMethod: MethodMember, Confidence: ConfMember}
	}
	return query.Callee{Name: name, ResolutionMethod: MethodAmbiguous, Confidence: ConfAmbiguous}
}

// testish: test code, test-support packages (a directory named *test*,
// e.g. test_commons) and mock/fake/stub classes — what production code never
// calls, for member-name guessing.
func testish(isTest bool, pkg, recv string) bool {
	if isTest {
		return true
	}
	for _, seg := range strings.Split(pkg, "/") {
		if strings.Contains(strings.ToLower(seg), "test") {
			return true
		}
	}
	for _, p := range []string{"Mock", "Fake", "Stub"} {
		if strings.HasPrefix(recv, p) {
			return true
		}
	}
	return false
}

// pickOne chooses among a (Recv, name)'s definitions: the only one, else the
// one in the caller's own package; several elsewhere stay unresolved.
func pickOne(cands []string, x siteIndex) (string, bool) {
	switch len(cands) {
	case 0:
		return "", false
	case 1:
		return cands[0], true
	}
	var local []string
	for _, q := range cands {
		if x.pkgOfQID[q] == x.callerPkg {
			local = append(local, q)
		}
	}
	if len(local) == 1 {
		return local[0], true
	}
	return "", false
}
