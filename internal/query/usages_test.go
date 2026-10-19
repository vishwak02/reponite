package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Usages returns each call site with its line + enclosing function, and marks
// a site `confirmed` when that function is a resolved caller in the call graph.
func TestUsages(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path: "billing/charge.go",
		Content: "package billing\n" +
			"func Charge() error {\n" +
			"\treturn validateCard(n)\n" + // line 3: a real call inside Charge
			"}\n" +
			"func validateCard(n string) bool { return true }\n", // line 5: the definition (not a usage)
		Symbols: []query.SymbolSpan{
			{Name: "Charge", StartLine: 2, EndLine: 4},
			{Name: "validateCard", StartLine: 5, EndLine: 5},
		},
	})
	// Call graph: Charge calls validateCard (resolved).
	m.Put("r", "HEAD", "billing.Charge", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "billing.validateCard", ResolutionMethod: "name-resolved", Confidence: 0.9}},
	})
	m.Put("r", "HEAD", "billing.validateCard", storage.SymbolRecord{})

	res := query.Usages(m, "r", "HEAD", "validateCard")
	if res.Total != 1 {
		t.Fatalf("want 1 usage (the call in Charge; the definition excluded), got %d: %+v", res.Total, res.Usages)
	}
	u := res.Usages[0]
	if u.Line != 3 {
		t.Errorf("usage line = %d, want 3", u.Line)
	}
	if !strings.Contains(u.Text, "validateCard(n)") {
		t.Errorf("usage text should be the calling line, got %q", u.Text)
	}
	if u.In != "billing.Charge" {
		t.Errorf("enclosing function = %q, want billing.Charge", u.In)
	}
	if !u.Confirmed {
		t.Error("usage inside Charge (a resolved caller) must be confirmed")
	}
}

// Usages is the ground-truth call-site list for verify_edit/blast_radius, so it
// must NOT inherit grep's default 50-match window (P0-2 regression).
func TestUsagesNotCappedByGrepDefault(t *testing.T) {
	m := storage.NewMem()
	var b strings.Builder
	b.WriteString("package p\n")
	for i := 0; i < 60; i++ {
		b.WriteString("\thotpath(x)\n") // 60 call sites > grep's default 50
	}
	m.PutFile("r", "HEAD", query.File{Path: "p/many.go", Content: b.String()})
	res := query.Usages(m, "r", "HEAD", "hotpath")
	if res.Total != 60 || len(res.Usages) != 60 {
		t.Fatalf("usages must list every call site (60), got total=%d returned=%d", res.Total, len(res.Usages))
	}
}

// A lexical hit outside any known caller comes back unconfirmed, not dropped.
func TestUsagesUnconfirmedLexical(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "notes.go",
		Content: "package x\nfunc note() {\n\t// call helper() here later\n\thelper()\n}\n",
		Symbols: []query.SymbolSpan{{Name: "note", StartLine: 2, EndLine: 5}},
	})
	// No call-graph edge recorded for note->helper (e.g. helper unresolved).
	m.Put("r", "HEAD", "x.note", storage.SymbolRecord{})
	res := query.Usages(m, "r", "HEAD", "helper")
	if res.Total == 0 {
		t.Fatal("lexical call sites should still be reported")
	}
	for _, u := range res.Usages {
		if u.Confirmed {
			t.Errorf("no call-graph edge exists, so usages must be unconfirmed: %+v", u)
		}
	}
}

// A call written without parentheses — a shell function, a JSX element — is
// still a usage when the call graph has the caller: it is found in the
// caller's body and confirmed.
func TestUsagesWithoutParentheses(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "scripts/deploy.sh",
		Content: "check_repo() {\n  git status\n}\ndeploy() {\n  check_repo \"io\"\n  echo done\n}\n",
		Symbols: []query.SymbolSpan{{Name: "check_repo", StartLine: 1, EndLine: 3}, {Name: "deploy", StartLine: 4, EndLine: 7}},
	})
	m.PutFile("r", "HEAD", query.File{
		Path:    "web/Home.tsx",
		Content: "export function Home() {\n  return (\n    <Layout model={m} />\n  );\n}\n",
		Symbols: []query.SymbolSpan{{Name: "Home", StartLine: 1, EndLine: 5}},
	})
	m.Put("r", "HEAD", "scripts.deploy", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "scripts.check_repo", ResolutionMethod: "name-resolved", Confidence: 0.9}}})
	m.Put("r", "HEAD", "scripts.check_repo", storage.SymbolRecord{})
	m.Put("r", "HEAD", "web.Home", storage.SymbolRecord{
		Callees: []query.Callee{{Name: "web.Layout", ResolutionMethod: "name-resolved", Confidence: 0.9}}})
	m.Put("r", "HEAD", "web.Layout", storage.SymbolRecord{})

	if res := query.Usages(m, "r", "HEAD", "scripts.check_repo"); res.Total != 1 || res.Usages[0].Line != 5 || !res.Usages[0].Confirmed {
		t.Errorf("shell call: %+v", res.Usages)
	}
	if res := query.Usages(m, "r", "HEAD", "web.Layout"); res.Total != 1 || res.Usages[0].Line != 3 || res.Usages[0].In != "web.Home" {
		t.Errorf("JSX element: %+v", res.Usages)
	}
}

// A shell function called from top-level script code — no enclosing caller
// symbol, no parentheses — is still a usage; its definition is not.
func TestUsagesShellTopLevelCommand(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "v1", query.File{Path: "images/entrypoint.sh", Content: "#!/bin/bash\n_main() {\n  echo hi\n}\nif ! _is_sourced; then\n\t_main \"$@\"\nfi\n",
		Symbols: []query.SymbolSpan{{Name: "_main", StartLine: 2, EndLine: 4}}})
	res := query.Usages(m, "r", "v1", "_main")
	if res.Total != 1 || res.Usages[0].Line != 6 {
		t.Fatalf("usages: %+v", res.Usages)
	}
}
