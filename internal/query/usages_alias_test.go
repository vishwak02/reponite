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
