// testmap.go answers "which tests exercise this, and how do I run just
// those?". Tests reach a symbol two ways: through the call graph (a test, or
// a test helper, calls something that calls it — walked up to N hops) and
// lexically (a test file names it: an import, a mock target, a call the graph
// could not resolve, a JS/TS test block whose callbacks carry no symbol).
// Each test is attributed to its framework, detected from the file itself
// (imports, macros, base classes, launch files) — never from the symbol name
// — and the answer carries the exact command that runs only those tests.
// Pure over the Store (ADR-018).
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// DefaultTestsBudget is the token budget when the caller passes <= 0.
const DefaultTestsBudget = 1500

// DefaultTestDepth is how many caller hops a test may be from the symbol.
const DefaultTestDepth = 3

// TestHit is one test that exercises the symbol.
type TestHit struct {
	Repo, Path string
	Line       int
	Name       string   // runnable name: test function, Class.method, Suite.Name, or the it()/test() title
	Handle     string   // qualified id when the test is a symbol
	Framework  string   // pytest | unittest | django | go | jest | vitest | mocha | gtest | rostest | robot | cargo | junit | unknown
	Depth      int      // call hops from the test to the symbol (1 = calls it directly)
	Via        []string // the call chain below the test, nearest first (Depth > 1)
	How        string   // call_graph | reference (named in the test file)
	Command    string   // runs just this test
}

// TestRun is one command running every listed test of a framework.
type TestRun struct {
	Repo      string
	Framework string
	Tests     int
	Command   string
}

// TestsResult is the answer.
type TestsResult struct {
	Symbol    string
	Repo, Ref string
	Tests     []TestHit
	Commands  []TestRun
	Total     int
	Truncated bool
	Next      string
	Note      string
	Meta      Meta
}

var (
	reGTest     = regexp.MustCompile(`^\s*(?:TEST|TEST_F|TEST_P|TYPED_TEST)\s*\(\s*(\w+)\s*,\s*(\w+)\s*\)`)
	reJSBlock   = regexp.MustCompile(`^\s*(?:it|test|specify)(?:\.\w+)?\s*\(\s*(["'` + "`" + `])(.*?)["'` + "`" + `]`)
	rePyClass   = regexp.MustCompile(`^(\s*)class\s+(\w+)`)
	reJavaClass = regexp.MustCompile(`\bclass\s+(\w+)`)
	reLaunchTst = regexp.MustCompile(`<test\b[^>]*\btype\s*=\s*"([^"]+)"`)
)

// testFramework detects a test file's framework from its own content and the
// repo's other files (conftest.py, manage.py, rostest launch files).
func testFramework(f File, repoFiles map[string]File, launchTypes map[string]string) string {
	base := filepath.Base(f.Path)
	if _, ok := launchTypes[base]; ok {
		return "rostest"
	}
	if e := strings.ToLower(filepath.Ext(base)); e == ".robot" || e == ".resource" {
		return "robot"
	}
	c := f.Content
	switch errLang(f.Path) {
	case "py":
		if strings.Contains(c, "import pytest") || strings.Contains(c, "from pytest") || hasConftest(f.Path, repoFiles) {
			return "pytest"
		}
		if strings.Contains(c, "rostest") {
			return "rostest"
		}
		if strings.Contains(c, "django.test") || strings.Contains(c, "rest_framework.test") {
			return "django"
		}
		if strings.Contains(c, "unittest") {
			return "unittest"
		}
		return "pytest"
	case "go":
		return "go"
	case "js":
		switch {
		case strings.Contains(c, "from 'vitest'") || strings.Contains(c, `from "vitest"`):
			return "vitest"
		case strings.Contains(c, "@playwright/test"):
			return "playwright"
		case strings.Contains(c, "from 'chai'") || strings.Contains(c, `from "chai"`) || strings.Contains(c, "require('chai')"):
			return "mocha"
		}
		return "jest"
	case "cpp":
		if strings.Contains(c, "gtest/gtest.h") || reGTest.MatchString(c) || strings.Contains(c, "TEST(") || strings.Contains(c, "TEST_F(") {
			return "gtest"
		}
		if strings.Contains(c, "catch2") || strings.Contains(c, "TEST_CASE(") {
			return "catch2"
		}
	case "rust":
		return "cargo"
	case "java":
		return "junit"
	}
	return "unknown"
}

