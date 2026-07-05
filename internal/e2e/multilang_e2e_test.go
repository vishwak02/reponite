//go:build sqlite && treesitter

package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// The whole moat on a NON-Go language: index two refs of a tiny Python repo
// through tree-sitter + SQLite. charge is byte-identical but its callee
// validate's body changes, so charge must come back behavior_changed. Also
// guards the roadmap's opening regression — indexing Python used to yield an
// empty index because the walk hardcoded ".go".
func TestEndToEndMultiLangPython(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "svc.py")
	st, err := sqlite.Open(filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	write(t, src, "def charge():\n    return validate()\n\ndef validate():\n    return True\n")
	if err := processing.IndexDir(st, "svc", "prod", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	write(t, src, "def charge():\n    return validate()\n\ndef validate():\n    return False\n")
	if err := processing.IndexDir(st, "svc", "HEAD", dir, version.NormVer); err != nil {
		t.Fatal(err)
	}

	syms := st.SymbolsAt("svc", "HEAD")
	if len(syms) == 0 {
		t.Fatal("python index empty — multi-language indexing failed (the .go-hardcode regression)")
	}
	var qid string
	for k := range syms {
		if strings.HasSuffix(k, "charge") {
			qid = k
		}
	}
	if qid == "" {
		t.Fatalf("charge not indexed; got keys %v", keysOf(syms))
	}
	origin, _ := st.SymbolAt("svc", qid, "HEAD")
	prod, _ := st.SymbolAt("svc", qid, "prod")
	if v := query.Compat(origin, prod).Verdict; v != query.BehaviorChanged {
		t.Fatalf("python charge across refs must be behavior_changed, got %s", v)
	}
}

func keysOf(m map[string]query.SymbolRef) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
