//go:build treesitter

package processing

import (
	"testing"

	"github.com/reponite/reponite/internal/content"
)

// Validates the adapter + canon() end to end on real tree-sitter Go trees.
func TestParseGoCanonIntegration(t *testing.T) {
	withCommentSpaces := []byte("package p\n\n// Add sums two ints.\nfunc Add(a, b int) int { return a + b }\n")
	reformatted := []byte("package p\nfunc Add(a,b int) int{return a+b}\n")
	opChanged := []byte("package p\nfunc Add(a, b int) int { return a - b }\n")

	root, err := ParseGo(withCommentSpaces)
	if err != nil {
		t.Fatal(err)
	}
	if root.Type() != "source_file" {
		t.Fatalf("root type = %q, want source_file", root.Type())
	}

	c1 := content.Canon(root, 1)
	if len(c1) == 0 {
		t.Fatal("canon produced empty output")
	}

	r2, _ := ParseGo(reformatted)
	if string(c1) != string(content.Canon(r2, 1)) {
		t.Fatal("reformatting + comment removal must not change canon (reformat-invariance on real trees)")
	}

	r3, _ := ParseGo(opChanged)
	if string(c1) == string(content.Canon(r3, 1)) {
		t.Fatal("operator change (+ -> -) must change canon")
	}
}
