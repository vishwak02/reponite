// configflow.go follows one configuration key through the files it travels
// in: where the key is defined (a section of a YAML/JSON file, e.g.
// master.yaml's policy.accept_x), which templates read it (Jinja
// `{{ master.policy.accept_x }}`, `master['policy']`, Helm `.Values.a.b`) and
// which key they write it to in the rendered file, which code reads that
// rendered key (a string literal, a ROS param path, a Helm values path), and
// where the field it lands in is used next. Lexical and medium confidence:
// each hop is a name match, shown with its line, never a guess about
// semantics. Pure over Store.Files (ADR-018).
package query

import (
	"path"
	"regexp"
	"sort"
	"strings"
)

// ConfigFlowSite is one place on a key's path.
type ConfigFlowSite struct {
	Repo, Path string
	Line       int
	Text       string
	In         string `json:",omitempty"` // enclosing symbol, for code
	Via        string `json:",omitempty"` // the name matched to get here
	Kind       string `json:",omitempty"` // value | condition (templates); code | helm | launch | config (readers)
	Writes     string `json:",omitempty"` // templates: the rendered file and key the value is written to
	Copies     int    `json:",omitempty"` // templates: same template in other directories (one per version)
}

// ConfigFlow is a key's path from definition to use.
type ConfigFlow struct {
	Key       string           // the dotted path followed, with its root file stem first
	Defined   []ConfigFlowSite // sections defining the key (examples)
	NDefined  int
	Templates []ConfigFlowSite // template lines reading it
	Readers   []ConfigFlowSite // code/manifests reading what the templates write
	Uses      []ConfigFlowSite // where the field a reader fills is used
	Note      string
}

var (
	cfBracket  = regexp.MustCompile(`\[\s*['"]([\w-]+)['"]\s*\]`)
	cfGet      = regexp.MustCompile(`\.get\(\s*['"]([\w-]+)['"]`)
	cfJSONKey  = regexp.MustCompile(`["']([\w.-]+)["']\s*:`) // JSON, or a dict literal in {% set %}
	cfYAMLKey  = regexp.MustCompile(`^\s*-?\s*["']?([\w.-]+)["']?\s*:`)
	cfEnvKey   = regexp.MustCompile(`^\s*(?:export\s+)?([A-Z][A-Z0-9_]+)\s*=`)
	cfMember   = regexp.MustCompile(`(?:\.|->)\s*([a-z_][a-z0-9_]{3,})\b(\s*\()?`)
	cfJinjaTag = regexp.MustCompile(`\{[{%]`)
	// 'key': value — the value's start tells a group ({}) from a write
	cfJSONKeyValue = regexp.MustCompile(`["']([\w.-]+)["']\s*:\s*(\{\s*\}|[^,}]*)`)
	cfUpdateTarget = regexp.MustCompile(`\.([\w-]+)\.update\(`)
	cfBlockOpen    = regexp.MustCompile(`^\{%-?\s*(if|for)\b`)
	cfBlockEnd     = regexp.MustCompile(`^\{%-?\s*end(if|for)\b`)
	cfBlockElse    = regexp.MustCompile(`^\{%-?\s*(else|elif)\b`)
)

// keys too common to follow by name alone: a reader must also name the parent
var cfGenericKeys = map[string]bool{"enable": true, "enabled": true, "name": true, "type": true, "value": true,
	"id": true, "host": true, "port": true, "user": true, "password": true, "path": true, "url": true, "timeout": true,
	"version": true, "mode": true, "default": true, "max": true, "min": true, "size": true, "tag": true}

const cfMaxSites = 12