func hasConftest(path string, files map[string]File) bool {
	dir := filepath.ToSlash(filepath.Dir(path))
	for {
		p := "conftest.py"
		if dir != "." && dir != "" {
			p = dir + "/conftest.py"
		}
		if _, ok := files[p]; ok {
			return true
		}
		if dir == "." || dir == "" || dir == "/" {
			return false
		}
		dir = filepath.ToSlash(filepath.Dir(dir))
	}
}

// Tests finds the tests exercising symbol at repo@ref, up to depth caller
// hops away (<= 0 = DefaultTestDepth).
func Tests(s Store, repo, ref, symbol string, depth, budget int) TestsResult {
	if depth <= 0 {
		depth = DefaultTestDepth
	}
	if budget <= 0 {
		budget = DefaultTestsBudget
	}
	res := TestsResult{Symbol: symbol, Repo: repo, Ref: EffectiveRef(s, repo, ref), Meta: NewMeta(s, repo, ref)}
	names := ResolveSymbol(s, repo, ref, symbol)
	if len(names) == 0 {
		res.Note = fmt.Sprintf("symbol %q not found at %s@%s", symbol, repo, res.Ref)
		return res
	}
	qid := names[0]
	res.Symbol = qid
	if len(names) > 1 {
		res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf("ambiguous %q; using %s (also: %s)", symbol, qid, strings.Join(names[1:], ", ")))
	}
	base := baseName(qid)

	files := s.Files(repo, ref)
	byPath := map[string]File{}
	launchTypes := map[string]string{} // test script basename -> launch file
	for _, f := range files {
		byPath[f.Path] = f
		if strings.HasSuffix(f.Path, ".test") || strings.HasSuffix(f.Path, ".launch") || strings.HasSuffix(f.Path, ".launch.xml") {
			for _, m := range reLaunchTst.FindAllStringSubmatch(f.Content, -1) {
				launchTypes[m[1]] = f.Path
			}
		}
	}

	// 1. Call graph: reverse BFS from the symbol, recording each test reached.
	snap := s.Snapshot(repo, ref)
	rev := map[string][]string{}
	for caller, cs := range snap.Callees {
		for _, c := range cs {
			rev[c.Name] = append(rev[c.Name], caller)
		}
	}
	// HTTP hop: a test that calls an endpoint (client.get("/api/orders/"))
	// exercises the view serving it — and, for a class-based view, each of
	// its methods. The call graph cannot see this edge; routes can.
	httpEdge := map[[2]string]bool{}
	syms := s.SymbolsAt(repo, ref)
	symIdx := baseIndexOf(syms)
	for _, l := range RoutesWith(s, repo, ref, "", 100000, RoutesOptions{}).Links {
		testClient := false
		for _, c := range l.Clients {
			if c.Repo == repo && IsTestPath(c.Path) {
				testClient = true
				break
			}
		}
		if !testClient {
			continue // only a test's request makes the route an edge here
		}
		var targets []string
		for _, t := range resolveIn(syms, l.Route.Handler) {
			targets = append(targets, t)
			for k := range syms {
				if strings.HasPrefix(k, t+".") {
					targets = append(targets, k)
				}
			}
		}
		for _, c := range l.Clients {
			if c.Repo != repo || !IsTestPath(c.Path) {
				continue
			}
			caller := siteHandleIn(symIdx, c.Path, c.In)
			if caller == "" {
				continue
			}
			for _, t := range targets {
				if !httpEdge[[2]string{caller, t}] {
					httpEdge[[2]string{caller, t}] = true
					rev[t] = append(rev[t], caller)
				}
			}
		}
	}
	for k := range rev {
		sort.Strings(rev[k])
	}
	type node struct {
		qid  string
		via  []string
		http bool // the chain crosses an HTTP request
	}
	seen := map[string]bool{qid: true}
	frontier := []node{{qid: qid}}
	hits := map[string]*TestHit{} // path:line -> hit
	order := []string{}
	addHit := func(h TestHit) {
		k := fmt.Sprintf("%s:%d", h.Path, h.Line)
		if old, ok := hits[k]; ok {
			if h.Depth < old.Depth || howRank(h.How) > howRank(old.How) {
				*old = h
			}
			return
		}
		hits[k] = &h
		order = append(order, k)
	}
	for d := 1; d <= depth && len(frontier) > 0; d++ {
		var next []node
		for _, n := range frontier {
			for _, caller := range rev[n.qid] {
				if seen[caller] {
					continue
				}
				seen[caller] = true
				via := append([]string{n.qid}, n.via...)
				viaHTTP := n.http || httpEdge[[2]string{caller, n.qid}]
				if IsTestQID(s, repo, ref, caller) {
					path, span, ok := locateSymbol(files, caller)
					if ok && isRobotPath(path) && !robotTestCase(byPath[path], span.StartLine) {
						ok = false // a keyword in a suite file is a helper the walk passes through, not a test
					}
					if ok {
						f := byPath[path]
						h := TestHit{Repo: repo, Path: path, Line: span.StartLine, Name: testName(f, caller, span.StartLine),
							Handle: caller, Depth: d, How: "call_graph"}
						if viaHTTP {
							h.How = "http"
						}
						if d > 1 {
							h.Via = via[:len(via)-1]
						}
						h.Framework = testFramework(f, byPath, launchTypes)
						addHit(h)
					}
				}
				next = append(next, node{qid: caller, via: via, http: viaHTTP})
			}
		}
		frontier = next
	}

	// 2. Lexical: test files that name the symbol. A test block found this way
	// may be a mock target or an unresolved call; it is labeled as such.
	nameRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(base) + `\b`)
	// A method's bare name (validate, save) is everywhere; a test file must
	// also name its class to count.
	owner := ""
	if rest := strings.TrimSuffix(qid, "."+base); rest != qid && !strings.HasSuffix(rest, "/"+baseName(rest)) {
		if o := baseName(rest); o != "" && o[0] >= 'A' && o[0] <= 'Z' {
			owner = o
		}
	}
	for _, f := range files {
		if !IsTestPath(f.Path) || errLang(f.Path) == "" || !strings.Contains(f.Content, base) {
			continue
		}
		if owner != "" && !strings.Contains(f.Content, owner) {
			continue
		}
		lines := strings.Split(f.Content, "\n")
		for i, l := range lines {
			if !nameRe.MatchString(l) || isImportLine(l) {
				continue
			}
			name, line, handle := enclosingTest(f, lines, i)
			if name == "" {
				continue
			}
			addHit(TestHit{Repo: repo, Path: f.Path, Line: line, Name: name, Handle: handle, Depth: 1, How: "reference",
				Framework: testFramework(f, byPath, launchTypes)})
		}
	}

	all := make([]TestHit, 0, len(order))
	for _, k := range order {
		h := *hits[k]
		h.Command = testCommand(h, byPath, launchTypes, []TestHit{h})
		all = append(all, h)
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.How != b.How {
			return howRank(a.How) > howRank(b.How)
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	res.Total = len(all)

	spent := 40
	for _, h := range all {
		cost := 14 + estTokens(h.Path) + estTokens(h.Name) + estTokens(h.Command) + estTokens(strings.Join(h.Via, ","))
		if spent+cost > budget*3/4 && len(res.Tests) > 0 {
			res.Truncated = true
			break
		}
		spent += cost
		res.Tests = append(res.Tests, h)
	}
	// One command per framework running every test found (not only the shown).
	group := map[string][]TestHit{}
	var fws []string
	for _, h := range all {
		if _, ok := group[h.Framework]; !ok {
			fws = append(fws, h.Framework)
		}
		group[h.Framework] = append(group[h.Framework], h)
	}
	sort.Strings(fws)
	for _, fw := range fws {
		hs := group[fw]
		res.Commands = append(res.Commands, TestRun{Repo: repo, Framework: fw, Tests: len(hs), Command: testCommand(hs[0], byPath, launchTypes, hs)})
	}
	if res.Truncated {
		res.Next = fmt.Sprintf("%d of %d tests shown (commands cover all); re-run with --budget %d, or --depth 1 for direct tests only", len(res.Tests), res.Total, budget*2)
	}
	note := fmt.Sprintf("tests reaching %s within %d call hops (call_graph; http = through a request the test sends to the route serving it) or naming it in a test file (reference: an import-free mention — a call the graph missed, a mock target, a JS/TS test block); framework detected per file", base, depth)
	if res.Total == 0 {
		note = "no test reaches " + base + " within " + fmt.Sprint(depth) + " call hops and no test file names it — untested, or exercised only end-to-end/through dynamic dispatch; try --depth 5"
	}
	res.Note = note
	return res
}

func howRank(h string) int {
	switch h {
	case "call_graph":
		return 2
	case "http":
		return 1
	}
	return 0
}

func isImportLine(l string) bool {
	t := strings.TrimSpace(l)
	return strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "from ") && strings.Contains(t, " import ") ||
		strings.HasPrefix(t, "#include") || strings.HasPrefix(t, "use ") || strings.HasPrefix(t, "} from ")
}

