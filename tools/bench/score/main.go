//go:build bench && sqlite

// score compares reponite's index of a repository with ground truth produced
// by an independent extractor (tools/bench/gt) and prints precision and recall
// of definitions and call edges. Symbols are joined by definition location
// (file and line), never by name, so a naming difference cannot hide an error.
//
//	go run -tags "bench sqlite" ./tools/bench/score -store DIR -repo NAME -ref REF -truth truth.json
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

type def struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line int    `json:"line"`
	End  int    `json:"end"`
	Name string `json:"name"`
	Test bool   `json:"test"`
}

type edge struct {
	From string `json:"from"`
	To   string `json:"to"`
	Kind string `json:"kind"`
	Line int    `json:"line"`
}

type truth struct {
	Lang  string `json:"lang"`
	Defs  []def  `json:"defs"`
	Edges []edge `json:"edges"`
	// Names, when present, lists every name each definition calls: an edge
	// the extractor could not resolve statically is then "undetermined" if
	// its callee's name is called, "false" if it is never called.
	Names map[string][]string `json:"names"`
}

// Score is one repository's result.
type Score struct {
	Repo, Ref, Lang string
	Defs            ratio            // ground-truth definitions reponite stores at the same place
	Edges           map[string]ratio // by confidence band: precision of reponite's edges
	Undetermined    map[string]int   // by band: edges the extractor cannot judge (dynamic receivers)
	EdgeRecall      ratio            // ground-truth static edges reponite has
	FalseEdges      []string         `json:",omitempty"` // examples
	MissedEdges     []string         `json:",omitempty"`
	MissedDefs      []string         `json:",omitempty"`
	Unmapped        int              // reponite symbols with no ground-truth definition at their place
}

type ratio struct{ Hit, Total int }

var minPrecision, minRecall float64
var gateFailed []string

// gate records a failure when r is below min percent (min < 0: no gate; an
// empty ratio passes).
func gate(what string, r ratio, min float64) {
	if min < 0 || r.Total == 0 {
		return
	}
	if pct := 100 * float64(r.Hit) / float64(r.Total); pct+1e-9 < min {
		gateFailed = append(gateFailed, fmt.Sprintf("%s %.1f%% < %.1f%%", what, pct, min))
	}
}

func gateExit() {
	if len(gateFailed) == 0 {
		return
	}
	for _, g := range gateFailed {
		fmt.Printf("  GATE FAILED: %s\n", g)
	}
	os.Exit(1)
}

func (r ratio) String() string {
	if r.Total == 0 {
		return "n/a"
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(r.Hit)/float64(r.Total), r.Hit, r.Total)
}

func band(conf float64) string {
	switch {
	case conf >= 0.85:
		return "resolved(>=0.85)"
	case conf >= 0.6:
		return "medium(0.6-0.85)"
	}
	return "low(<0.6)"
}

