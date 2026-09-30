//go:build treesitter

package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// A C++ repo shaped like the real regression: a plugin's Splitter::forgetWork
// called `it->second.erase(pos)` on a std::map value, and name-only resolution
// pinned it on the only `erase` the repo defines — an unrelated class's method —
// at name-resolved confidence. Call-site shape now decides.
func TestCppCallSiteResolution(t *testing.T) {
	files := map[string]string{
		"splitter/include/splitter.hpp": `
class Splitter {
 public:
  void forgetWork(int id);
  void helper(int id);
  static Splitter make(int n);
 private:
  void inlineCaller() { helper(1); }
};`,
		"splitter/src/splitter.cpp": `
void Splitter::helper(int id) {}
Splitter Splitter::make(int n) { return Splitter(); }
void Splitter::forgetWork(int id) {
  auto it = std::find_if(v.begin(), v.end(), [](int x){ return x; });
  it->second.erase(it);
  this->helper(id);
  helper(id);
  Splitter::make(3);
  registry_->uniqueOnly(id);
  sort(v);
}`,
		"assigner/src/assigner.cpp": `
void Assigner::erase(int id) {}
void Assigner::uniqueOnly(int id) {}
void sort(int v) {}`,
	}
	var parsed []ParsedFile
	for path, src := range files {
		ext := ".cpp"
		if path[len(path)-4:] == ".hpp" {
			ext = ".hpp"
		}
		rules, _ := RulesForExt(ext)
		root, spans, err := parseFileRules([]byte(src), ext, rules)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, ParsedFile{Path: path, Content: src, Lang: rules.Name,
			Symbols: Extract(root, rules, 1), Spans: spans})
	}
	m := storage.NewMem()
	if err := IndexFiles(m, "r", "v1", 1, parsed); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot("r", "v1")
	const caller = "splitter/src.Splitter.forgetWork"
	if _, ok := snap.Symbols[caller]; !ok {
		t.Fatalf("out-of-class definition should be qualified by its class; symbols: %v", symKeys(snap.Symbols))
	}
	got := map[string]query.Callee{}
	for _, c := range snap.Callees[caller] {
		got[c.Name] = c
	}
	want := func(name, method string) {
		t.Helper()
		c, ok := got[name]
		if !ok {
			t.Errorf("missing edge %s (have %v)", name, got)
			return
		}
		if c.ResolutionMethod != method {
			t.Errorf("%s: method %s, want %s", name, c.ResolutionMethod, method)
		}
	}
	if c, ok := got["assigner/src.Assigner.erase"]; ok {
		t.Errorf("member call on a std container was pinned on the repo's erase: %+v", c)
	}
	want("erase", MethodAmbiguous)                         // std member name, repo also defines it
	want("splitter/src.Splitter.helper", MethodResolved)   // this->helper and helper(): own class
	want("splitter/src.Splitter.make", MethodResolved)     // Splitter::make
	want("std::find_if", MethodExternal)                   // std:: is never the repo
	want("assigner/src.Assigner.uniqueOnly", MethodMember) // unique member, receiver type unproven
	want("assigner/src.sort", MethodResolved)              // plain call: old name rules
	for _, c := range snap.Callees[caller] {
		if c.Name == "begin" || c.Name == "end" {
			if c.ResolutionMethod != MethodExternal {
				t.Errorf("%s: %s, want external (no repo method)", c.Name, c.ResolutionMethod)
			}
		}
	}
	// The in-class definition resolves its unqualified call to its own class
	// even though the definition lives in another directory (header vs src).
	inl := snap.Callees["splitter/include.Splitter.inlineCaller"]
	if len(inl) != 1 || inl[0].Name != "splitter/src.Splitter.helper" {
		t.Errorf("inline caller edges: %+v", inl)
	}
}

func symKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
