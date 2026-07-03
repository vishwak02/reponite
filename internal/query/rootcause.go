// rootcause.go implements the root-cause drill-down (architecture ext §8A):
// given a symbol whose behavior changed between two refs, walk down the callee
// graph to the frontier of *mutation sites* — symbols whose own text /
// signature / edge-set changed — as opposed to symbols merely carried along by
// a callee's change. The distinction is exactly the three-hash model:
// symbol_hash-changed = origin; behavior_hash-only-changed = propagation. Pure
// over ref snapshots (ADR-018); confidence follows the weakest edge on the path
// (invariant 5), and an origin outside the snapshot is stated, never guessed.
package query

import (
	"sort"

	"github.com/vishwak02/reponite/internal/content"
)

// SymbolFacts is a symbol's identity at one ref for root-cause purposes.
type SymbolFacts struct {
	SymbolHash    content.Hash
	SignatureHash content.Hash
	BehaviorHash  content.Hash
}

// Callee is a resolved CALLS edge target with its resolution provenance and
// confidence (invariant 5): ResolutionMethod records HOW the edge was resolved
// (see processing.Method*), so a verdict can state its basis, not just a number.
type Callee struct {
	Name             string
	ResolutionMethod string
	Confidence       float64
}

// RefSnapshot is one ref's call graph keyed by symbol name.
type RefSnapshot struct {
	Symbols map[string]SymbolFacts
	Callees map[string][]Callee
}

// OriginKind describes why a symbol is a mutation site.
type OriginKind string

const (
	KindText      OriginKind = "text_changed"
	KindSignature OriginKind = "signature_changed"
	KindEdges     OriginKind = "edges_changed"
)

// Origin is a change origin: a mutation site on a behavior-changed path.
type Origin struct {
	Name       string
	Kind       OriginKind
	Depth      int
	Confidence float64
}

// RootCauseResult is the drill-down outcome for one target symbol.
type RootCauseResult struct {
	Target  string
	Changed bool
	Origins []Origin
	Note    string
}

// RootCause walks from target down the to-ref callee graph, following only
// behavior-changed symbols, and records the mutation sites (origins), ordered by
// depth then name. If the target's behavior differs but no internal mutation is
// found, the origin is outside the snapshot (e.g. a cross-repo/unindexed callee,
// §8.4), which is stated in Note rather than guessed.
func RootCause(target string, from, to RefSnapshot) RootCauseResult {
	ff, fok := from.Symbols[target]
	tf, tok := to.Symbols[target]
	if !fok || !tok {
		return RootCauseResult{Target: target, Changed: false, Note: "target not present in both refs"}
	}
	if ff.BehaviorHash == tf.BehaviorHash {
		return RootCauseResult{Target: target, Changed: false, Note: "behavior unchanged between refs"}
	}

	type item struct {
		name  string
		depth int
		conf  float64
	}
	visited := map[string]bool{target: true}
	queue := []item{{target, 0, 1.0}}
	var origins []Origin
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if k, ok := mutationKind(cur.name, from, to); ok {
			origins = append(origins, Origin{Name: cur.name, Kind: k, Depth: cur.depth, Confidence: cur.conf})
		}
		for _, c := range to.Callees[cur.name] {
			if visited[c.Name] || !behaviorChanged(c.Name, from, to) {
				continue
			}
			visited[c.Name] = true
			queue = append(queue, item{c.Name, cur.depth + 1, minConf(cur.conf, c.Confidence)})
		}
	}
	sort.Slice(origins, func(i, j int) bool {
		if origins[i].Depth != origins[j].Depth {
			return origins[i].Depth < origins[j].Depth
		}
		return origins[i].Name < origins[j].Name
	})
	note := ""
	if len(origins) == 0 {
		note = "behavior changed but no internal mutation found; origin is likely a cross-repo/unindexed callee (§8.4)"
	}
	return RootCauseResult{Target: target, Changed: true, Origins: origins, Note: note}
}

func behaviorChanged(name string, from, to RefSnapshot) bool {
	f, fok := from.Symbols[name]
	t, tok := to.Symbols[name]
	return fok && tok && f.BehaviorHash != t.BehaviorHash
}

// mutationKind reports whether a symbol present in both refs is itself a
// mutation site, and how (signature > text > edge-set, in precedence).
func mutationKind(name string, from, to RefSnapshot) (OriginKind, bool) {
	f, fok := from.Symbols[name]
	t, tok := to.Symbols[name]
	if !fok || !tok {
		return "", false
	}
	if f.SignatureHash != t.SignatureHash {
		return KindSignature, true
	}
	if f.SymbolHash != t.SymbolHash {
		return KindText, true
	}
	if !sameCalleeNames(from.Callees[name], to.Callees[name]) {
		return KindEdges, true
	}
	return "", false
}

func sameCalleeNames(a, b []Callee) bool {
	if len(a) != len(b) {
		return false
	}
	an := make([]string, len(a))
	bn := make([]string, len(b))
	for i := range a {
		an[i] = a[i].Name
	}
	for i := range b {
		bn[i] = b[i].Name
	}
	sort.Strings(an)
	sort.Strings(bn)
	for i := range an {
		if an[i] != bn[i] {
			return false
		}
	}
	return true
}
