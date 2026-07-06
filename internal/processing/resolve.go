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
)

// Confidence per resolution method (§7, invariant 5), monotonic with certainty:
// type-proven > uniquely name-resolved > opaque external > ambiguous.
const (
	ConfTypes     = 1.0
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
