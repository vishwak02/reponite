// codemap.go implements reponite_map — the first call an AI makes on an
// unfamiliar codebase. For one repo at a ref it reports what the repo is (its
// README summary line), what it is written in (languages with file/symbol
// counts), where execution enters (entrypoints.go), how it is divided
// (modules with the one-line purpose the code's own docs give — never
// invented), which symbols hold it together (hubs: most resolved in+out call
// edges), and what it depends on (deps.go). Without a repo it maps the fleet:
// every indexed repo briefly plus the cross-repo links already known (HTTP
// routes, ROS topics, import-resolved references, declared dependencies).
// Every section is ranked and filled to a token budget, with an explicit
// truncated flag and a next hint; every item carries a drill-in handle for
// explain/brief/context. Pure over Store (ADR-018).
package query

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultMapBudget is the token budget when the caller passes <= 0.
const DefaultMapBudget = 3000

// LangStat is one language's share of a repo.
type LangStat struct {
	Lang    string
	Files   int
	Symbols int
}

// Module is one package/directory with the purpose its own docs state.
type Module struct {
	Path    string // directory ("." = repo root); the drill-in handle for explain
	Files   int
	Symbols int
	FanIn   int    // resolved call edges entering from other modules
	Purpose string // "" when the code states none — never invented
	Source  string // where Purpose was read (path:line)
}

// Hub is one of the most-connected symbols.
type Hub struct {
	Symbol string // qualified id — the drill-in handle
	Path   string
	Line   int
	In     int // distinct resolved callers
	Out    int // distinct resolved in-repo callees
}

// MapResult is one repo's orientation map.
type MapResult struct {
	Repo, Ref, Commit string
	Module            string // module path (go.mod module, package name, ...)
	Summary           string // the README's summary line
	SummarySource     string
	Languages         []LangStat
	Files, Symbols    int
	Edges             int // resolved call edges
	EntryPoints       []EntryPoint
	Modules           []Module
	Hubs              []Hub
	Dependencies      []Dependency
	Manifests         []string // manifests the dependencies were read from
	InternalDeps      int      // dependencies on the repo's own packages (dropped)
	Truncated         bool
	Omitted           map[string]int // items dropped per section for budget
	Next              []string       // drill-in commands
	Meta              Meta
}

// RepoSummary is one repo in the fleet map.
type RepoSummary struct {
	Repo, Ref, Commit string
	Module            string
	Summary           string
	Languages         []string // "go:120" (files), top three
	Files, Symbols    int
	EntryPoints       map[string]int // kind -> count
	Routes            int
}

// FleetLink is a known cross-repo link, aggregated per (from, to, kind).
type FleetLink struct {
	From, To string
	Kind     string // http | ros-topic | import | declared-dep
	Count    int
	Examples []string
	Handle   string // the command that lists every instance
}

// FleetMapResult is the fleet orientation map.
type FleetMapResult struct {
	Repos     []RepoSummary
	Links     []FleetLink
	Truncated bool
	Omitted   map[string]int
	Next      []string
	Meta      Meta
}

// ExternalRefLister is implemented by stores that can list a ref's captured
// cross-repo references, so the fleet map can count import-resolved links
// without a query per exported symbol. Optional: without it the link kind is
// simply absent (and said so).
type ExternalRefLister interface {
	ExternalRefsFrom(repo, ref string) []ExternalRefHit
}

