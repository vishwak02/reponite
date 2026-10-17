// graph.go answers the dashboard's graph view: a symbol's call-graph
// neighborhood, a few hops out in either direction, as nodes and edges ready
// to draw. Pure over Store.Snapshot.
package query

import (
	"path"
	"sort"
	"strings"
)

// GraphNode is one symbol of a neighborhood. Hop is its signed distance from
// the focus: negative for callers, positive for callees, 0 the focus.
type GraphNode struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Pkg   string `json:"pkg"`
	Hop   int    `json:"hop"`
	Test  bool   `json:"test,omitempty"`
	More  int    `json:"more,omitempty"` // neighbors not shown (over the limit)
	// External counts calls to code outside the repo that are not drawn.
	External int `json:"external,omitempty"`
}

// GraphEdge is a call from one node to another.
type GraphEdge struct {
	From       string  `json:"from"`
	To         string  `json:"to"`
	Method     string  `json:"method"`
	Confidence float64 `json:"confidence"`
}

// GraphResult is a call-graph neighborhood.
type GraphResult struct {
	Focus     string      `json:"focus"`
	Nodes     []GraphNode `json:"nodes"`
	Edges     []GraphEdge `json:"edges"`
	Truncated bool        `json:"truncated"`
	Note      string      `json:"note"`
	Meta      Meta        `json:"_meta"`
}

// CallGraph returns the neighborhood of symbol in repo at ref: callers up to
// depth hops back and callees up to depth hops forward, at most limit nodes
// (the nearest first). External callees (not defined in the repo) are leaves.
func CallGraph(s Store, repo, ref, symbol string, depth, limit int, tests, external bool) GraphResult {
	if depth <= 0 {
		depth = 1
	}
	if depth > 4 {
		depth = 4
	}
	if limit <= 0 {
		limit = 120
	}
	res := GraphResult{Meta: NewMeta(s, repo, ref), Nodes: []GraphNode{}, Edges: []GraphEdge{}}
	qids := ResolveSymbol(s, repo, ref, symbol)
	if len(qids) == 0 {
		res.Note = "no symbol " + symbol + " in " + repo + " at " + ref
		return res
	}
	focus := qids[0]
	res.Focus = focus
	snap := s.Snapshot(repo, ref)
	syms := s.SymbolsAt(repo, ref)
	callers := map[string][]Callee{} // callee -> callers (as Callee{Name: caller})
	for caller, cs := range snap.Callees {
		for _, c := range cs {
			callers[c.Name] = append(callers[c.Name], Callee{Name: caller, ResolutionMethod: c.ResolutionMethod, Confidence: c.Confidence})
		}
	}
	hop := map[string]int{focus: 0}
	order := []string{focus}
	more := map[string]int{}
	externalCount := map[string]int{}
	isTest := func(q string) bool { sr, ok := syms[q]; return ok && IsTestSymbol(sr, q) }
	edges := map[[2]string]GraphEdge{}
	expand := func(dir int) {
		frontier := []string{focus}
		for d := 1; d <= depth && len(frontier) > 0; d++ {
			var next []string
			for _, n := range frontier {
				var nbrs []Callee
				if dir > 0 {
					nbrs = snap.Callees[n]
				} else {
					nbrs = callers[n]
				}
				sort.Slice(nbrs, func(i, j int) bool {
					if nbrs[i].Confidence != nbrs[j].Confidence {
						return nbrs[i].Confidence > nbrs[j].Confidence
					}
					return nbrs[i].Name < nbrs[j].Name
				})
				for _, c := range nbrs {
					if !tests && isTest(c.Name) {
						continue
					}
					if _, in := syms[c.Name]; !in && !external {
						externalCount[n]++
						continue // a library call: counted on its caller, not drawn
					}
					from, to := n, c.Name
					if dir < 0 {
						from, to = c.Name, n
					}
					if _, seen := hop[c.Name]; !seen {
						if len(order) >= limit {
							more[n]++
							res.Truncated = true
							continue
						}
						hop[c.Name] = d * dir
						order = append(order, c.Name)
						next = append(next, c.Name)
					}
					edges[[2]string{from, to}] = GraphEdge{From: from, To: to, Method: c.ResolutionMethod, Confidence: c.Confidence}
				}
			}
			frontier = next
		}
	}
	expand(-1)
	expand(1)
	for _, q := range order {
		label := baseName(q)
		pkg := strings.TrimSuffix(q, "."+label)
		if i := strings.LastIndex(pkg, "/"); i >= 0 && strings.Contains(pkg[i:], ".") {
			label = pkg[strings.LastIndex(pkg, ".")+1:] + "." + label // Class.method
			pkg = pkg[:strings.LastIndex(pkg, ".")]
		}
		_, inRepo := syms[q]
		if !inRepo {
			pkg = "(external)"
		}
		res.Nodes = append(res.Nodes, GraphNode{ID: q, Label: label, Pkg: path.Clean(pkg), Hop: hop[q], Test: isTest(q), More: more[q],
			External: externalCount[q]})
	}
	for _, e := range edges {
		if _, a := hop[e.From]; a {
			if _, b := hop[e.To]; b {
				res.Edges = append(res.Edges, e)
			}
		}
	}
	sort.Slice(res.Edges, func(i, j int) bool {
		if res.Edges[i].From != res.Edges[j].From {
			return res.Edges[i].From < res.Edges[j].From
		}
		return res.Edges[i].To < res.Edges[j].To
	})
	if len(qids) > 1 {
		res.Note = "ambiguous name; showing " + focus + " (also: " + strings.Join(qids[1:min(len(qids), 6)], ", ") + ")"
	}
	return res
}
