package roslaunch

import (
	"sort"
	"strings"
)

// Resolution labels how an endpoint's effective topic name was determined.
const (
	// ResolvedAsWritten: no launch file remaps this name for this package, so
	// the source name IS the runtime name.
	ResolvedAsWritten = "as-written"
	// ResolvedRemapped: a launch file remaps this name for a node in the
	// endpoint's package, so the runtime name differs from the source.
	ResolvedRemapped = "launch-remapped"
	// ResolvedAmbiguous: several launch entries remap the same name in this
	// package to DIFFERENT targets — which one applies depends on which launch
	// file actually ran, so no single answer is asserted.
	ResolvedAmbiguous = "launch-ambiguous"
	// ResolvedUnexpanded: a launch file DOES remap this name, but the target
	// still contains an unexpanded substitution ($(find …), $(env …), $(eval …),
	// or an $(arg …) with no default), so the runtime name is genuinely unknown.
	// The source name is kept: applying the literal "$(arg scan_topic)" as a
	// topic would be worse than not remapping at all.
	ResolvedUnexpanded = "launch-unexpanded"
)

// Table indexes every remap found across a set of launch files, keyed by the
// package the remap's node belongs to. The package is the link back to source:
// a `<node pkg="rr_navigation">` remap applies to endpoints in rr_navigation's
// source, which is the only association a launch file offers.
type Table struct {
	// byPkgFrom maps package -> source topic name -> the distinct targets seen.
	byPkgFrom map[string]map[string]map[string]struct{}
	// origin records one citing launch file per (pkg, from, to).
	origin map[[3]string]string
	// global remaps apply regardless of package (declared outside any node).
	global map[string]map[string]struct{}
	// unexpanded records (pkg, from) pairs whose remap target could not be
	// expanded, so the gap is reported rather than applied.
	unexpanded map[string]map[string]string
	// Files counts the launch files that contributed, Unresolved the includes
	// whose $(find …) substitution was not expanded, and Unexpanded the remaps
	// dropped because their target kept a substitution — all reported, never
	// silently ignored.
	Files      int
	Unresolved int
	Unexpanded int
	// Declared is every node remap as the launch file wrote it, resolved or
	// not. A remap is evidence on its own that the launched node binds the
	// target name at runtime, even when that node's source is not indexed (a
	// third-party package) or names the topic through an expression — then the
	// launch file is the only place the wiring is visible at all.
	Declared []Declaration
}

// Declaration is one `<remap>` on a `<node>`: the node it wires and the name
// it binds at runtime. To is expanded against the file's args; Prefix holds
// the substitution(s) left unexpanded at the START of the target — a namespace
// prefix such as `$(arg ns)` — and Runtime the remainder, which is the runtime
// name when that prefix expands to the root (the usual default). Runtime is ""
// when a substitution sits anywhere but the front, so no name is claimed.
type Declaration struct {
	Pkg, Type, Node, Ns string
	From, To            string
	Prefix, Runtime     string
	Via                 string
	Line                int
}

