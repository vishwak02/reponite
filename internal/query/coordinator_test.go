package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func rc(sym, sig, beh string, conf float64, callees ...string) storage.SymbolRecord {
	cs := make([]query.Callee, len(callees))
	for i, c := range callees {
		cs[i] = query.Callee{Name: c, Confidence: 1}
	}
	return storage.SymbolRecord{
		SymbolHash: content.Hash(sym), SignatureHash: content.Hash(sig),
		BehaviorHash: content.Hash(beh), BehaviorConf: conf, Callees: cs,
	}
}

func verdictFor(vs []query.CompatVerdict, ref string) query.Verdict {
	for _, v := range vs {
		if v.Ref == ref {
			return v.Verdict
		}
	}
	return ""
}

func TestCompatSymbolAcrossRefs(t *testing.T) {
	m := storage.NewMem()
	m.Put("billing", "HEAD", "Charge", rc("c", "sig", "behNEW", 1))
	m.Put("billing", "prod", "Charge", rc("c", "sig", "behOLD", 1))
	rep, err := query.CompatSymbol(m, query.RepoRef{Repo: "billing", Ref: "HEAD"}, "Charge",
		[]query.RepoRef{{Repo: "billing", Ref: "prod"}, {Repo: "billing", Ref: "v1"}})
	if err != nil {
		t.Fatal(err)
	}
	if v := verdictFor(rep.Verdicts, "prod"); v != query.BehaviorChanged {
		t.Fatalf("prod verdict = %s", v)
	}
	if v := verdictFor(rep.Verdicts, "v1"); v != query.Absent {
		t.Fatalf("v1 verdict = %s", v)
	}
	if len(rep.Meta.Warnings) != 1 {
		t.Fatalf("expected one not-indexed warning, got %v", rep.Meta.Warnings)
	}
}

func TestCompatSymbolOriginMissing(t *testing.T) {
	m := storage.NewMem()
	m.Put("billing", "HEAD", "Charge", rc("c", "sig", "beh", 1))
	if _, err := query.CompatSymbol(m, query.RepoRef{Repo: "billing", Ref: "HEAD"}, "Ghost", nil); err == nil {
		t.Fatal("missing origin symbol must error")
	}
}

func TestDiffRefsBy(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "a", "Keep", rc("k", "s", "b", 1))
	m.Put("r", "a", "Gone", rc("g", "s", "b", 1))
	m.Put("r", "b", "Keep", rc("k", "s", "b", 1))
	m.Put("r", "b", "New", rc("n", "s", "b", 1))
	kinds := map[string]query.ChangeKind{}
	for _, c := range query.DiffRefsBy(m, "r", "a", "b").Changes {
		kinds[c.Name] = c.Kind
	}
	if kinds["Gone"] != query.ChangeRemoved || kinds["New"] != query.ChangeAdded || kinds["Keep"] != query.ChangeUnchanged {
		t.Fatalf("diff via coordinator: %+v", kinds)
	}
}

func TestRootCauseBy(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "from", "A", rc("A", "s", "A0", 1, "B"))
	m.Put("r", "from", "B", rc("B0", "s", "B0", 1))
	m.Put("r", "to", "A", rc("A", "s", "A1", 1, "B")) // propagation (text unchanged)
	m.Put("r", "to", "B", rc("B1", "s", "B1", 1))     // origin (text changed)
	res := query.RootCauseBy(m, "r", "A", "from", "to")
	if !res.Changed || len(res.Origins) != 1 || res.Origins[0].Name != "B" || res.Origins[0].Kind != query.KindText {
		t.Fatalf("rootcause via coordinator: %+v", res)
	}
}

func TestGrepRepo(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("billing", "HEAD", query.File{
		Path:    "charge.go",
		Content: "package billing\n\nfunc Charge() error {\n\treturn validateCard()\n}\n",
		Symbols: []query.SymbolSpan{{Name: "Charge", StartLine: 3, EndLine: 5}},
	})
	res, err := query.GrepRepo(m, "billing", "HEAD", "validateCard", query.GrepOptions{Fixed: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Matches) != 1 || res.Matches[0].Symbol != "Charge" || res.Matches[0].Line != 4 {
		t.Fatalf("grep via coordinator: %+v", res.Matches)
	}
}

func TestSearchName(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "GetUser", rc("a", "s", "b", 1))
	m.Put("r", "HEAD", "GetOrder", rc("b", "s", "b", 1))
	m.Put("r", "HEAD", "helper", rc("c", "s", "b", 1))
	hits := query.SearchName(m, "r", "HEAD", "Get", false)
	if len(hits) != 2 || hits[0].Name != "GetOrder" || hits[1].Name != "GetUser" {
		t.Fatalf("search: %+v", hits)
	}
}

func TestSearchNameExcludesTestsByDefault(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "GetUser", rc("a", "s", "b", 1))
	m.Put("r", "HEAD", "TestGetUser", rc("t", "s", "b", 1))
	if hits := query.SearchName(m, "r", "HEAD", "GetUser", false); len(hits) != 1 || hits[0].Name != "GetUser" {
		t.Fatalf("default search must exclude TestGetUser: %+v", hits)
	}
	if hits := query.SearchName(m, "r", "HEAD", "GetUser", true); len(hits) != 2 {
		t.Fatalf("includeTests must return both: %+v", hits)
	}
}
