package interfaces

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

func TestCompatJSON(t *testing.T) {
	rep := query.CompatReport{
		Symbol: "getUserV2",
		Origin: query.RepoRef{Repo: "user-service", Ref: "HEAD"},
		Verdicts: []query.CompatVerdict{
			{Repo: "billing", Ref: "v4.0.0", CompatResult: query.CompatResult{Verdict: query.ShapeChanged, Confidence: 1, Detail: "signature differs"}},
			{Repo: "analytics", Ref: "prod", CompatResult: query.CompatResult{Verdict: query.BehaviorChanged, Confidence: 0.72}},
		},
		Meta: query.Meta{Repo: "user-service", Ref: "HEAD", Warnings: []string{"x@y not indexed"}},
	}
	out, err := CompatJSON(rep)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(out)) {
		t.Fatalf("invalid JSON:\n%s", out)
	}
	for _, want := range []string{`"symbol": "getUserV2"`, `"verdict": "shape_changed"`, `"verdict": "behavior_changed"`, `"confidence": 0.72`, `"_meta"`, `"warnings"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("JSON missing %s:\n%s", want, out)
		}
	}
}

func TestGrepJSON(t *testing.T) {
	g := query.GrepResult{Matches: []query.Match{{Path: "a.go", Line: 4, Text: "x", Symbol: "F"}}, Total: 1, Scanned: 1}
	out, err := GrepJSON(g)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"path": "a.go"`, `"line": 4`, `"symbol": "F"`, `"total": 1`} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s in %s", want, out)
		}
	}
}

func TestDiffJSON(t *testing.T) {
	d := query.DiffReport{Repo: "r", From: "a", To: "b", Changes: []query.SymbolChange{{Name: "X", Kind: query.ChangeBehavior, Confidence: 0.5}}}
	out, _ := DiffJSON(d)
	if !strings.Contains(out, `"change": "behavior_changed"`) || !strings.Contains(out, `"name": "X"`) {
		t.Fatalf("diff json: %s", out)
	}
}
