// links.go is the reference graph between files of any kind: the edges no
// call graph has because they are written as data, not calls. One mechanism
// serves every format without a parser per format:
//
//   - path: a string that resolves to another tracked file — relative to the
//     file, to the repo root, to a ROS package ($(find pkg)/…, package://pkg/…),
//     to an Ansible role's templates/ or files/, or by a unique suffix — so an
//     include_tasks, a {% include %}, a Dockerfile COPY, a CMake
//     add_subdirectory, a CI step running a script or a launch <include> all
//     become "A refers to B";
//   - id: a large number or UUID defined as an "id" in one structured file and
//     used in another (ALICA plans referring to behaviours, generated configs);
//   - template: a Helm/Go-template `define "x"` and every `include "x"` /
//     `template "x"`;
//   - names-symbol: a value of a name/type/class/plugin/implementationName key
//     in a configuration file that is exactly the name of a code symbol in the
//     same repo (a pluginlib class, an ALICA behaviour's implementation).
//
// Pure over Store.Files, cached per content (factcache.go).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// Link is one reference from a place in a file to a file or a symbol.
type Link struct {
	Repo     string `json:"repo"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Kind     string `json:"kind"` // path | id | template | names-symbol
	To       string `json:"to"`   // a path in Repo, or for names-symbol a symbol id
	ToLine   int    `json:"to_line,omitempty"`
	Text     string `json:"text"`
	Resolved string `json:"resolved,omitempty"` // how a path was resolved: relative, root, package, role, suffix, basename
}

var (
	linkIDDef   = regexp.MustCompile(`"id"\s*:\s*"?([0-9]{7,}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})"?`)
	linkIDUse   = regexp.MustCompile(`\b([0-9]{7,}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})\b`)
	linkDefine  = regexp.MustCompile(`\{\{-?\s*define\s+"([^"]+)"`)
	linkInclude = regexp.MustCompile(`\{\{-?.*?\b(?:include|template)\s+"([^"]+)"`)
	linkNameKey = regexp.MustCompile(`(?i)["']?\b(name|type|class|plugin|implementationName|implementation|handler|behaviour|behavior|controller|node_type)\b["']?\s*[:=]\s*["']?([A-Za-z_][A-Za-z0-9_:./]*)["']?`)
	linkPkgName = regexp.MustCompile(`<name>\s*([^<\s]+)\s*</name>`)
	// an Ansible role named in a play's roles: list or an include_role/import_role
	linkRoleUse = regexp.MustCompile(`^\s*(?:-\s+(?:role:\s*)?|role:\s*|name:\s*)["']?([A-Za-z0-9_.-]+)["']?\s*$`)
)

const maxLinksPerFile = 200

// Links is the reference graph of repo at ref (every repo for FleetRepo).
func Links(s Store, repo, ref string) []Link {
	return cachedFact(s, repo, ref, "links", "", func() []Link {
		var out []Link
		for _, rp := range reposFor(s, repo) {
			out = append(out, repoLinks(s, rp, ref)...)
		}
		return out
	})
}

// isConfigLike reports a file whose values name things (not a programming language).
func isConfigLike(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".json", ".yaml", ".yml", ".xml", ".launch", ".toml", ".ini", ".cfg", ".conf", ".beh", ".pml", ".pty",
		".tmx", ".bpmn", ".j2", ".jinja", ".jinja2", ".tpl", ".tmpl", ".sls", ".properties", ".env", ".rviz":
		return true
	}
	return false
}