// Map builds one repo's orientation map at ref.
func Map(s Store, repo, ref string, budget int) MapResult {
	if budget <= 0 {
		budget = DefaultMapBudget
	}
	res := MapResult{Repo: repo, Ref: EffectiveRef(s, repo, ref), Module: s.ModulePath(repo), Omitted: map[string]int{}, Meta: NewMeta(s, repo, ref)}
	if !refIndexed(s, repo, ref) {
		res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf("%s is not indexed at %s (indexed: %v) — index it with `reponite index <dir> --git %s`", repo, res.Ref, s.Refs(repo), res.Ref))
		return res
	}
	if man, ok := s.Manifest(repo, ref); ok {
		res.Commit = man.Commit
	}
	files := s.Files(repo, ref)
	syms := s.SymbolsAt(repo, ref)
	snap := s.Snapshot(repo, ref)
	res.Files, res.Symbols = len(files), len(syms)
	for _, cs := range snap.Callees {
		res.Edges += len(cs)
	}
	res.Summary, res.SummarySource = readmeSummary(files)
	res.Languages = languageStats(files, syms)
	if !hasProjectFiles(files) {
		res.Meta.Warnings = append(res.Meta.Warnings, "no README, manifest, Dockerfile, Makefile or CI file is stored at this ref — it was indexed before project files were (IndexVer < 6); reindex it to fill entry points from build files, purposes from READMEs, and dependencies")
	}

	eps := append(EntryPoints(repo, files), httpEntries(repo, files)...)
	eps = mergeHTTP(eps)
	RankEntryPoints(eps)
	idx := newSpanIndex(files)
	mods := modules(files, syms, snap, idx)
	hubs := hubs(syms, snap, idx)
	deps, manifests, internal := RepoDependencies(files)
	res.Manifests, res.InternalDeps = manifests, internal
	if len(manifests) == 1 {
		for i := range deps {
			deps[i].Source = "" // the one manifest is res.Manifests
		}
	}

	// Fill by rounds: each round takes the next-ranked items from every
	// section in priority order, so a tight budget shows the top of each
	// section rather than all of one and none of the rest.
	spent := estTokens(res.Summary) + 40 + 12*len(res.Languages)
	type section struct {
		name string
		n    int
		per  int
		cost func(i int) int
		take func(i int)
	}
	secs := []section{
		{"entry_points", len(eps), 3, func(i int) int { return estEntry(eps[i]) }, func(i int) { res.EntryPoints = append(res.EntryPoints, eps[i]) }},
		{"modules", len(mods), 2, func(i int) int { return estTokens(mods[i].Path+mods[i].Purpose+mods[i].Source) + 22 }, func(i int) { res.Modules = append(res.Modules, mods[i]) }},
		{"hubs", len(hubs), 2, func(i int) int { return estTokens(hubs[i].Symbol+hubs[i].Path) + 20 }, func(i int) { res.Hubs = append(res.Hubs, hubs[i]) }},
		{"dependencies", len(deps), 4, func(i int) int { return estTokens(deps[i].Name+deps[i].Version+deps[i].Source) + 18 }, func(i int) { res.Dependencies = append(res.Dependencies, deps[i]) }},
	}
	pos := make([]int, len(secs))
	stopped := make([]bool, len(secs))
	for progress := true; progress; {
		progress = false
		for si, sc := range secs {
			for k := 0; k < sc.per && !stopped[si] && pos[si] < sc.n; k++ {
				c := sc.cost(pos[si])
				if spent+c > budget {
					stopped[si] = true
					break
				}
				spent += c
				sc.take(pos[si])
				pos[si]++
				progress = true
			}
		}
	}
	for si, sc := range secs {
		if left := sc.n - pos[si]; left > 0 {
			res.Omitted[sc.name] = left
			res.Truncated = true
		}
	}
	res.Next = withRef(mapNext(res, budget), ref)
	return res
}

// mergeHTTP folds a "server start" entry into its framework's route entry
// when the repo has routes, so a server reads as one entry: where it starts
// and how many routes it serves.
func mergeHTTP(eps []EntryPoint) []EntryPoint {
	hasRoutes := false
	for _, e := range eps {
		if e.Kind == EntryHTTP && e.Count > 0 {
			hasRoutes = true
		}
	}
	if !hasRoutes {
		return eps
	}
	var starts []string
	var out []EntryPoint
	for _, e := range eps {
		if e.Kind == EntryHTTP && e.Count == 0 {
			starts = append(starts, fmt.Sprintf("%s:%d", e.Path, e.Line))
			continue
		}
		out = append(out, e)
	}
	if len(starts) > 0 {
		for i := range out {
			if out[i].Kind == EntryHTTP && out[i].Count > 0 {
				out[i].Detail += "; started at " + strings.Join(firstN(starts, 3), ", ")
				break
			}
		}
	}
	return out
}

func firstN(ss []string, n int) []string {
	if len(ss) > n {
		return ss[:n]
	}
	return ss
}

