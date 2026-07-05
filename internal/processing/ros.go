// ros.go extracts ROS interface definitions (.msg / .srv / .action) as type
// symbols whose signature IS the field contract, so the compat Oracle flags a
// changed message contract (added / removed / retyped / reordered field) as
// shape_changed across refs — cross-language interface compatibility for ROS
// packages (roadmap 4.1). ROS IDL is line-oriented, so this is pure text
// parsing (no tree-sitter) and is unit-tested in-sandbox (ADR-018); IndexDir /
// IndexGitRef route ROS files here before the tree-sitter path.
package processing

import (
	"path/filepath"
	"strings"
)

// rosSections maps a ROS interface extension to its `---`-separated sub-message
// suffixes (a .msg is a single unnamed section).
var rosSections = map[string][]string{
	".msg":    {""},
	".srv":    {"Request", "Response"},
	".action": {"Goal", "Result", "Feedback"},
}

// IsROSFile reports whether path is a ROS interface definition.
func IsROSFile(path string) bool {
	_, ok := rosSections[filepath.Ext(path)]
	return ok
}

// rosFile parses a ROS interface file into one type symbol per section, or
// (_, false) if path is not a ROS interface. The symbol name is the file's base
// name plus the section suffix (Point.msg → Point; AddTwoInts.srv →
// AddTwoIntsRequest, AddTwoIntsResponse). Signature = the canonical field list.
func rosFile(path, content string) (ParsedFile, bool) {
	suffixes, ok := rosSections[filepath.Ext(path)]
	if !ok {
		return ParsedFile{}, false
	}
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	sections := splitROSSections(content, len(suffixes))
	syms := make([]Symbol, 0, len(sections))
	for i, sec := range sections {
		if i >= len(suffixes) {
			break
		}
		sig := canonROSFields(sec)
		syms = append(syms, Symbol{
			Name:      base + suffixes[i],
			Kind:      "type",
			Signature: sig,
			CanonBody: []byte(sig), // body == contract: any field change is a shape change
		})
	}
	return ParsedFile{Path: path, Content: content, Lang: "ros", Symbols: syms}, true
}

// splitROSSections splits a body on lines that are exactly "---" into at most n
// sections (n==1 → the whole body).
func splitROSSections(content string, n int) []string {
	if n == 1 {
		return []string{content}
	}
	var out []string
	var cur []string
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) == "---" {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	return append(out, strings.Join(cur, "\n"))
}

// canonROSFields reduces a section to its field contract: each non-comment,
// non-blank line collapsed to space-normalized "type name" (or "type NAME=const"),
// comments stripped, ORDER PRESERVED — ROS serialization order is part of the
// wire contract, so fields are not sorted.
func canonROSFields(section string) string {
	var fields []string
	for _, line := range strings.Split(section, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue // blank, comment-only, or malformed
		}
		fields = append(fields, strings.Join(f, " "))
	}
	return strings.Join(fields, "\n")
}
