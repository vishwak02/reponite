package query

import (
	"reflect"
	"testing"
)

// resolveIndexed answers exactly what resolveIn answers.
func TestResolveIndexedMatchesResolveIn(t *testing.T) {
	syms := map[string]SymbolRef{}
	for _, k := range []string{
		"pkg.f", "pkg/sub.f", "pkg/sub.Cls.f", "a/b.mod.Cls.method", "internal/storage",
		"frontend.package:dependencies:@tanstack/react-router", "web.Makefile:build/all",
		"docs.README:Setup/Install", "x.y.z", "f", "Cls.f", "dir/components/motor.f", "lib.mod.g",
	} {
		syms[k] = SymbolRef{Present: true}
	}
	idx := baseIndexOf(syms)
	for _, q := range []string{
		"f", "Cls.f", "sub.f", "storage", "react-router", "@tanstack/react-router", "all", "Install",
		"z", "y.z", "motor.f", "components/motor.f", "g", "method", "Cls.method", "mod.Cls.method",
		"nothing", "Cls::f", "", "pkg.f",
	} {
		want, got := resolveIn(syms, q), resolveIndexed(idx, syms, q)
		if len(want) == 0 && len(got) == 0 {
			continue
		}
		if !reflect.DeepEqual(want, got) {
			t.Errorf("q=%q: resolveIn %v, resolveIndexed %v", q, want, got)
		}
	}
}
