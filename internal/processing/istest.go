// istest.go decides whether a source file is test code. §9A.1 specifies
// `nodes.is_test` as captured "at parse/resolve time: *_test.go, test framework
// markers, per-language heuristics" — and that is the only place the
// information exists, because a symbol's stored id keeps the file's DIRECTORY
// but not its basename, so `test_foo.py` is undetectable downstream.
//
// Until this landed, test detection was a Go-only NAME heuristic
// (Test*/Benchmark*/…), so a C++ fixture in `test/` or a `test_x.py` was not a
// test — and `brief` reported "0 covering tests" for every non-Go language
// while advertising the section. Pure and stdlib-only (ADR-018).
package processing

import (
	"path/filepath"
	"sort"
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

// DirFootprint counts indexed files per top-level-ish directory prefix, so the
// indexer can report where its symbols actually came from. A repo that vendors
// third-party code under a non-standard path (not vendor/ or third_party/) is
// otherwise invisible: it inflates the index and outranks first-party code in
// search, and the only way to notice today is by accident.
func DirFootprint(paths []string, depth int) []DirCount {
	if depth < 1 {
		depth = 2
	}
	counts := map[string]int{}
	for _, p := range paths {
		segs := strings.Split(filepath.ToSlash(p), "/")
		if len(segs) > depth {
			segs = segs[:depth]
		} else if len(segs) > 1 {
			segs = segs[:len(segs)-1] // drop the filename
		} else {
			segs = []string{"."}
		}
		counts[strings.Join(segs, "/")]++
	}
	out := make([]DirCount, 0, len(counts))
	for dir, n := range counts {
		out = append(out, DirCount{Dir: dir, Files: n})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Files != out[j].Files {
			return out[i].Files > out[j].Files
		}
		return out[i].Dir < out[j].Dir
	})
	return out
}

// DirCount is one directory's contribution to the index.
type DirCount struct {
	Dir   string
	Files int
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
