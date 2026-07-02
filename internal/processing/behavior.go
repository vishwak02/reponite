// behavior.go computes behavior_hash for every symbol in a call graph
// (architecture §6.2–§6.3): a Merkle hash over the resolved CALLS graph so a
// callee's change propagates to every transitive caller — the property the
// Compatibility Oracle's behavior-changed verdict rests on. Pure and
// stdlib-only: it operates on Node/Edge values supplied by the resolver, so it
// is fully unit-tested in-sandbox (ADR-018), independent of parser/store.
package processing

import "github.com/vishwak02/reponite/internal/content"

// Node is a symbol participating in the call graph.
type Node struct {
	ID         string       // stable identifier within this graph (e.g. the symbol_hash)
	SymbolHash content.Hash // textual identity (§6.1)
}

// Edge is a resolved CALLS edge with its confidence (§7).
type Edge struct {
	From, To   string
	Confidence float64
}

// Result holds the computed behavior hash and confidence per node ID.
type Result struct {
	BehaviorHash map[string]content.Hash
	BehaviorConf map[string]float64
}

// ComputeBehavior runs the reverse-topological, SCC-condensed, memoized pass.
// SCCs (mutual recursion) are hashed as a unit over their members' symbol
// hashes (§6.3). Edges to nodes not in the set (cross-repo callees, §8.4) are
// treated as opaque leaves. behavior_conf is the minimum edge confidence over
// the transitive subgraph (invariant 5).
func ComputeBehavior(nodes []Node, edges []Edge, normVer int) Result {
	node := make(map[string]Node, len(nodes))
	ids := make([]string, 0, len(nodes))
	for _, n := range nodes {
		node[n.ID] = n
		ids = append(ids, n.ID)
	}

	type outEdge struct {
		to    string
		conf  float64
		known bool
	}
	out := make(map[string][]outEdge, len(nodes))
	adj := make(map[string][]string, len(nodes))
	for _, e := range edges {
		_, known := node[e.To]
		out[e.From] = append(out[e.From], outEdge{e.To, e.Confidence, known})
		if known {
			adj[e.From] = append(adj[e.From], e.To)
		}
	}

	sccs := stronglyConnected(ids, adj) // callees (sinks) first
	sccOf := make(map[string]int, len(ids))
	for i, comp := range sccs {
		for _, v := range comp {
			sccOf[v] = i
		}
	}

	res := Result{
		BehaviorHash: make(map[string]content.Hash, len(nodes)),
		BehaviorConf: make(map[string]float64, len(nodes)),
	}
	sccBH := make([]content.Hash, len(sccs))
	sccConf := make([]float64, len(sccs))

	for i, comp := range sccs {
		inComp := make(map[string]bool, len(comp))
		for _, v := range comp {
			inComp[v] = true
		}
		var unit content.Hash
		if len(comp) > 1 {
			members := make([]content.Hash, 0, len(comp))
			for _, v := range comp {
				members = append(members, node[v].SymbolHash)
			}
			unit = content.GroupHash(members)
		} else {
			unit = node[comp[0]].SymbolHash
		}

		conf := 1.0
		calleeSet := make(map[content.Hash]bool)
		for _, v := range comp {
			for _, oe := range out[v] {
				if oe.conf < conf {
					conf = oe.conf
				}
				if oe.known && inComp[oe.to] {
					continue // internal edge: confidence counted, not an external callee
				}
				if oe.known {
					j := sccOf[oe.to]
					calleeSet[sccBH[j]] = true
					if sccConf[j] < conf {
						conf = sccConf[j]
					}
				} else {
					calleeSet[extLeaf(oe.to)] = true
				}
			}
		}
		callees := make([]content.Hash, 0, len(calleeSet))
		for hsh := range calleeSet {
			callees = append(callees, hsh)
		}
		bh := content.BehaviorHash(unit, normVer, callees)
		sccBH[i], sccConf[i] = bh, conf
		for _, v := range comp {
			res.BehaviorHash[v] = bh
			res.BehaviorConf[v] = conf
		}
	}
	return res
}

// extLeaf gives a deterministic opaque hash for an edge target outside the
// graph (cross-repo/unresolved callee), so its identity still contributes.
func extLeaf(id string) content.Hash { return content.Hash("ext:" + id) }

// stronglyConnected returns Tarjan SCCs in reverse-topological order (a
// component is emitted only after every component it can reach) — i.e. callees
// before callers, exactly the order the behavior pass consumes.
func stronglyConnected(ids []string, adj map[string][]string) [][]string {
	index := 0
	idxOf := make(map[string]int)
	low := make(map[string]int)
	onStack := make(map[string]bool)
	var stack []string
	var out [][]string
	var strong func(v string)
	strong = func(v string) {
		idxOf[v] = index
		low[v] = index
		index++
		stack = append(stack, v)
		onStack[v] = true
		for _, w := range adj[v] {
			if _, seen := idxOf[w]; !seen {
				strong(w)
				if low[w] < low[v] {
					low[v] = low[w]
				}
			} else if onStack[w] {
				if idxOf[w] < low[v] {
					low[v] = idxOf[w]
				}
			}
		}
		if low[v] == idxOf[v] {
			var comp []string
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			out = append(out, comp)
		}
	}
	for _, v := range ids {
		if _, seen := idxOf[v]; !seen {
			strong(v)
		}
	}
	return out
}