// FollowConfigKey follows key ("master.policy.accept_x", "policy.accept_x" or
// just "accept_x") across repo (FleetRepo = every repo) at ref.
func FollowConfigKey(s Store, repo, ref, key string) *ConfigFlow {
	parts := strings.FieldsFunc(key, func(r rune) bool { return r == '.' || r == ':' || r == '/' })
	if len(parts) == 0 {
		return nil
	}
	repos := reposFor(s, repo)
	files := map[string][]File{}
	for _, rp := range repos {
		files[rp] = s.Files(rp, ref)
	}

	// 1. Definitions: sections "<stem>:<path>" ending in the key's parts.
	suffix := ":" + strings.Join(parts, ":")
	type def struct {
		site ConfigFlowSite
		stem string
		path []string
	}
	var defs []def
	for _, rp := range repos {
		for _, f := range files[rp] {
			if isTemplatePath(f.Path) {
				continue
			}
			for _, sp := range f.Symbols {
				name := sp.Name
				if !strings.HasSuffix(":"+name, suffix) || !strings.Contains(name, ":") {
					continue
				}
				segs := strings.Split(name, ":")
				defs = append(defs, def{
					site: ConfigFlowSite{Repo: rp, Path: f.Path, Line: sp.StartLine, Text: strings.TrimSpace(lineAt(f.Content, sp.StartLine))},
					stem: segs[0], path: segs[1:],
				})
			}
		}
	}
	if len(parts) > 3 {
		// indexed YAML sections stop three keys down: read deeper keys
		// (workflow.print.at_picking.enable) from the files themselves
		last := parts[len(parts)-1] + ":"
		for _, rp := range repos {
			for _, f := range files[rp] {
				ext := path.Ext(f.Path)
				if ext != ".yaml" && ext != ".yml" || isTemplatePath(f.Path) || !strings.Contains(f.Content, last) {
					continue
				}
				stem := strings.TrimSuffix(path.Base(f.Path), ext)
				for _, k := range yamlKeyLines(f.Content) {
					if strings.HasSuffix(":"+strings.Join(k.path, ":"), suffix) {
						defs = append(defs, def{
							site: ConfigFlowSite{Repo: rp, Path: f.Path, Line: k.line, Text: strings.TrimSpace(lineAt(f.Content, k.line))},
							stem: stem, path: k.path,
						})
					}
				}
			}
		}
	}
	if len(defs) == 0 {
		return nil
	}
	// the most common (stem, path) is the key meant; others are listed apart
	count := map[string]int{}
	for _, d := range defs {
		count[d.stem+"."+strings.Join(d.path, ".")]++
	}
	best := ""
	for k, n := range count {
		if best == "" || n > count[best] || n == count[best] && k < best {
			best = k
		}
	}
	stem, kpath := strings.SplitN(best, ".", 2)[0], strings.Split(best, ".")[1:]
	flow := &ConfigFlow{Key: best}
	for _, d := range defs {
		if d.stem+"."+strings.Join(d.path, ".") == best {
			flow.NDefined++
			if len(flow.Defined) < cfMaxSites {
				flow.Defined = append(flow.Defined, d.site)
			}
		}
	}
	if len(count) > 1 {
		var other []string
		for k := range count {
			if k != best {
				other = append(other, k)
			}
		}
		sort.Strings(other)
		flow.Note = "also named " + strings.Join(firstN(other, 5), ", ") + " — follow one with its full path"
	}

	// 2. Templates reading <stem>.<path> (dotted, ['x'], .get('x')).
	want := stem + "." + strings.Join(kpath, ".")
	type tplUse struct {
		site      ConfigFlowSite
		outFile   string
		outKeys   []string
		outParent string
	}
	byShape := map[string]*tplUse{} // collapse one template copied per version directory
	var shapes []string
	for _, rp := range repos {
		for _, f := range files[rp] {
			if !isTemplatePath(f.Path) && !strings.Contains(f.Content, "{{") {
				continue
			}
			if !strings.Contains(f.Content, stem) {
				continue
			}
			lines := strings.Split(f.Content, "\n")
			for i, ln := range lines {
				if !cfJinjaTag.MatchString(ln) || !strings.Contains(ln, stem) {
					continue
				}
				norm := cfGet.ReplaceAllString(cfBracket.ReplaceAllString(ln, ".$1"), ".$1")
				at := indexPath(norm, want)
				if at < 0 {
					continue
				}
				u := tplUse{site: ConfigFlowSite{Repo: rp, Path: f.Path, Line: i + 1, Text: clipN(strings.TrimSpace(ln), 200), Via: want, Kind: "value"}}
				u.outFile = renderedName(f.Path)
				u.outKeys, u.outParent = emittedKeys(lines, i, at)
				if strings.Contains(ln, "{%") && !strings.Contains(ln, "{{") && !cfJSONKey.MatchString(ln[:at]) {
					u.site.Kind = "condition"
				}
				if len(u.outKeys) > 0 {
					u.site.Writes = path.Base(u.outFile) + " " + strings.Join(u.outKeys, ", ")
				}
				shape := rp + "\x00" + versionlessPath(f.Path) + "\x00" + strings.Join(u.outKeys, ",") + "\x00" + u.site.Kind
				if prev, ok := byShape[shape]; ok {
					prev.site.Copies++
					if VersionLess(prev.site.Path, f.Path) { // show the newest copy
						c := prev.site.Copies
						*prev = u
						prev.site.Copies = c
					}
					continue
				}
				uu := u
				byShape[shape] = &uu
				shapes = append(shapes, shape)
			}
		}
	}
	sort.Strings(shapes)
	emitted := map[string]string{} // rendered key -> parent key (for generic names)
	for _, sh := range shapes {
		u := byShape[sh]
		flow.Templates = append(flow.Templates, u.site)
		for _, k := range u.outKeys {
			emitted[k] = u.outParent
		}
	}
	if len(flow.Templates) > cfMaxSites {
		flow.Templates = flow.Templates[:cfMaxSites]
	}

	// 3. Readers of each rendered key — and of the key itself, for code that
	// reads the source file directly.
	readKeys := map[string]string{kpath[len(kpath)-1]: parentOf(kpath)}
	for k, p := range emitted {
		readKeys[k] = p
	}
	seenReader := map[string]bool{}
	var members []string
	for k, parent := range readKeys {
		for _, rp := range repos {
			for _, f := range files[rp] {
				if isTemplatePath(f.Path) || !strings.Contains(f.Content, k) {
					continue
				}
				kind := readerKind(f.Path)
				if kind == "" {
					continue
				}
				var x *spanIndex
				for i, ln := range strings.Split(f.Content, "\n") {
					if !readsKey(ln, k, kind) || cfGenericKeys[k] && parent != "" && !strings.Contains(ln, parent) {
						continue
					}
					id := rp + ":" + f.Path + ":" + itoa(i+1)
					if seenReader[id] {
						continue
					}
					seenReader[id] = true
					site := ConfigFlowSite{Repo: rp, Path: f.Path, Line: i + 1, Text: clipN(strings.TrimSpace(ln), 200), Via: k, Kind: kind}
					if kind == "code" {
						if x == nil {
							x = newSpanIndex([]File{f})
						}
						site.In, _ = x.qidAt(s, rp, ref, f.Path, i+1)
						for _, m := range cfMember.FindAllStringSubmatch(ln, -1) {
							if m[2] == "" && m[1] != k && !containsStr(members, m[1]) {
								members = append(members, m[1])
							}
						}
					}
					flow.Readers = append(flow.Readers, site)
				}
			}
		}
	}
	sort.SliceStable(flow.Readers, func(i, j int) bool {
		a, b := flow.Readers[i], flow.Readers[j]
		if IsTestPath(a.Path) != IsTestPath(b.Path) {
			return !IsTestPath(a.Path)
		}
		if (a.Kind == "code") != (b.Kind == "code") {
			return a.Kind == "code"
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		return a.Path < b.Path || a.Path == b.Path && a.Line < b.Line
	})
	if len(flow.Readers) > cfMaxSites {
		flow.Readers = flow.Readers[:cfMaxSites]
	}

	// 4. Where the field a code reader fills is used next.
	for _, m := range firstN(members, 3) {
		for _, rp := range repos {
			for _, f := range files[rp] {
				if readerKind(f.Path) != "code" || !strings.Contains(f.Content, m) {
					continue
				}
				var x *spanIndex
				for i, ln := range strings.Split(f.Content, "\n") {
					if seenReader[rp+":"+f.Path+":"+itoa(i+1)] || !usesMember(ln, m) {
						continue
					}
					if x == nil {
						x = newSpanIndex([]File{f})
					}
					in, _ := x.qidAt(s, rp, ref, f.Path, i+1)
					flow.Uses = append(flow.Uses, ConfigFlowSite{Repo: rp, Path: f.Path, Line: i + 1, Text: clipN(strings.TrimSpace(ln), 200), In: in, Via: "." + m, Kind: "code"})
				}
			}
		}
	}
	sort.SliceStable(flow.Uses, func(i, j int) bool {
		a, b := flow.Uses[i], flow.Uses[j]
		if IsTestPath(a.Path) != IsTestPath(b.Path) {
			return !IsTestPath(a.Path)
		}
		return a.Repo+a.Path < b.Repo+b.Path || a.Repo+a.Path == b.Repo+b.Path && a.Line < b.Line
	})
	if len(flow.Uses) > cfMaxSites {
		flow.Uses = flow.Uses[:cfMaxSites]
	}
	return flow
}

func isTemplatePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".j2", ".jinja", ".jinja2", ".tpl", ".tmpl", ".gotmpl":
		return true
	}
	return strings.Contains(p, "/templates/") && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml"))
}

