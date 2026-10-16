//go:build treesitter

package processing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// YAML parameter files are stored (content only) so `topics` can resolve a
// topic name read from a parameter; an oversized YAML is not a param file.
func TestIndexDirStoresParamFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"planner/config/topics.yaml": "status_topic: /robot_status\n",
		"planner/data/huge.yml":      "k: " + strings.Repeat("x", maxParamFileBytes) + "\n",
		"planner/src/a.cpp":          "void a() {}\n",
	}
	for p, c := range files {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := storage.NewMem()
	if err := IndexDirWith(m, "r", "HEAD", dir, 1, IndexOptions{}); err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range m.Files("r", "HEAD") {
		got[f.Path] = f.Content
	}
	if got["planner/config/topics.yaml"] != files["planner/config/topics.yaml"] {
		t.Errorf("param YAML must be stored: %v", got)
	}
	if _, ok := got["planner/data/huge.yml"]; ok {
		t.Error("an oversized YAML must not be stored")
	}
}
