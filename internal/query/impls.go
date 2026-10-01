// impls.go answers "what actually runs when code calls this interface?" across
// repo boundaries. A platform defines a base class (a plugin interface, a
// strategy) and calls it through a base pointer; the classes that implement it
// often live in another repo that is loaded at runtime (pluginlib), so no
// in-repo call graph reaches them. Impls scans every C++ class head at the ref
// fleet-wide (`class X : public ns::Base<T>`), walks the derivation tree from
// the named base, and, for a method, returns each class's own definition of
// it. PLUGINLIB_EXPORT_CLASS registrations are reported so a runtime plugin
// is told apart from a plain subclass. Pure over Store.Files and SymbolsAt,
// like grep and the ROS comms graph (ADR-018).
package query

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Impl is one class deriving (transitively) from the base.
type Impl struct {
	Repo, Path string
	Line       int
	Class      string
	Bases      []string // its direct bases, as written (namespace stripped)
	Depth      int      // 1 = derives directly from the base
	Plugin     bool     // registered with PLUGINLIB_EXPORT_CLASS
	Test       bool     // test code or a mock/fake/stub — listed after production classes
	// Method: the class's own definition of the asked method (its symbol id
	// in Repo), "" when it inherits it.
	Method string
}

// ImplsResult is a base class's implementations fleet-wide.
type ImplsResult struct {
	Base, Method string
	BaseDefs     []string // where the base itself defines the method (symbol ids, repo-qualified)
	Impls        []Impl
	Note         string
	Meta         Meta
}

var (
	classHead = regexp.MustCompile(`\b(?:class|struct)\s+(?:[A-Z][A-Z0-9_]*\s+)?([A-Za-z_]\w*)\s*(?:final\s*)?:\s*([^{;()]+)\{`)
	pluginReg = regexp.MustCompile(`PLUGINLIB_EXPORT_CLASS\s*\(\s*([\w:]+)\s*,\s*([\w:]+)\s*\)`)
	cppExts   = map[string]bool{".h": true, ".hh": true, ".hpp": true, ".hxx": true, ".cc": true, ".cpp": true, ".cxx": true}
)

// classGraph is the fleet's C++ class heads at a ref.
type classGraph struct {
	derived map[string][]classDecl // base name -> classes naming it directly
	byName  map[string][]classDecl // class name -> its declarations (with bases)
	plugins map[string]bool        // classes registered as plugins
	syms    map[string]map[string]SymbolRef
}

func scanClasses(s Store, repos []string, ref string) *classGraph {
	g := &classGraph{derived: map[string][]classDecl{}, byName: map[string][]classDecl{}, plugins: map[string]bool{},
		syms: map[string]map[string]SymbolRef{}}
	for _, rp := range repos {
		for _, f := range s.Files(rp, ref) {
			if !cppExts[strings.ToLower(filepath.Ext(f.Path))] || !strings.Contains(f.Content, ":") {
				continue
			}
			src := stripCppComments(f.Content)
			for _, m := range classHead.FindAllStringSubmatchIndex(src, -1) {
				d := classDecl{repo: rp, path: f.Path, line: 1 + strings.Count(src[:m[0]], "\n"), name: src[m[2]:m[3]]}
				d.bases = splitBases(src[m[4]:m[5]])
				g.byName[d.name] = append(g.byName[d.name], d)
				for _, b := range d.bases {
					g.derived[b] = append(g.derived[b], d)
				}
			}
			for _, m := range pluginReg.FindAllStringSubmatch(src, -1) {
				g.plugins[lastSeg(m[1])] = true
			}
		}
	}
	return g
}

// descendants are the classes deriving (transitively) from base.
func (g *classGraph) descendants(base string) []classDecl {
	var out []classDecl
	seen := map[string]bool{base: true}
	queue := []string{base}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range g.derived[cur] {
			key := d.repo + "\x00" + d.name
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, d)
			if !seen[d.name] {
				seen[d.name] = true
				queue = append(queue, d.name)
			}
		}
	}
	return out
}

// ancestors are every base class of cls, transitively, nearest first.
func (g *classGraph) ancestors(cls string) []string {
	var out []string
	seen := map[string]bool{cls: true}
	queue := []string{cls}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range g.byName[cur] {
			for _, b := range d.bases {
				if !seen[b] {
					seen[b] = true
					out = append(out, b)
					queue = append(queue, b)
				}
			}
		}
	}
	return out
}

// defOf is class's own definition of method in repo (a symbol id), or "".
func (g *classGraph) defOf(s Store, ref, repo, class, method string) string {
	m, ok := g.syms[repo]
	if !ok {
		m = s.SymbolsAt(repo, ref)
		g.syms[repo] = m
	}
	suffix := "." + class + "." + method
	var hits []string
	for id := range m {
		if strings.HasSuffix(id, suffix) || id == class+"."+method {
			hits = append(hits, id)
		}
	}
	sort.Strings(hits)
	if len(hits) == 0 {
		return ""
	}
	return hits[0]
}

type classDecl struct {
	repo, path string
	line       int
	name       string
	bases      []string
}