// renderedName is the file a template renders to: x.json.j2 -> x.json.
func renderedName(p string) string {
	for _, ext := range []string{".j2", ".jinja2", ".jinja", ".tmpl", ".tpl", ".gotmpl"} {
		if strings.HasSuffix(strings.ToLower(p), ext) {
			return p[:len(p)-len(ext)]
		}
	}
	return p
}

// versionlessPath drops a version-named directory, so one template copied
// per release collapses to one entry.
func versionlessPath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		if s != "" && (s[0] >= '0' && s[0] <= '9' || s[0] == 'v' && len(s) > 1 && s[1] >= '0' && s[1] <= '9') && strings.Contains(s, ".") {
			segs[i] = "*"
		}
	}
	return strings.Join(segs, "/")
}

// indexPath finds want (a dotted path) in ln as a whole path or a prefix of a
// deeper one: "master.policy.x" but not "master.policy.xy".
func indexPath(ln, want string) int {
	from := 0
	for {
		i := strings.Index(ln[from:], want)
		if i < 0 {
			return -1
		}
		i += from
		end := i + len(want)
		before := i == 0 || !isIdentByte(ln[i-1]) && ln[i-1] != '.'
		after := end == len(ln) || !isIdentByte(ln[end])
		if before && after {
			return i
		}
		from = end
	}
}