// enclosingTest names the runnable test around line i of a test file: the
// enclosing test symbol, a gtest TEST(Suite, Name), or a JS it()/test() title.
func enclosingTest(f File, lines []string, i int) (name string, line int, handle string) {
	switch errLang(f.Path) {
	case "js":
		for j := i; j >= 0; j-- {
			if m := reJSBlock.FindStringSubmatch(lines[j]); m != nil {
				return m[2], j + 1, ""
			}
		}
		return "", 0, ""
	case "cpp":
		for j := i; j >= 0; j-- {
			if m := reGTest.FindStringSubmatch(lines[j]); m != nil {
				return m[1] + "." + m[2], j + 1, ""
			}
		}
		return "", 0, ""
	}
	// Symbol-backed languages: the innermost enclosing span that is a test.
	best := SymbolSpan{}
	for _, sp := range f.Symbols {
		if i+1 >= sp.StartLine && i+1 <= sp.EndLine && (best.Name == "" || sp.EndLine-sp.StartLine < best.EndLine-best.StartLine) {
			best = sp
		}
	}
	if best.Name == "" {
		return "", 0, ""
	}
	qid := pkgFromPath(f.Path) + "." + best.Name
	if pkgFromPath(f.Path) == "" {
		qid = best.Name
	}
	return testName(f, qid, best.StartLine), best.StartLine, qid
}