func repoLinks(s Store, repo, ref string) []Link {
	files := s.Files(repo, ref)
	paths := make(map[string]bool, len(files))
	byBase := map[string][]string{}
	pkgDir := map[string]string{} // ROS package name -> its directory
	roles := map[string]string{}  // Ansible role name -> its tasks/main
	for _, f := range files {
		paths[f.Path] = true
		if i := strings.LastIndex(f.Path, "roles/"); i >= 0 {
			rest := strings.Split(f.Path[i+len("roles/"):], "/")
			if len(rest) == 3 && rest[1] == "tasks" && (rest[2] == "main.yml" || rest[2] == "main.yaml") {
				roles[rest[0]] = f.Path
			}
		}
		byBase[path.Base(f.Path)] = append(byBase[path.Base(f.Path)], f.Path)
		if path.Base(f.Path) == "package.xml" {
			if m := linkPkgName.FindStringSubmatch(f.Content); m != nil {
				pkgDir[m[1]] = path.Dir(f.Path)
			}
		}
	}
	// ids defined in structured files, template defines, code symbol names
	type def struct {
		path string
		line int
	}
	ids, defines := map[string]def{}, map[string]def{}
	for _, f := range files {
		if !isConfigLike(f.Path) && !strings.Contains(f.Content, "define \"") {
			continue
		}
		for i, ln := range strings.Split(f.Content, "\n") {
			if m := linkIDDef.FindStringSubmatch(ln); m != nil {
				if _, ok := ids[m[1]]; !ok {
					ids[m[1]] = def{f.Path, i + 1}
				}
			}
			if m := linkDefine.FindStringSubmatch(ln); m != nil {
				defines[m[1]] = def{f.Path, i + 1}
			}
		}
	}
	symByName := map[string]string{} // bare symbol name -> qid, only when unique
	ambiguous := map[string]bool{}
	for qid := range s.SymbolsAt(repo, ref) {
		b := baseName(qid)
		if len(b) < 4 {
			continue
		}
		if _, ok := symByName[b]; ok {
			ambiguous[b] = true
		}
		symByName[b] = qid
	}

	resolve := func(from, tok, pkg string) (string, string) {
		tok = strings.TrimSuffix(tok, "/")
		if tok == "" || strings.Contains(tok, "://") || strings.HasPrefix(tok, "//") {
			return "", ""
		}
		if pkg != "" {
			if d, ok := pkgDir[pkg]; ok {
				if p := path.Clean(path.Join(d, tok)); paths[p] {
					return p, "package"
				}
			}
			return "", ""
		}
		if strings.HasPrefix(tok, "/") {
			if p := path.Clean(strings.TrimPrefix(tok, "/")); paths[p] {
				return p, "root"
			}
			return "", ""
		}
		if p := path.Clean(path.Join(path.Dir(from), tok)); paths[p] && p != from {
			return p, "relative"
		}
		if p := path.Clean(tok); paths[p] && p != from {
			return p, "root"
		}
		// an Ansible role's tasks name templates and files by base name
		if i := strings.LastIndex(from, "/tasks/"); i >= 0 {
			for _, sub := range []string{"/templates/", "/files/", "/vars/", "/tasks/"} {
				if p := path.Clean(from[:i] + sub + tok); paths[p] {
					return p, "role"
				}
			}
		}
		if strings.Contains(tok, "/") {
			// files ending in /tok all share tok's base name: look only at those
			var hits []string
			for _, p := range byBase[path.Base(tok)] {
				if strings.HasSuffix(p, "/"+tok) {
					hits = append(hits, p)
					if len(hits) > 1 {
						break
					}
				}
			}
			if len(hits) == 1 && hits[0] != from {
				return hits[0], "suffix"
			}
			return "", ""
		}
		// a bare file name, unique in the repo, with an extension
		if strings.Contains(tok, ".") && !strings.HasPrefix(tok, ".") {
			if hs := byBase[tok]; len(hs) == 1 && hs[0] != from {
				return hs[0], "basename"
			}
		}
		return "", ""
	}

	var out []Link
	for _, f := range files {
		cfg := isConfigLike(f.Path)
		n := 0
		for i, ln := range strings.Split(f.Content, "\n") {
			if n >= maxLinksPerFile {
				break
			}
			if len(ln) > 2000 {
				continue
			}
			text := strings.TrimSpace(ln)
			add := func(kind, to string, toLine int, how string) {
				out = append(out, Link{Repo: repo, Path: f.Path, Line: i + 1, Kind: kind, To: to, ToLine: toLine,
					Text: clipN(text, 200), Resolved: how})
				n++
			}
			seen := map[string]bool{}
			for _, t := range pathTokens(ln) {
				if to, how := resolve(f.Path, t.tok, t.pkg); to != "" && !seen[to] {
					seen[to] = true
					add("path", to, 0, how)
				}
			}
			if len(roles) > 0 && (strings.HasSuffix(f.Path, ".yml") || strings.HasSuffix(f.Path, ".yaml")) {
				if m := linkRoleUse.FindStringSubmatch(ln); m != nil {
					if to, ok := roles[m[1]]; ok && to != f.Path && !seen[to] {
						seen[to] = true
						add("path", to, 0, "ansible-role")
					}
				}
			}
			if cfg {
				for _, m := range linkIDUse.FindAllStringSubmatch(ln, -1) {
					if d, ok := ids[m[1]]; ok && !(d.path == f.Path && d.line == i+1) && !seen["id:"+m[1]] {
						seen["id:"+m[1]] = true
						add("id", d.path, d.line, "")
					}
				}
				for _, m := range linkNameKey.FindAllStringSubmatch(ln, -1) {
					v := m[2]
					if j := strings.LastIndexAny(v, ":/."); j >= 0 && j < len(v)-1 {
						v = v[j+1:] // pkg/Class, ns::Class -> Class
					}
					if q, ok := symByName[v]; ok && !ambiguous[v] && !seen["sym:"+q] {
						seen["sym:"+q] = true
						add("names-symbol", q, 0, "")
					}
				}
			}
			if strings.Contains(ln, "{{") {
				for _, m := range linkInclude.FindAllStringSubmatch(ln, -1) {
					if d, ok := defines[m[1]]; ok && !seen["tpl:"+m[1]] {
						seen["tpl:"+m[1]] = true
						add("template", d.path, d.line, "")
					}
				}
			}
		}
	}
	out = append(out, adapterLinks(s, repo, ref, files)...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

// LinksResult is the references from and to one file (or naming one symbol).
type LinksResult struct {
	Target   string `json:"target"`
	RefersTo []Link `json:"refers_to"`
	// ReferredBy are the places that refer to the target.
	ReferredBy []Link `json:"referred_by"`
	Note       string `json:"note"`
	Meta       Meta   `json:"_meta"`
}

// LinksFor answers "what does this file refer to, and what refers to it?";
// target may also be a symbol name, for the configuration files naming it.
func LinksFor(s Store, repo, ref, target string) LinksResult {
	res := LinksResult{Target: target, RefersTo: []Link{}, ReferredBy: []Link{}, Meta: NewMeta(s, repo, ref)}
	t := strings.TrimPrefix(target, "./")
	for _, l := range Links(s, repo, ref) {
		if l.Path == t || strings.HasSuffix(l.Path, "/"+t) {
			res.RefersTo = append(res.RefersTo, l)
		}
		if l.To == t || strings.HasSuffix(l.To, "/"+t) || l.Kind == "names-symbol" && (l.To == t || baseName(l.To) == t) {
			res.ReferredBy = append(res.ReferredBy, l)
		}
	}
	res.Note = "references written as data, not calls: path (a string resolving to a tracked file — relative, " +
		"repo root, ROS package, Ansible role, unique suffix or unique file name), id (a large number or UUID " +
		"defined as \"id\" in another file), template (Helm/Go-template include of a define), names-symbol " +
		"(a configuration value naming a code symbol of the repo). Within one repo; resolved statically."
	return res
}

var (
	linkValues   = regexp.MustCompile(`\.Values\.([A-Za-z0-9_]+(?:\.[A-Za-z0-9_]+)*)`)
	linkRPC      = regexp.MustCompile(`^\s*rpc\s+([A-Za-z_]\w*)\s*\(`)
	linkOpenAPI  = regexp.MustCompile(`^\s{0,4}["']?(/[^"':\s]*)["']?\s*:\s*(?:\{|$)`)
	linkSpecMark = regexp.MustCompile(`(?m)^\s*["']?(openapi|swagger)["']?\s*:`)
)

// adapterLinks are the format-specific references on top of the generic ones:
// a Helm template's .Values.a.b to the values.yaml key; a protobuf rpc to the
// code that implements it; an OpenAPI path to the route that serves it.
func adapterLinks(s Store, repo, ref string, files []File) []Link {
	var out []Link
	// Helm: values.yaml keys by chart directory, from the stored sections
	valueLines := map[string]map[string]int{} // chart dir -> "a:b" -> line
	for _, f := range files {
		if path.Base(f.Path) != "values.yaml" && path.Base(f.Path) != "values.yml" {
			continue
		}
		keys := map[string]int{}
		for _, sp := range f.Symbols {
			if i := strings.IndexByte(sp.Name, ':'); i >= 0 {
				keys[sp.Name[i+1:]] = sp.StartLine
			}
		}
		valueLines[path.Dir(f.Path)] = keys
	}
	type implSym struct {
		qid  string
		test bool
	}
	var impl map[string][]implSym
	implNames := func() map[string][]implSym {
		if impl == nil {
			impl = map[string][]implSym{}
			for qid, sr := range s.SymbolsAt(repo, ref) {
				impl[baseName(qid)] = append(impl[baseName(qid)], implSym{qid, sr.IsTest})
			}
		}
		return impl
	}
	var routes []Route
	routesLoaded := false
	for _, f := range files {
		lines := strings.Split(f.Content, "\n")
		if i := strings.Index(f.Path, "/templates/"); i >= 0 && strings.Contains(f.Content, ".Values.") {
			if keys, ok := valueLines[f.Path[:i]]; ok {
				for n, ln := range lines {
					for _, m := range linkValues.FindAllStringSubmatch(ln, -1) {
						parts := strings.Split(m[1], ".")
						for k := len(parts); k > 0; k-- { // the deepest key values.yaml defines
							if line, ok := keys[strings.Join(parts[:k], ":")]; ok {
								out = append(out, Link{Repo: repo, Path: f.Path, Line: n + 1, Kind: "values",
									To: f.Path[:i] + "/values.yaml", ToLine: line, Text: clipN(strings.TrimSpace(ln), 200)})
								break
							}
						}
					}
				}
			}
		}
		if strings.HasSuffix(f.Path, ".proto") {
			for n, ln := range lines {
				if m := linkRPC.FindStringSubmatch(ln); m != nil {
					for _, sym := range implNames()[m[1]] {
						if sym.test || IsGeneratedPath(sym.qid) {
							continue
						}
						out = append(out, Link{Repo: repo, Path: f.Path, Line: n + 1, Kind: "rpc-impl", To: sym.qid,
							Text: clipN(strings.TrimSpace(ln), 200)})
					}
				}
			}
		}
		ext := strings.ToLower(path.Ext(f.Path))
		if (ext == ".yaml" || ext == ".yml" || ext == ".json") && linkSpecMark.MatchString(f.Content) {
			if !routesLoaded {
				routes = RoutesWith(s, repo, ref, "", 1<<30, RoutesOptions{}).allRoutes()
				routesLoaded = true
			}
			for n, ln := range lines {
				m := linkOpenAPI.FindStringSubmatch(ln)
				if m == nil || len(m[1]) < 2 {
					continue
				}
				spec := segments(m[1])
				for _, r := range routes {
					if sameRoute(spec, segments(r.Pattern)) {
						out = append(out, Link{Repo: repo, Path: f.Path, Line: n + 1, Kind: "openapi-route",
							To: r.Path, ToLine: r.Line, Text: clipN(strings.TrimSpace(ln), 200)})
					}
				}
			}
		}
	}
	return out
}

// allRoutes are the routes of a result, linked or not.
func (r RoutesResult) allRoutes() []Route {
	out := make([]Route, 0, len(r.Links))
	for _, l := range r.Links {
		out = append(out, l.Route)
	}
	return out
}

// sameRoute reports two paths of equal length whose segments agree, a
// placeholder matching anything.
func sameRoute(a, b []string) bool {
	if len(a) != len(b) || len(a) == 0 {
		return false
	}
	for i := range a {
		if a[i] != b[i] && !wild(a[i]) && !wild(b[i]) {
			return false
		}
	}
	return true
}

type pathTok struct{ tok, pkg string }

func pathChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '@' || c == '+' || c == '.' || c == '-' || c == '/'
}

// pathTokens are the path-shaped words of a line — containing a '/' or an
// extension — with the ROS package a $(find pkg)/ or package://pkg/ prefix
// names. A hand scan: a regular expression over every line of a large repo
// was most of the reference graph's cost.
func pathTokens(ln string) []pathTok {
	if !strings.ContainsAny(ln, "./") {
		return nil
	}
	var out []pathTok
	for i := 0; i < len(ln); {
		if !pathChar(ln[i]) {
			i++
			continue
		}
		j := i
		for j < len(ln) && pathChar(ln[j]) {
			j++
		}
		tok, pkg := ln[i:j], ""
		// $(find pkg)/rest and package://pkg/rest
		if i >= 1 && ln[i-1] == ')' {
			if k := strings.LastIndex(ln[:i-1], "$(find "); k >= 0 && !strings.ContainsAny(ln[k+7:i-1], " )") {
				pkg = ln[k+7 : i-1]
			}
		}
		if strings.HasPrefix(tok, "//") && i >= len("package:") && ln[i-len("package:"):i] == "package:" {
			rest := tok[2:]
			if k := strings.IndexByte(rest, '/'); k > 0 {
				pkg, tok = rest[:k], rest[k+1:]
			}
		}
		i = j
		if len(tok) < 3 || len(tok) > 300 {
			continue
		}
		slash := strings.IndexByte(tok, '/') >= 0
		dot := strings.LastIndexByte(tok, '.')
		ext := dot > 0 && dot < len(tok)-1 && !strings.ContainsAny(tok[dot+1:], "/") &&
			(tok[dot+1] < '0' || tok[dot+1] > '9')
		if !slash && !ext {
			continue
		}
		out = append(out, pathTok{strings.TrimRight(tok, "."), pkg})
	}
	return out
}