func estEntry(e EntryPoint) int {
	// Field text plus the JSON keys and punctuation around it.
	return estTokens(e.Name+e.Path+e.Detail+e.Framework+e.Repo+strings.Join(e.Items, `", "`)) + 30
}

func mapNext(r MapResult, budget int) []string {
	var next []string
	if r.Truncated {
		var parts []string
		for _, k := range sortedKeys(r.Omitted) {
			parts = append(parts, fmt.Sprintf("%d %s", r.Omitted[k], k))
		}
		next = append(next, fmt.Sprintf("omitted for budget: %s — rerun with --budget %d, or drill in below", strings.Join(parts, ", "), budget*2))
	}
	if len(r.Modules) > 0 {
		next = append(next, "reponite explain "+r.Modules[0].Path+" --repo "+r.Repo+"   (a module: its files, purpose, top symbols)")
	}
	if len(r.Hubs) > 0 {
		next = append(next, "reponite explain "+r.Hubs[0].Symbol+" --repo "+r.Repo+"   (a symbol: what it is, callers/callees, routes, tests)")
	}
	for _, e := range r.EntryPoints {
		if e.Kind == EntryHTTP && e.Count > 0 {
			next = append(next, "reponite routes   (every route with the client calls that reach it)")
			break
		}
	}
	for _, e := range r.EntryPoints {
		if e.Kind == EntryROSNode || e.Kind == EntryLaunch {
			next = append(next, "reponite topics   (the ROS publish/subscribe graph)")
			break
		}
	}
	next = append(next, `reponite investigate "<how does X work>"   (a feature, by question)`)
	return next
}

// isAuxPath reports test-support trees the path rule does not classify as
// test code (mocks, fixtures, snapshots): kept in the index, but they are not
// what a map of the product should rank.
func isAuxPath(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "__mocks__", "__fixtures__", "__snapshots__", "mocks", "fixtures", "testdata":
			return true
		}
	}
	return false
}

func hasProjectFiles(files []File) bool {
	for _, f := range files {
		if ProjectFileKind(f.Path) != "" {
			return true
		}
	}
	return false
}

// languageStats counts code files and symbols per language, largest first.
func languageStats(files []File, syms map[string]SymbolRef) []LangStat {
	byLang := map[string]*LangStat{}
	for _, f := range files {
		l := langOfPath(f.Path)
		if !isCodeLang(l) && l != "ros-interface" && l != "ros-launch" {
			continue
		}
		if l == "script" {
			if len(f.Symbols) == 0 {
				continue
			}
			l = "shell"
		}
		st := byLang[l]
		if st == nil {
			st = &LangStat{Lang: l}
			byLang[l] = st
		}
		st.Files++
	}
	for _, sym := range syms {
		l := normLang(sym.Lang)
		if st := byLang[l]; st != nil {
			st.Symbols++
		} else if l != "" {
			byLang[l] = &LangStat{Lang: l, Symbols: 1}
		}
	}
	out := make([]LangStat, 0, len(byLang))
	for _, st := range byLang {
		out = append(out, *st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Lang < out[j].Lang
	})
	return out
}

// normLang maps the indexer's language names onto langOfPath's.
func normLang(l string) string {
	switch l {
	case "js", "jsx":
		return "javascript"
	case "ts", "tsx":
		return "typescript"
	case "py":
		return "python"
	case "c++":
		return "cpp"
	case "bash", "sh":
		return "shell"
	case "ros", "msg":
		return "ros-interface"
	case "roslaunch":
		return "ros-launch"
	}
	return l
}

var (
	reMDHeading  = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	reRSTUnder   = regexp.MustCompile(`^(?:={3,}|-{3,}|~{3,}|\^{3,}|"{3,}|'{3,}|\*{3,}|\+{3,}|#{3,})\s*$`)
	reMDNoise    = regexp.MustCompile(`^(?:\[!\[|!\[|<|\[\]\(|\|)`)
	reMDLink     = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reMDEmphasis = regexp.MustCompile("[*_`]{1,3}")
)

