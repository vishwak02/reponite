//go:build sqlite && treesitter

package e2e

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/reponite/reponite/internal/processing"
	"github.com/reponite/reponite/internal/query"
	"github.com/reponite/reponite/internal/storage/sqlite"
	"github.com/reponite/reponite/internal/version"
)

func write(t *testing.T, path, content string) {
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The full moat on real source: index two refs of a tiny repo through
// tree-sitter + SQLite; Charge is byte-identical but validateCard's body
// changes, so Charge must come back behavior_changed.
func TestEndToEndCompatAcrossRefs(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "billing.go")
	st, err := sqlite.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	write(t, src, "package billing\n\nfunc Charge() error { return validateCard() }\nfunc validateCard() error { return nil }\n")
	if err := processing.IndexDir(st, "billing", "prod", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	write(t, src, "package billing\n\nfunc Charge() error { return validateCard() }\nfunc validateCard() error { return errBad }\n")
	if err := processing.IndexDir(st, "billing", "HEAD", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	origin, ok := st.SymbolAt("billing", "Charge", "HEAD")
	if !ok {
		t.Fatal("Charge not indexed at HEAD")
	}
	prod, _ := st.SymbolAt("billing", "Charge", "prod")
	if v := query.Compat(origin, prod).Verdict; v != query.BehaviorChanged {
		t.Fatalf("end-to-end: Charge across refs must be behavior_changed, got %s", v)
	}
}
