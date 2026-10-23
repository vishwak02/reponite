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

// A test build recompiles every package that imports the package under test:
// a generated mock imported by an external test package is type-checked as a
// variant of its own ("mock [app.test]"), and calls into it are still the
// tree's — as is an explicitly instantiated generic function (F[T](x)).
func TestGoTypesTestVariantsAndGenerics(t *testing.T) {
	dir := t.TempDir()
	writeMod(t, dir, "go.mod", "module tt\n\ngo 1.21\n")
	writeMod(t, dir, "app/app.go", "package app\n\ntype Store interface{ Get() int }\n\nfunc Total(s Store) int { return s.Get() }\n")
	writeMod(t, dir, "app/inner_test.go", "package app\n\nimport \"testing\"\n\nfunc TestInner(t *testing.T) {}\n")
	writeMod(t, dir, "app/mock/store.go", "package mock\n\nimport \"tt/app\"\n\nvar _ app.Store = (*Store)(nil)\n\ntype Store struct{ n int }\n\nfunc NewStore(n int) *Store { return &Store{n} }\n\nfunc (s *Store) Get() int { return s.n }\n\nfunc (s *Store) Expect() *Store { return s }\n")
	writeMod(t, dir, "app/total_test.go", "package app_test\n\nimport (\n\t\"testing\"\n\n\t\"tt/app\"\n\t\"tt/app/mock\"\n\t\"tt/ring\"\n)\n\nfunc TestTotal(t *testing.T) {\n\ts := mock.NewStore(2)\n\ts.Expect()\n\t_ = ring.New[int](4)\n\tif app.Total(s) != 2 {\n\t\tt.Fatal()\n\t}\n}\n")
	writeMod(t, dir, "ring/ring.go", "package ring\n\ntype Ring[T any] struct{ items []T }\n\nfunc New[T any](n int) *Ring[T] { return &Ring[T]{make([]T, n)} }\n")

	st, err := sqlite.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := processing.IndexDir(st, "tt", "HEAD", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}
	callees := st.Snapshot("tt", "HEAD").Callees["app.TestTotal"]
	for _, want := range []string{"app/mock.NewStore", "app/mock.Store.Expect", "ring.New", "app.Total"} {
		found := false
		for _, c := range callees {
			if c.Name == want {
				found = true
				if c.ResolutionMethod != processing.MethodTypes {
					t.Errorf("%s: want a go-types edge, got %+v", want, c)
				}
			}
		}
		if !found {
			t.Errorf("TestTotal must call %s; callees %+v", want, callees)
		}
	}
}
