// istest.go holds the path rule that decides whether a file is test code
// (§9A.1). The indexer stamps it on every symbol (SymbolRecord.IsTest) and
// query-time filters (--no-tests) apply the same rule to file paths, so the
// two never disagree. Pure and stdlib-only (ADR-018).
package query

import (
	"path/filepath"
	"strings"
)

// testDirs are path segments that mark a test tree in essentially every
// ecosystem. Matched as whole segments, so `latest/` or `contest/` never count.
var testDirs = map[string]bool{
	"test": true, "tests": true, "spec": true, "specs": true,
	"__tests__": true, "testing": true, "it": false, // "it" is too ambiguous
}

// IsTestPath reports whether a repo-relative path is test code, by directory
// convention (any `test`/`tests`/`spec`/`__tests__` segment) or by the
// language's filename convention. Conservative in the direction that matters:
// a false negative merely omits a covering test, while a false positive would
// hide production code from search, so only well-established markers count.
func IsTestPath(path string) bool {
	path = filepath.ToSlash(path)
	segs := strings.Split(path, "/")
	for _, s := range segs[:max(0, len(segs)-1)] { // directories only
		if testDirs[strings.ToLower(s)] {
			return true
		}
	}
	base := strings.ToLower(filepath.Base(path))
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	switch ext {
	case ".go":
		return strings.HasSuffix(stem, "_test")
	case ".py":
		// pytest/unittest discovery: test_*.py and *_test.py.
		return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test")
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") ||
			strings.HasSuffix(stem, "_tests") || strings.HasPrefix(stem, "mock_") ||
			strings.HasSuffix(stem, "_mock")
	case ".js", ".jsx", ".mjs", ".cjs", ".ts", ".tsx":
		// jest/mocha/vitest: foo.test.ts, foo.spec.ts.
		return strings.HasSuffix(stem, ".test") || strings.HasSuffix(stem, ".spec")
	case ".java":
		return strings.HasSuffix(stem, "test") || strings.HasSuffix(stem, "tests") ||
			strings.HasPrefix(stem, "test")
	case ".rs":
		return strings.HasSuffix(stem, "_test") || strings.HasSuffix(stem, "_tests")
	case ".sh", ".bash", ".zsh", ".ksh":
		return strings.HasPrefix(stem, "test_") || strings.HasSuffix(stem, "_test") ||
			strings.HasPrefix(stem, "bats_") || strings.HasSuffix(stem, ".bats")
	}
	return false
}

// generatedSuffixes are file names code generators write: protobuf/gRPC,
// ROS message bindings, Qt moc/uic, Flutter, Thrift, GraphQL codegen.
var generatedSuffixes = []string{
	".pb.go", ".pb.gw.go", "_pb2.py", "_pb2_grpc.py", "_pb2.pyi", ".pb.h", ".pb.cc", ".grpc.pb.h", ".grpc.pb.cc",
	".g.dart", ".freezed.dart", ".gen.go", "_gen.go", "_generated.go", "_generated.ts", ".generated.ts",
	"_string.go", "zz_generated.deepcopy.go", ".designer.cs", "_thrift.py",
}

// IsGeneratedPath reports a file a generator writes, by name convention
// (foo.pb.go, foo_pb2.py, moc_foo.cpp, a generated/ or gen/ directory, ROS
// msg/_Foo.py bindings). Such code is indexed but is rarely the answer, so
// rankings put it after hand-written code.
func IsGeneratedPath(path string) bool {
	path = filepath.ToSlash(path)
	base := strings.ToLower(filepath.Base(path))
	for _, s := range generatedSuffixes {
		if strings.HasSuffix(base, s) {
			return true
		}
	}
	if strings.HasPrefix(base, "moc_") || strings.HasPrefix(base, "ui_") && strings.HasSuffix(base, ".h") ||
		strings.HasPrefix(base, "qrc_") {
		return true
	}
	segs := strings.Split(path, "/")
	for i, s := range segs[:max(0, len(segs)-1)] {
		switch strings.ToLower(s) {
		case "generated", "__generated__", "gen-py", "gen-cpp", "gen-go", "autogen":
			return true
		case "msg", "srv", "action":
			// ROS python bindings: <pkg>/msg/_Foo.py
			if i == len(segs)-2 && strings.HasPrefix(segs[len(segs)-1], "_") && strings.HasSuffix(base, ".py") {
				return true
			}
		}
	}
	return false
}
