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

	"github.com/vishwak02/reponite/internal/query"
)

// IsTestPath reports whether a repo-relative path is test code. The rule
// lives in query (query.IsTestPath) so query-time filters (--no-tests) apply
// exactly the classification captured here at index time.
func IsTestPath(path string) bool { return query.IsTestPath(path) }

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
