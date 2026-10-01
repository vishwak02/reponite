// xdispatch.go carries virtual dispatch across the repo boundary. Each repo is
// indexed alone, so a call through a platform interface (`splitter_->forget()`
// on a base declared in repo A) only reaches the overrides repo A itself
// defines — the implementation an application repo plugs in at runtime is
// invisible to A's call graph, and its callers are invisible to the
// application's. WithCrossRepoDispatch adds both directions to a Context
// result from the fleet's class declarations (impls.go): callee edges into
// other repos' overrides, and callers in other repos that reach the symbol
// through one of its bases. Pure over Store (ADR-018).
package query

import (
	"sort"
	"strings"
)

// MethodXRepoOverride: a subclass's override in ANOTHER repo of the method a
// call reaches through a base class — a possible runtime target (a plugin).
// MethodXRepoCaller: a caller in another repo that calls the method through a
// base class this symbol's class derives from.
const (
	MethodXRepoOverride = "xrepo-override"
	MethodXRepoCaller   = "xrepo-base-call"
	ConfXRepo           = 0.6
)

// classOf splits a method id "pkg.Class.m" into ("Class", "m").
func classOf(qid string) (class, method string, ok bool) {
	i := strings.LastIndex(qid, ".")
	if i <= 0 {
		return "", "", false
	}
	j := strings.LastIndex(qid[:i], ".")
	class = qid[j+1 : i]
	if class == "" || strings.Contains(class, "/") || class[0] < 'A' || class[0] > 'Z' {
		return "", "", false // a package-level function, or not a class name
	}
	return class, qid[i+1:], true
}

// WithCrossRepoDispatch extends r (Context of a symbol in repo) with the
// fleet's cross-repo dispatch edges. It is a no-op outside a fleet.
func WithCrossRepoDispatch(s Store, repo, ref string, r ContextResult, includeTests bool) ContextResult {
	repos := s.Repos()
	if len(repos) < 2 {
		return r
	}
	var g *classGraph
	graph := func() *classGraph {
		if g == nil {
			g = scanClasses(s, repos, ref)
		}
		return g
	}
	// Callees: a call to Base.m reaches each other-repo subclass's own m.
	seen := map[string]bool{}
	for _, e := range r.CalleeEdges {
		seen[e.Name] = true
	}
	for _, e := range append([]CalleeEdge(nil), r.CalleeEdges...) {
		if e.Repo != "" {
			continue
		}
		cls, m, ok := classOf(e.Name)
		if !ok {
			continue
		}
		for _, d := range graph().descendants(cls) {
			if d.repo == repo || (!includeTests && testCode(d.path, d.name)) {
				continue
			}
			def := graph().defOf(s, ref, d.repo, d.name, m)
			name := d.repo + ":" + def
			if def == "" || seen[name] {
				continue
			}
			seen[name] = true
			r.CalleeEdges = append(r.CalleeEdges, CalleeEdge{Name: name, ResolutionMethod: MethodXRepoOverride, Confidence: ConfXRepo, Repo: d.repo})
			r.Callees = append(r.Callees, name)
		}
	}
	// Callers: whoever in another repo calls Base.m for a base of this class.
	if cls, m, ok := classOf(r.Symbol); ok {
		bases := graph().ancestors(cls)
		if len(bases) > 0 {
			want := map[string]bool{}
			for _, b := range bases {
				want["."+b+"."+m] = true
			}
			have := map[string]bool{}
			for _, c := range r.CallerEdges {
				have[c.Name] = true
			}
			for _, rp := range repos {
				if rp == repo {
					continue
				}
				snap := s.Snapshot(rp, ref)
				for caller, cs := range snap.Callees {
					for _, c := range cs {
						j := strings.LastIndex(c.Name, ".")
						k := strings.LastIndex(c.Name[:max(j, 0)], ".")
						if j <= 0 || k < 0 || !want[c.Name[k:]] {
							continue
						}
						isTest := IsTestQID(s, rp, ref, caller) || testCode(caller, "")
						name := rp + ":" + caller
						if (!includeTests && isTest) || have[name] {
							break
						}
						have[name] = true
						r.CallerEdges = append(r.CallerEdges, CallerEdge{Name: name, IsTest: isTest, Repo: rp, Via: c.Name})
						r.Callers = append(r.Callers, name)
						break
					}
				}
			}
		}
	}
	sort.Strings(r.Callers)
	sort.Strings(r.Callees)
	sort.SliceStable(r.CallerEdges, func(i, j int) bool { return r.CallerEdges[i].Name < r.CallerEdges[j].Name })
	sort.SliceStable(r.CalleeEdges, func(i, j int) bool { return r.CalleeEdges[i].Name < r.CalleeEdges[j].Name })
	return r
}