// Impls lists the classes deriving from base ("Base", "ns::Base", or with a
// method "Base.method" / "Base::method") fleet-wide at ref; limit 0 = 50.
func Impls(s Store, repo, ref, target string, limit int) ImplsResult {
	if limit <= 0 {
		limit = 50
	}
	base, method := splitTarget(target)
	res := ImplsResult{Base: base, Method: method, Meta: Meta{Repo: repo, Ref: ref}}
	if base == "" {
		res.Note = "no class named"
		return res
	}
	repos := reposFor(s, repo)
	g := scanClasses(s, repos, ref)
	derived, plugins := g.derived, g.plugins
	defOf := func(rp, class string) string {
		if method == "" {
			return ""
		}
		return g.defOf(s, ref, rp, class, method)
	}
	if method != "" {
		for _, rp := range repos {
			if d := defOf(rp, base); d != "" {
				res.BaseDefs = append(res.BaseDefs, rp+":"+d)
			}
		}
	}
	seen := map[string]bool{base: true}
	type item struct {
		name  string
		depth int
	}
	queue := []item{{base, 0}}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, d := range derived[cur.name] {
			key := d.repo + "\x00" + d.name
			if seen[key] {
				continue
			}
			seen[key] = true
			res.Impls = append(res.Impls, Impl{Repo: d.repo, Path: d.path, Line: d.line, Class: d.name, Bases: d.bases,
				Depth: cur.depth + 1, Plugin: plugins[d.name], Method: defOf(d.repo, d.name), Test: testCode(d.path, d.name)})
			if !seen[d.name] {
				seen[d.name] = true
				queue = append(queue, item{d.name, cur.depth + 1})
			}
		}
	}
	sort.SliceStable(res.Impls, func(i, j int) bool {
		a, b := res.Impls[i], res.Impls[j]
		if a.Test != b.Test {
			return b.Test
		}
		if (a.Method != "") != (b.Method != "") {
			return a.Method != ""
		}
		if a.Depth != b.Depth {
			return a.Depth < b.Depth
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path
	})
	total := len(res.Impls)
	if total > limit {
		res.Impls = res.Impls[:limit]
	}
	switch {
	case total == 0:
		res.Note = "no class at this ref derives from " + base + " — check the name (the class, not a typedef), or the repo that implements it may not be indexed at the deployed ref"
	default:
		res.Note = "classes matched by base name from their declarations (namespaces stripped, so two bases sharing a name in different namespaces merge); plugin = registered with PLUGINLIB_EXPORT_CLASS, which one a deployment loads is chosen by its config"
		if method != "" {
			res.Note += "; method = the class's own definition, empty when it inherits " + method
		}
		if total > limit {
			res.Note += " — more classes than shown"
		}
	}
	return res
}

// testCode: a test directory or file, or a mock/fake/stub class.
func testCode(path, class string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(path), "/") {
		if strings.Contains(strings.ToLower(seg), "test") {
			return true
		}
	}
	for _, p := range []string{"Mock", "Fake", "Stub"} {
		if strings.HasPrefix(class, p) {
			return true
		}
	}
	return false
}

func splitTarget(t string) (base, method string) {
	t = strings.TrimSpace(t)
	if i := strings.LastIndex(t, "."); i > 0 {
		return lastSeg(t[:i]), t[i+1:]
	}
	if i := strings.LastIndex(t, "::"); i > 0 {
		// ns::Base stays a class; Base::method needs a lowercase-initial member
		// to be read as a method (C++ classes are conventionally capitalized).
		if m := t[i+2:]; m != "" && strings.ToLower(m[:1]) == m[:1] {
			return lastSeg(t[:i]), m
		}
	}
	return lastSeg(t), ""
}

func lastSeg(s string) string {
	if i := strings.LastIndex(s, "::"); i >= 0 {
		return s[i+2:]
	}
	return s
}

// splitBases reads "public ns::A<T, U>, protected virtual B" -> [A, B].
func splitBases(list string) []string {
	var out []string
	depth, start := 0, 0
	flush := func(part string) {
		part = strings.TrimSpace(part)
		if i := strings.Index(part, "<"); i >= 0 {
			part = part[:i]
		}
		fields := strings.Fields(part)
		if len(fields) == 0 {
			return
		}
		name := lastSeg(fields[len(fields)-1])
		if name != "" && name != "public" && name != "private" && name != "protected" && name != "virtual" {
			out = append(out, name)
		}
	}
	for i, c := range list {
		switch c {
		case '<':
			depth++
		case '>':
			depth--
		case ',':
			if depth == 0 {
				flush(list[start:i])
				start = i + 1
			}
		}
	}
	flush(list[start:])
	return out
}

// stripCppComments blanks // and /* */ comments, keeping newlines so line
// numbers stay true.
func stripCppComments(src string) string {
	b := []byte(src)
	for i := 0; i < len(b); i++ {
		switch {
		case b[i] == '"':
			for i++; i < len(b) && b[i] != '"' && b[i] != '\n'; i++ {
				if b[i] == '\\' {
					i++
				}
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '/':
			for ; i < len(b) && b[i] != '\n'; i++ {
				b[i] = ' '
			}
		case b[i] == '/' && i+1 < len(b) && b[i+1] == '*':
			for ; i < len(b) && !(b[i] == '*' && i+1 < len(b) && b[i+1] == '/'); i++ {
				if b[i] != '\n' {
					b[i] = ' '
				}
			}
			if i+1 < len(b) {
				b[i], b[i+1] = ' ', ' '
				i++
			}
		}
	}
	return string(b)
}