// testName is a test's runnable name: Class.method for a method in a test
// class (Python/Java), Suite.Name for gtest, else the function name.
func testName(f File, qid string, line int) string {
	lines := strings.Split(f.Content, "\n")
	name := baseName(qid)
	if line < 1 || line > len(lines) {
		return name
	}
	if isRobotPath(f.Path) {
		// Robot runs a test by its written name; the stored one is snake form.
		if cells := strings.SplitN(strings.TrimSpace(lines[line-1]), "  ", 2); cells[0] != "" {
			return strings.TrimSpace(cells[0])
		}
		return name
	}
	switch errLang(f.Path) {
	case "cpp":
		for j := line - 1; j >= 0 && j >= line-3; j-- {
			if m := reGTest.FindStringSubmatch(lines[j]); m != nil {
				return m[1] + "." + m[2]
			}
		}
	case "py", "java":
		// The class whose body indents this def.
		def := lines[line-1]
		indent := len(def) - len(strings.TrimLeft(def, " \t"))
		if indent == 0 {
			return name
		}
		for j := line - 2; j >= 0; j-- {
			if m := rePyClass.FindStringSubmatch(lines[j]); m != nil && len(m[1]) < indent {
				return m[2] + "." + name
			}
			if m := reJavaClass.FindStringSubmatch(lines[j]); m != nil && errLang(f.Path) == "java" {
				return m[1] + "." + name
			}
		}
	}
	return name
}

func isRobotPath(p string) bool {
	e := strings.ToLower(filepath.Ext(p))
	return e == ".robot" || e == ".resource"
}

// robotTestCase reports whether line (1-based) of a Robot file lies in a
// *** Test Cases *** or *** Tasks *** section.
func robotTestCase(f File, line int) bool {
	lines := strings.Split(f.Content, "\n")
	for j := line - 1; j >= 0 && j < len(lines); j-- {
		t := strings.ToLower(strings.TrimSpace(lines[j]))
		if strings.HasPrefix(t, "*") {
			t = strings.Trim(t, "* ")
			return strings.HasPrefix(t, "test case") || strings.HasPrefix(t, "task")
		}
	}
	return false
}