// emittedKeys are the keys the template writes the value under: the key
// before it on the line (JSON, a {% set %} dict literal, YAML, or an env
// name); for a {% if %} line, every key written in the block it guards up to
// its {% else %}/{% endif %}. A key whose value is {} only creates a group and
// is skipped. parent is the key one level up (by indentation, or the group a
// dict update targets), to qualify generic names.
func emittedKeys(lines []string, i, at int) (keys []string, parent string) {
	ln := lines[i]
	before := ln[:at]
	if ms := cfJSONKey.FindAllStringSubmatch(before, -1); len(ms) > 0 {
		keys = []string{ms[len(ms)-1][1]}
	} else if m := cfYAMLKey.FindStringSubmatch(before); m != nil {
		keys = []string{m[1]}
	} else if m := cfEnvKey.FindStringSubmatch(before); m != nil {
		keys = []string{m[1]}
	} else if strings.Contains(ln, "{%") {
		return blockKeys(lines, i)
	}
	if len(keys) == 0 {
		return nil, ""
	}
	if m := cfUpdateTarget.FindStringSubmatch(ln); m != nil {
		return keys, m[1]
	}
	ind := indentOf(lines[i])
	for j := i - 1; j >= 0 && j >= i-200; j-- {
		t := lines[j]
		if strings.TrimSpace(t) == "" || strings.HasPrefix(strings.TrimSpace(t), "{%") {
			continue
		}
		if indentOf(t) < ind {
			if m := cfJSONKey.FindStringSubmatch(t); m != nil {
				return keys, m[1]
			}
			if m := cfYAMLKey.FindStringSubmatch(t); m != nil {
				return keys, m[1]
			}
			return keys, ""
		}
	}
	return keys, ""
}

