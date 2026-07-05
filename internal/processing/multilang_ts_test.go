//go:build treesitter

package processing

import "testing"

// Validates the LangRules node-type assumptions against REAL tree-sitter trees
// for every non-Go language (runs in CI under -tags treesitter, where the
// grammars are fetched). The fake-AST tests in lang_test.go pin the same shapes
// in-sandbox; this proves they match the actual grammars.
func TestMultiLangRealTrees(t *testing.T) {
	cases := []struct {
		name, ext, src                             string
		wantType, wantMethod, wantRecv, wantCallee string
		wantFunc                                   string // "" to skip
	}{
		{
			name: "python", ext: ".py",
			src:      "class Svc:\n    def charge(self):\n        return validate()\n\ndef helper():\n    pass\n",
			wantType: "Svc", wantMethod: "charge", wantRecv: "Svc", wantCallee: "validate", wantFunc: "helper",
		},
		{
			name: "javascript", ext: ".js",
			src:      "class Svc {\n  charge() { return validate(); }\n}\nfunction helper() {}\n",
			wantType: "Svc", wantMethod: "charge", wantRecv: "Svc", wantCallee: "validate", wantFunc: "helper",
		},
		{
			name: "typescript", ext: ".ts",
			src:      "interface Repo {}\nclass Svc {\n  charge(): void { validate(); }\n}\nfunction helper(): void {}\n",
			wantType: "Svc", wantMethod: "charge", wantRecv: "Svc", wantCallee: "validate", wantFunc: "helper",
		},
		{
			name: "java", ext: ".java",
			src:      "class Payment {\n  Payment() {}\n  void charge() { validate(); }\n}\n",
			wantType: "Payment", wantMethod: "charge", wantRecv: "Payment", wantCallee: "validate", wantFunc: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rules, ok := RulesForExt(c.ext)
			if !ok {
				t.Fatalf("no rules for %s", c.ext)
			}
			root, spans, err := parseFileRules([]byte(c.src), c.ext, rules)
			if err != nil {
				t.Fatal(err)
			}
			syms := Extract(root, rules, 1)
			if find(syms, c.wantType) == nil {
				t.Fatalf("type %q not extracted; got %v", c.wantType, symNames(syms))
			}
			m := find(syms, c.wantMethod)
			if m == nil {
				t.Fatalf("method %q not extracted; got %v", c.wantMethod, symNames(syms))
			}
			if m.Recv != c.wantRecv {
				t.Fatalf("method %q recv = %q, want %q", c.wantMethod, m.Recv, c.wantRecv)
			}
			if !hasCallee(m.Callees, c.wantCallee) {
				t.Fatalf("method %q callees = %v, want %q", c.wantMethod, m.Callees, c.wantCallee)
			}
			if c.wantFunc != "" && find(syms, c.wantFunc) == nil {
				t.Fatalf("function %q not extracted; got %v", c.wantFunc, symNames(syms))
			}
			spanFound := false
			for _, s := range spans {
				if s.Name == c.wantMethod {
					spanFound = true
				}
			}
			if !spanFound {
				t.Fatalf("no grep-fusion span for %q", c.wantMethod)
			}
		})
	}
}

func symNames(syms []Symbol) []string {
	out := make([]string, len(syms))
	for i := range syms {
		out[i] = syms[i].Name
	}
	return out
}

func hasCallee(callees []string, want string) bool {
	for _, c := range callees {
		if c == want {
			return true
		}
	}
	return false
}
