package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func sym(name, kind, sig, body string, callees ...string) Symbol {
	return Symbol{Name: name, Kind: kind, Signature: sig, CanonBody: []byte(body), Callees: callees}
}

// Full moat, end to end through the indexer: Charge is byte-identical across
// refs but prod's validateCard body differs, so Charge's behavior_hash differs.
func TestIndexFilesMoatEndToEnd(t *testing.T) {
	m := storage.NewMem()
	prod := []ParsedFile{{Path: "billing.go", Content: "x", Symbols: []Symbol{
		sym("Charge", "function", "func Charge() error", "return validateCard()", "validateCard"),
		sym("validateCard", "function", "func validateCard() error", "old-logic"),
	}}}
	head := []ParsedFile{{Path: "billing.go", Content: "x", Symbols: []Symbol{
		sym("Charge", "function", "func Charge() error", "return validateCard()", "validateCard"),
		sym("validateCard", "function", "func validateCard() error", "new-logic"),
	}}}
	if err := IndexFiles(m, "billing", "prod", 1, prod); err != nil {
		t.Fatal(err)
	}
	if err := IndexFiles(m, "billing", "HEAD", 1, head); err != nil {
		t.Fatal(err)
	}

	origin, _ := m.SymbolAt("billing", "Charge", "HEAD")
	prodRef, _ := m.SymbolAt("billing", "Charge", "prod")
	r := query.Compat(origin, prodRef)
	if r.Verdict != query.BehaviorChanged {
		t.Fatalf("Charge across refs must be behavior_changed; got %s", r.Verdict)
	}
	if r.Confidence != ConfResolved {
		t.Fatalf("behavior verdict confidence should reflect the in-repo name-resolved edge (%v), got %v", ConfResolved, r.Confidence)
	}
	vh, _ := m.SymbolAt("billing", "validateCard", "HEAD")
	vp, _ := m.SymbolAt("billing", "validateCard", "prod")
	if query.Compat(vh, vp).Verdict != query.BehaviorChanged {
		t.Fatal("validateCard itself must be behavior_changed (its text changed)")
	}
}

// Resolution provenance flows end to end: an in-repo callee resolves
// (name-resolved@0.9); an unindexed callee is an opaque external leaf
// (unresolved-external@0.6), whose weaker confidence caps the caller's
// behavior_conf (invariant 5).
func TestIndexFilesResolutionProvenance(t *testing.T) {
	m := storage.NewMem()
	files := []ParsedFile{{Path: "a.go", Content: "x", Symbols: []Symbol{
		sym("Charge", "function", "func Charge() error", "b", "validateCard", "externalLog"),
		sym("validateCard", "function", "func validateCard() error", "b"),
	}}}
	if err := IndexFiles(m, "billing", "HEAD", 1, files); err != nil {
		t.Fatal(err)
	}

	got := map[string]query.Callee{}
	for _, c := range m.Snapshot("billing", "HEAD").Callees["Charge"] {
		got[c.Name] = c
	}
	if c := got["validateCard"]; c.ResolutionMethod != MethodResolved || c.Confidence != ConfResolved {
		t.Fatalf("in-repo callee must be name-resolved@%v: %+v", ConfResolved, c)
	}
	if c := got["externalLog"]; c.ResolutionMethod != MethodExternal || c.Confidence != ConfExternal {
		t.Fatalf("unindexed callee must be unresolved-external@%v: %+v", ConfExternal, c)
	}

	charge, _ := m.SymbolAt("billing", "Charge", "HEAD")
	if charge.BehaviorConf != ConfExternal {
		t.Fatalf("Charge behavior_conf must be capped by its weakest edge (%v), got %v", ConfExternal, charge.BehaviorConf)
	}
	vc, _ := m.SymbolAt("billing", "validateCard", "HEAD")
	if vc.BehaviorConf != 1.0 {
		t.Fatalf("validateCard has no callees, behavior_conf must be 1.0, got %v", vc.BehaviorConf)
	}
}

