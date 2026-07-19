// grep.go implements the lexical/grep matcher — the base of the retrieval ladder
// (architecture ext §10A). A trigram index over file content yields candidate
// files for a literal query (a file must contain every trigram of the literal);
// candidates are then verified exactly, and each hit is fused with its enclosing
// symbol so a match is one hop from the graph. A regex prefilters through its
// required literals (AND-of-ORs — an alternation ORs its branches' candidate
// sets, never intersects across branches); a regex with no usable literal atoms
// falls back to a full scan, labeled in the result. Candidate selection only
// ever over-approximates: a search must never return fewer matches than ground
// truth. Pure and stdlib-only (Go regexp + regexp/syntax), so it is unit-tested
// in-sandbox (ADR-018); the production adapter persists the same trigram index
// in SQLite over content-addressed raw blobs.
package query

import (
	"regexp"
	"regexp/syntax"
	"sort"
	"strings"
)

// SymbolSpan is an enclosing symbol's line range within a file (1-based, inclusive).
type SymbolSpan struct {
	Name      string
	StartLine int
	EndLine   int
}

// File is one indexed file's raw content plus its symbol spans (for fusion).
type File struct {
	Path    string
	Content string
	Symbols []SymbolSpan
}

// TrigramIndex maps each trigram to the files that contain it (content-addressed
// dedup means this is built once per unique file in production).
type TrigramIndex struct {
	files    []File
	postings map[string][]int
}

// BuildTrigramIndex indexes the trigrams of every file's content.
func BuildTrigramIndex(files []File) *TrigramIndex {
	ix := &TrigramIndex{files: files, postings: map[string][]int{}}
	for i, f := range files {
		for tg := range trigrams(f.Content) {
			ix.postings[tg] = append(ix.postings[tg], i)
		}
	}
	return ix
}

func trigrams(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for i := 0; i+3 <= len(s); i++ {
		set[s[i:i+3]] = struct{}{}
	}
	return set
}

// GrepOptions controls a search.
type GrepOptions struct {
	Fixed  bool // treat pattern as a literal string, not a regex
	Limit  int  // max matches returned (0 = default, <0 = unlimited)
	Offset int  // matches to skip before the returned window (paging)
}

// Match is one hit, annotated with its enclosing symbol. Repo is set on
// fleet-wide greps (repo="*") so a hit stays attributable to its source repo.
type Match struct {
	Repo   string
	Path   string
	Line   int
	Text   string
	Symbol string
}

// GrepResult is the token-lean search result. The counts mean:
//   - Total: every matching LINE found (the ground truth count) — independent
//     of the returned window.
//   - Matches: the [Offset, Offset+Limit) window of those, in deterministic
//     (repo, path, line) order, so limit/offset paging walks the full set.
//   - Truncated: more matches exist AFTER this window (Offset+len(Matches) <
//     Total) — page forward with a larger offset to get them.
//   - Scanned: candidate FILES examined (post trigram prefilter), not lines
//     and not the match count.
type GrepResult struct {
	Matches   []Match
	Total     int
	Truncated bool
	Offset    int
	Scanned   int
	Note      string
}

const defaultGrepLimit = 50

// effectiveLimit resolves a GrepOptions.Limit: 0 = default, <0 = unlimited.
func effectiveLimit(limit int) int {
	if limit == 0 {
		return defaultGrepLimit
	}
	return limit
}

// Grep runs a literal or regex search, prefiltering by trigram where possible.
// Matches are emitted in (path, line) order regardless of store file order, so
// an Offset/Limit window is stable across calls (honest paging).
func (ix *TrigramIndex) Grep(pattern string, opt GrepOptions) (GrepResult, error) {
	limit := effectiveLimit(opt.Limit)
	if opt.Offset < 0 {
		opt.Offset = 0
	}
	literal := opt.Fixed || regexp.QuoteMeta(pattern) == pattern
	var re *regexp.Regexp
	if !literal {
		var err error
		if re, err = regexp.Compile(pattern); err != nil {
			return GrepResult{}, err
		}
	}

	var candidates []int
	note := ""
	switch {
	case literal && len(pattern) >= 3:
		candidates = ix.candidatesFor(pattern)
	case literal:
		candidates = ix.allFiles()
	default:
		var ok bool
		if candidates, ok = ix.regexCandidates(pattern); !ok {
			candidates = ix.allFiles()
			note = "regex without literal atoms: full scan (no trigram prefilter)"
		}
	}

	match := func(line string) bool {
		if literal {
			return strings.Contains(line, pattern)
		}
		return re.MatchString(line)
	}

	// Path-sorted candidates make match order deterministic regardless of the
	// store's file order — without this, an offset window shifts between calls.
	sort.Slice(candidates, func(i, j int) bool { return ix.files[candidates[i]].Path < ix.files[candidates[j]].Path })

	res := GrepResult{Scanned: len(candidates), Offset: opt.Offset, Note: note}
	for _, fi := range candidates {
		f := ix.files[fi]
		for ln, text := range strings.Split(f.Content, "\n") {
			if !match(text) {
				continue
			}
			res.Total++
			if res.Total > opt.Offset && (limit < 0 || len(res.Matches) < limit) {
				res.Matches = append(res.Matches, Match{
					Path: f.Path, Line: ln + 1, Text: text, Symbol: enclosing(f.Symbols, ln+1),
				})
			}
		}
	}
	res.Truncated = opt.Offset+len(res.Matches) < res.Total
	return res, nil
}

