//go:build treesitter

package processing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// IndexDir applies the exclusion stack — defaults (vendor/ …), the repo's
// .reponiteignore, and --exclude globs — at index time, so vendored trees never
// reach the store (P1: rr_sootballs bundling external/ros_comm polluted every
// search surface).
func TestIndexDirHonorsIgnores(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"src/main.cpp":                 "void keep() {}\n",
		"scripts/tool.py":              "def keep_too():\n    pass\n",
		"vendor/lib.cpp":               "void vendored() {}\n",       // default set
		"external/ros_comm/roscpp.cc":  "void ros_internal() {}\n",   // .reponiteignore
		"logs/gen.py":                  "def generated():\n    pass\n", // --exclude
		".reponiteignore":              "external/\n",
	}
	for p, content := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	m := storage.NewMem()
	if err := IndexDirWith(m, "r", "HEAD", dir, 1, IndexOptions{Excludes: []string{"logs/**"}}); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}
	for _, f := range m.Files("r", "HEAD") {
		got[f.Path] = true
	}
	for _, want := range []string{"src/main.cpp", "scripts/tool.py"} {
		if !got[want] {
			t.Errorf("%s should be indexed; got %v", want, got)
		}
	}
	for _, bad := range []string{"vendor/lib.cpp", "external/ros_comm/roscpp.cc", "logs/gen.py"} {
		if got[bad] {
			t.Errorf("%s must be excluded (got %v)", bad, got)
		}
	}
	// The excluded trees' symbols must not exist either.
	syms := m.SymbolsAt("r", "HEAD")
	for name := range syms {
		if name == "vendor.vendored" || name == "external/ros_comm.ros_internal" || name == "logs.generated" {
			t.Errorf("excluded symbol leaked into the store: %s", name)
		}
	}
}
