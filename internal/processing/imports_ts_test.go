//go:build treesitter

package processing

import "testing"

// Validates import extraction + qualified-call resolution against REAL
// tree-sitter trees for every language (CI under -tags treesitter). The fake-AST
// tests in imports_test.go pin the same shapes in-sandbox; this proves they
// match the actual grammars, end to end into external references.
func TestImportsAndExternalRefsRealTrees(t *testing.T) {
	cases := []struct {
		name, ext, src string
		wantRefs       []ExternalRefKey // (module, name) the caller must depend on
		absentRefs     []ExternalRefKey // must NOT be produced (intra-repo / relative)
	}{
		{
			name: "go", ext: ".go",
			src: `package p
import (
	bar "github.com/x/bar"
	"fmt"
)
func F() { bar.Do(); fmt.Println(); local() }
func local() {}
`,
			wantRefs:   []ExternalRefKey{{"github.com/x/bar", "Do"}, {"fmt", "Println"}},
			absentRefs: []ExternalRefKey{{"", "local"}},
		},
		{
			name: "python", ext: ".py",
			src: `import numpy as np
from foo.bar import baz, qux as q
from . import sibling
def f():
    np.array()
    baz()
    q()
    sibling.run()
`,
			// np.array -> numpy.array; baz -> foo.bar.baz; q (alias) -> foo.bar.qux.
			wantRefs:   []ExternalRefKey{{"numpy", "array"}, {"foo.bar", "baz"}, {"foo.bar", "qux"}},
			absentRefs: []ExternalRefKey{{"foo.bar", "q"}}, // alias must resolve to the real name qux
		},
		{
			name: "javascript", ext: ".js",
			src: `import { bar, baz as qux } from 'bar-mod';
import * as ns from 'ns-mod';
import './side-effect';
function f() { bar(); qux(); ns.thing(); }
`,
			wantRefs: []ExternalRefKey{{"bar-mod", "bar"}, {"bar-mod", "baz"}, {"ns-mod", "thing"}},
		},
		{
			name: "java", ext: ".java",
			src: `package com.example;
import com.foo.Bar;
import static com.foo.Util.helper;
class C { void f() { Bar.x(); helper(); local(); } }
`,
			wantRefs:   []ExternalRefKey{{"com.foo", "x"}, {"com.foo.Util", "helper"}},
			absentRefs: []ExternalRefKey{{"", "local"}},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules, ok := RulesForExt(c.ext)
			if !ok {
				t.Fatalf("no rules for %s", c.ext)
			}
			root, _, err := parseFileRules([]byte(c.src), c.ext, rules)
			if err != nil {
				t.Fatal(err)
			}
			byLocal := importsByLocal(Imports(root, rules))
			// Gather every external ref across all extracted callables.
			got := map[ExternalRefKey]bool{}
			for _, s := range Extract(root, rules, 1) {
				for _, r := range resolveExternalRefs(s.Name, s.QualifiedCalls, byLocal) {
					got[ExternalRefKey{r.Module, r.Name}] = true
				}
			}
			for _, w := range c.wantRefs {
				if !got[w] {
					t.Errorf("missing external ref %+v; got %v", w, keys(got))
				}
			}
			for _, a := range c.absentRefs {
				if got[a] {
					t.Errorf("unexpected external ref %+v (should be intra-repo/aliased): %v", a, keys(got))
				}
			}
		})
	}
}

func keys(m map[ExternalRefKey]bool) []ExternalRefKey {
	out := make([]ExternalRefKey, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
