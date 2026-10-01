package query

import "testing"

type implsStore struct {
	filesStore
	syms map[string]map[string]SymbolRef
}

func (s implsStore) SymbolsAt(repo, ref string) map[string]SymbolRef { return s.syms[repo] }

// A platform repo calls a plugin interface; the implementations live in an
// application repo, one of them indirectly (a subclass of a subclass).
func TestImplsAcrossRepos(t *testing.T) {
	platform := `namespace wm {
class WorkSplitter {
 public:
  virtual void forget(int id) = 0;  // class Fake : public WorkSplitter { (a comment)
};
class DefaultSplitter final : public WorkSplitter { void forget(int id) override; };
}`
	app := `namespace app {
class AppWorkSplitter : public wm::WorkSplitter, private Helper<int, std::map<A, B>> {
 public:
  void forget(int id) override;
};
class PickSplitter : public AppWorkSplitter {};
class MockSplitter : public wm::WorkSplitter { void forget(int id) override; };
}
PLUGINLIB_EXPORT_CLASS(app::AppWorkSplitter, wm::WorkSplitter)`
	s := implsStore{
		filesStore: filesStore{files: map[string][]File{
			"platform": {{Path: "work_manager/include/work_manager/splitter.h", Content: platform}},
			"app":      {{Path: "app_work_splitter/src/splitter.cpp", Content: app}},
		}},
		syms: map[string]map[string]SymbolRef{
			"platform": {"work_manager/src.DefaultSplitter.forget": {}},
			"app":      {"app_work_splitter/src.AppWorkSplitter.forget": {}, "app_work_splitter/src.MockSplitter.forget": {}},
		},
	}
	r := Impls(s, FleetRepo, "v1", "wm::WorkSplitter::forget", 0)
	if r.Base != "WorkSplitter" || r.Method != "forget" {
		t.Fatalf("target parse: %q %q", r.Base, r.Method)
	}
	got := map[string]Impl{}
	for _, i := range r.Impls {
		got[i.Class] = i
	}
	if !got["MockSplitter"].Test || r.Impls[len(r.Impls)-1].Class != "MockSplitter" {
		t.Errorf("a mock is listed after production classes: %+v", r.Impls)
	}
	delete(got, "MockSplitter")
	if len(got) != 3 {
		t.Fatalf("want 3 impls (comment ignored), got %+v", r.Impls)
	}
	sw := got["AppWorkSplitter"]
	if !sw.Plugin || sw.Depth != 1 || sw.Repo != "app" || sw.Line != 2 || sw.Method != "app_work_splitter/src.AppWorkSplitter.forget" {
		t.Errorf("plugin impl: %+v", sw)
	}
	if len(sw.Bases) != 2 || sw.Bases[1] != "Helper" {
		t.Errorf("bases: %v", sw.Bases)
	}
	if p := got["PickSplitter"]; p.Depth != 2 || p.Method != "" {
		t.Errorf("indirect subclass inherits the method: %+v", p)
	}
	if d := got["DefaultSplitter"]; d.Method == "" || d.Plugin {
		t.Errorf("platform default: %+v", d)
	}
	if r.Impls[len(r.Impls)-2].Class != "PickSplitter" {
		t.Errorf("classes that define the method come first: %+v", r.Impls)
	}
	if r := Impls(s, FleetRepo, "v1", "NoSuchBase", 0); len(r.Impls) != 0 || r.Note == "" {
		t.Errorf("unknown base: %+v", r)
	}
}