func (ix *TrigramIndex) allFiles() []int {
	out := make([]int, len(ix.files))
	for i := range ix.files {
		out[i] = i
	}
	return out
}

// candidatesFor returns files containing every trigram of the literal pattern —
// a necessary condition for containing the literal, so verification never misses.
func (ix *TrigramIndex) candidatesFor(pattern string) []int {
	var tgs []string
	for tg := range trigrams(pattern) {
		tgs = append(tgs, tg)
	}
	if len(tgs) == 0 {
		return ix.allFiles()
	}
	count := make(map[int]int)
	for _, tg := range tgs {
		for _, fi := range ix.postings[tg] {
			count[fi]++
		}
	}
	var out []int
	for fi, c := range count {
		if c == len(tgs) {
			out = append(out, fi)
		}
	}
	sort.Ints(out)
	return out
}

// regexCandidates prefilters candidate files for a regex pattern. It derives an
// AND-of-ORs of literals every match must contain (requiredLiteralSets), then
// evaluates it over the trigram index: union of per-literal candidates within a
// set, intersection across sets. The result is always a SUPERSET of the files
// containing a match — an alternation ORs its branches' candidates instead of
// intersecting trigrams across branches (which selected nothing) — so the
// verify pass never misses. ok=false means no usable literal constraint exists
// (e.g. a branch with only short/case-folded literals): caller must full-scan.
func (ix *TrigramIndex) regexCandidates(pattern string) (candidates []int, ok bool) {
	re, err := syntax.Parse(pattern, syntax.Perl)
	if err != nil {
		return nil, false // regexp.Compile accepted it; be safe and full-scan
	}
	var sets [][]string
	for _, set := range requiredLiteralSets(re) {
		if usableLiteralSet(set) {
			sets = append(sets, set)
		}
	}
	if len(sets) == 0 {
		return nil, false
	}
	var result map[int]struct{}
	for _, set := range sets {
		union := map[int]struct{}{}
		for _, lit := range set {
			for _, fi := range ix.candidatesFor(lit) {
				union[fi] = struct{}{}
			}
		}
		if result == nil {
			result = union
			continue
		}
		for fi := range result {
			if _, hit := union[fi]; !hit {
				delete(result, fi)
			}
		}
	}
	out := make([]int, 0, len(result))
	for fi := range result {
		out = append(out, fi)
	}
	sort.Ints(out)
	return out, true
}

// usableLiteralSet reports whether every alternative in the set can prefilter:
// one literal too short for a trigram makes the whole OR-set select all files.
func usableLiteralSet(set []string) bool {
	if len(set) == 0 {
		return false
	}
	for _, lit := range set {
		if len(lit) < 3 {
			return false
		}
	}
	return true
}

// requiredLiteralSets walks a parsed regex and returns literal requirements in
// AND-of-ORs form: every string the regex matches must contain at least one
// literal from EACH returned set. Conservative — a construct it can't reason
// about contributes no requirement (never a wrong one), so candidate selection
// can only over-approximate. nil means no requirement derivable.
func requiredLiteralSets(re *syntax.Regexp) [][]string {
	switch re.Op {
	case syntax.OpLiteral:
		if re.Flags&syntax.FoldCase != 0 {
			return nil // case-insensitive: the byte-trigram index can't filter
		}
		return [][]string{{string(re.Rune)}}
	case syntax.OpConcat:
		var out [][]string
		for _, sub := range re.Sub {
			out = append(out, requiredLiteralSets(sub)...)
		}
		return out
	case syntax.OpCapture:
		return requiredLiteralSets(re.Sub[0])
	case syntax.OpPlus:
		return requiredLiteralSets(re.Sub[0]) // occurs at least once
	case syntax.OpRepeat:
		if re.Min >= 1 {
			return requiredLiteralSets(re.Sub[0])
		}
		return nil
	case syntax.OpAlternate:
		// A match satisfies SOME branch, so the requirement is a single OR-set:
		// one representative set per branch, unioned. Every branch must
		// contribute a usable set, else the alternation constrains nothing.
		var union []string
		for _, sub := range re.Sub {
			best := strongestSet(requiredLiteralSets(sub))
			if best == nil {
				return nil
			}
			union = append(union, best...)
		}
		return [][]string{union}
	}
	return nil
}

// strongestSet picks the branch requirement that filters hardest: among usable
// sets, the one whose shortest literal is longest. nil when none is usable.
func strongestSet(sets [][]string) []string {
	var best []string
	bestMin := 0
	for _, set := range sets {
		if !usableLiteralSet(set) {
			continue
		}
		min := len(set[0])
		for _, lit := range set[1:] {
			if len(lit) < min {
				min = len(lit)
			}
		}
		if min > bestMin {
			bestMin, best = min, set
		}
	}
	return best
}

// enclosing returns the innermost symbol span containing line, or "".
func enclosing(spans []SymbolSpan, line int) string {
	best := ""
	bestSize := int(^uint(0) >> 1)
	for _, s := range spans {
		if line >= s.StartLine && line <= s.EndLine {
			if size := s.EndLine - s.StartLine; size < bestSize {
				bestSize, best = size, s.Name
			}
		}
	}
	return best
}
