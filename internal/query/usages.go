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
	for _, m := range g.Matches {
		if defRe.MatchString(m.Text) || ProjectFileKind(m.Path) != "" {
			continue // its own definition, or prose/build text in a README or manifest
		}
		u := Usage{Repo: m.Repo, Path: m.Path, Line: m.Line, Text: strings.TrimSpace(m.Text), In: m.Symbol}
		if set := callers[m.Repo]; set != nil && m.Symbol != "" {
			// grep's enclosing symbol is a bare name; match it against caller base names.
			for c := range set {
				if baseName(c) == baseName(m.Symbol) {
					u.Confirmed = true
					break
				}
			}
		}
		res.Usages = append(res.Usages, u)
	}
	res.Usages = append(res.Usages, callerSiteUsages(s, ref, base, siteCallers, res.Usages)...)
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
func callerSiteUsages(s Store, ref, base string, callers map[string]map[string]bool, have []Usage) []Usage {
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
		for _, f := range files {
			byPath[f.Path] = f
		}
		for _, caller := range sortedSet(set) {
			path, span, ok := locateSymbol(files, caller)
			if !ok {
				continue
			}
			f := byPath[path]
			re := word
			if e := strings.ToLower(filepath.Ext(path)); e == ".robot" || e == ".resource" {
				re = robot
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
					In: baseName(caller), Confirmed: true})
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
