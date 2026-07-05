//go:build sqlite && treesitter

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

func writeMod(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, p, content)
}

// go/types resolves a call whose base name is ambiguous across packages (a.T.Do
// vs b.U.Do) to the exact concrete target, upgrading the edge to go-types@1.0 —
// where name-based resolution alone could only report it ambiguous.
func TestGoTypesPreciseResolution(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "go.mod", "module tt\n\ngo 1.18\n")
	writeMod(t, dir, "a/a.go", "package a\n\ntype T struct{}\n\nfunc (T) Do() error { return nil }\n")
	writeMod(t, dir, "b/b.go", "package b\n\ntype U struct{}\n\nfunc (U) Do() error { return nil }\n")
	writeMod(t, dir, "c/c.go", "package c\n\nimport (\n\t\"tt/a\"\n\t\"tt/b\"\n)\n\nfunc Run() error { var t a.T; _ = b.U{}; return t.Do() }\n")

	st, err := sqlite.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := processing.IndexDir(st, "tt", "HEAD", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	var doEdge query.Callee
	for _, c := range st.Snapshot("tt", "HEAD").Callees["c.Run"] {
		if c.Name == "a.T.Do" || c.Name == "Do" {
			doEdge = c
		}
	}
	if doEdge.Name != "a.T.Do" {
		t.Fatalf("go/types must resolve t.Do() to a.T.Do, got %+v (callees: %+v)", doEdge, st.Snapshot("tt", "HEAD").Callees["c.Run"])
	}
	if doEdge.ResolutionMethod != processing.MethodTypes || doEdge.Confidence != processing.ConfTypes {
		t.Fatalf("precise edge must be go-types@%v, got %+v", processing.ConfTypes, doEdge)
	}
}