func main() {
	storeDir := flag.String("store", "", "reponite store dir (REPONITE_STORE_DIR)")
	repo := flag.String("repo", "", "repo name")
	ref := flag.String("ref", "HEAD", "indexed ref")
	tf := flag.String("truth", "", "ground truth JSON")
	rf := flag.String("routes", "", "golden endpoint list (tools/bench/golden/routes): score routes instead")
	cf := flag.String("calls", "", "golden call list (tools/bench/golden/calls): score those callers' edges instead")
	cmds := flag.Bool("commands", false, "with -truth: also check what context (callers) and usages (call sites) answer for each definition")
	jsonOut := flag.Bool("json", false, "print JSON")
	flag.Float64Var(&minPrecision, "min-precision", -1, "gate: exit 1 when precision of resolved edges (or routes) is below this percent")
	flag.Float64Var(&minRecall, "min-recall", -1, "gate: exit 1 when recall (static edges, golden calls at any confidence, or method+path routes) is below this percent")
	flag.Parse()
	defer gateExit()
	if *rf != "" {
		scoreRoutes(*storeDir, *repo, *ref, *rf)
		return
	}
	if *cf != "" {
		scoreCalls(*storeDir, *repo, *ref, *cf)
		return
	}
	var t truth
	b, err := os.ReadFile(*tf)
	must(err)
	must(json.Unmarshal(b, &t))
	st, err := sqlite.Open(*storeDir + "/" + *repo + "/index.db")
	must(err)
	defer st.Close()

	files := st.Files(*repo, *ref)
	// ground-truth definitions by file, to join reponite spans by place
	byPath := map[string][]def{}
	defByID := map[string]def{}
	for _, d := range t.Defs {
		byPath[d.Path] = append(byPath[d.Path], d)
		defByID[d.ID] = d
	}
	// a definition joins a reponite symbol of the same name whose span
	// starts at, or a few lines above (decorators, comments), its line
	at := func(path string, start, end int, name string) string {
		best, bestGap := "", 1<<30
		for _, d := range byPath[path] {
			if d.Line < start || d.Line > end || baseOf(d.Name) != name {
				continue
			}
			if gap := d.Line - start; gap < bestGap {
				best, bestGap = d.ID, gap
			}
		}
		return best
	}
	syms := st.SymbolsAt(*repo, *ref)
	qidAt := map[string]string{} // qid -> ground-truth def id
	defHit := map[string]bool{}
	sc := Score{Repo: *repo, Ref: *ref, Lang: t.Lang, Edges: map[string]ratio{}, Undetermined: map[string]int{}}
	for q := range syms {
		p, s, e, ok := query.LocateSymbol(files, q)
		if !ok {
			continue
		}
		if id := at(p, s, e, baseOf(q)); id != "" {
			qidAt[q] = id
			defHit[id] = true
		} else if strings.HasSuffix(p, ext(t.Lang)) {
			sc.Unmapped++
		}
	}
	for _, d := range t.Defs {
		sc.Defs.Total++
		if defHit[d.ID] {
			sc.Defs.Hit++
		} else if len(sc.MissedDefs) < 15 {
			sc.MissedDefs = append(sc.MissedDefs, d.ID+" "+d.Name)
		}
	}
	truthEdge := map[string]bool{}
	anyEdge := map[string]bool{}
	resolvedName := map[string]bool{} // from + ">" + callee name: the extractor resolved that call
	for _, e := range t.Edges {
		anyEdge[e.From+">"+e.To] = true
		if e.Kind == "static" {
			truthEdge[e.From+">"+e.To] = true
		}
		resolvedName[e.From+">"+baseOf(defByID[e.To].Name)] = true
	}
	calls := map[string]bool{} // from + ">" + name: the definition calls that name
	for from, ns := range t.Names {
		for _, n := range ns {
			calls[from+">"+n] = true
		}
	}
	got := map[string]bool{}
	snap := st.Snapshot(*repo, *ref)
	var callers []string
	for c := range snap.Callees {
		callers = append(callers, c)
	}
	sort.Strings(callers)
	for _, caller := range callers {
		from, ok := qidAt[caller]
		if !ok {
			continue
		}
		for _, c := range snap.Callees[caller] {
			to, ok := qidAt[c.Name]
			if !ok {
				continue // external or not a definition of this language
			}
			k := from + ">" + to
			got[k] = true
			bd := band(c.Confidence)
			name := baseOf(defByID[to].Name)
			if !anyEdge[k] && t.Names != nil && calls[from+">"+name] && !resolvedName[from+">"+name] {
				sc.Undetermined[bd]++ // called on a receiver the extractor cannot type
				continue
			}
			r := sc.Edges[bd]
			r.Total++
			if anyEdge[k] {
				r.Hit++
			} else if len(sc.FalseEdges) < 25 {
				sc.FalseEdges = append(sc.FalseEdges, fmt.Sprintf("[%s %.2f] %s (%s) -> %s (%s)", c.ResolutionMethod, c.Confidence,
					caller, from, c.Name, to))
			}
			sc.Edges[bd] = r
		}
	}
	for k := range truthEdge {
		sc.EdgeRecall.Total++
		if got[k] {
			sc.EdgeRecall.Hit++
		} else if len(sc.MissedEdges) < 25 {
			ft := strings.SplitN(k, ">", 2)
			sc.MissedEdges = append(sc.MissedEdges, fmt.Sprintf("%s %s -> %s %s", ft[0], defByID[ft[0]].Name, ft[1], defByID[ft[1]].Name))
		}
	}
	sort.Strings(sc.MissedEdges)
	if *cmds {
		checkCommands(st, *repo, *ref, t, qidAt, calls)
	}
	if *jsonOut {
		out, _ := json.MarshalIndent(sc, "", " ")
		fmt.Println(string(out))
		return
	}
	fmt.Printf("%s@%s (%s)\n", sc.Repo, sc.Ref, sc.Lang)
	fmt.Printf("  definitions found      %s\n", sc.Defs)
	for _, bd := range []string{"resolved(>=0.85)", "medium(0.6-0.85)", "low(<0.6)"} {
		u := ""
		if n := sc.Undetermined[bd]; n > 0 {
			u = fmt.Sprintf("  (+%d undetermined)", n)
		}
		fmt.Printf("  edge precision %-17s %s%s\n", bd, sc.Edges[bd], u)
	}
	fmt.Printf("  edge recall (static)   %s\n", sc.EdgeRecall)
	gate("resolved edge precision", sc.Edges["resolved(>=0.85)"], minPrecision)
	gate("static edge recall", sc.EdgeRecall, minRecall)
	fmt.Printf("  reponite symbols with no definition at their place: %d\n", sc.Unmapped)
	for _, x := range sc.FalseEdges {
		fmt.Printf("  FALSE  %s\n", x)
	}
	for _, x := range sc.MissedEdges {
		fmt.Printf("  MISSED %s\n", x)
	}
	for _, x := range sc.MissedDefs {
		fmt.Printf("  NODEF  %s\n", x)
	}
}

