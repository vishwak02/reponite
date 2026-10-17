// robot.go indexes Robot Framework suites (.robot) and resource files
// (.resource): every test case and keyword is a symbol, and every keyword a
// body calls is a call edge. Robot names are case- and space-insensitive and
// a Python library keyword `open_door` is called as `Open Door`, so names are
// stored in snake form — a call to `Open Door` resolves to a Robot keyword of
// that name and to a Python function `open_door` alike, which links an
// acceptance test to the code it drives (`tests`, `usages`, `trace`). Pure and
// stdlib-only (ADR-018): the format is line-oriented, no grammar needed.
package processing

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// IsRobotFile reports whether path is a Robot Framework suite or resource file.
func IsRobotFile(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".robot", ".resource":
		return true
	}
	return false
}

var (
	robotSection = regexp.MustCompile(`^\*+\s*([A-Za-z ]+?)\s*\**\s*$`)
	robotCellSep = regexp.MustCompile(`\t+|\s{2,}|\s+\|\s+`)
	robotVar     = regexp.MustCompile(`^[$@&%]\{[^}]*\}\s*=?$`)
	robotNonWord = regexp.MustCompile(`[^a-z0-9]+`)
)

// robotControl are the structural words that are not keyword calls.
var robotControl = map[string]bool{
	"for": true, "end": true, "if": true, "else": true, "else if": true, "while": true, "try": true,
	"except": true, "finally": true, "break": true, "continue": true, "return": true, "in": true,
	"in range": true, "in enumerate": true, "in zip": true, "var": true, "...": true, "group": true,
}

// robotBDD are the prefixes Robot strips before looking a keyword up.
var robotBDD = []string{"given ", "when ", "then ", "and ", "but "}

// RobotName is the stored form of a Robot test or keyword name: lowercase,
// words joined by '_' — the form a Python library function has.
func RobotName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	return strings.Trim(robotNonWord.ReplaceAllString(n, "_"), "_")
}

// robotCall turns a called cell into a callee name: the BDD prefix and a
// library or resource qualifier (`Lib.Keyword`) dropped.
func robotCall(cell string) string {
	c := strings.TrimSpace(cell)
	lc := strings.ToLower(c)
	for _, p := range robotBDD {
		if strings.HasPrefix(lc, p) {
			c = c[len(p):]
			break
		}
	}
	if i := strings.LastIndex(c, "."); i > 0 && !strings.ContainsAny(c[:i], " ${") {
		c = c[i+1:]
	}
	return RobotName(c)
}

