// grep.go implements the lexical/grep matcher — the base of the retrieval ladder
// (architecture ext §10A). A trigram index over file content yields candidate
// files for a literal query (a file must contain every trigram of the literal);
// candidates are then verified exactly, and each hit is fused with its enclosing
// symbol so a match is one hop from the graph. A regex with no literal atoms
// falls back to a bounded full scan, labeled in the result. Pure and stdlib-only
// (Go regexp), so it is unit-tested in-sandbox (ADR-018); the production adapter
// persists the same trigram index in SQLite over content-addressed raw blobs.
package query

import (
	"regexp"
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
	Fixed bool // treat pattern as a literal string, not a regex
	Limit int  // max matches returned (<=0 uses the default)
}

// Match is one hit, annotated with its enclosing symbol.
type Match struct {
	Path   string
	Line   int
	Text   string
	Symbol string
}

// GrepResult is the token-lean search result.
type GrepResult struct {
	Matches   []Match
	Total     int
	Truncated bool
	Scanned   int
	Note      string
}

const defaultGrepLimit = 50

// Grep runs a literal or regex search, prefiltering by trigram where possible.
func (ix *TrigramIndex) Grep(pattern string, opt GrepOptions) (GrepResult, error) {
	limit := opt.Limit
	if limit <= 0 {
		limit = defaultGrepLimit
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
	if literal && len(pattern) >= 3 {
		candidates = ix.candidatesFor(pattern)
	} else {
		candidates = ix.allFiles()
		if !literal {
			note = "regex without literal atoms: full scan (no trigram prefilter)"
		}
	}

	match := func(line string) bool {
		if literal {
			return strings.Contains(line, pattern)
		}
		return re.MatchString(line)
	}

	res := GrepResult{Scanned: len(candidates), Note: note}
	for _, fi := range candidates {
		f := ix.files[fi]
		for ln, text := range strings.Split(f.Content, "\n") {
			if !match(text) {
				continue
			}
			res.Total++
			if len(res.Matches) < limit {
				res.Matches = append(res.Matches, Match{
					Path: f.Path, Line: ln + 1, Text: text, Symbol: enclosing(f.Symbols, ln+1),
				})
			}
		}
	}
	res.Truncated = res.Total > len(res.Matches)
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
