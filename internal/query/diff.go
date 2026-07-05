// diff.go computes the per-symbol delta between two refs (architecture §10,
// `reponite diff`): added / removed / shape_changed / behavior_changed /
// unchanged. Pure set + hash comparison over ref_history snapshots, reusing the
// Oracle's tiered verdict; deterministic (sorted by symbol name).
package query

import (
	"sort"
	"strings"
)

// ChangeKind classifies one symbol's delta between two refs.
type ChangeKind string

const (
	ChangeAdded     ChangeKind = "added"
	ChangeRemoved   ChangeKind = "removed"
	ChangeShape     ChangeKind = "shape_changed"
	ChangeBehavior  ChangeKind = "behavior_changed"
	ChangeUnchanged ChangeKind = "unchanged"
)

// SymbolChange is one symbol's delta between refs A and B.
type SymbolChange struct {
	Name       string
	Kind       ChangeKind
	Confidence float64
}

// DiffRefs compares two ref snapshots keyed by symbol name. Symbols present on
// only one side are added/removed; symbols in both are classified with the same
// tiered comparison the Oracle uses. Output is sorted by name.
func DiffRefs(a, b map[string]SymbolRef) []SymbolChange {
	seen := make(map[string]bool, len(a)+len(b))
	names := make([]string, 0, len(a)+len(b))
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for n := range a {
		add(n)
	}
	for n := range b {
		add(n)
	}
	sort.Strings(names)

	out := make([]SymbolChange, 0, len(names))
	for _, n := range names {
		av, aok := a[n]
		bv, bok := b[n]
		switch {
		case aok && !bok:
			out = append(out, SymbolChange{Name: n, Kind: ChangeRemoved, Confidence: 1.0})
		case !aok && bok:
			out = append(out, SymbolChange{Name: n, Kind: ChangeAdded, Confidence: 1.0})
		default:
			r := Compat(av, bv)
			out = append(out, SymbolChange{Name: n, Kind: verdictToChange(r.Verdict), Confidence: r.Confidence})
		}
	}
	return out
}

// DiffOptions scopes a diff for agents/CLI. Zero value = no filtering.
type DiffOptions struct {
	ChangedOnly   bool    // drop unchanged symbols (the big token win)
	Package       string  // keep only symbols whose package has this prefix
	MinConfidence float64 // drop changes below this confidence
}

// FilterChanges applies DiffOptions to a change list (pure). --kind is not
// offered because symbol kind (function/type) is not persisted in ref_history.
func FilterChanges(changes []SymbolChange, opt DiffOptions) []SymbolChange {
	if opt == (DiffOptions{}) {
		return changes
	}
	out := make([]SymbolChange, 0, len(changes))
	for _, c := range changes {
		if opt.ChangedOnly && c.Kind == ChangeUnchanged {
			continue
		}
		if opt.MinConfidence > 0 && c.Confidence < opt.MinConfidence {
			continue
		}
		if opt.Package != "" && !strings.HasPrefix(packageOfQID(c.Name), opt.Package) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// packageOfQID extracts the package qualifier from a qualified id: the directory
// prefix before the first "." that follows the last "/" (e.g.
// "internal/query.Recv.Foo" -> "internal/query"). "" for a rootless bare name.
func packageOfQID(qid string) string {
	slash := strings.LastIndex(qid, "/")
	dot := strings.Index(qid[slash+1:], ".")
	if dot < 0 {
		return ""
	}
	return qid[:slash+1+dot]
}

func verdictToChange(v Verdict) ChangeKind {
	switch v {
	case ShapeChanged:
		return ChangeShape
	case BehaviorChanged:
		return ChangeBehavior
	default:
		return ChangeUnchanged
	}
}
