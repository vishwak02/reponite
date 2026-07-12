package query

import (
	"strconv"
	"strings"
	"testing"
)

func sampleFiles() []File {
	return []File{
		{
			Path:    "charge.go",
			Content: "package billing\n\nfunc Charge() error {\n\treturn validateCard()\n}\n",
			Symbols: []SymbolSpan{{"Charge", 3, 5}},
		},
		{
			Path:    "util.go",
			Content: "package billing\n\nfunc helper() {}\n",
			Symbols: []SymbolSpan{{"helper", 3, 3}},
		},
	}
}

func TestGrepLiteralWithSymbolFusion(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, err := ix.Grep("validateCard", GrepOptions{Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 {
		t.Fatalf("want 1 match, got %d", len(r.Matches))
	}
	m := r.Matches[0]
	if m.Path != "charge.go" || m.Line != 4 || m.Symbol != "Charge" {
		t.Fatalf("wrong match/fusion: %+v", m)
	}
}

func TestGrepCandidateSelection(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, _ := ix.Grep("helper", GrepOptions{Fixed: true})
	if len(r.Matches) != 1 || r.Matches[0].Path != "util.go" || r.Matches[0].Symbol != "helper" {
		t.Fatalf("expected single util.go/helper match, got %+v", r.Matches)
	}
}

func TestGrepLiteralRegexUsesPrefilter(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, err := ix.Grep("Charge", GrepOptions{}) // no metachars -> literal path, no note
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].Line != 3 || r.Note != "" {
		t.Fatalf("literal regex should prefilter with no note: %+v", r)
	}
}

func TestGrepRegexWithAtomsUsesPrefilter(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, err := ix.Grep("valid.*Card", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].Symbol != "Charge" {
		t.Fatalf("regex should match validateCard line: %+v", r.Matches)
	}
	if r.Note != "" {
		t.Fatalf("regex with literal atoms (valid, Card) should prefilter, not full-scan: %q", r.Note)
	}
	if r.Scanned != 1 {
		t.Fatalf("prefilter should narrow to the one file containing both atoms, scanned %d", r.Scanned)
	}
}

func TestGrepRegexNoAtomFullScan(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, err := ix.Grep("v.l.d", GrepOptions{}) // every literal < 3 bytes: unfilterable
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Matches) != 1 || r.Matches[0].Symbol != "Charge" {
		t.Fatalf("regex should match validateCard line: %+v", r.Matches)
	}
	if r.Note == "" {
		t.Fatal("no-atom regex must be labeled as a full scan")
	}
}

// alternationFiles is a corpus where TODO and FIXME never co-occur in one file,
// so a trigram intersection across the raw alternation string selects nothing.
func alternationFiles() []File {
	return []File{
		{Path: "a.go", Content: "// TODO: refactor\nfunc a() {}\n"},
		{Path: "b.go", Content: "// FIXME: broken\nfunc b() {}\n"},
		{Path: "c.go", Content: "// TODO: one\n// FIXME: two\nfunc c() {}\n"},
		{Path: "d.go", Content: "func d() {} // clean\n"},
	}
}

