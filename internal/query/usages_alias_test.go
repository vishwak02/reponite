package query

import "testing"

// A Python caller that imports the target under another name, in a
// parenthesized import list, calls it by that name.
func TestPythonImportAliasAcrossLines(t *testing.T) {
	f := File{Path: "tasks/export.py", Content: "from reports.gen import (\n    generate_report as _generate_report,\n)\n\ndef run():\n    _generate_report()\n"}
	got := jsImportAliases(f, "reports.gen.generate_report", "generate_report")
	if len(got) != 1 || got[0] != "_generate_report" {
		t.Errorf("aliases: %v", got)
	}
}

// A class template's member defined out of class (Owner<T, U>::f) belongs to
// Owner, not to another template whose name merely precedes ::f.
func TestScopedDefTemplate(t *testing.T) {
	for line, want := range map[string]bool{
		"void ResponderMockup<RequestMsg, ResponseMsg>::addFailureInfo(std::string&& f) const": true,
		"void ActionServerMockup<Action>::addFailureInfo(std::string&& f) const":               false,
		"void ResponderMockup::addFailureInfo()":                                               true,
		"void ResponderMockup<A>::addFailureInfoLater()":                                       false,
	} {
		if got := scopedDef(line, "ResponderMockup", "addFailureInfo"); got != want {
			t.Errorf("%q: got %v", line, got)
		}
	}
}

// A class whose text holds a matching method ranks below that method.
func TestDemoteContainers(t *testing.T) {
	hits := []SemanticHit{
		{Repo: "r", Path: "a.py", Symbol: "Handler", Line: 10, Score: 1.0},
		{Repo: "r", Path: "a.py", Symbol: "Handler.timeout_for_mode", Line: 20, Score: 0.8},
		{Repo: "r", Path: "b.py", Symbol: "Other", Line: 1, Score: 0.9},
	}
	demoteContainers(hits, []int{60, 30, 50})
	SortSemanticHits(hits)
	if hits[0].Symbol != "Other" || hits[1].Symbol != "Handler.timeout_for_mode" {
		t.Errorf("order: %+v", hits)
	}
}