// leadingSubst returns the length of the run of substitutions at the very
// start of s (parentheses balanced, so `$(eval f('x'))` is one substitution),
// 0 when s does not start with one.
func leadingSubst(s string) int {
	i := 0
	for {
		j := i
		for j < len(s) && (s[j] == ' ' || s[j] == '\t') {
			j++
		}
		if !strings.HasPrefix(s[j:], "$(") {
			return i
		}
		depth := 0
		k := j + 1
		for ; k < len(s); k++ {
			if s[k] == '(' {
				depth++
			} else if s[k] == ')' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if k >= len(s) {
			return i // unbalanced: claim nothing
		}
		i = k + 1
	}
}

// declare records a node remap with whatever runtime name can honestly be read
// from it.
func declare(path string, n Node, r Remap, args map[string]string) Declaration {
	to := strings.TrimSpace(Substitute(r.To, args))
	d := Declaration{Pkg: n.Pkg, Type: n.Type, Node: n.Name, Ns: n.Ns,
		From: strings.TrimSpace(Substitute(r.From, args)), To: to, Via: path, Line: n.Line}
	if !HasSubstitution(to) {
		d.Runtime = norm(to)
		return d
	}
	if n := leadingSubst(to); n > 0 {
		if rest := strings.TrimSpace(to[n:]); rest != "" && !HasSubstitution(rest) {
			d.Prefix, d.Runtime = strings.TrimSpace(to[:n]), norm(rest)
		}
	}
	return d
}

// NewTable builds a remap table from parsed launch files.
func NewTable(files []File) *Table {
	t := &Table{
		byPkgFrom:  map[string]map[string]map[string]struct{}{},
		origin:     map[[3]string]string{},
		global:     map[string]map[string]struct{}{},
		unexpanded: map[string]map[string]string{},
	}
	for _, f := range files {
		t.Files++
		t.Unresolved += len(f.Includes)
		// Expand $(arg …) against this file's declared defaults first; a target
		// that still carries a substitution is not a runtime name.
		resolve := func(v string) (string, bool) {
			v = Substitute(v, f.Args)
			return norm(v), !HasSubstitution(v)
		}
		for _, r := range f.GlobalRemaps {
			from, okF := resolve(r.From)
			to, okT := resolve(r.To)
			if !okF || !okT {
				t.Unexpanded++
				continue
			}
			add(t.global, from, to)
			// Cite global remaps too: every resolution should name its source.
			if _, seen := t.origin[[3]string{"", from, to}]; !seen {
				t.origin[[3]string{"", from, to}] = f.Path
			}
		}
		for _, n := range f.Nodes {
			if n.Pkg == "" {
				continue // nothing to link it to source with
			}
			for _, r := range n.Remaps {
				t.Declared = append(t.Declared, declare(f.Path, n, r, f.Args))
				from, okF := resolve(r.From)
				to, okT := resolve(r.To)
				if !okF || !okT {
					// Record the gap so Effective can SAY the name is remapped
					// to something unknown, instead of silently reading as-written.
					t.Unexpanded++
					if okF {
						if t.unexpanded[n.Pkg] == nil {
							t.unexpanded[n.Pkg] = map[string]string{}
						}
						t.unexpanded[n.Pkg][from] = strings.TrimSpace(Substitute(r.To, f.Args))
					}
					continue
				}
				if t.byPkgFrom[n.Pkg] == nil {
					t.byPkgFrom[n.Pkg] = map[string]map[string]struct{}{}
				}
				add(t.byPkgFrom[n.Pkg], from, to)
				key := [3]string{n.Pkg, from, to}
				if _, seen := t.origin[key]; !seen {
					t.origin[key] = f.Path
				}
			}
		}
	}
	return t
}

func add(m map[string]map[string]struct{}, from, to string) {
	if m[from] == nil {
		m[from] = map[string]struct{}{}
	}
	m[from][to] = struct{}{}
}

// norm strips a single leading "/" so an absolute `/scan` and a relative `scan`
// compare equal — the same normalization the comms graph applies to names.
func norm(s string) string { return strings.TrimPrefix(strings.TrimSpace(s), "/") }

// Effective returns the runtime topic name for a source-level endpoint, given
// the path of the file declaring it.
//
// The association is by PACKAGE: a remap on `<node pkg="X">` is applied to
// endpoints whose source path contains X as a directory segment. That is a
// heuristic — a launch file names an executable, not a source file — so the
// result is always labeled, and a name remapped inconsistently within one
// package resolves to ResolvedAmbiguous rather than picking a winner.
func (t *Table) Effective(srcPath, topic string) (effective, resolution, via string) {
	from := norm(topic)
	if t == nil {
		return from, ResolvedAsWritten, ""
	}
	segs := strings.Split(strings.ReplaceAll(srcPath, "\\", "/"), "/")
	// Prefer a package-scoped remap; fall back to a global one.
	for _, seg := range segs {
		tos, ok := t.byPkgFrom[seg][from]
		if !ok || len(tos) == 0 {
			continue
		}
		if len(tos) > 1 {
			return from, ResolvedAmbiguous, strings.Join(sortedKeys(tos), " | ")
		}
		to := sortedKeys(tos)[0]
		return to, ResolvedRemapped, t.origin[[3]string{seg, from, to}]
	}
	if tos, ok := t.global[from]; ok && len(tos) > 0 {
		if len(tos) > 1 {
			return from, ResolvedAmbiguous, strings.Join(sortedKeys(tos), " | ")
		}
		to := sortedKeys(tos)[0]
		return to, ResolvedRemapped, t.origin[[3]string{"", from, to}]
	}
	// A remap exists but its target could not be expanded: say so rather than
	// reporting the source name as if nothing rewrote it.
	for _, seg := range segs {
		if target, ok := t.unexpanded[seg][from]; ok {
			return from, ResolvedUnexpanded, target
		}
	}
	return from, ResolvedAsWritten, ""
}

// Remaps returns every (pkg, from, to) triple, sorted — for reporting what the
// table actually knows.
func (t *Table) Remaps() []PkgRemap {
	if t == nil {
		return nil
	}
	var out []PkgRemap
	for pkg, froms := range t.byPkgFrom {
		for from, tos := range froms {
			for to := range tos {
				out = append(out, PkgRemap{Pkg: pkg, From: from, To: to, Via: t.origin[[3]string{pkg, from, to}]})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].To < out[j].To
	})
	return out
}

// PkgRemap is one remap with the package it applies to and the file citing it.
type PkgRemap struct {
	Pkg  string
	From string
	To   string
	Via  string
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