func robotCells(line string) []string {
	parts := robotCellSep.Split(strings.TrimRight(line, " \t\r"), -1)
	out := parts[:0]
	for _, p := range parts {
		if p = strings.TrimSpace(strings.Trim(p, "|")); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// robotFile parses one suite or resource file.
func robotFile(path, content string) ParsedFile {
	lines := strings.Split(content, "\n")
	section := ""
	hasTests := false
	type def struct {
		name, kind string
		start, end int
		args       []string
		calls      []string
	}
	var defs []def
	cur := -1
	closeCur := func(at int) {
		if cur >= 0 && defs[cur].end == 0 {
			defs[cur].end = at
		}
	}
	for i, ln := range lines {
		if m := robotSection.FindStringSubmatch(ln); m != nil {
			closeCur(i)
			cur = -1
			section = strings.ToLower(strings.TrimSpace(m[1]))
			if strings.HasPrefix(section, "test case") || strings.HasPrefix(section, "task") {
				hasTests = true
			}
			continue
		}
		testSec := strings.HasPrefix(section, "test case") || strings.HasPrefix(section, "task")
		kwSec := strings.HasPrefix(section, "keyword")
		if !testSec && !kwSec {
			continue
		}
		if strings.TrimSpace(ln) == "" || strings.HasPrefix(strings.TrimSpace(ln), "#") {
			continue
		}
		if ln[0] != ' ' && ln[0] != '\t' && ln[0] != '|' {
			// A name at column 0 starts a test case or keyword.
			closeCur(i)
			cells := robotCells(ln)
			if len(cells) == 0 {
				continue
			}
			kind := "keyword"
			if testSec {
				kind = "test"
			}
			defs = append(defs, def{name: cells[0], kind: kind, start: i + 1})
			cur = len(defs) - 1
			if len(cells) > 1 { // a body step written on the name's own line
				ln = "  " + strings.Join(cells[1:], "  ")
			} else {
				continue
			}
		}
		if cur < 0 {
			continue
		}
		cells := robotCells(ln)
		for len(cells) > 0 && robotVar.MatchString(cells[0]) {
			cells = cells[1:] // ${result}=    Keyword
		}
		if len(cells) == 0 {
			continue
		}
		first := strings.ToLower(cells[0])
		switch {
		case first == "[arguments]":
			defs[cur].args = cells[1:]
			continue
		case first == "[setup]" || first == "[teardown]" || first == "[template]":
			if len(cells) > 1 && !strings.EqualFold(cells[1], "none") {
				defs[cur].calls = append(defs[cur].calls, robotCall(cells[1]))
			}
			continue
		case strings.HasPrefix(first, "["):
			continue // [Documentation], [Tags], [Timeout], [Return]
		case robotControl[first]:
			// IF    condition    Keyword    args — an inline IF calls its third cell.
			if (first == "if" || first == "else if") && len(cells) > 2 && !robotControl[strings.ToLower(cells[2])] {
				defs[cur].calls = append(defs[cur].calls, robotCall(cells[2]))
			}
			continue
		}
		if c := robotCall(cells[0]); c != "" {
			defs[cur].calls = append(defs[cur].calls, c)
		}
	}
	closeCur(len(lines))
	pf := ParsedFile{Path: path, Content: content, Lang: "robot",
		IsTest: hasTests || IsTestPath(path)}
	seen := map[string]bool{}
	for _, d := range defs {
		name := RobotName(d.name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		sig := d.name
		if len(d.args) > 0 {
			sig += " [" + strings.Join(d.args, " ") + "]"
		}
		body := strings.Join(lines[d.start-1:d.end], "\n")
		pf.Symbols = append(pf.Symbols, Symbol{Name: name, Kind: "function", Signature: sig,
			CanonBody: []byte(strings.TrimSpace(body)), Callees: dedupeStrings(d.calls)})
		pf.Spans = append(pf.Spans, query.SymbolSpan{Name: name, StartLine: d.start, EndLine: d.end})
	}
	return pf
}

func dedupeStrings(xs []string) []string {
	seen := map[string]bool{}
	out := xs[:0]
	for _, x := range xs {
		if x != "" && !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

var robotEmbeddedVar = regexp.MustCompile(`\$\{[^}]*\}`)

// linkRobotEmbedded resolves calls to keywords with embedded arguments
// (`Open ${door} Door` called as `Open Front Door`): a call whose stored name
// matches such a keyword's pattern is rewritten to the keyword's own name,
// across every Robot file of the ref (resource files define most keywords).
func linkRobotEmbedded(files []ParsedFile) {
	type pat struct {
		re   *regexp.Regexp
		name string
	}
	var pats []pat
	for _, f := range files {
		if f.Lang != "robot" {
			continue
		}
		for _, s := range f.Symbols {
			raw := s.Signature
			if i := strings.Index(raw, " ["); i >= 0 {
				raw = raw[:i]
			}
			if !robotEmbeddedVar.MatchString(raw) {
				continue
			}
			var b strings.Builder
			b.WriteString("^")
			for i, lit := range robotEmbeddedVar.Split(raw, -1) {
				if i > 0 {
					b.WriteString("_?[a-z0-9_]+?_?")
				}
				b.WriteString(regexp.QuoteMeta(RobotName(lit)))
			}
			b.WriteString("$")
			if re, err := regexp.Compile(b.String()); err == nil {
				pats = append(pats, pat{re, s.Name})
			}
		}
	}
	if len(pats) == 0 {
		return
	}
	for fi := range files {
		if files[fi].Lang != "robot" {
			continue
		}
		for si := range files[fi].Symbols {
			cs := files[fi].Symbols[si].Callees
			for ci, c := range cs {
				for _, p := range pats {
					if c != p.name && p.re.MatchString(c) {
						cs[ci] = p.name
						break
					}
				}
			}
		}
	}
}
