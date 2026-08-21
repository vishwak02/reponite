package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

func newTestMem() *storage.Mem { return storage.NewMem() }

// The bug this fixes: test detection was a Go-only NAME heuristic, so a C++
// fixture in `test/` and a `test_x.py` were not tests — and `brief` reported
// "0 covering tests" for every non-Go language while advertising the section.
func TestIsTestPathPerLanguage(t *testing.T) {
	tests := map[string]bool{
		// the reported case: a C++ fixture under test/
		"sootballs_pgs/test/include/sootballs_picker_guiding_system_test/test_helpers.hpp": true,
		"sootballs_pgs/test/src/test_pgs.cpp":                                              true,
		"sootballs_pgs/src/pgs.cpp":                                                        false,
		"sootballs_pgs/include/sootballs_picker_guiding_system/pgs.hpp":                    false,

		"internal/query/grep_test.go": true,
		"internal/query/grep.go":      false,

		"pkg/test_client.py": true,
		"pkg/client_test.py": true,
		"pkg/client.py":      false,
		"tests/conftest.py":  true,
		"spec/helper.rb":     true, // a spec/ directory is a test tree in any language

		"src/api.test.ts":  true,
		"src/api.spec.tsx": true,
		"src/api.ts":       false,
		"__tests__/api.js": true,

		"src/test/java/FooTest.java": true,
		"src/main/java/Foo.java":     false,

		"crate/tests/integration.rs": true,
		"crate/src/lib.rs":           false,

		"ci/test_deploy.sh": true,
		"ci/deploy.sh":      false,

		// A directory whose name merely CONTAINS "test" is not a test tree.
		"src/latest/api.go":  false,
		"src/contest/api.go": false,
		// The file itself being named `test` in a normal dir is not enough
		// for languages with no such convention.
		"src/protest.go": false,
	}
	for path, want := range tests {
		if got := IsTestPath(path); got != want {
			t.Errorf("IsTestPath(%q) = %v, want %v", path, got, want)
		}
	}
}

// A `test` segment must be a whole directory, and only a DIRECTORY — a file
// literally named `test.go` in a source dir is production code by convention.
func TestIsTestPathDirectoryOnly(t *testing.T) {
	if !IsTestPath("a/test/b.cpp") {
		t.Error("a test/ directory anywhere in the path marks test code")
	}
	if IsTestPath("test") {
		t.Error("a bare file named `test` with no extension is not test code")
	}
}

// End to end through the indexer: a C++ test fixture calling a production
// symbol must be recorded as test code, so brief's covering-tests section
// finds it. Before is_test was captured, IsTestName saw "PgsTestFixture",
// found no Go Test* prefix, and brief reported zero covering tests.
func TestIndexFilesRecordsIsTestForNonGo(t *testing.T) {
	m := newTestMem()
	files := []ParsedFile{
		{
			Path: "pkg/src/pgs.cpp", Lang: "cpp", Content: "x", IsTest: IsTestPath("pkg/src/pgs.cpp"),
			Symbols: []Symbol{sym("PickerGuidingSystem", "type", "class PickerGuidingSystem", "b")},
		},
		{
			Path: "pkg/test/src/test_pgs.cpp", Lang: "cpp", Content: "x",
			IsTest:  IsTestPath("pkg/test/src/test_pgs.cpp"),
			Symbols: []Symbol{sym("PgsTestFixture", "type", "class PgsTestFixture", "b", "PickerGuidingSystem")},
		},
	}
	if err := IndexFiles(m, "r", "HEAD", 1, files); err != nil {
		t.Fatal(err)
	}
	prod, _ := m.SymbolAt("r", "pkg/src.PickerGuidingSystem", "HEAD")
	if prod.IsTest {
		t.Error("production source must not be flagged as test code")
	}
	fixture, ok := m.SymbolAt("r", "pkg/test/src.PgsTestFixture", "HEAD")
	if !ok {
		t.Fatal("fixture not indexed")
	}
	if !fixture.IsTest {
		t.Error("a C++ fixture under test/ must be flagged as test code — this is the bug")
	}
}