// blockKeys collects the keys written inside the {% if %} block opening at
// line i, stopping at its own {% else %}, {% elif %} or {% endif %}.
func blockKeys(lines []string, i int) (keys []string, parent string) {
	depth := 0
	seen := map[string]bool{}
	for j := i + 1; j < len(lines) && j <= i+40; j++ {
		t := strings.TrimSpace(lines[j])
		switch {
		case cfBlockOpen.MatchString(t):
			depth++
		case cfBlockEnd.MatchString(t):
			if depth == 0 {
				return keys, parent
			}
			depth--
			continue
		case cfBlockElse.MatchString(t) && depth == 0:
			return keys, parent
		}
		for _, m := range cfJSONKeyValue.FindAllStringSubmatch(t, -1) {
			if strings.HasPrefix(strings.TrimSpace(m[2]), "{}") {
				continue // creates a group, writes nothing
			}
			if !seen[m[1]] {
				seen[m[1]] = true
				keys = append(keys, m[1])
				if u := cfUpdateTarget.FindStringSubmatch(t); u != nil && parent == "" {
					parent = u[1]
				}
			}
		}
		if !strings.HasPrefix(t, "{%") {
			if m := cfYAMLKey.FindStringSubmatch(t); m != nil && !seen[m[1]] {
				seen[m[1]] = true
				keys = append(keys, m[1])
			}
		}
	}
	return keys, parent
}

func parentOf(p []string) string {
	if len(p) < 2 {
		return ""
	}
	return p[len(p)-2]
}

// readerKind classifies a file that may read a rendered key; "" skips it.
func readerKind(p string) string {
	switch l := langOfPath(p); {
	case isCodeLang(l):
		return "code"
	case strings.HasSuffix(p, ".launch") || strings.HasSuffix(p, ".launch.xml"):
		return "launch"
	case strings.Contains(p, "/templates/") && (strings.HasSuffix(p, ".yaml") || strings.HasSuffix(p, ".yml") || strings.HasSuffix(p, ".tpl")):
		return "helm"
	}
	return ""
}

// readsKey: ln names k as a quoted string, the end of a param path
// ("/ns/k"), an attribute value (name="k") or a Helm values path (.k).
func readsKey(ln, k, kind string) bool {
	for _, q := range []string{`"` + k + `"`, `'` + k + `'`, "/" + k + `"`, "/" + k + `'`, "`" + k + "`"} {
		if strings.Contains(ln, q) {
			return true
		}
	}
	if kind == "helm" && strings.Contains(ln, ".Values.") {
		i := strings.Index(ln, "."+k)
		return i >= 0 && (i+1+len(k) == len(ln) || !isIdentByte(ln[i+1+len(k)]))
	}
	return false
}

// usesMember: ln reads or writes the field m (x.m / x->m), not a call of m().
func usesMember(ln, m string) bool {
	for _, x := range cfMember.FindAllStringSubmatch(ln, -1) {
		if x[1] == m && x[2] == "" {
			return true
		}
	}
	return false
}

// yamlKeyLine is a mapping key of a YAML file, with every key above it.
type yamlKeyLine struct {
	path []string
	line int
}

// yamlKeyLines lists a YAML file's mapping keys at any depth, by indentation
// (comments, list items and block scalars read as the indexer reads them).
func yamlKeyLines(content string) []yamlKeyLine {
	type open struct {
		indent int
		name   string
	}
	var stack []open
	var out []yamlKeyLine
	for i, ln := range strings.Split(content, "\n") {
		m := cfYAMLKey.FindStringSubmatch(ln)
		if m == nil || strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		indent := len(ln) - len(strings.TrimLeft(ln, " "))
		if strings.HasPrefix(strings.TrimLeft(ln, " "), "- ") {
			indent += 2 // a key opening a list item belongs inside it
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, open{indent, m[1]})
		p := make([]string, len(stack))
		for k, o := range stack {
			p[k] = o.name
		}
		out = append(out, yamlKeyLine{path: p, line: i + 1})
	}
	return out
}
