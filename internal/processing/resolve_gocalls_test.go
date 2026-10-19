package processing

import "testing"

// A caller whose package type-checked in full never has a call guessed by
// name; one whose package did not build keeps the checker's verdicts and
// resolves its other calls by Go's scoping.
func TestResolveGoCallsCheckedVsPartial(t *testing.T) {
	x := siteIndex{nodeSet: map[string]bool{"spice.NewClient": true, "app.helper": true}, methods: map[string][]string{}}
	imports := map[string]ImportBinding{"spice": {Local: "spice", Module: "example.com/m/spice"}}
	mods := map[string]string{"": "example.com/m"}
	qcs := []QualifiedCall{{Qualifier: "spice", Name: "NewClient", Chain: 2}, {Name: "helper", Chain: 1}, {Qualifier: "log", Name: "Info", Chain: 2}}
	got := func(precise map[string][]string) map[string]string {
		out := map[string]string{}
		for _, c := range resolveGoCalls("app", "", qcs, imports, mods, x, precise) {
			out[c.Name] = c.ResolutionMethod
		}
		return out
	}
	partial := got(map[string][]string{"Info": {goExternalMark}})
	if partial["spice.NewClient"] != MethodResolved || partial["app.helper"] != MethodResolved || partial["Info"] != MethodExternal {
		t.Errorf("partial package: %v", partial)
	}
	checked := got(map[string][]string{goCheckedKey: nil, "helper": {"app.helper"}})
	if checked["app.helper"] != MethodTypes || checked["NewClient"] != MethodExternal {
		t.Errorf("checked package: %v", checked)
	}
}