// readmeSummary is the root README's first prose line (badges, HTML and
// headings skipped), else its title heading. Shallowest README wins.
func readmeSummary(files []File) (string, string) {
	var best *File
	for i := range files {
		f := &files[i]
		if ProjectFileKind(f.Path) != ProjReadme {
			continue
		}
		if best == nil || strings.Count(f.Path, "/") < strings.Count(best.Path, "/") ||
			strings.Count(f.Path, "/") == strings.Count(best.Path, "/") && readmePref(f.Path) < readmePref(best.Path) {
			best = f
		}
	}
	if best == nil {
		return "", ""
	}
	s, line := docSummary(best.Content)
	if s == "" {
		return "", ""
	}
	return s, fmt.Sprintf("%s:%d", best.Path, line)
}

func readmePref(p string) int {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".md", ".markdown":
		return 0
	case ".rst":
		return 1
	}
	return 2
}

// docSummary reads a markdown/rst/plain document's summary line.
func docSummary(content string) (string, int) {
	lines := strings.Split(content, "\n")
	heading, headingLine := "", 0
	inFence, inFront, inComment := false, false, false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		switch {
		case i == 0 && t == "---":
			inFront = true
			continue
		case inFront:
			if t == "---" {
				inFront = false
			}
			continue
		case strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~"):
			inFence = !inFence
			continue
		case inFence:
			continue
		case strings.HasPrefix(t, "<!--"):
			inComment = !strings.Contains(t, "-->")
			continue
		case inComment:
			if strings.Contains(t, "-->") {
				inComment = false
			}
			continue
		case t == "" || reRSTUnder.MatchString(t) || reMDNoise.MatchString(t) || strings.HasPrefix(t, ".. "):
			continue
		}
		if m := reMDHeading.FindStringSubmatch(t); m != nil {
			if heading == "" {
				heading, headingLine = cleanMD(m[1]), i+1
			}
			continue
		}
		if i+1 < len(lines) && reRSTUnder.MatchString(strings.TrimSpace(lines[i+1])) {
			if heading == "" {
				heading, headingLine = cleanMD(t), i+1
			}
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") || strings.HasPrefix(t, ">") && len(t) < 3 {
			continue
		}
		if s := firstSentence(cleanMD(strings.TrimPrefix(t, "> "))); len(s) >= 12 {
			return s, i + 1
		}
	}
	return heading, headingLine
}

func cleanMD(s string) string {
	s = reMDLink.ReplaceAllString(s, "$1")
	s = reMDEmphasis.ReplaceAllString(s, "")
	return strings.TrimSpace(s)
}

var (
	reGoPkgDoc    = regexp.MustCompile(`(?m)^// Package \w+ `)
	reRustInner   = regexp.MustCompile(`(?m)^//!\s?(.*)$`)
	reDjangoName  = regexp.MustCompile(`verbose_name\s*=\s*(?:_\(\s*)?["']([^"']+)["']`)
	reJSFileDoc   = regexp.MustCompile(`(?s)^\s*/\*\*(.*?)\*/`)
	reJSFileTag   = regexp.MustCompile(`@(?:file|fileoverview|module|packageDocumentation)\b\s*(.*)`)
	rePkgXMLDesc  = regexp.MustCompile(`(?s)<description[^>]*>\s*(.*?)\s*</description>`)
	reCatkinStock = regexp.MustCompile(`(?i)^the \S+ package\.?$`)
	reJavaPkgDoc  = regexp.MustCompile(`(?s)/\*\*(.*?)\*/\s*package\b`)
)

