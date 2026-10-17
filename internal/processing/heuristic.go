// heuristic.go gives languages without a bundled grammar a symbol table and
// a call graph anyway, from their definition keywords — the way ctags does.
// Weaker than a parser (a definition written in an unusual form is missed,
// a call without parentheses is not seen, spans end at the next definition),
// but a Kotlin, Swift, C#, Ruby, PHP, Lua, Scala, Dart, Groovy, Perl, R,
// Elixir, Julia or Zig file is then searchable by symbol, has callers and
// callees, and takes part in compat — instead of being plain text. Every
// symbol it produces says so in its signature ("heuristic"). Pure and
// stdlib-only (ADR-018).
package processing

import (
	"path"
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

type heuristicRules struct {
	lang  string
	defs  []*regexp.Regexp // group 1 = the defined name
	types []*regexp.Regexp // group 1 = a type/class/module name
	brace bool             // bodies are {…}; else they end at the next definition at the same indent
}

func rx(s string) *regexp.Regexp { return regexp.MustCompile(s) }

var heuristicByExt = map[string]*heuristicRules{}

func init() {
	add := func(r *heuristicRules, exts ...string) {
		for _, e := range exts {
			heuristicByExt[e] = r
		}
	}
	add(&heuristicRules{lang: "kotlin", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:[a-z]+\s+)*fun\s+(?:<[^>]*>\s*)?(?:[\w.]+\.)?(\w+)\s*\(`)},
		types: []*regexp.Regexp{rx(`^\s*(?:[a-z]+\s+)*(?:class|object|interface)\s+(\w+)`)}}, ".kt", ".kts")
	add(&heuristicRules{lang: "swift", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:@\w+\s+)*(?:[a-z]+\s+)*func\s+(\w+)`), rx(`^\s*(?:[a-z]+\s+)*(init)\s*\(`)},
		types: []*regexp.Regexp{rx(`^\s*(?:[a-z]+\s+)*(?:class|struct|enum|protocol|actor|extension)\s+(\w+)`)}}, ".swift")
	add(&heuristicRules{lang: "csharp", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:\[[^\]]*\]\s*)*(?:(?:public|private|protected|internal|static|virtual|override|abstract|async|sealed|extern|unsafe|new|partial)\s+)+[\w<>\[\],.?\s]*?\s(\w+)\s*(?:<[^>]*>)?\s*\([^;]*$`)},
		types: []*regexp.Regexp{rx(`^\s*(?:(?:public|private|protected|internal|static|abstract|sealed|partial)\s+)*(?:class|struct|interface|record|enum)\s+(\w+)`)}}, ".cs")
	add(&heuristicRules{lang: "ruby",
		defs:  []*regexp.Regexp{rx(`^\s*def\s+(?:self\.)?([\w?!=]+)`)},
		types: []*regexp.Regexp{rx(`^\s*(?:class|module)\s+([\w:]+)`)}}, ".rb", ".rake", ".gemspec")
	add(&heuristicRules{lang: "php", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:(?:public|private|protected|static|abstract|final)\s+)*function\s+&?(\w+)\s*\(`)},
		types: []*regexp.Regexp{rx(`^\s*(?:(?:abstract|final)\s+)*(?:class|interface|trait|enum)\s+(\w+)`)}}, ".php")
	add(&heuristicRules{lang: "lua",
		defs: []*regexp.Regexp{rx(`^\s*(?:local\s+)?function\s+(?:[\w.]+[.:])?(\w+)\s*\(`), rx(`^\s*(?:local\s+)?([\w.]+)\s*=\s*function\s*\(`)}}, ".lua")
	add(&heuristicRules{lang: "scala", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:(?:override|private|protected|final|implicit|inline)\s+)*def\s+(\w+)`)},
		types: []*regexp.Regexp{rx(`^\s*(?:(?:case|abstract|sealed|final|implicit)\s+)*(?:class|object|trait|enum)\s+(\w+)`)}}, ".scala", ".sc")
	add(&heuristicRules{lang: "dart", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:static\s+|external\s+|@override\s+)*(?:Future<[^>]*>|Stream<[^>]*>|void|[A-Z]\w*(?:<[^>]*>)?\??|int|double|bool|String|dynamic)\s+(\w+)\s*\([^;]*$`)},
		types: []*regexp.Regexp{rx(`^\s*(?:abstract\s+)?(?:class|mixin|extension|enum)\s+(\w+)`)}}, ".dart")
	add(&heuristicRules{lang: "groovy", brace: true,
		defs:  []*regexp.Regexp{rx(`^\s*(?:(?:public|private|protected|static)\s+)*def\s+(\w+)\s*\(`)},
		types: []*regexp.Regexp{rx(`^\s*(?:class|interface|trait)\s+(\w+)`)}}, ".groovy", ".gradle", ".gvy")
	add(&heuristicRules{lang: "perl", brace: true, defs: []*regexp.Regexp{rx(`^\s*sub\s+(\w+)`)},
		types: []*regexp.Regexp{rx(`^\s*package\s+([\w:]+)`)}}, ".pl", ".pm")
	add(&heuristicRules{lang: "r", brace: true, defs: []*regexp.Regexp{rx(`^\s*([\w.]+)\s*(?:<-|=)\s*function\s*\(`)}}, ".r", ".R")
	add(&heuristicRules{lang: "elixir", defs: []*regexp.Regexp{rx(`^\s*defp?\s+(\w+[?!]?)`), rx(`^\s*defmacrop?\s+(\w+[?!]?)`)},
		types: []*regexp.Regexp{rx(`^\s*defmodule\s+([\w.]+)`)}}, ".ex", ".exs")
	add(&heuristicRules{lang: "julia", defs: []*regexp.Regexp{rx(`^\s*function\s+(?:[\w.]+\.)?([\w!]+)`), rx(`^\s*([\w!]+)\s*\([^)]*\)\s*=[^=]`)},
		types: []*regexp.Regexp{rx(`^\s*(?:mutable\s+)?struct\s+(\w+)`), rx(`^\s*module\s+(\w+)`)}}, ".jl")
	add(&heuristicRules{lang: "zig", brace: true, defs: []*regexp.Regexp{rx(`^\s*(?:pub\s+)?(?:export\s+)?fn\s+(\w+)\s*\(`)}}, ".zig")
}

