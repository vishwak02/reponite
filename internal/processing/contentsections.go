// contentsections.go gives content-only files a structure: the sections a
// person would name. A Markdown or reStructuredText heading, a YAML or JSON key
// (and a list entry named by its `name`/`id` field), a TOML or INI table, a
// Dockerfile build stage, a Makefile target, an rviz display. Each section is
// a symbol of kind "section" with its line range, so grep reports which
// section a hit landed in, `search` finds a key or a heading by name,
// `explain` shows it, and `compat`/`diff` say whether it changed between two
// refs — the same three-hash identity code symbols get. Sections are never
// part of a public API (IsExportedName), so ci-check does not gate on them.
// Pure and stdlib-only (ADR-018).
package processing

import (
	"path"
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// maxSections caps the sections taken from one file; a generated table with
// thousands of entries keeps its first ones and stays searchable as content.
const maxSections = 2000

// section is one named region of a file, lines 1-based and inclusive.
type section struct {
	path  []string // key path / heading path, outermost first
	start int
	end   int
}

// sectionSymbols turns a file's sections into symbols and spans. The symbol
// name is the file's stem and the section path joined with ':' — no '.',
// which the symbol id uses as its own separator.
func sectionSymbols(p, content string) ([]Symbol, []query.SymbolSpan) {
	return sectionSymbolsAs(p, content, "")
}

// sectionSymbolsAs is sectionSymbols for a file whose structure was
// recognized from its content (lang "json", "xml", "csv") when its name says
// nothing.
func sectionSymbolsAs(p, content, lang string) ([]Symbol, []query.SymbolSpan) {
	secs := fileSections(p, content)
	if secs == nil {
		switch lang {
		case "json":
			secs = jsonSections(content)
		case "xml":
			secs = xmlSections(content)
		case "csv":
			secs = csvSections(content)
		}
	}
	if len(secs) == 0 {
		return nil, nil
	}
	lines := strings.Split(content, "\n")
	stem := path.Base(p)
	if i := strings.LastIndex(stem, "."); i > 0 {
		stem = stem[:i]
	}
	stem = cleanSectionPart(stem)
	seen := map[string]int{}
	var syms []Symbol
	var spans []query.SymbolSpan
	for _, s := range secs {
		if len(syms) >= maxSections {
			break
		}
		parts := make([]string, 0, len(s.path)+1)
		parts = append(parts, stem)
		for _, x := range s.path {
			if c := cleanSectionPart(x); c != "" {
				parts = append(parts, c)
			}
		}
		if len(parts) == 1 {
			continue
		}
		name := strings.Join(parts, ":")
		if n := seen[name]; n > 0 {
			seen[name] = n + 1
			name += "~" + itoaSmall(n+1) // a repeated heading stays distinct
		} else {
			seen[name] = 1
		}
		if s.end < s.start {
			s.end = s.start
		}
		if s.end > len(lines) {
			s.end = len(lines)
		}
		body := strings.Join(lines[s.start-1:s.end], "\n")
		syms = append(syms, Symbol{Name: name, Kind: "section", Signature: strings.Join(s.path, " > "),
			CanonBody: []byte(strings.TrimSpace(body))})
		spans = append(spans, query.SymbolSpan{Name: name, StartLine: s.start, EndLine: s.end})
	}
	return syms, spans
}

func itoaSmall(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return itoaSmall(n/10) + string(rune('0'+n%10))
}

// cleanSectionPart keeps a section name readable and safe inside a symbol id.
func cleanSectionPart(s string) string {
	s = strings.TrimSpace(strings.Trim(strings.TrimSpace(s), `"'`))
	s = strings.NewReplacer(".", "_", ":", "_", "\t", " ", "\n", " ").Replace(s)
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// fileSections dispatches on the file kind.
func fileSections(p, content string) []section {
	base := strings.ToLower(path.Base(p))
	ext := strings.ToLower(path.Ext(base))
	switch {
	case ext == ".md" || ext == ".markdown":
		return markdownSections(content)
	case ext == ".rst":
		return rstSections(content)
	case ext == ".yaml" || ext == ".yml" || ext == ".sls":
		return yamlSections(content)
	case ext == ".json":
		return jsonSections(content)
	case ext == ".toml" || ext == ".ini" || ext == ".cfg" || ext == ".conf":
		return tableSections(content)
	case ext == ".rviz":
		return yamlSections(content)
	case ext == ".html" || ext == ".htm":
		return htmlSections(content)
	case ext == ".xml" || ext == ".tmx" || ext == ".tsx" && strings.Contains(content, "<tileset") || ext == ".bpmn" ||
		ext == ".svg" || ext == ".xacro" || ext == ".urdf" || ext == ".sdf":
		return xmlSections(content)
	case ext == ".just" || base == "justfile":
		return makeSections(content)
	case ext == ".csv" || ext == ".tsv":
		return csvSections(content)
	case base == "dockerfile" || base == "containerfile" || strings.HasPrefix(base, "dockerfile.") || ext == ".dockerfile":
		return dockerSections(content)
	case base == "makefile" || base == "gnumakefile" || ext == ".mk":
		return makeSections(content)
	}
	return nil
}

// closeAt sets each open section's end to the line before the next section
// at the same or a shallower level, and the last ones to the file end.
func closeSections(secs []section, depthOf func(section) int, total int) []section {
	for i := range secs {
		secs[i].end = total
		for j := i + 1; j < len(secs); j++ {
			if depthOf(secs[j]) <= depthOf(secs[i]) {
				secs[i].end = secs[j].start - 1
				break
			}
		}
	}
	return secs
}

var (
	htmlHeading = regexp.MustCompile(`(?i)<h([1-4])\b[^>]*>(.*?)</h[1-4]>`)
	htmlTag     = regexp.MustCompile(`<[^>]*>`)
)

// htmlSections takes <h1>…<h4> headings written on one line as sections.
func htmlSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	var stack []string
	for i, ln := range lines {
		m := htmlHeading.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		lvl := int(m[1][0] - '0')
		title := strings.TrimSpace(htmlTag.ReplaceAllString(m[2], ""))
		if title == "" {
			continue
		}
		if len(stack) >= lvl {
			stack = stack[:lvl-1]
		}
		for len(stack) < lvl-1 {
			stack = append(stack, "")
		}
		stack = append(stack, title)
		secs = append(secs, section{path: nonEmpty(stack), start: i + 1})
	}
	return closeSections(secs, func(s section) int { return len(s.path) }, len(lines))
}

var (
	xmlTag     = regexp.MustCompile(`<([A-Za-z_][\w:.-]*)(\s[^<>]*?)/?>`)
	xmlNameAtt = regexp.MustCompile(`\sname\s*=\s*"([^"]+)"`)
	xmlIDAtt   = regexp.MustCompile(`\sid\s*=\s*"([^"]+)"`)
)

// xmlSections takes every element named by a name (else id) attribute as a
// section "<tag>:<name>" — a Tiled map's layers and objects, a BPMN process's
// tasks, a URDF's links and joints, an SVG's groups.
func xmlSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	for i, ln := range lines {
		for _, m := range xmlTag.FindAllStringSubmatch(ln, -1) {
			val := ""
			if n := xmlNameAtt.FindStringSubmatch(m[2]); n != nil {
				val = n[1]
			} else if n := xmlIDAtt.FindStringSubmatch(m[2]); n != nil {
				val = n[1]
			}
			if val == "" {
				continue
			}
			tag := m[1]
			if j := strings.LastIndexByte(tag, ':'); j >= 0 {
				tag = tag[j+1:] // bpmn:task -> task
			}
			secs = append(secs, section{path: []string{tag, val}, start: i + 1, end: i + 1})
			if len(secs) >= maxSections {
				return secs
			}
		}
	}
	return secs
}