// modulePurpose reads the one-line purpose a directory states about itself,
// with where it was read. The sources, in order: a package manifest's
// description, a README in the directory, a Go package doc, a Python
// __init__ docstring, a Rust //! crate/module doc, a Java package-info, a
// Django app's verbose_name, a JS/TS index file's @file/@module doc. A
// boilerplate description (catkin's "The x package") is no purpose.
func modulePurpose(dir string, inDir []*File) (string, string) {
	pick := func(pred func(base string) bool) []*File {
		var out []*File
		for _, f := range inDir {
			if pred(strings.ToLower(filepath.Base(f.Path))) {
				out = append(out, f)
			}
		}
		return out
	}
	for _, f := range pick(func(b string) bool { return b == "package.xml" }) {
		if m := rePkgXMLDesc.FindStringSubmatchIndex(f.Content); m != nil {
			d := firstSentence(f.Content[m[2]:m[3]])
			if d != "" && !reCatkinStock.MatchString(d) {
				return d, fmt.Sprintf("%s:%d", f.Path, lineOf(f.Content, m[0]))
			}
		}
	}
	for _, f := range pick(func(b string) bool { return b == "package.json" }) {
		var pj struct {
			Description string `json:"description"`
		}
		if json.Unmarshal([]byte(f.Content), &pj) == nil && strings.TrimSpace(pj.Description) != "" {
			return firstSentence(pj.Description), fmt.Sprintf("%s:%d", f.Path, jsonKeyLine(f.Content, "description"))
		}
	}
	for _, f := range pick(func(b string) bool { return b == "cargo.toml" || b == "pyproject.toml" }) {
		if d := tomlValue(f.Content, []string{"package", "project", "tool.poetry"}, "description"); d != "" {
			return firstSentence(d), f.Path
		}
	}
	readmes := pick(func(b string) bool { return ProjectFileKind(b) == ProjReadme })
	sort.Slice(readmes, func(i, j int) bool { return readmePref(readmes[i].Path) < readmePref(readmes[j].Path) })
	for _, f := range readmes {
		if s, line := docSummary(f.Content); s != "" {
			return s, fmt.Sprintf("%s:%d", f.Path, line)
		}
	}
	gos := pick(func(b string) bool { return strings.HasSuffix(b, ".go") && !strings.HasSuffix(b, "_test.go") })
	sort.SliceStable(gos, func(i, j int) bool {
		return filepath.Base(gos[i].Path) == "doc.go" && filepath.Base(gos[j].Path) != "doc.go"
	})
	for _, f := range gos {
		if loc := reGoPkgDoc.FindStringIndex(f.Content); loc != nil {
			lines := strings.Split(f.Content, "\n")
			start := lineOf(f.Content, loc[0])
			var block []string
			for k := start - 1; k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "//"); k++ {
				block = append(block, lines[k])
			}
			return firstSentence(cleanDoc(block)), fmt.Sprintf("%s:%d", f.Path, start)
		}
	}
	for _, f := range pick(func(b string) bool { return b == "__init__.py" }) {
		lines := strings.Split(f.Content, "\n")
		for k, ln := range lines {
			t := strings.TrimSpace(ln)
			if t == "" || strings.HasPrefix(t, "#") {
				continue
			}
			if d, at := docstringAt(lines, k); d != "" {
				return firstSentence(d), fmt.Sprintf("%s:%d", f.Path, at)
			}
			break
		}
	}
	for _, f := range pick(func(b string) bool { return b == "lib.rs" || b == "mod.rs" || b == "main.rs" }) {
		var block []string
		first := 0
		for _, m := range reRustInner.FindAllStringSubmatchIndex(f.Content, -1) {
			if first == 0 {
				first = lineOf(f.Content, m[0])
			}
			block = append(block, f.Content[m[2]:m[3]])
		}
		if d := firstSentence(strings.Join(block, " ")); d != "" {
			return d, fmt.Sprintf("%s:%d", f.Path, first)
		}
	}
	for _, f := range pick(func(b string) bool { return b == "package-info.java" }) {
		if m := reJavaPkgDoc.FindStringSubmatch(f.Content); m != nil {
			if d := firstSentence(cleanDoc(strings.Split(m[1], "\n"))); d != "" {
				return d, f.Path
			}
		}
	}
	for _, f := range pick(func(b string) bool { return b == "apps.py" }) {
		if m := reDjangoName.FindStringSubmatchIndex(f.Content); m != nil {
			return f.Content[m[2]:m[3]], fmt.Sprintf("%s:%d", f.Path, lineOf(f.Content, m[0]))
		}
	}
	for _, f := range pick(func(b string) bool {
		return strings.HasPrefix(b, "index.") && (strings.HasSuffix(b, ".ts") || strings.HasSuffix(b, ".js") || strings.HasSuffix(b, ".tsx"))
	}) {
		if m := reJSFileDoc.FindStringSubmatch(f.Content); m != nil {
			if t := reJSFileTag.FindStringSubmatch(m[1]); t != nil && strings.TrimSpace(t[1]) != "" {
				return firstSentence(cleanDoc([]string{t[1]})), f.Path + ":1"
			}
		}
	}
	return "", ""
}

