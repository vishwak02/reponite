// resolve_gocalls.go resolves a Go function's calls by Go's own scoping rules
// when the type checker could not run (or proved nothing): an unqualified F()
// is in the caller's package (or a builtin); pkg.F() is in the imported
// package — a directory of this module, or outside it; x.M() is a method on a
// value whose type is not known here, so the method is only guessed by name.
// Pure and stdlib-only (ADR-018).
package processing

import (
	"path"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// goExternalMark, among a caller's type-checked targets of a name, records a
// call of that name the checker resolved outside the repo (a library, an
// interface method) in a package that did not fully build.
const goExternalMark = "\x00external"

// goCheckedKey, present among a caller's names, says its package type-checked
// in full: a call the checker did not resolve into the repo is a library's,
// an interface's or a function value's — never guessed by name. Without it
// (a package that did not build) the names listed are the checker's verdicts
// and every other call falls to the name rules.
const goCheckedKey = "\x00checked"

// goModules maps each Go module's directory (relative to the tree root, ""
// at the root) to its module path, read from the tree's go.mod files.
func goModules(manifests map[string][]byte) map[string]string {
	out := map[string]string{}
	for p, b := range manifests {
		if path.Base(p) != "go.mod" {
			continue
		}
		for _, ln := range strings.Split(string(b), "\n") {
			ln = strings.TrimSpace(ln)
			if strings.HasPrefix(ln, "module ") {
				mod := strings.Trim(strings.TrimSpace(strings.TrimPrefix(ln, "module")), `"`)
				dir := path.Dir(p)
				if dir == "." {
					dir = ""
				}
				out[dir] = mod
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// goImportDir is the directory of this tree an import path names, when the
// path is inside one of its modules (the longest module path wins).
func goImportDir(importPath string, mods map[string]string) (string, bool) {
	best, bestDir, found := "", "", false
	for dir, mod := range mods {
		if importPath != mod && !strings.HasPrefix(importPath, mod+"/") {
			continue
		}
		if len(mod) <= len(best) {
			continue
		}
		rest := strings.TrimPrefix(strings.TrimPrefix(importPath, mod), "/")
		best, bestDir, found = mod, path.Join(dir, rest), true
	}
	if bestDir == "." {
		bestDir = ""
	}
	return bestDir, found
}

// resolveGoCalls resolves one Go function's call sites. precise holds the
// type checker's verdicts for this caller (base name -> qid) and wins; with
// goCheckedKey the caller's package type-checked in full, so a call it did
// not resolve is not guessed by name.
func resolveGoCalls(callerPkg, callerRecv string, qcs []QualifiedCall, imports map[string]ImportBinding, mods map[string]string, x siteIndex, precise map[string][]string) []query.Callee {
	out := make([]query.Callee, 0, len(qcs))
	seen := map[string]int{}
	add := func(c query.Callee) {
		if i, ok := seen[c.Name]; ok {
			if c.Confidence > out[i].Confidence {
				out[i] = c
			}
			return
		}
		seen[c.Name] = len(out)
		out = append(out, c)
	}
	for _, qc := range qcs {
		name := qc.Name
		if qs := precise[name]; len(qs) > 0 {
			repo := false
			for _, q := range qs {
				if x.nodeSet[q] {
					add(query.Callee{Name: q, ResolutionMethod: MethodTypes, Confidence: ConfTypes})
					repo = true
				}
			}
			if !repo {
				// every call of the name the checker resolved left the repo
				add(query.Callee{Name: name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
			}
			continue
		}
		if _, checked := precise[goCheckedKey]; checked {
			// the caller type-checked and this call reaches no repo function:
			// a builtin, a library, an interface or a function value
			add(query.Callee{Name: name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
			continue
		}
		switch b, imported := imports[qc.Qualifier]; {
		case qc.Qualifier == "":
			// F(): only the caller's own package defines it; anything else is a
			// builtin or a dot import
			if q := qualify(callerPkg, name); x.nodeSet[q] {
				add(query.Callee{Name: q, ResolutionMethod: MethodResolved, Confidence: ConfResolved})
			} else {
				add(query.Callee{Name: name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
			}
		case imported && qc.Chain == 2:
			// pkg.F(): the imported package's F, here or outside the module
			if dir, ok := goImportDir(b.Module, mods); ok {
				if q := qualify(dir, name); x.nodeSet[q] {
					add(query.Callee{Name: q, ResolutionMethod: MethodResolved, Confidence: ConfResolved})
					continue
				}
			}
			add(query.Callee{Name: b.Module + "." + name, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
		default:
			// x.M(), a.b.M(), f().M(): a method on a value of unknown type
			add(memberCalleeLang(name, "go", x))
		}
	}
	return out
}

// unresolvedLabel names an unresolved target's kind for its marked name.
func unresolvedLabel(method string) string {
	if method == MethodAmbiguous {
		return "ambiguous"
	}
	return "external"
}
