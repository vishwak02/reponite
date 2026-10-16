//go:build treesitter

package processing

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// Schema and configuration files are stored — including the .env files and
// CI workflows that live under dot-paths — while other dot-dirs stay skipped.
func TestIndexDirStoresDataFiles(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".env":                     "APP_PORT=8000\n",
		".github/workflows/ci.yml": "env:\n  APP_PORT: 9000\n",
		".github/scripts/x.py":     "def x():\n    pass\n",
		".venv/lib/site.py":        "def y():\n    pass\n",
		"Dockerfile":               "FROM scratch\nENV APP_PORT=80\n",
		"db/001.sql":               "CREATE TABLE orders (id int);\n",
		"api/order.proto":          "message Order {\n  string id = 1;\n}\n",
		"app/main.py":              "def main():\n    pass\n",
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
	got := map[string]bool{}
	for _, f := range m.Files("r", "HEAD") {
		got[f.Path] = true
	}
	for _, p := range []string{".env", ".github/workflows/ci.yml", "Dockerfile", "db/001.sql", "api/order.proto", "app/main.py"} {
		if !got[p] {
			t.Errorf("%s must be stored (have %v)", p, got)
		}
	}
	for _, p := range []string{".github/scripts/x.py", ".venv/lib/site.py"} {
		if got[p] {
			t.Errorf("%s must stay skipped", p)
		}
	}
}