// packageRoots are the directories holding a package manifest. Three or more
// make the repo a multi-package workspace (a ROS src/, a JS monorepo), whose
// natural modules are those packages, not every directory.
func packageRoots(files []File) []string {
	set := map[string]bool{}
	for _, f := range files {
		switch strings.ToLower(filepath.Base(f.Path)) {
		case "package.xml", "package.json", "cargo.toml", "pyproject.toml", "setup.py", "go.mod":
			if !IsTestPath(f.Path) {
				set[pkgFromPath(f.Path)] = true
			}
		}
	}
	return sortedKeys(set)
}

// moduleOf assigns a file to its module: the nearest package root above it
// in a workspace, else its own directory.
func moduleOf(path string, roots []string) string {
	d := pkgFromPath(path)
	if roots == nil {
		return d
	}
	best := ""
	found := false
	for _, r := range roots {
		if r == "" || d == r || strings.HasPrefix(d, r+"/") {
			if !found || len(r) > len(best) {
				best, found = r, true
			}
		}
	}
	if !found {
		return d
	}
	return best
}

func modules(files []File, syms map[string]SymbolRef, snap RefSnapshot, idx *spanIndex) []Module {
	roots := packageRoots(files)
	if len(roots) < 3 {
		roots = nil
	}
	byMod := map[string][]*File{}
	allTest := map[string]bool{}
	for i := range files {
		f := &files[i]
		m := moduleOf(f.Path, roots)
		if _, ok := byMod[m]; !ok {
			allTest[m] = true
		}
		byMod[m] = append(byMod[m], f)
		if !IsTestPath(f.Path) && isCodeLang(langOfPath(f.Path)) {
			allTest[m] = false
		}
	}
	// Symbols and fan-in per module, from qids via their defining file.
	modOfQID := func(q string) string {
		if loc, ok := idx.locate(q); ok {
			return moduleOf(loc.Path, roots)
		}
		return "\x00"
	}
	symCount := map[string]int{}
	qmod := map[string]string{}
	for q, sym := range syms {
		m := modOfQID(q)
		qmod[q] = m
		if !IsTestSymbol(sym, q) {
			symCount[m]++
		}
	}
	fanIn := map[string]int{}
	for caller, cs := range snap.Callees {
		cm, ok := qmod[caller]
		if !ok {
			continue
		}
		for _, c := range cs {
			if tm, ok := qmod[c.Name]; ok && tm != cm && tm != "\x00" {
				fanIn[tm]++
			}
		}
	}
	var out []Module
	for m, fs := range byMod {
		if allTest[m] || isAuxPath(m+"/") {
			continue
		}
		code := 0
		for _, f := range fs {
			if isCodeLang(langOfPath(f.Path)) {
				code++
			}
		}
		if code == 0 && symCount[m] == 0 {
			continue
		}
		var inDir []*File
		for _, f := range fs {
			if pkgFromPath(f.Path) == m {
				inDir = append(inDir, f)
			}
		}
		p, src := modulePurpose(m, inDir)
		if p == "" {
			p, src = inheritedPurpose(m, files, byMod)
		}
		name := m
		if name == "" {
			name = "."
		}
		out = append(out, Module{Path: name, Files: code, Symbols: symCount[m], FanIn: fanIn[m], Purpose: p, Source: src})
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		sa, sb := a.Symbols+2*a.FanIn, b.Symbols+2*b.FanIn
		if sa != sb {
			return sa > sb
		}
		return a.Path < b.Path
	})
	return out
}

// inheritedPurpose reads the purpose of the nearest ancestor directory that
// holds no code of its own (a package.json or README one level above src/),
// cited at its source. The repo root is not inherited: its README is the
// repo's summary, not a module's.
func inheritedPurpose(m string, files []File, byMod map[string][]*File) (string, string) {
	for d := pkgFromPath(m); d != "" && m != ""; d = pkgFromPath(d) {
		if fs, ok := byMod[d]; ok {
			for _, f := range fs {
				if isCodeLang(langOfPath(f.Path)) {
					return "", ""
				}
			}
		}
		var inDir []*File
		for i := range files {
			if pkgFromPath(files[i].Path) == d {
				inDir = append(inDir, &files[i])
			}
		}
		if p, src := modulePurpose(d, inDir); p != "" {
			return p, src
		}
	}
	return "", ""
}

