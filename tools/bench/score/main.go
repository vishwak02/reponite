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
	jsonOut := flag.Bool("json", false, "print JSON")
	flag.Parse()
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
