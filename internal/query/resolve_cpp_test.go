package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// C++ spellings resolve to reponite's dir.Class.method ids: Class::method,
// ns::Class::method (the namespace is not part of the id), ::ns::f. A bare
// method name is never matched across classes from Class::method.
func TestResolveSymbolCppSpellings(t *testing.T) {
	m := storage.NewMem()
	for _, q := range []string{"planner/src.Planner.publish", "planner/src.Planner.Planner", "monitor/src.Monitor.publish", "util/src.clampSpeed"} {
		_ = m.Put("platform", "HEAD", q, storage.SymbolRecord{Lang: "cpp"})
	}
	for in, want := range map[string]string{
		"Planner::publish":                  "planner/src.Planner.publish",
		"platform::Planner::publish":        "planner/src.Planner.publish",
		"::platform::nav::Planner::Planner": "planner/src.Planner.Planner",
		"util::clampSpeed":                  "util/src.clampSpeed",
	} {
		got := query.ResolveSymbol(m, "platform", "HEAD", in)
		if len(got) != 1 || got[0] != want {
			t.Errorf("%s -> %v, want %s", in, got, want)
		}
	}
	if got := query.ResolveSymbol(m, "platform", "HEAD", "Executor::publish"); len(got) != 0 {
		t.Errorf("Class::method must not fall back to every method of that name: %v", got)
	}
	near := query.Suggest(m, "platform", "HEAD", "Executor::publish", 5)
	if len(near) == 0 {
		t.Error("a qualified miss should suggest same-named members of other classes")
	}
}