// hubs ranks non-test symbols by distinct resolved non-test callers +
// in-repo callees.
func hubs(syms map[string]SymbolRef, snap RefSnapshot, idx *spanIndex) []Hub {
	return hubsIn(syms, syms, snap, idx)
}

// hubsIn ranks the symbols of scope (a directory's, or all) by their edges in
// the whole ref's graph; all classifies callers as test code or not.
func hubsIn(scope, all map[string]SymbolRef, snap RefSnapshot, idx *spanIndex) []Hub {
	syms := all
	in := map[string]map[string]bool{}
	out := map[string]int{}
	for caller, cs := range snap.Callees {
		seen := map[string]bool{}
		callerTest := IsTestSymbol(syms[caller], caller)
		for _, c := range cs {
			if _, known := syms[c.Name]; !known || c.Name == caller || seen[c.Name] {
				continue
			}
			seen[c.Name] = true
			if callerTest {
				continue // a hub is held together by production code; tests are counted by explain
			}
			if in[c.Name] == nil {
				in[c.Name] = map[string]bool{}
			}
			in[c.Name][caller] = true
		}
		out[caller] = len(seen)
	}
	var hs []Hub
	for q, sym := range scope {
		if IsTestSymbol(sym, q) {
			continue
		}
		h := Hub{Symbol: q, In: len(in[q]), Out: out[q]}
		if h.In+h.Out == 0 {
			continue
		}
		hs = append(hs, h)
	}
	sort.Slice(hs, func(i, j int) bool {
		a, b := hs[i], hs[j]
		if a.In+a.Out != b.In+b.Out {
			return a.In+a.Out > b.In+b.Out
		}
		if a.In != b.In {
			return a.In > b.In
		}
		return a.Symbol < b.Symbol
	})
	var kept []Hub
	for _, h := range hs {
		if len(kept) == 60 {
			break
		}
		if loc, ok := idx.locate(h.Symbol); ok {
			if isAuxPath(loc.Path) {
				continue
			}
			h.Path, h.Line = loc.Path, loc.Span.StartLine
		}
		kept = append(kept, h)
	}
	return kept
}

// FleetMap maps every indexed repo briefly plus the cross-repo links known.
func FleetMap(s Store, ref string, budget int) FleetMapResult {
	if budget <= 0 {
		budget = DefaultMapBudget
	}
	res := FleetMapResult{Omitted: map[string]int{}, Meta: NewMeta(s, FleetRepo, ref)}
	var all []RepoSummary
	declared := map[string][]Dependency{}
	for _, repo := range s.Repos() {
		if !refIndexed(s, repo, ref) {
			res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf("%s: not indexed at %s (indexed: %v)", repo, EffectiveRef(s, repo, ref), firstN(s.Refs(repo), 5)))
			continue
		}
		files := s.Files(repo, ref)
		syms := s.SymbolsAt(repo, ref)
		rs := RepoSummary{Repo: repo, Ref: EffectiveRef(s, repo, ref), Module: s.ModulePath(repo), Files: len(files), Symbols: len(syms), EntryPoints: map[string]int{}}
		if man, ok := s.Manifest(repo, ref); ok {
			rs.Commit = man.Commit
		}
		rs.Summary, _ = readmeSummary(files)
		for i, l := range languageStats(files, syms) {
			if i == 3 {
				break
			}
			rs.Languages = append(rs.Languages, fmt.Sprintf("%s:%d", l.Lang, l.Files))
		}
		for _, e := range EntryPoints(repo, files) {
			rs.EntryPoints[e.Kind]++
		}
		for _, e := range httpEntries(repo, files) {
			rs.Routes += e.Count
		}
		deps, _, _ := RepoDependencies(files)
		declared[repo] = deps
		all = append(all, rs)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Symbols != all[j].Symbols {
			return all[i].Symbols > all[j].Symbols
		}
		return all[i].Repo < all[j].Repo
	})
	links := fleetLinks(s, ref, all, declared, &res.Meta)

	spent := 30
	for i, r := range all {
		c := estTokens(r.Repo+r.Summary+r.Module+strings.Join(r.Languages, " ")) + 10 + 4*len(r.EntryPoints)
		if spent+c > budget && i > 0 {
			res.Omitted["repos"] = len(all) - i
			res.Truncated = true
			break
		}
		spent += c
		res.Repos = append(res.Repos, r)
	}
	for i, l := range links {
		c := estTokens(l.From+l.To+l.Kind+strings.Join(l.Examples, " ")+l.Handle) + 8
		if spent+c > budget {
			res.Omitted["links"] = len(links) - i
			res.Truncated = true
			break
		}
		spent += c
		res.Links = append(res.Links, l)
	}
	if res.Truncated {
		res.Next = append(res.Next, fmt.Sprintf("omitted for budget — rerun with --budget %d", budget*2))
	}
	if len(res.Repos) > 0 {
		res.Next = append(res.Next, "reponite map --repo "+res.Repos[0].Repo+"   (one repo in depth: entry points, modules, hubs, dependencies)")
	}
	res.Next = append(res.Next, "reponite routes | reponite topics | reponite ximpact <symbol>   (every instance of a link kind)")
	res.Next = withRef(res.Next, ref)
	return res
}

