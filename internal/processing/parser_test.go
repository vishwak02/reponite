//go:build treesitter

package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
)

func canonOf(t *testing.T, src string) string {
	root, err := ParseGo([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return string(content.Canon(root, 1))
}

// Validates the adapter + canon() on real tree-sitter Go trees, one property per
// assertion so a CI failure pinpoints the cause and prints the canon bytes.
func TestParseGoCanonIntegration(t *testing.T) {
	base := canonOf(t, "package p\nfunc Add(a, b int) int { return a + b }\n")
	if len(base) == 0 {
		t.Fatal("canon produced empty output")
	}

	// (1) whitespace-only reformat must not change canon (whitespace is not a node).
	if got := canonOf(t, "package p\nfunc Add( a,b  int ) int {return a+b}\n"); got != base {
		t.Fatalf("whitespace-only reformat changed canon:\n base=%q\n got =%q", base, got)
	}

	// (2) a doc comment must be dropped from canon (differs from base only by the comment).
	if got := canonOf(t, "package p\n// Add sums two ints.\nfunc Add(a, b int) int { return a + b }\n"); got != base {
		t.Fatalf("doc comment leaked into canon (not dropped):\n base=%q\n got =%q", base, got)
	}

	// (3) an operator change must change canon.
	if got := canonOf(t, "package p\nfunc Add(a, b int) int { return a - b }\n"); got == base {
		t.Fatalf("operator change (+ -> -) did not change canon: %q", got)
	}
}