// baseOf is the last segment of a qualified name (Class.method -> method).
func baseOf(q string) string {
	if i := strings.LastIndexAny(q, "./"); i >= 0 {
		return q[i+1:]
	}
	return q
}

func ext(lang string) string {
	switch lang {
	case "go":
		return ".go"
	case "python":
		return ".py"
	case "typescript":
		return ".ts"
	}
	return ""
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// scoreRoutes compares reponite's server routes for a repo with a golden list.
func scoreRoutes(storeDir, repo, ref, golden string) {
	b, err := os.ReadFile(golden)
	must(err)
	type ep struct{ method, path, handler string }
	var want []ep
	for _, ln := range strings.Split(string(b), "\n") {
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		h := "-"
		if len(f) > 2 {
			h = f[2]
		}
		want = append(want, ep{f[0], normPath(f[1]), h})
	}
	st, err := sqlite.Open(storeDir + "/" + repo + "/index.db")
	must(err)
	defer st.Close()
	res := query.Routes(st, repo, ref, "", 1<<20)
	var got []ep
	for _, l := range res.Links {
		r := l.Route
		got = append(got, ep{strings.ToUpper(r.Method), normPath(r.Pattern), baseOf(r.Handler)})
	}
	wantPath := map[string]bool{}
	wantExact := map[string]ep{}
	for _, w := range want {
		wantPath[w.path] = true
		wantExact[w.method+" "+w.path] = w
	}
	gotExact, gotPath := map[string]ep{}, map[string]bool{}
	var extra []string
	matched := 0
	for _, g := range got {
		gotPath[g.path] = true
		if g.method != "*" {
			gotExact[g.method+" "+g.path] = g
		}
		ok := false
		if g.method == "*" {
			ok = wantPath[g.path]
		} else {
			_, ok = wantExact[g.method+" "+g.path]
			if !ok {
				_, ok = wantExact["* "+g.path]
			}
		}
		if ok {
			matched++
		} else {
			extra = append(extra, g.method+" "+g.path+" "+g.handler)
		}
	}
	var pathHit, exactHit, handlerHit, handlerTotal int
	var missing []string
	for _, w := range want {
		if gotPath[w.path] {
			pathHit++
		}
		g, ok := gotExact[w.method+" "+w.path]
		if ok || w.method == "*" && gotPath[w.path] {
			exactHit++
		} else {
			missing = append(missing, w.method+" "+w.path)
		}
		if ok && w.handler != "-" {
			handlerTotal++
			if g.handler == w.handler {
				handlerHit++
			}
		}
	}
	fmt.Printf("%s@%s routes (%d golden, %d found)\n", repo, ref, len(want), len(got))
	fmt.Printf("  path recall            %s\n", ratio{pathHit, len(want)})
	fmt.Printf("  method+path recall     %s\n", ratio{exactHit, len(want)})
	fmt.Printf("  precision              %s\n", ratio{matched, len(got)})
	fmt.Printf("  handler names          %s\n", ratio{handlerHit, handlerTotal})
	gate("route precision", ratio{matched, len(got)}, minPrecision)
	gate("method+path recall", ratio{exactHit, len(want)}, minRecall)
	sort.Strings(missing)
	sort.Strings(extra)
	for _, m := range missing {
		fmt.Printf("  MISSING %s\n", m)
	}
	for i, e := range extra {
		if i >= 20 {
			fmt.Printf("  … %d more extra\n", len(extra)-20)
			break
		}
		fmt.Printf("  EXTRA   %s\n", e)
	}
}

// normPath makes route paths comparable: placeholders as {}, no trailing
// slash, a leading slash.
func normPath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		if strings.HasPrefix(s, ":") || strings.HasPrefix(s, "{") || strings.HasPrefix(s, "<") || strings.HasPrefix(s, "[") {
			segs[i] = "{}"
		}
	}
	return "/" + strings.Join(segs, "/")
}

