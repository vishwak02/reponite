package query

import "testing"

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

func TestGrepRegexNoAtomFullScan(t *testing.T) {
	ix := BuildTrigramIndex(sampleFiles())
	r, err := ix.Grep("valid.*Card", GrepOptions{})
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