// #1 keystone: two packages defining the same bare name must be distinct nodes
// (no conflation), and a call to that name across packages must be flagged
// ambiguous rather than silently attributed to one.
func TestIndexFilesPackageQualifiedNoCollision(t *testing.T) {
	m := storage.NewMem()
	files := []ParsedFile{
		{Path: "storage/mem.go", Content: "x", Symbols: []Symbol{
			sym("Put", "method", "func Put() error", "mem-logic"),
			sym("MemOnly", "function", "func MemOnly()", "b"),
		}},
		{Path: "sqlite/store.go", Content: "x", Symbols: []Symbol{
			sym("Put", "method", "func Put() error", "sqlite-logic"), // same name, diff pkg + body
		}},
		{Path: "proc/index.go", Content: "x", Symbols: []Symbol{
			sym("IndexFiles", "function", "func IndexFiles()", "b", "Put", "MemOnly"),
		}},
	}
	if err := IndexFiles(m, "r", "HEAD", 1, files); err != nil {
		t.Fatal(err)
	}

	memPut, ok1 := m.SymbolAt("r", "storage.Put", "HEAD")
	sqlPut, ok2 := m.SymbolAt("r", "sqlite.Put", "HEAD")
	if !ok1 || !ok2 {
		t.Fatal("both Put definitions must exist as distinct qualified symbols")
	}
	if memPut.BehaviorHash == sqlPut.BehaviorHash {
		t.Fatal("distinct Put bodies must have distinct behavior hashes (no conflation)")
	}

	edges := map[string]query.Callee{}
	for _, c := range m.Snapshot("r", "HEAD").Callees["proc.IndexFiles"] {
		edges[c.Name] = c
	}
	if e := edges["Put"]; e.ResolutionMethod != MethodAmbiguous || e.Confidence != ConfAmbiguous {
		t.Fatalf("cross-pkg call to Put must be ambiguous, not silently resolved: %+v", e)
	}
	if e := edges["storage.MemOnly"]; e.ResolutionMethod != MethodResolved {
		t.Fatalf("repo-unique callee must resolve to its qualified id: %+v", edges)
	}
}

// Reindexing a ref replaces its symbols; a symbol that vanished must not linger.
func TestIndexFilesReindexReplaces(t *testing.T) {
	m := storage.NewMem()
	if err := IndexFiles(m, "r", "HEAD", 1, []ParsedFile{{Path: "p/a.go", Content: "x", Symbols: []Symbol{
		sym("A", "function", "func A()", "b"), sym("B", "function", "func B()", "b"),
	}}}); err != nil {
		t.Fatal(err)
	}
	if err := IndexFiles(m, "r", "HEAD", 1, []ParsedFile{{Path: "p/a.go", Content: "x", Symbols: []Symbol{
		sym("A", "function", "func A()", "b2"),
	}}}); err != nil {
		t.Fatal(err)
	}
	if _, ok := m.SymbolAt("r", "p.B", "HEAD"); ok {
		t.Fatal("reindex must drop the vanished symbol p.B (no stale rows)")
	}
	if _, ok := m.SymbolAt("r", "p.A", "HEAD"); !ok {
		t.Fatal("reindex must keep p.A")
	}
}

func TestIndexFilesDiffAndGrep(t *testing.T) {
	m := storage.NewMem()
	_ = IndexFiles(m, "r", "a", 1, []ParsedFile{{
		Path: "a.go", Content: "func Keep(){}\nfunc Gone(){}\n",
		Symbols: []Symbol{sym("Keep", "function", "func Keep()", "b"), sym("Gone", "function", "func Gone()", "b")},
		Spans:   []query.SymbolSpan{{Name: "Keep", StartLine: 1, EndLine: 1}, {Name: "Gone", StartLine: 2, EndLine: 2}},
	}})
	_ = IndexFiles(m, "r", "b", 1, []ParsedFile{{
		Path: "a.go", Content: "func Keep(){}\nfunc New(){}\n",
		Symbols: []Symbol{sym("Keep", "function", "func Keep()", "b"), sym("New", "function", "func New()", "b")},
	}})

	kinds := map[string]query.ChangeKind{}
	for _, c := range query.DiffRefsBy(m, "r", "a", "b").Changes {
		kinds[c.Name] = c.Kind
	}
	if kinds["Keep"] != query.ChangeUnchanged || kinds["Gone"] != query.ChangeRemoved || kinds["New"] != query.ChangeAdded {
		t.Fatalf("diff via indexed store: %+v", kinds)
	}

	g, err := query.GrepRepo(m, "r", "a", "Keep", query.GrepOptions{Fixed: true})
	if err != nil || len(g.Matches) == 0 || g.Matches[0].Symbol != "Keep" {
		t.Fatalf("grep via indexed store: %+v err=%v", g.Matches, err)
	}
}
