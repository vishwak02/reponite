package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestIsExportedName(t *testing.T) {
	cases := []struct {
		lang, name string
		want       bool
	}{
		{"go", "GetUser", true},
		{"go", "getUser", false},   // Go: lowercase = unexported
		{"go", "_internal", false}, // underscore is not uppercase
		{"python", "get_user", true},
		{"python", "_private", false}, // Python: leading underscore = private
		{"javascript", "handler", true},
		{"typescript", "_helper", false},
		{"java", "doThing", true},   // no name convention -> conservative (exported)
		{"rust", "compute", true},   // no name convention -> conservative
		{"rust", "_scratch", false}, // underscore still respected
		{"", "Anything", true},      // unknown language -> conservative default
	}
	for _, c := range cases {
		if got := query.IsExportedName(c.lang, c.name); got != c.want {
			t.Errorf("IsExportedName(%q, %q) = %v; want %v", c.lang, c.name, got, c.want)
		}
	}
}

// The regression the fix targets: a diff must carry each symbol's language, so a
// Python public function removal (lowercase, but public) is caught, while the old
// Go-uppercase rule would have wrongly treated it as unexported and skipped it.
func TestDiffCarriesLangForExportDetection(t *testing.T) {
	base := storage.NewMem()
	base.Put("svc", "base", "svc.get_user", storage.SymbolRecord{Lang: "python", SignatureHash: "s", BehaviorHash: "b"})
	head := storage.NewMem()
	// get_user removed at head.

	a := base.SymbolsAt("svc", "base")
	b := head.SymbolsAt("svc", "base") // empty
	changes := query.DiffRefs(a, b)
	if len(changes) != 1 || changes[0].Kind != query.ChangeRemoved {
		t.Fatalf("expected get_user removed, got %+v", changes)
	}
	if changes[0].Lang != "python" {
		t.Fatalf("diff must carry the symbol's language, got %q", changes[0].Lang)
	}
	// The Python public function (lowercase) must count as an exported break.
	if !query.IsExportedName(changes[0].Lang, "get_user") {
		t.Fatal("a lowercase Python public function must be treated as exported (the old Go-only rule missed this)")
	}
}
