// sourceguard.go keeps files that are not hand-written source out of the
// parser: a self-extracting installer saved as `.sh` (tens of MB of shell
// header plus a binary payload), a generated amalgamation, a data blob with a
// code extension. Parsing one is slow at best, and a tree-sitter grammar fed
// binary bytes can crash the whole process. Such files are skipped and
// reported, never silently dropped. Pure and stdlib-only (ADR-018).
package processing

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// DefaultMaxSourceBytes is the largest file parsed as source: far above any
// hand-written file, far below an installer or a generated dump.
const DefaultMaxSourceBytes = 4 << 20

// maxSourceBytes is DefaultMaxSourceBytes, or REPONITE_MAX_SOURCE_BYTES when set
// to a positive integer.
func maxSourceBytes() int64 {
	if v, err := strconv.ParseInt(os.Getenv("REPONITE_MAX_SOURCE_BYTES"), 10, 64); err == nil && v > 0 {
		return v
	}
	return DefaultMaxSourceBytes
}

// sniffLen is how much of a file is checked for binary content.
const sniffLen = 8 << 10

// skipSourceReason says why a file of this size and content is not parsed as
// source, or "" when it is parsed. A NUL byte in the first 8 KB means binary.
func skipSourceReason(path string, size int64, head []byte) string {
	if limit := maxSourceBytes(); size > limit {
		return fmt.Sprintf("larger than %d bytes (%d)", limit, size)
	}
	sniff := head
	if len(sniff) > sniffLen {
		sniff = sniff[:sniffLen]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		return "binary content"
	}
	if isWebAsset(path) && minified(head) {
		return "minified or bundled"
	}
	return ""
}

// isWebAsset is a file kind that is often shipped built (a bundle, a minified
// library copied into static/) under an ordinary name.
func isWebAsset(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".js", ".mjs", ".cjs", ".jsx", ".ts", ".css":
		return true
	}
	return false
}

// minified reports built output: a line far longer than anyone writes by hand
// in the first 64 KB, or an average line length no hand-written file has.
func minified(b []byte) bool {
	if len(b) > 64<<10 {
		b = b[:64<<10]
	}
	lines, longest, cur := 1, 0, 0
	for _, c := range b {
		if c == '\n' {
			lines++
			if cur > longest {
				longest = cur
			}
			cur = 0
			continue
		}
		cur++
	}
	if cur > longest {
		longest = cur
	}
	return longest > 5000 || len(b) > 4096 && len(b)/lines > 300
}

// SkippedFile is a file not parsed, and why.
type SkippedFile struct {
	Path   string
	Reason string
}

// summarize is the IndexSummary of one index pass.
func summarize(files []ParsedFile, skipped []SkippedFile) IndexSummary {
	paths := make([]string, 0, len(files))
	tests := 0
	for _, f := range files {
		paths = append(paths, f.Path)
		if f.IsTest {
			tests++
		}
	}
	return IndexSummary{Files: len(files), TestFiles: tests, TopDirs: DirFootprint(paths, 2), Skipped: skipped}
}