// scoreCalls compares the edges of hand-picked callers with a golden list
// that names, for each caller, every function of the repo it calls:
//
//	src/main.rs:80 run
//	  src/walk.rs:717 scan
//
// Locations are definition lines; a class's overloads count as the one id
// reponite gives them. Since each caller's list is complete, an
// edge reponite draws from a listed caller to a repo symbol not in its list is
// false. Edges to names reponite left unresolved (external, ambiguous) are
// neither: they are counted as recall misses when the golden lists a target.
func scoreCalls(storeDir, repo, ref, golden string) {
	b, err := os.ReadFile(golden)
	must(err)
	type loc struct {
		path string
		line int
		name string
	}
	parse := func(f []string) loc {
		pl := strings.SplitN(f[0], ":", 2)
		var n int
		fmt.Sscanf(pl[1], "%d", &n)
		return loc{pl[0], n, f[1]}
	}
	var callers []loc
	want := map[loc]map[loc]bool{}
	allowed := map[loc]map[loc]bool{} // ~ lines: possible runtime targets
	var cur loc
	for _, ln := range strings.Split(string(b), "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		f := strings.Fields(t)
		opt := f[0] == "~"
		if opt {
			f = f[1:]
		}
		if len(f) < 2 {
			continue
		}
		if !strings.HasPrefix(ln, " ") {
			cur = parse(f)
			callers = append(callers, cur)
			want[cur], allowed[cur] = map[loc]bool{}, map[loc]bool{}
			continue
		}
		if opt {
			allowed[cur][parse(f)] = true
		} else {
			want[cur][parse(f)] = true
		}
	}
	st, err := sqlite.Open(storeDir + "/" + repo + "/index.db")
	must(err)
	defer st.Close()
	files := st.Files(repo, ref)
	// every symbol by its definition place
	at := map[string]loc{}
	byLoc := map[loc]string{}
	for q := range st.SymbolsAt(repo, ref) {
		p, s, _, ok := query.LocateSymbol(files, q)
		if !ok {
			continue
		}
		l := loc{p, s, baseOf(q)}
		at[q] = l
		byLoc[l] = q
	}
	// a golden line is the declaration's own line; a symbol's span may start
	// at its annotations or doc attributes a few lines above
	canon := func(l loc) loc {
		for d := 0; d <= 8; d++ {
			c := loc{l.path, l.line - d, l.name}
			if _, ok := byLoc[c]; ok {
				return c
			}
		}
		// an overload: reponite gives a class's overloads one id, placed at
		// the first; the golden names the one a call picks
		var same []loc
		for c := range byLoc {
			if c.path == l.path && c.name == l.name {
				same = append(same, c)
			}
		}
		if len(same) == 1 {
			return same[0]
		}
		return l
	}
	for _, c := range callers {
		w, a := map[loc]bool{}, map[loc]bool{}
		for x := range want[c] {
			w[canon(x)] = true
		}
		for x := range allowed[c] {
			a[canon(x)] = true
		}
		cc := canon(c)
		delete(want, c)
		delete(allowed, c)
		want[cc], allowed[cc] = w, a
	}
	for i, c := range callers {
		callers[i] = canon(c)
	}
	snap := st.Snapshot(repo, ref)
	prec := map[string]ratio{}
	var recall, recallResolved ratio
	var falses, misses []string
	for _, c := range callers {
		q, ok := byLoc[c]
		if !ok {
			misses = append(misses, fmt.Sprintf("caller not found: %s:%d %s", c.path, c.line, c.name))
			recall.Total += len(want[c])
			recallResolved.Total += len(want[c])
			continue
		}
		got := map[loc]float64{}
		for _, e := range snap.Callees[q] {
			to, ok := at[e.Name]
			if !ok {
				continue // an unresolved name, not a symbol
			}
			got[to] = e.Confidence
			bd := band(e.Confidence)
			r := prec[bd]
			r.Total++
			if want[c][to] || allowed[c][to] {
				r.Hit++
			} else {
				falses = append(falses, fmt.Sprintf("[%s %.2f] %s:%d %s -> %s:%d %s", e.ResolutionMethod, e.Confidence,
					c.path, c.line, c.name, to.path, to.line, to.name))
			}
			prec[bd] = r
		}
		for w := range want[c] {
			recall.Total++
			recallResolved.Total++
			conf, ok := got[w]
			if ok {
				recall.Hit++
			}
			if ok && conf >= 0.85 {
				recallResolved.Hit++
			} else {
				how := "missing"
				if ok {
					how = fmt.Sprintf("at %.2f", conf)
				}
				misses = append(misses, fmt.Sprintf("%s %s:%d %s -> %s:%d %s", how, c.path, c.line, c.name, w.path, w.line, w.name))
			}
		}
	}
	sort.Strings(falses)
	sort.Strings(misses)
	fmt.Printf("%s@%s calls (%d callers, %d golden edges)\n", repo, ref, len(callers), recall.Total)
	for _, bd := range []string{"resolved(>=0.85)", "medium(0.6-0.85)", "low(<0.6)"} {
		fmt.Printf("  edge precision %-17s %s\n", bd, prec[bd])
	}
	fmt.Printf("  recall (any confidence)  %s\n", recall)
	gate("resolved edge precision", prec["resolved(>=0.85)"], minPrecision)
	gate("golden call recall", recall, minRecall)
	fmt.Printf("  recall (resolved)        %s\n", recallResolved)
	for _, x := range falses {
		fmt.Printf("  FALSE  %s\n", x)
	}
	for _, x := range misses {
		fmt.Printf("  MISSED %s\n", x)
	}
}

