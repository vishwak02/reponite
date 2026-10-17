package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

type skipStore struct {
	*storage.Mem
	skipped []query.SkippedPath
}

func (s skipStore) Skipped(repo, ref string) []query.SkippedPath { return s.skipped }

func TestCoverageCountsStoredAndSkipped(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "v1", query.File{Path: "src/a.go", Content: "package a\n"})
	m.PutFile("r", "v1", query.File{Path: "docs/x.md", Content: "# x\n"})
	s := skipStore{m, []query.SkippedPath{{Path: "img/a.png", Reason: "binary format"}, {Path: "img/b.png", Reason: "binary format"}}}
	res := query.Coverage(s, "r", "v1")
	if len(res.Repos) != 1 {
		t.Fatalf("repos %+v", res.Repos)
	}
	r := res.Repos[0]
	if r.Stored != 2 || r.Skipped != 2 || r.Reasons["binary format"] != 2 || !r.Recorded {
		t.Fatalf("coverage %+v", r)
	}
	if r.Kinds[0].Kind != ".png" || r.Kinds[0].Skipped["binary format"] != 2 {
		t.Fatalf("kinds %+v", r.Kinds)
	}
}
