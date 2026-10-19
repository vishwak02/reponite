// usages.go implements reponite_usages — the refactoring companion to brief:
// every call site of a symbol, with the exact calling line and the function it
// sits in, cross-checked against the call graph. An agent about to change foo's
// signature wants to SEE the N places that call it (`foo(a, b)` at file:line),
// not just the caller names context gives. It fuses the lexical layer (grep for
// the call pattern, which already tags each hit with its enclosing symbol) with
// the resolved reverse call graph: a usage whose enclosing symbol is a known
// caller is `confirmed`, distinguishing a real call from a same-named token in a
// comment or string. Pure over the Store, tested in-sandbox (ADR-018).
package query

import (
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Usage is one call site of the target.
type Usage struct {
	Repo      string
	Path      string
	Line      int
	Text      string // the calling source line, trimmed
	In        string // enclosing symbol (the caller), qualified
	Confirmed bool   // In is a resolved caller in the call graph (not just a lexical match)
}

// UsagesResult is the call sites of a symbol, confirmed-first.
type UsagesResult struct {
	Symbol string
	Usages []Usage
	Total  int
	Note   string
	Meta   Meta
}

// Usages finds every call site of symbol (by its bare name) across repo
// (FleetRepo "*" = the whole fleet) at ref: the exact line and the enclosing
// function, with a `confirmed` flag when that function is a resolved caller in
// the call graph. Lexical, so it also surfaces dynamic/reflective calls the
// static graph misses — those come back unconfirmed rather than dropped.
func Usages(s Store, repo, ref, symbol string) UsagesResult {
	base := baseName(symbol)
	res := UsagesResult{Symbol: base, Meta: NewMeta(s, repo, ref)}
	if base == "" {
		return res
	}

	// The set of resolved callers of the symbol, per repo, to confirm lexical hits.
	callers := map[string]map[string]bool{} // repo -> set of caller qids
	exact := map[string]map[string]bool{}   // callers resolved to this very symbol (qualified queries)
	for _, rp := range reposFor(s, repo) {
		set, ex := map[string]bool{}, map[string]bool{}
		snap := s.Snapshot(rp, ref)
		for caller, callees := range snap.Callees {
			for _, c := range callees {
				if baseName(c.Name) == base {
					set[caller] = true
					if c.Name == symbol {
						ex[caller] = true
					}
				}
			}
		}
		callers[rp], exact[rp] = set, ex
	}
	siteCallers := callers
	if symbol != base {
		siteCallers = exact // a qualified id: only its own callers, not every same-named function's
	}

	// Lexical call sites: the name used as a call — `name(` allowing whitespace.
	// Reuse GrepRepo (trigram-backed, enclosing-symbol fusion) via a regex.
	// Limit -1 = every call site: usages is the ground-truth list its consumers
	// (verify_edit, blast_radius) reason over — a silent 50-cap would hide
	// breaking call sites.
	pat := `\b` + regexp.QuoteMeta(base) + `\s*\(`
	g, err := GrepRepo(s, repo, ref, pat, GrepOptions{Fixed: false, Limit: -1})
	if err != nil {
		res.Note = "usage search failed: " + err.Error()
		return res
	}
	// A line that DECLARES the target (keyword-based langs), so its own definition
	// isn't reported as a usage of itself. Requires the name right after the
	// keyword (+ optional Go receiver), so a one-line `func f(){ x.name() }`
	// counts as a call, not a definition.
	defRe := regexp.MustCompile(`\b(func|def|fn|function)\b\s+(\([^)]*\)\s*)?` + regexp.QuoteMeta(base) + `\s*[(<]` +
		// a POSIX shell function or a class method written `name() {`
		`|^\s*` + regexp.QuoteMeta(base) + `\s*\(\s*\)\s*\{`)
	in := placeCallers(s, ref, siteCallers, g.Matches)
	for _, m := range g.Matches {
		if defRe.MatchString(m.Text) || ProjectFileKind(m.Path) != "" {
			continue // its own definition, or prose/build text in a README or manifest
		}
		u := Usage{Repo: m.Repo, Path: m.Path, Line: m.Line, Text: strings.TrimSpace(m.Text), In: m.Symbol}
		// confirmed: the line sits inside a function the call graph says
		// calls the target (that exact symbol, for a qualified query) — the
		// caller told apart from every other function of the same name
		if q := in(m.Repo, m.Path, m.Line); q != "" {
			u.In, u.Confirmed = q, true
		}
		res.Usages = append(res.Usages, u)
	}
	res.Usages = append(res.Usages, callerSiteUsages(s, ref, symbol, base, siteCallers, res.Usages)...)
	res.Usages = append(res.Usages, shellCommandUsages(s, repo, ref, base, defRe, res.Usages)...)
	sort.SliceStable(res.Usages, func(i, j int) bool {
		a, b := res.Usages[i], res.Usages[j]
		if a.Confirmed != b.Confirmed {
			return a.Confirmed // confirmed call-graph usages first
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	res.Total = len(res.Usages)
	res.Note = "call sites by name; `confirmed` = the enclosing function is a resolved caller in the call graph (unconfirmed = comment/string/other-scope or a dynamic call the static graph can't see)"
	return res
}

// callerSiteUsages finds the call sites the `name(` search cannot: a resolved
// caller's body naming the target without parentheses — a shell function
// call (`check_repository "x"`), a JSX element (`<Layout …/>`), a Robot
// keyword (`Open Door`), a function passed as a value. Every one is
// confirmed: the call graph already says that caller calls the target.
func callerSiteUsages(s Store, ref, symbol, base string, callers map[string]map[string]bool, have []Usage) []Usage {
	seen := map[string]bool{}
	for _, u := range have {
		seen[u.Repo+"\x00"+u.Path+"\x00"+strconv.Itoa(u.Line)] = true
	}
	word := regexp.MustCompile(`\b` + regexp.QuoteMeta(base) + `\b`)
	robot := regexp.MustCompile(`(?i)\b` + strings.ReplaceAll(regexp.QuoteMeta(base), "_", "[_ ]") + `\b`)
	var out []Usage
	for _, rp := range sortedKeysBool(callers) {
		set := callers[rp]
		if len(set) == 0 {
			continue
		}
		files := s.Files(rp, ref)
		byPath := map[string]File{}
		byDir := map[string][]File{} // a caller's package directory -> its files
		for _, f := range files {
			byPath[f.Path] = f
			byDir[pkgFromPath(f.Path)] = append(byDir[pkgFromPath(f.Path)], f)
		}
		for _, caller := range sortedSet(set) {
			// look only in the caller's own package directory: scanning every
			// file per caller is quadratic on a large repo
			cand := files
			if i := strings.LastIndexByte(caller, '/'); i >= 0 {
				if j := strings.IndexByte(caller[i:], '.'); j > 0 {
					if fs, ok := byDir[caller[:i+j]]; ok {
						cand = fs
					}
				}
			} else if fs, ok := byDir[""]; ok {
				cand = fs
			}
			path, span, ok := locateSymbol(cand, caller)
			if !ok && len(cand) != len(files) {
				path, span, ok = locateSymbol(files, caller)
			}
			if !ok {
				continue
			}
			f := byPath[path]
			re := word
			if e := strings.ToLower(filepath.Ext(path)); e == ".robot" || e == ".resource" {
				re = robot
			}
			if aliases := jsImportAliases(f, symbol, base); len(aliases) > 0 {
				// the caller names the target by the alias it imported it as
				names := []string{regexp.QuoteMeta(base)}
				for _, a := range aliases {
					names = append(names, regexp.QuoteMeta(a))
				}
				re = regexp.MustCompile(`\b(?:` + strings.Join(names, "|") + `)\b`)
			}
			lines := strings.Split(f.Content, "\n")
			for ln := span.StartLine + 1; ln <= span.EndLine && ln <= len(lines); ln++ {
				text := lines[ln-1]
				if !re.MatchString(text) {
					continue
				}
				k := rp + "\x00" + path + "\x00" + strconv.Itoa(ln)
				if seen[k] {
					continue
				}
				seen[k] = true
				out = append(out, Usage{Repo: rp, Path: path, Line: ln, Text: strings.TrimSpace(text),
					In: caller, Confirmed: true})
			}
		}
	}
	return out
}

func sortedKeysBool(m map[string]map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// shellCommandUsages finds a shell function called as a command — `name args`
// with no parentheses, at the start of a command (line start, after ; & | (
// or a then/do/else/if/while/until/! keyword) — in shell scripts, including
// top-level script code no caller symbol encloses.
func shellCommandUsages(s Store, repo, ref, base string, defRe *regexp.Regexp, have []Usage) []Usage {
	pat := `(^|[;&|({]|\b(then|do|else|if|while|until|time|exec)\s|!\s)\s*` + regexp.QuoteMeta(base) + `(\s|;|&|\||\)|$)`
	// only shell scripts can call a function as a command: scan just those
	var matches []Match
	for _, rp := range reposFor(s, repo) {
		g, err := ScanGrep(filesMatching(s, rp, ref, isShellPath), pat, GrepOptions{Limit: -1})
		if err != nil {
			return nil
		}
		for _, m := range g.Matches {
			m.Repo = rp
			matches = append(matches, m)
		}
	}
	seen := map[string]bool{}
	for _, u := range have {
		seen[u.Repo+"\x00"+u.Path+"\x00"+strconv.Itoa(u.Line)] = true
	}
	var out []Usage
	for _, m := range matches {
		if !isShellPath(m.Path) || defRe.MatchString(m.Text) || strings.HasPrefix(strings.TrimSpace(m.Text), "#") {
			continue
		}
		k := m.Repo + "\x00" + m.Path + "\x00" + strconv.Itoa(m.Line)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, Usage{Repo: m.Repo, Path: m.Path, Line: m.Line, Text: strings.TrimSpace(m.Text), In: m.Symbol})
	}
	return out
}

func isShellPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".sh", ".bash", ".zsh", ".ksh":
		return true
	case "":
		return true // an extension-less script; grep only matched command syntax
	}
	return false
}

// placeCallers locates the resolved callers in the files the matches are in
// and returns a lookup: the innermost caller whose body holds a line, "" for
// none.
func placeCallers(s Store, ref string, callers map[string]map[string]bool, matches []Match) func(repo, path string, line int) string {
	type span struct {
		qid        string
		start, end int
	}
	paths := map[string]map[string]bool{}
	for _, m := range matches {
		if paths[m.Repo] == nil {
			paths[m.Repo] = map[string]bool{}
		}
		paths[m.Repo][m.Path] = true
	}
	placed := map[string]map[string][]span{}
	for rp, set := range callers {
		ps := paths[rp]
		if len(ps) == 0 || len(set) == 0 {
			continue
		}
		files := filesMatching(s, rp, ref, func(p string) bool { return ps[p] })
		placed[rp] = map[string][]span{}
		for c := range set {
			p, st, en, ok := LocateSymbol(files, c)
			if !ok || !ownFile(c, p) {
				continue // its own file is not among these: no match is in it
			}
			placed[rp][p] = append(placed[rp][p], span{c, st, en})
		}
	}
	return func(repo, path string, line int) string {
		best, size := "", 1<<30
		for _, sp := range placed[repo][path] {
			if sp.start <= line && line <= sp.end && sp.end-sp.start < size {
				best, size = sp.qid, sp.end-sp.start
			}
		}
		return best
	}
}

// ownFile reports whether qid can be defined in the file at p: its id
// starts with the file's directory, and with its module for languages whose
// module is the file (except index/__init__ files and a symbol named like
// its file, which carry the directory alone).
func ownFile(qid, p string) bool {
	dir := pkgFromPath(p)
	if dir != "" && !strings.HasPrefix(qid, dir+".") {
		return false
	}
	stem := fileModuleStem(p)
	if stem == "" {
		return true
	}
	mod := stem
	if dir != "" {
		mod = dir + "." + stem
	}
	return strings.HasPrefix(qid, mod+".") || qid == mod || !strings.Contains(strings.TrimPrefix(qid, dir+"."), ".")
}

var (
	reJSDefaultImport = regexp.MustCompile(`(?m)^\s*import\s+([A-Za-z_$][\w$]*)\s*(?:,\s*\{[^}]*\})?\s*from\s*['"]([^'"]+)['"]`)
	reJSNamedImports  = regexp.MustCompile(`(?s)import\s*(?:[A-Za-z_$][\w$]*\s*,\s*)?\{([^}]*)\}\s*from\s*['"]([^'"]+)['"]`)
)

// jsImportAliases are the local names a JS/TS file imports the target under:
// `{ base as X }`, and — when the target's id is known — a default import
// `import X from './m'` of the module the target is defined in.
func jsImportAliases(f File, symbol, base string) []string {
	switch strings.ToLower(filepath.Ext(f.Path)) {
	case ".js", ".jsx", ".ts", ".tsx", ".mjs", ".cjs", ".vue", ".svelte":
	case ".py":
		// from .testing import FlaskClient as cls
		var out []string
		// from m import f as g, or a parenthesized list across lines
		re := regexp.MustCompile(`\bimport\s+(?:\([^)]*?|[^\n(]*?)\b` + regexp.QuoteMeta(base) + `\s+as\s+(\w+)`)
		for _, m := range re.FindAllStringSubmatch(f.Content, -1) {
			out = append(out, m[1])
		}
		return out
	default:
		return nil
	}
	var out []string
	for _, m := range reJSNamedImports.FindAllStringSubmatch(f.Content, -1) {
		for _, spec := range strings.Split(m[1], ",") {
			fs := strings.Fields(spec)
			if len(fs) == 3 && fs[0] == base && fs[1] == "as" {
				out = append(out, fs[2])
			}
		}
	}
	if symbol == base {
		return out // a bare name: which module defines it is unknown
	}
	for _, m := range reJSDefaultImport.FindAllStringSubmatch(f.Content, -1) {
		alias, spec := m[1], m[2]
		var mod string
		switch {
		case strings.HasPrefix(spec, "./") || strings.HasPrefix(spec, "../"):
			mod = filepath.ToSlash(filepath.Clean(filepath.Join(filepath.Dir(f.Path), spec)))
			// dir/mod.Name, or dir.mod when the symbol is named like its file
			collapsed := filepath.ToSlash(filepath.Dir(mod)) + "." + filepath.Base(mod)
			if strings.HasPrefix(symbol, mod+".") || strings.HasPrefix(symbol, mod+"/") || symbol == collapsed {
				out = append(out, alias)
			}
		default:
			// a path alias (@app/components/x): the part after the alias root
			if i := strings.IndexByte(spec, '/'); i > 0 && i < len(spec)-1 {
				rest := spec[i+1:]
				collapsed := ""
				if j := strings.LastIndexByte(rest, '/'); j > 0 {
					collapsed = "/" + rest[:j] + "." + rest[j+1:]
				}
				if strings.Contains(symbol, "/"+rest+".") || strings.HasPrefix(symbol, rest+".") || collapsed != "" && strings.HasSuffix(symbol, collapsed) {
					out = append(out, alias)
				}
			}
		}
	}
	return out
}
