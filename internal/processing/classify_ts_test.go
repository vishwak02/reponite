//go:build treesitter

package processing

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// Every tracked file is stored, parsed or skipped with a reason; text with no
// specific handler is stored with structure recognized from its content.
func TestClassifyAnyFile(t *testing.T) {
	dir := t.TempDir()
	write := func(p, s string) {
		full := filepath.Join(dir, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("etc/plans/DropBin.pml", "{\n  \"name\": \"DropBin\",\n  \"states\": [{\"name\": \"Lower\", \"id\": 1}]\n}\n")
	write("maps/site.tmx", "<?xml version=\"1.0\"?>\n<map>\n <objectgroup name=\"spots\">\n  <object id=\"3\" name=\"SC-01-03\" type=\"spot\"/>\n </objectgroup>\n</map>\n")
	write("data/countries.csv", "code,name\nJP,Japan\n")
	write("big/fixture.csv", "a,b\n"+strings.Repeat("1,2\n", 20000))
	write("deploy/hosts", "[robots]\namr01\n")
	write("img/logo.png", "\x89PNG\r\n\x1a\n\x00\x00")
	write("model/weights.dat", "abc\x00def")
	write("assets/big.bin.txt", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 12\n")
	write("maps/align.py", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 12\n")
	write("maps/site.yaml", "version https://git-lfs.github.com/spec/v1\noid sha256:abc\nsize 12\n")
	write(".github/scripts/release.sh", "#!/bin/bash\nrelease() { echo hi; }\n")
	write(".venv/lib/site.py", "def x(): pass\n")
	write("justfile", "build:\n\tgo build\ntest: build\n\tgo test\n")

	m := storage.NewMem()
	var sum IndexSummary
	if err := IndexDirWith(m, "r", "HEAD", dir, 1, IndexOptions{Report: func(s IndexSummary) { sum = s }}); err != nil {
		t.Fatal(err)
	}
	stored := map[string]string{}
	syms := map[string][]string{}
	for _, f := range m.Files("r", "HEAD") {
		stored[f.Path] = f.Content
		for _, s := range f.Symbols {
			syms[f.Path] = append(syms[f.Path], s.Name)
		}
	}
	for _, p := range []string{"etc/plans/DropBin.pml", "maps/site.tmx", "data/countries.csv", "deploy/hosts",
		".github/scripts/release.sh", "justfile"} {
		if _, ok := stored[p]; !ok {
			t.Errorf("%s must be stored", p)
		}
	}
	has := func(p, name string) bool {
		for _, s := range syms[p] {
			if s == name {
				return true
			}
		}
		return false
	}
	if !has("maps/site.tmx", "site:object:SC-01-03") || !has("maps/site.tmx", "site:objectgroup:spots") {
		t.Errorf("tmx sections: %v", syms["maps/site.tmx"])
	}
	if !has("data/countries.csv", "countries:columns:name") {
		t.Errorf("csv sections: %v", syms["data/countries.csv"])
	}
	if len(syms["etc/plans/DropBin.pml"]) == 0 {
		t.Errorf("a JSON-shaped file gets sections whatever its extension")
	}
	if !has(".github/scripts/release.sh", "release") {
		t.Errorf("a script in .github is parsed: %v", syms[".github/scripts/release.sh"])
	}
	reasons := map[string]string{}
	for _, s := range sum.Skipped {
		reasons[s.Path] = s.Reason
	}
	for p, want := range map[string]string{"big/fixture.csv": "large data table", "img/logo.png": "binary format",
		"model/weights.dat": "binary content", "assets/big.bin.txt": "Git LFS pointer", ".venv/": "tool or cache",
		"maps/align.py": "Git LFS pointer", "maps/site.yaml": "Git LFS pointer"} {
		if !strings.Contains(reasons[p], want) {
			t.Errorf("%s: reason %q, want %q", p, reasons[p], want)
		}
		if _, ok := stored[p]; ok {
			t.Errorf("%s must not be stored", p)
		}
	}
}