// HeuristicLang is the language of a file a heuristic extractor handles, or "".
func HeuristicLang(p string) string {
	if r := heuristicByExt[path.Ext(p)]; r != nil {
		return r.lang
	}
	if r := heuristicByExt[strings.ToLower(path.Ext(p))]; r != nil {
		return r.lang
	}
	return ""
}

var (
	heurCall    = regexp.MustCompile(`\b([A-Za-z_]\w*[?!]?)\s*\(`)
	heurKeyword = map[string]bool{
		"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "when": true,
		"function": true, "fun": true, "func": true, "def": true, "sizeof": true, "typeof": true, "new": true,
		"elif": true, "unless": true, "until": true, "case": true, "match": true, "guard": true, "foreach": true,
		"using": true, "lock": true, "fixed": true, "nameof": true, "await": true, "super": true, "this": true,
		"self": true, "assert": true, "print": true, "require": true, "local": true, "fn": true, "sub": true,
	}
)

// heuristicFile extracts symbols, spans and calls from a file of a language
// with no grammar bundled.
func heuristicFile(p, content string, normVer int) ParsedFile {
	r := heuristicByExt[path.Ext(p)]
	if r == nil {
		r = heuristicByExt[strings.ToLower(path.Ext(p))]
	}
	pf := ParsedFile{Path: p, Content: content, Lang: r.lang, IsTest: IsTestPath(p)}
	lines := strings.Split(content, "\n")
	type def struct {
		name, kind string
		line       int
		indent     int
	}
	var defs []def
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "--") ||
			strings.HasPrefix(t, "*") || strings.HasPrefix(t, "/*") {
			continue
		}
		ind := len(ln) - len(strings.TrimLeft(ln, " \t"))
		for _, re := range r.types {
			if m := re.FindStringSubmatch(ln); m != nil {
				defs = append(defs, def{m[1], "type", i + 1, ind})
			}
		}
		for _, re := range r.defs {
			if m := re.FindStringSubmatch(ln); m != nil && !heurKeyword[m[1]] {
				defs = append(defs, def{m[1], "function", i + 1, ind})
				break
			}
		}
	}
	seen := map[string]int{}
	for i, d := range defs {
		end := len(lines)
		if r.brace && d.kind == "function" {
			end = braceEnd(lines, d.line-1)
		} else {
			for _, n := range defs[i+1:] {
				if n.indent <= d.indent {
					end = n.line - 1
					break
				}
			}
		}
		if end < d.line {
			end = d.line
		}
		name := d.name
		if j := strings.LastIndexAny(name, ".:"); j >= 0 && j < len(name)-1 {
			name = name[j+1:]
		}
		if n := seen[name]; n > 0 { // an overload keeps its own record
			seen[name] = n + 1
			continue
		}
		seen[name] = 1
		body := strings.Join(lines[d.line-1:end], "\n")
		var callees []string
		if d.kind == "function" {
			callSeen := map[string]bool{}
			for _, ln := range lines[d.line:end] {
				for _, m := range heurCall.FindAllStringSubmatch(ln, -1) {
					c := m[1]
					if !heurKeyword[c] && c != name && !callSeen[c] {
						callSeen[c] = true
						callees = append(callees, c)
					}
				}
			}
		}
		sig := strings.TrimSpace(lines[d.line-1])
		pf.Symbols = append(pf.Symbols, Symbol{Name: name, Kind: d.kind, Signature: sig + " /* heuristic */",
			CanonBody: []byte(strings.Join(strings.Fields(body), " ")), Callees: callees})
		pf.Spans = append(pf.Spans, query.SymbolSpan{Name: name, StartLine: d.line, EndLine: end})
	}
	return pf
}

// braceEnd is the line (1-based) closing the first { at or after line start,
// counting braces outside string and character literals.
func braceEnd(lines []string, start int) int {
	depth, opened := 0, false
	for i := start; i < len(lines); i++ {
		ln := lines[i]
		var q byte
		for j := 0; j < len(ln); j++ {
			c := ln[j]
			switch {
			case q != 0:
				if c == '\\' {
					j++
				} else if c == q {
					q = 0
				}
			case c == '"' || c == '\'' || c == '`':
				q = c
			case c == '/' && j+1 < len(ln) && ln[j+1] == '/':
				j = len(ln)
			case c == '{':
				depth++
				opened = true
			case c == '}':
				depth--
				if opened && depth == 0 {
					return i + 1
				}
			}
		}
		if !opened && i > start+3 {
			return start + 1 // an expression body or a declaration: the line itself
		}
	}
	return len(lines)
}