// checkCommands asks context and usages about every definition with static
// callers in the truth, as a user would, and compares the answers with it.
// A caller (or call site) is wrong when the truth knows the caller and it
// never calls that name; one the extractor cannot judge is not counted.
func checkCommands(st *sqlite.Store, repo, ref string, t truth, qidAt map[string]string, calls map[string]bool) {
	qidOf := map[string]string{}
	dup := map[string]bool{}
	for q, id := range qidAt {
		if _, ok := qidOf[id]; ok {
			dup[id] = true
		}
		qidOf[id] = q
	}
	defByID := map[string]def{}
	for _, d := range t.Defs {
		defByID[d.ID] = d
	}
	edgeAny := map[string]bool{}
	for _, e := range t.Edges {
		edgeAny[e.From+">"+e.To] = true
	}
	// judge says whether caller id really calls target to: true/false, or
	// unknown when the truth cannot tell (called by name on an untyped value)
	judge := func(id, to, name string) (bool, bool) {
		if edgeAny[id+">"+to] {
			return true, true
		}
		if t.Names == nil {
			return false, false
		}
		if _, known := t.Names[id]; !known {
			return false, false
		}
		if calls[id+">"+name] {
			return false, false // calls that name on something the truth cannot type
		}
		return false, true
	}
	callersOf := map[string]map[string]bool{}
	sites := map[string]map[string]bool{} // target -> "path:line" of its static call sites
	for _, e := range t.Edges {
		if e.Kind != "static" {
			continue
		}
		if callersOf[e.To] == nil {
			callersOf[e.To], sites[e.To] = map[string]bool{}, map[string]bool{}
		}
		callersOf[e.To][e.From] = true
		sites[e.To][fmt.Sprintf("%s:%d", defByID[e.From].Path, e.Line)] = true
	}
	var targets []string
	for to := range callersOf {
		if _, ok := qidOf[to]; ok && !dup[to] {
			targets = append(targets, to)
		}
	}
	sort.Strings(targets)
	var ctxRecall, ctxPrec, useRecall, usePrec ratio
	var bad []string
	for _, to := range targets {
		q := qidOf[to]
		name := baseOf(defByID[to].Name)
		res := query.Context(st, repo, ref, q, true)
		got := map[string]bool{}
		for _, c := range res.Callers {
			id, ok := qidAt[c]
			if !ok {
				continue
			}
			got[id] = true
			if ok, decided := judge(id, to, name); decided {
				ctxPrec.Total++
				if ok {
					ctxPrec.Hit++
				} else if len(bad) < 20 {
					bad = append(bad, fmt.Sprintf("context %s: caller %s never calls it", defByID[to].Name, defByID[id].Name))
				}
			}
		}
		for c := range callersOf[to] {
			ctxRecall.Total++
			if got[c] {
				ctxRecall.Hit++
			} else if len(bad) < 20 {
				bad = append(bad, fmt.Sprintf("context %s: missing caller %s", defByID[to].Name, defByID[c].Name))
			}
		}
		u := query.Usages(st, repo, ref, q)
		at := map[string]bool{}
		for _, x := range u.Usages {
			if x.Repo != "" && x.Repo != repo {
				continue
			}
			at[fmt.Sprintf("%s:%d", x.Path, x.Line)] = true
			if x.Confirmed {
				if id, ok := qidAt[x.In]; ok {
					if hit, decided := judge(id, to, name); decided {
						usePrec.Total++
						if hit {
							usePrec.Hit++
						} else if len(bad) < 20 {
							bad = append(bad, fmt.Sprintf("usages %s: confirmed site %s:%d in %s, which never calls it", defByID[to].Name, x.Path, x.Line, defByID[id].Name))
						}
					}
				}
			}
		}
		for s := range sites[to] {
			useRecall.Total++
			if at[s] {
				useRecall.Hit++
			} else if len(bad) < 20 {
				bad = append(bad, fmt.Sprintf("usages %s: missing call site %s", defByID[to].Name, s))
			}
		}
	}
	fmt.Printf("  commands (%d definitions with static callers)\n", len(targets))
	fmt.Printf("    context callers recall    %s\n", ctxRecall)
	fmt.Printf("    context callers precision %s\n", ctxPrec)
	fmt.Printf("    usages call-site recall   %s\n", useRecall)
	fmt.Printf("    usages confirmed precision %s\n", usePrec)
	for _, b := range bad {
		fmt.Printf("    WRONG %s\n", b)
	}
	gate("context caller precision", ctxPrec, minPrecision)
	gate("usages confirmed precision", usePrec, minPrecision)
}
