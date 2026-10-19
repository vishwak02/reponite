package query

import (
	"strings"
	"testing"
)

func flowFixture() dataStore {
	master := "policy:\n  accept_dup: true\nworkflow:\n  print:\n    at_pick:\n      enable: false\n"
	tpl := "{\n  \"policy_allow_dup\": {{ master.policy.accept_dup | lower }},\n  \"print_at_pick\": {{ master.workflow.print.at_pick.enable | lower }}\n}\n"
	cpp := "void Sync::update(const Json& site) {\n  getConfigParam(cfg.allow_dup, site, \"policy_allow_dup\");\n}\n"
	use := "void Splitter::check() {\n  if (!getConfig().allow_dup) { reject(); }\n}\n"
	return dataStore{filesStore{files: map[string][]File{
		"sites": {
			{Path: "sites/a/master.yaml", Content: master, Symbols: []SymbolSpan{
				{Name: "master:policy", StartLine: 1, EndLine: 2}, {Name: "master:policy:accept_dup", StartLine: 2, EndLine: 2},
				{Name: "master:workflow:print:at_pick", StartLine: 5, EndLine: 6}}},
			{Path: "template/v1/app/site.json.j2", Content: tpl},
		},
		"robot": {
			{Path: "common/src/sync.cpp", Content: cpp, Symbols: []SymbolSpan{{Name: "Sync.update", StartLine: 1, EndLine: 3}}},
			{Path: "splitter/src/splitter.cpp", Content: use, Symbols: []SymbolSpan{{Name: "Splitter.check", StartLine: 1, EndLine: 3}}},
		},
	}}}
}

// A key is followed from the file defining it through the template that
// renders it under another name to the code reading that name.
func TestFollowConfigKey(t *testing.T) {
	f := FollowConfigKey(flowFixture(), FleetRepo, "v1", "policy.accept_dup")
	if f == nil {
		t.Fatal("no flow")
	}
	if f.Key != "master.policy.accept_dup" || f.NDefined != 1 {
		t.Errorf("key %q defined %d", f.Key, f.NDefined)
	}
	if len(f.Templates) != 1 || !strings.Contains(f.Templates[0].Writes, "policy_allow_dup") {
		t.Fatalf("templates: %+v", f.Templates)
	}
	if len(f.Readers) != 1 || f.Readers[0].Path != "common/src/sync.cpp" || f.Readers[0].Line != 2 {
		t.Errorf("readers: %+v", f.Readers)
	}
	// the field the reader fills (cfg.allow_dup), where it is used next
	if len(f.Uses) != 1 || f.Uses[0].Path != "splitter/src/splitter.cpp" {
		t.Errorf("uses: %+v", f.Uses)
	}
}

// A key deeper than the indexed YAML sections is read from the file itself.
func TestFollowConfigKeyDeep(t *testing.T) {
	f := FollowConfigKey(flowFixture(), FleetRepo, "v1", "workflow.print.at_pick.enable")
	if f == nil {
		t.Fatal("no flow")
	}
	if f.Key != "master.workflow.print.at_pick.enable" || f.NDefined != 1 || f.Defined[0].Line != 6 {
		t.Errorf("deep key: %+v", f)
	}
	if len(f.Templates) != 1 || !strings.Contains(f.Templates[0].Writes, "print_at_pick") {
		t.Errorf("templates: %+v", f.Templates)
	}
}
