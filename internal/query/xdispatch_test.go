package query

import "testing"

type dispatchStore struct {
	implsStore
	snaps map[string]RefSnapshot
}

func (s dispatchStore) Snapshot(repo, ref string) RefSnapshot { return s.snaps[repo] }
func (s dispatchStore) SymbolAt(repo, symbol, ref string) (SymbolRef, bool) {
	r, ok := s.syms[repo][symbol]
	return r, ok
}

// A platform calls its plugin interface; the plugin lives in another repo.
// The platform's caller sees the plugin as a callee, and the plugin's method
// sees the platform's caller.
func TestCrossRepoDispatch(t *testing.T) {
	platform := `namespace wm {
class Splitter { public: virtual void forget(int id) = 0; };
}`
	app := `class AppSplitter : public wm::Splitter { void forget(int id) override; };
class MockSplitter : public wm::Splitter { void forget(int id) override; };`
	s := dispatchStore{
		implsStore: implsStore{
			filesStore: filesStore{files: map[string][]File{
				"platform": {{Path: "wm/include/wm/splitter.h", Content: platform}},
				"app":      {{Path: "app/src/splitter.cpp", Content: app}, {Path: "app/test/mock.cpp", Content: ""}},
			}},
			syms: map[string]map[string]SymbolRef{
				"platform": {"wm/src.Manager.update": {}, "wm/include/wm.Splitter.forget": {}},
				"app":      {"app/src.AppSplitter.forget": {}, "app/src.MockSplitter.forget": {}},
			},
		},
		snaps: map[string]RefSnapshot{
			"platform": {Callees: map[string][]Callee{
				"wm/src.Manager.update": {{Name: "wm/include/wm.Splitter.forget", ResolutionMethod: "receiver-typed", Confidence: 0.85}},
			}},
			"app": {Callees: map[string][]Callee{}},
		},
	}
	r := WithCrossRepoDispatch(s, "platform", "v1", Context(s, "platform", "v1", "wm/src.Manager.update", false), false)
	var got []CalleeEdge
	for _, e := range r.CalleeEdges {
		if e.Repo != "" {
			got = append(got, e)
		}
	}
	if len(got) != 1 || got[0].Name != "app:app/src.AppSplitter.forget" || got[0].ResolutionMethod != MethodXRepoOverride {
		t.Errorf("cross-repo override (mock excluded): %+v", r.CalleeEdges)
	}
	c := WithCrossRepoDispatch(s, "app", "v1", Context(s, "app", "v1", "app/src.AppSplitter.forget", false), false)
	if len(c.CallerEdges) != 1 || c.CallerEdges[0].Name != "platform:wm/src.Manager.update" || c.CallerEdges[0].Via != "wm/include/wm.Splitter.forget" {
		t.Errorf("caller through the base in another repo: %+v", c.CallerEdges)
	}
}