func shellQuote(s string) string {
	if regexp.MustCompile(`^[\w./:=@,+-]+$`).MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func pyModule(path string) string {
	return strings.ReplaceAll(strings.TrimSuffix(path, ".py"), "/", ".")
}

// rosPackage guesses the ROS package of a test file: the directory above
// its test/ directory.
func rosPackage(path string) string {
	segs := strings.Split(filepath.ToSlash(path), "/")
	for i := len(segs) - 2; i >= 1; i-- {
		if segs[i] == "test" || segs[i] == "tests" {
			return segs[i-1]
		}
	}
	if len(segs) > 1 {
		return segs[0]
	}
	return "<package>"
}

// testCommand runs exactly hs (all of one framework) — or one test.
func testCommand(h TestHit, files map[string]File, launchTypes map[string]string, hs []TestHit) string {
	uniqPaths := func() []string {
		var ps []string
		seen := map[string]bool{}
		for _, x := range hs {
			if !seen[x.Path] {
				seen[x.Path] = true
				ps = append(ps, x.Path)
			}
		}
		return ps
	}
	names := func(sep string, conv func(TestHit) string) string {
		var out []string
		seen := map[string]bool{}
		for _, x := range hs {
			n := conv(x)
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		return strings.Join(out, sep)
	}
	switch h.Framework {
	case "pytest":
		return "pytest " + names(" ", func(x TestHit) string { return shellQuote(x.Path + "::" + strings.ReplaceAll(x.Name, ".", "::")) })
	case "unittest":
		return "python -m unittest " + names(" ", func(x TestHit) string { return pyModule(x.Path) + "." + x.Name })
	case "django":
		root := ""
		for p := range files {
			if filepath.Base(p) == "manage.py" && (root == "" || len(p) < len(root)) {
				root = filepath.ToSlash(filepath.Dir(p))
			}
		}
		prefix := "python manage.py test "
		if root != "" && root != "." {
			prefix = "cd " + root + " && " + prefix
		}
		return prefix + names(" ", func(x TestHit) string {
			p := x.Path
			if root != "" && root != "." {
				p = strings.TrimPrefix(p, root+"/")
			}
			return pyModule(p) + "." + x.Name
		})
	case "go":
		var cmds []string
		for _, p := range uniqPaths() {
			var ns []string
			for _, x := range hs {
				if x.Path == p || filepath.Dir(x.Path) == filepath.Dir(p) {
					ns = append(ns, regexp.QuoteMeta(x.Name))
				}
			}
			cmds = append(cmds, fmt.Sprintf("go test ./%s -run %s", filepath.ToSlash(filepath.Dir(p)), shellQuote("^("+strings.Join(dedupe(ns), "|")+")$")))
		}
		return strings.Join(dedupe(cmds), " && ")
	case "jest", "vitest", "mocha", "playwright":
		titles := names("|", func(x TestHit) string { return regexp.QuoteMeta(x.Name) })
		paths := strings.Join(quoteAll(uniqPaths()), " ")
		switch h.Framework {
		case "vitest":
			return "npx vitest run " + paths + " -t " + shellQuote(titles)
		case "mocha":
			return "npx mocha " + paths + " --grep " + shellQuote(titles)
		case "playwright":
			return "npx playwright test " + paths + " -g " + shellQuote(titles)
		}
		return "npx jest " + paths + " -t " + shellQuote(titles)
	case "gtest":
		return "<gtest binary built from " + strings.Join(uniqPaths(), ", ") + "> --gtest_filter=" + shellQuote(names(":", func(x TestHit) string { return x.Name }))
	case "catch2":
		return "<test binary built from " + strings.Join(uniqPaths(), ", ") + "> " + shellQuote(names(",", func(x TestHit) string { return x.Name }))
	case "rostest":
		var cmds []string
		for _, p := range uniqPaths() {
			if lf, ok := launchTypes[filepath.Base(p)]; ok {
				cmds = append(cmds, "rostest "+rosPackage(lf)+" "+filepath.Base(lf))
			} else {
				cmds = append(cmds, "rostest "+rosPackage(p)+" <launch .test for "+filepath.Base(p)+">")
			}
		}
		return strings.Join(dedupe(cmds), " && ")
	case "robot":
		var ts []string
		for _, x := range hs {
			ts = append(ts, "--test "+shellQuote(x.Name))
		}
		return "robot " + strings.Join(dedupe(ts), " ") + " " + strings.Join(quoteAll(uniqPaths()), " ")
	case "cargo":
		return "cargo test " + shellQuote(names(" ", func(x TestHit) string { return x.Name }))
	case "junit":
		return "mvn test -Dtest=" + shellQuote(names(",", func(x TestHit) string {
			if i := strings.LastIndex(x.Name, "."); i >= 0 {
				return x.Name[:i] + "#" + x.Name[i+1:]
			}
			return x.Name
		}))
	}
	return "unrecognized framework: run " + strings.Join(uniqPaths(), " ") + " with the project's test runner"
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = shellQuote(x)
	}
	return out
}

func dedupe(xs []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, x := range xs {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}