// csvSections takes a table's header cells as its sections ("columns:<name>").
func csvSections(content string) []section {
	head := content
	if i := strings.IndexByte(head, '\n'); i >= 0 {
		head = head[:i]
	}
	sep := ","
	if strings.Count(head, "\t") > strings.Count(head, ",") {
		sep = "\t"
	}
	var secs []section
	for _, c := range strings.Split(strings.TrimSpace(head), sep) {
		if c = strings.Trim(strings.TrimSpace(c), `"`); c != "" {
			secs = append(secs, section{path: []string{"columns", c}, start: 1, end: 1})
		}
	}
	return secs
}

var mdHeading = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*#*\s*$`)

func markdownSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	var stack []string
	fence := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") {
			fence = !fence
			continue
		}
		if fence {
			continue
		}
		m := mdHeading.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		lvl := len(m[1])
		if len(stack) >= lvl {
			stack = stack[:lvl-1]
		}
		for len(stack) < lvl-1 {
			stack = append(stack, "")
		}
		stack = append(stack, stripMarkdown(m[2]))
		secs = append(secs, section{path: nonEmpty(stack), start: i + 1})
	}
	return closeSections(secs, func(s section) int { return len(s.path) }, len(lines))
}

var mdLink = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)

func stripMarkdown(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return strings.NewReplacer("`", "", "**", "", "__", "", "*", "").Replace(s)
}