// fleetLinks aggregates the cross-repo links the index already knows.
func fleetLinks(s Store, ref string, repos []RepoSummary, declared map[string][]Dependency, meta *Meta) []FleetLink {
	type key struct{ from, to, kind string }
	agg := map[key]*FleetLink{}
	add := func(from, to, kind, example, handle string) {
		if from == to || from == "" || to == "" {
			return
		}
		k := key{from, to, kind}
		l := agg[k]
		if l == nil {
			l = &FleetLink{From: from, To: to, Kind: kind, Handle: handle}
			agg[k] = l
		}
		l.Count++
		if len(l.Examples) < 3 && example != "" && !containsStr(l.Examples, example) {
			l.Examples = append(l.Examples, example)
		}
	}
	if len(repos) > 1 {
		rr := RoutesWith(s, FleetRepo, ref, "", 1<<30, RoutesOptions{})
		for _, ln := range rr.Links {
			for _, c := range ln.Clients {
				add(c.Repo, ln.Route.Repo, "http", strings.TrimSpace(ln.Route.Method+" /"+strings.TrimPrefix(ln.Route.Pattern, "/")), "reponite routes")
			}
		}
		cg := CommGraph(s, FleetRepo, ref)
		for _, g := range cg.Groups {
			pairs := map[[2]string]bool{}
			for _, p := range g.Producers {
				for _, c := range g.Consumers {
					pairs[[2]string{p.Repo, c.Repo}] = true
				}
			}
			for pr := range pairs {
				add(pr[0], pr[1], "ros-"+g.Family, g.Name, "reponite topics")
			}
		}
	}
	modules := map[string]string{} // module path -> repo
	names := map[string]string{}   // package/repo name -> repo
	for _, r := range repos {
		if r.Module != "" {
			modules[r.Module] = r.Repo
			names[r.Module] = r.Repo
		}
		names[r.Repo] = r.Repo
	}
	if lister, ok := s.(ExternalRefLister); ok {
		for _, r := range repos {
			for _, h := range lister.ExternalRefsFrom(r.Repo, ref) {
				for mod, target := range modules {
					if ModuleMatches(mod, h.Module) {
						add(r.Repo, target, "import", h.Name, "reponite ximpact "+h.Name)
						break
					}
				}
			}
		}
	} else if len(repos) > 1 {
		meta.Warnings = append(meta.Warnings, "this store cannot list cross-repo references, so import links are not shown — `reponite ximpact <symbol>` answers per symbol")
	}
	for _, r := range repos {
		for _, d := range declared[r.Repo] {
			if target, ok := names[d.Name]; ok {
				add(r.Repo, target, "declared-dep", d.Name, "reponite map --repo "+r.Repo)
			}
		}
	}
	out := make([]FleetLink, 0, len(agg))
	for _, l := range agg {
		out = append(out, *l)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Count != b.Count {
			return a.Count > b.Count
		}
		if a.From != b.From {
			return a.From < b.From
		}
		if a.To != b.To {
			return a.To < b.To
		}
		return a.Kind < b.Kind
	})
	return out
}