// The correctness law P0-1 enforces: matches(a|b) == matches(a) ∪ matches(b).
func TestGrepAlternationEqualsUnion(t *testing.T) {
	ix := BuildTrigramIndex(alternationFiles())
	key := func(m Match) string { return m.Path + ":" + strconv.Itoa(m.Line) }
	union := map[string]bool{}
	for _, p := range []string{"TODO", "FIXME"} {
		r, err := ix.Grep(p, GrepOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range r.Matches {
			union[key(m)] = true
		}
	}
	r, err := ix.Grep("TODO|FIXME", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != len(union) || len(r.Matches) != len(union) {
		t.Fatalf("alternation must equal union of branches: want %d matches, got total=%d returned=%d (%+v)",
			len(union), r.Total, len(r.Matches), r.Matches)
	}
	for _, m := range r.Matches {
		if !union[key(m)] {
			t.Fatalf("alternation returned a match not in the union: %+v", m)
		}
	}
	if r.Note != "" {
		t.Fatalf("both branches have trigrams; expected prefilter, got note %q", r.Note)
	}
	if r.Scanned != 3 {
		t.Fatalf("prefilter should OR branch candidates (a,b,c), scanned %d", r.Scanned)
	}
}

func TestGrepAlternationUnfilterableBranchFullScans(t *testing.T) {
	files := append(alternationFiles(), File{Path: "e.go", Content: "xy\n"})
	ix := BuildTrigramIndex(files)
	r, err := ix.Grep("TODO|xy", GrepOptions{}) // "xy" too short for a trigram
	if err != nil {
		t.Fatal(err)
	}
	if r.Note == "" {
		t.Fatal("a branch with no usable trigram must fall back to a labeled full scan")
	}
	want := 3 // TODO in a.go + c.go, xy in e.go
	if r.Total != want {
		t.Fatalf("full-scan fallback must still find every branch match: want %d, got %d (%+v)", want, r.Total, r.Matches)
	}
}

func TestGrepCaseInsensitiveRegexFullScans(t *testing.T) {
	ix := BuildTrigramIndex(alternationFiles())
	r, err := ix.Grep("(?i)todo", GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 2 { // TODO in a.go + c.go
		t.Fatalf("case-folded literal can't trigram-prefilter but must still match: got %d (%+v)", r.Total, r.Matches)
	}
	if r.Note == "" {
		t.Fatal("case-insensitive scan must be labeled as a full scan")
	}
}

func TestGrepNestedAlternationPrefilter(t *testing.T) {
	ix := BuildTrigramIndex(alternationFiles())
	// Concat of a required literal and a group alternation: candidates =
	// files(TODO|FIXME-comment marker) ∩ (files(refactor) ∪ files(broken)).
	r, err := ix.Grep(`// (TODO: refactor|FIXME: broken)`, GrepOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Total != 2 || r.Note != "" {
		t.Fatalf("nested alternation should prefilter and match a.go+b.go: %+v", r)
	}
}

func TestGrepNoMatch(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, _ := ix.Grep("nonexistent_symbol", GrepOptions{Fixed: true})
	if r.Total != 0 || len(r.Matches) != 0 {
		t.Fatalf("want zero matches, got %+v", r)
	}
}

func TestGrepLimitTruncation(t *testing.T) {
	files := []File{{Path: "x.txt", Content: "x\nx\nx\n"}} // 3 lines with 'x'
	ix := BuildTrigramIndex(files)
	r, _ := ix.Grep("x", GrepOptions{Fixed: true, Limit: 2})
	if r.Total != 3 || len(r.Matches) != 2 || !r.Truncated {
		t.Fatalf("want total 3, returned 2, truncated; got %+v", r)
	}
}

// Paging (P0-2): offset windows walk the full match set; truncated means
// "matches remain AFTER this window"; total is window-independent.
func TestGrepOffsetPaging(t *testing.T) {
	files := []File{{Path: "x.txt", Content: "x1\nx2\nx3\nx4\nx5\n"}}
	ix := BuildTrigramIndex(files)
	var walked []string
	for _, page := range []struct {
		offset, wantN int
		wantTrunc     bool
	}{{0, 2, true}, {2, 2, true}, {4, 1, false}} {
		r, err := ix.Grep("x", GrepOptions{Fixed: true, Limit: 2, Offset: page.offset})
		if err != nil {
			t.Fatal(err)
		}
		if r.Total != 5 || len(r.Matches) != page.wantN || r.Truncated != page.wantTrunc || r.Offset != page.offset {
			t.Fatalf("offset %d: want n=%d trunc=%v total=5, got %+v", page.offset, page.wantN, page.wantTrunc, r)
		}
		for _, m := range r.Matches {
			walked = append(walked, m.Text)
		}
	}
	if len(walked) != 5 || walked[0] != "x1" || walked[4] != "x5" {
		t.Fatalf("paging must walk every match exactly once, in order: %v", walked)
	}
}

func TestGrepUnlimited(t *testing.T) {
	var content strings.Builder
	for i := 0; i < 60; i++ {
		content.WriteString("match me\n") // > defaultGrepLimit lines
	}
	ix := BuildTrigramIndex([]File{{Path: "big.txt", Content: content.String()}})
	r, _ := ix.Grep("match", GrepOptions{Fixed: true, Limit: -1})
	if r.Total != 60 || len(r.Matches) != 60 || r.Truncated {
		t.Fatalf("limit -1 must return everything untruncated: total=%d returned=%d trunc=%v", r.Total, len(r.Matches), r.Truncated)
	}
}

// Match order must be (path, line) regardless of the order files entered the
// index — otherwise an offset window shifts between calls.
func TestGrepDeterministicOrderForPaging(t *testing.T) {
	files := []File{
		{Path: "zzz.txt", Content: "needle\n"},
		{Path: "aaa.txt", Content: "needle\n"},
		{Path: "mmm.txt", Content: "needle\n"},
	}
	ix := BuildTrigramIndex(files)
	r, _ := ix.Grep("needle", GrepOptions{Fixed: true})
	if len(r.Matches) != 3 || r.Matches[0].Path != "aaa.txt" || r.Matches[1].Path != "mmm.txt" || r.Matches[2].Path != "zzz.txt" {
		t.Fatalf("matches must be path-sorted for stable paging: %+v", r.Matches)
	}
}

func TestGrepInnermostEnclosing(t *testing.T) {
	content := "l1\nl2\nl3\ntarget\nl5\nl6\nl7\n"
	f := File{Path: "n.go", Content: content, Symbols: []SymbolSpan{{"outer", 1, 7}, {"inner", 3, 5}}}
	ix := BuildTrigramIndex([]File{f})
	r, _ := ix.Grep("target", GrepOptions{Fixed: true})
	if len(r.Matches) != 1 || r.Matches[0].Symbol != "inner" {
		t.Fatalf("must attribute to innermost enclosing symbol, got %+v", r.Matches)
	}
}

func TestGrepBadRegexErrors(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	if _, err := ix.Grep("(", GrepOptions{}); err == nil {
		t.Fatal("invalid regex must return an error")
	}
}