func nonEmpty(xs []string) []string {
	out := make([]string, 0, len(xs))
	for _, x := range xs {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

// rstAdornment returns the adornment character of an underline line ("====",
// "----", …), or 0 when the line is not one.
func rstAdornment(ln string) byte {
	t := strings.TrimRight(ln, " \t\r")
	if len(t) < 3 || !strings.ContainsRune("=-~^\"'+#*`:.", rune(t[0])) {
		return 0
	}
	for i := 1; i < len(t); i++ {
		if t[i] != t[0] {
			return 0
		}
	}
	return t[0]
}

func rstSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	var order []byte // underline characters in the order they first appear = levels
	var stack []string
	for i := 1; i < len(lines); i++ {
		title := strings.TrimSpace(lines[i-1])
		c := rstAdornment(lines[i])
		if c == 0 || title == "" || rstAdornment(lines[i-1]) != 0 || len(strings.TrimSpace(lines[i])) < len(title) {
			continue
		}
		lvl := strings.IndexByte(string(order), c)
		if lvl < 0 {
			order = append(order, c)
			lvl = len(order) - 1
		}
		if len(stack) > lvl {
			stack = stack[:lvl]
		}
		for len(stack) < lvl {
			stack = append(stack, "")
		}
		stack = append(stack, title)
		secs = append(secs, section{path: nonEmpty(stack), start: i})
	}
	return closeSections(secs, func(s section) int { return len(s.path) }, len(lines))
}

var (
	yamlKey     = regexp.MustCompile(`^(\s*)(?:-\s+)?["']?([A-Za-z0-9_][\w./@-]*)["']?\s*:(?:\s|$)`)
	yamlNamed   = regexp.MustCompile(`^(\s*)-\s+(?:name|id|key|title)\s*:\s*["']?([^"'#]+?)["']?\s*(?:#.*)?$`)
	yamlComment = regexp.MustCompile(`^\s*(#|$)`)
)

// yamlSections takes keys down to depth 3, and list entries named by a
// name/id/key/title field (a CI step, a Kubernetes container, an rviz display).
func yamlSections(content string) []section {
	lines := strings.Split(content, "\n")
	type open struct {
		indent int
		name   string
	}
	var stack []open
	var secs []section
	depth := map[int]int{}
	for i, ln := range lines {
		if yamlComment.MatchString(ln) || strings.HasPrefix(strings.TrimSpace(ln), "---") {
			continue
		}
		var indent int
		var name string
		if m := yamlNamed.FindStringSubmatch(ln); m != nil {
			indent, name = len(m[1]), strings.TrimSpace(m[2])
		} else if m := yamlKey.FindStringSubmatch(ln); m != nil {
			indent, name = len(m[1]), m[2]
			if strings.HasPrefix(strings.TrimLeft(ln, " "), "- ") {
				indent += 2 // a key opening a list item belongs inside it
			}
		} else {
			continue
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		stack = append(stack, open{indent, name})
		if len(stack) > 3 {
			continue
		}
		p := make([]string, len(stack))
		for k, o := range stack {
			p[k] = o.name
		}
		secs = append(secs, section{path: p, start: i + 1})
		depth[len(secs)-1] = indent
	}
	// A section ends before the next one at the same or a shallower indent.
	for i := range secs {
		secs[i].end = len(lines)
		for j := i + 1; j < len(secs); j++ {
			if depth[j] <= depth[i] {
				secs[i].end = secs[j].start - 1
				break
			}
		}
	}
	return secs
}

var (
	jsonKey   = regexp.MustCompile(`^(\s*)"([^"]{1,120})"\s*:\s*(.?)`)
	jsonNamed = regexp.MustCompile(`^\s*"(?:name|id|key|title)"\s*:\s*"([^"]{1,120})"`)
)

// jsonSections takes the keys of the top-level object and of the objects
// directly inside it, and in an array of objects — at the top or under a
// top-level key — each object named by its name/id/key/title field.
func jsonSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	var depth []int
	var stack []byte // open containers, outermost first
	var last string  // the current top-level key
	elemStart, named := -1, false
	for i, ln := range lines {
		st := string(stack)
		switch {
		case st == "{" || st == "{{":
			if m := jsonKey.FindStringSubmatch(ln); m != nil {
				if st == "{" {
					last = m[2]
					secs = append(secs, section{path: []string{m[2]}, start: i + 1})
					depth = append(depth, 1)
				} else if last != "" {
					secs = append(secs, section{path: []string{last, m[2]}, start: i + 1})
					depth = append(depth, 2)
				}
			}
		case (st == "[{" || st == "{[{") && !named && elemStart >= 0:
			if m := jsonNamed.FindStringSubmatch(ln); m != nil {
				if st == "[{" {
					secs = append(secs, section{path: []string{m[1]}, start: elemStart + 1})
					depth = append(depth, 1)
				} else if last != "" {
					secs = append(secs, section{path: []string{last, m[1]}, start: elemStart + 1})
					depth = append(depth, 2)
				}
				named = true
			}
		}
		inStr, esc := false, false
		for k := 0; k < len(ln); k++ {
			c := ln[k]
			if inStr {
				switch {
				case esc:
					esc = false
				case c == '\\':
					esc = true
				case c == '"':
					inStr = false
				}
				continue
			}
			switch c {
			case '"':
				inStr = true
			case '{', '[':
				stack = append(stack, c)
				if c == '{' && (string(stack) == "[{" || string(stack) == "{[{") {
					elemStart, named = i, false
				}
			case '}', ']':
				if len(stack) > 0 {
					stack = stack[:len(stack)-1]
				}
			}
		}
	}
	for i := range secs {
		secs[i].end = len(lines)
		for j := i + 1; j < len(secs); j++ {
			if depth[j] <= depth[i] {
				secs[i].end = secs[j].start - 1
				break
			}
		}
	}
	return secs
}

var tableHeader = regexp.MustCompile(`^\s*\[\[?\s*([^\]]+?)\s*\]\]?\s*(?:[#;].*)?$`)

func tableSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	for i, ln := range lines {
		if m := tableHeader.FindStringSubmatch(ln); m != nil {
			secs = append(secs, section{path: []string{m[1]}, start: i + 1})
		}
	}
	return closeSections(secs, func(section) int { return 1 }, len(lines))
}

var dockerFrom = regexp.MustCompile(`(?i)^\s*FROM\s+(\S+)(?:\s+AS\s+(\S+))?`)

func dockerSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	n := 0
	for i, ln := range lines {
		if m := dockerFrom.FindStringSubmatch(ln); m != nil {
			n++
			name := m[2]
			if name == "" {
				name = "stage" + itoaSmall(n) + " " + m[1]
			}
			secs = append(secs, section{path: []string{name}, start: i + 1})
		}
	}
	return closeSections(secs, func(section) int { return 1 }, len(lines))
}

var makeTarget = regexp.MustCompile(`^([A-Za-z0-9_][\w./%-]*)\s*:(?:[^=]|$)`)

func makeSections(content string) []section {
	lines := strings.Split(content, "\n")
	var secs []section
	for i, ln := range lines {
		if m := makeTarget.FindStringSubmatch(ln); m != nil && !strings.HasPrefix(m[1], ".") {
			secs = append(secs, section{path: []string{m[1]}, start: i + 1})
		}
	}
	return closeSections(secs, func(section) int { return 1 }, len(lines))
}
