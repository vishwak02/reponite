package processing

import "testing"

func TestHeuristicLanguages(t *testing.T) {
	cases := []struct {
		path, src string
		want      map[string][]string // symbol -> callees it must include
	}{
		{"app/Orders.kt", "class Orders {\n  fun save(o: Order) {\n    validate(o)\n    repo.insert(o)\n  }\n  private fun validate(o: Order) = check(o.id > 0)\n}\n",
			map[string][]string{"Orders": nil, "save": {"validate", "insert"}, "validate": nil}},
		{"lib/orders.rb", "module Shop\n  class Orders\n    def save(o)\n      validate(o)\n    end\n\n    def validate(o)\n      raise 'bad' unless o\n    end\n  end\nend\n",
			map[string][]string{"Orders": nil, "save": {"validate"}, "validate": nil}},
		{"src/Orders.cs", "public class Orders {\n    public async Task Save(Order o)\n    {\n        Validate(o);\n    }\n    private void Validate(Order o) { }\n}\n",
			map[string][]string{"Orders": nil, "Save": {"Validate"}, "Validate": nil}},
		{"main.swift", "struct Orders {\n  func save(_ o: Order) {\n    validate(o)\n  }\n  func validate(_ o: Order) {}\n}\n",
			map[string][]string{"Orders": nil, "save": {"validate"}}},
	}
	for _, c := range cases {
		pf := heuristicFile(c.path, c.src, 1)
		got := map[string][]string{}
		for _, s := range pf.Symbols {
			got[s.Name] = s.Callees
		}
		for name, callees := range c.want {
			have, ok := got[name]
			if !ok {
				t.Errorf("%s: no symbol %s (have %v)", c.path, name, got)
				continue
			}
			for _, cl := range callees {
				found := false
				for _, h := range have {
					found = found || h == cl
				}
				if !found {
					t.Errorf("%s: %s must call %s (calls %v)", c.path, name, cl, have)
				}
			}
		}
		if len(pf.Spans) != len(pf.Symbols) || pf.Lang == "" {
			t.Errorf("%s: spans %d symbols %d lang %q", c.path, len(pf.Spans), len(pf.Symbols), pf.Lang)
		}
	}
}
