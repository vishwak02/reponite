// launch.go routes ROS launch/rostest XML into the index. Launch files are not
// tree-sitter parsed: they are stored for content search, with one symbol span
// per `<node>` so a grep hit reports which node it landed in — and, crucially,
// so `topics` can read them back at query time to resolve remapping (§8D.4's
// former limitation). Zero new storage: the same query-time-scan model the
// comms graph already uses.
package processing

import (
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/roslaunch"
)

// IsROSLaunchFile reports whether path is a ROS launch/rostest file.
func IsROSLaunchFile(path string) bool { return roslaunch.IsLaunchFile(path) }

// launchFile turns a launch file into a stored ParsedFile: no code symbols, but
// a span per `<node>` so grep/usages can attribute a hit to a node. A parse
// error still stores the content — the file is searchable either way, and
// losing it entirely would hide wiring from every surface.
func launchFile(path, content string) ParsedFile {
	pf := ParsedFile{Path: path, Content: content, Lang: "roslaunch", IsTest: IsTestPath(path)}
	f, err := roslaunch.Parse(path, content)
	if err != nil && len(f.Nodes) == 0 {
		return pf
	}
	for _, n := range f.Nodes {
		name := n.Name
		if name == "" {
			name = n.Type
		}
		if name == "" {
			continue
		}
		pf.Spans = append(pf.Spans, query.SymbolSpan{Name: name, StartLine: n.Line, EndLine: n.Line})
	}
	return pf
}
