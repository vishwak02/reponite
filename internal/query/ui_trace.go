// ui_trace.go adapts the frontend edges (UIEdges) into a TraceEdgeSource, so a
// trace that starts at — or passes through — a component can follow what it
// renders, the handlers its markup binds and the API calls those handlers make,
// and from there cross into the server route like any client call in a body.
package query

import (
	"strings"
	"sync"
)

// UITraceEdges returns a TraceEdgeSource over the frontend edges of every repo
// (repo = FleetRepo) or one repo, at ref. The edges are computed once, on the
// first lookup, so a trace that never touches frontend code pays only that.
func UITraceEdges(s Store, repo, ref string) TraceEdgeSource {
	var once sync.Once
	down := map[string][]TraceEdge{}
	up := map[string][]TraceEdge{}
	build := func() {
		resolve := func(r, from, name string) string {
			// a handler is named as written in the markup; find its symbol id next
			// to the component (a nested function, a method, or a module function)
			dir := from
			if i := strings.LastIndex(from, "."); i > 0 {
				dir = from[:i]
			}
			for _, cand := range []string{from + "." + name, dir + "." + name, name} {
				if len(ResolveSymbol(s, r, ref, cand)) > 0 {
					return cand
				}
			}
			return ""
		}
		for _, e := range UIEdges(s, repo, ref) {
			switch e.Kind {
			case "renders":
				down[e.From] = append(down[e.From], TraceEdge{Repo: e.Repo, Target: e.To, Kind: "renders", Method: e.Resolution, Confidence: 0.6})
				up[e.To] = append(up[e.To], TraceEdge{Repo: e.Repo, Target: e.From, Kind: "rendered-by", Method: e.Resolution, Confidence: 0.6})
			case "handles":
				if h := resolve(e.Repo, e.From, e.To); h != "" {
					down[e.From] = append(down[e.From], TraceEdge{Repo: e.Repo, Target: h, Kind: "handles", Method: e.Detail, Confidence: 0.6})
					up[h] = append(up[h], TraceEdge{Repo: e.Repo, Target: e.From, Kind: "bound-by", Method: e.Detail, Confidence: 0.6})
				}
			case "calls_api":
				method, url, ok := strings.Cut(e.To, " ")
				if !ok {
					continue
				}
				edge := TraceEdge{Kind: "calls_api", HTTPMethod: method, URL: url, Method: "ui-api", Confidence: 0.6}
				from, handler, _ := strings.Cut(e.From, "#")
				down[from] = append(down[from], edge)
				if handler != "" {
					if h := resolve(e.Repo, from, handler); h != "" {
						down[h] = append(down[h], edge)
					}
				}
			}
		}
	}
	return func(r, qid string, isUp bool) []TraceEdge {
		once.Do(build)
		if isUp {
			return up[qid]
		}
		return down[qid]
	}
}
