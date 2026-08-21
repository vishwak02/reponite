// Package roslaunch parses ROS 1 launch files to recover the wiring the source
// alone cannot show: which node runs where, and — the point of this package —
// how its topic names are REMAPPED at launch time.
//
// This closes a limitation `topics` previously had to state in its own output:
// "namespace/launch-file remapping is NOT resolved." A node whose source
// subscribes to `scan` may actually receive `raw_scan_front`, because a launch
// file rewrote it:
//
//	<node pkg="rr_navigation" type="scan_filter" name="filter">
//	  <remap from="scan" to="raw_scan_front" />
//	</node>
//
// Without this the comms graph pairs producers and consumers on pre-remap
// names — honestly labeled medium-confidence, but still pointing at the wrong
// topic on a fleet where remapping is routine.
//
// Pure and stdlib-only (encoding/xml), so it is unit-tested in-sandbox
// (ADR-018) and needs no build tag: a launch file is just XML.
package roslaunch

import (
	"encoding/xml"
	"io"
	"path/filepath"
	"regexp"
	"strings"
)

// IsLaunchFile reports whether path is a ROS launch/rostest file. `.test` is
// the rostest variant — same schema, and it wires up integration tests.
func IsLaunchFile(path string) bool {
	p := strings.ToLower(path)
	if strings.HasSuffix(p, ".launch.xml") {
		return true
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".launch", ".test":
		return true
	}
	return false
}

// Remap is one topic-name rewrite: `From` as written in source becomes `To` at
// runtime.
type Remap struct {
	From string
	To   string
}

// Node is one `<node>` declaration with the remaps that apply to it.
type Node struct {
	Pkg    string // package providing the executable — how we link back to source
	Type   string // executable name
	Name   string // the node's runtime name
	Ns     string // resolved namespace (enclosing group chain + its own)
	Line   int    // 1-based line of the <node> element, for citation
	Remaps []Remap
}

// File is a parsed launch file.
type File struct {
	Path string
	// Args are `<arg name default>` values declared here. They let `$(arg X)`
	// substitutions in remap targets resolve to a real name instead of being
	// applied literally — a remap to the string "$(arg scan_topic)" is not a
	// runtime topic, and treating it as one would be worse than not remapping.
	Args map[string]string
	// Nodes declared here, each carrying its own remaps plus any inherited from
	// an enclosing <group>/<launch>.
	Nodes []Node
	// GlobalRemaps are remaps declared outside any <node>: they apply to every
	// node in scope, including ones pulled in by <include>.
	GlobalRemaps []Remap
	// Includes are the launch files this one pulls in, AS WRITTEN — usually with
	// $(find pkg) substitutions, which are not resolved (and callers say so).
	Includes []string
}

// Parse reads launch XML. It is deliberately lenient about everything it does
// not model (args, params, machines, vendor tags): launch files in the wild
// carry substitutions and extensions, and being strict would lose the remaps in
// the rest of the file. On a malformed document it returns whatever was read
// before the error together with that error, so a caller can use the partial
// data while still reporting the problem rather than silently dropping it.
func Parse(path, content string) (File, error) {
	f := File{Path: path}
	dec := xml.NewDecoder(strings.NewReader(content))
	dec.Strict = false // unescaped & and vendor oddities are common
	dec.AutoClose = xml.HTMLAutoClose

	var nsStack []string // from nested <group ns="...">
	// One scope per open element, so a <group>'s remaps apply to its children
	// and stop at its close.
	type scope struct {
		elem     string
		remaps   []Remap
		pushedNS bool
	}
	var scopes []scope
	var cur *Node // the <node> currently open, if any

	inherited := func() []Remap {
		var out []Remap
		for _, s := range scopes {
			out = append(out, s.remaps...)
		}
		return out
	}

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return f, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			off := int(dec.InputOffset())
			if off > len(content) {
				off = len(content)
			}
			line := 1 + strings.Count(content[:off], "\n")
			switch strings.ToLower(t.Name.Local) {
			case "node":
				n := Node{Line: line, Ns: strings.Join(nsStack, "/")}
				for _, a := range t.Attr {
					switch strings.ToLower(a.Name.Local) {
					case "pkg":
						n.Pkg = a.Value
					case "type":
						n.Type = a.Value
					case "name":
						n.Name = a.Value
					case "ns":
						n.Ns = joinNS(n.Ns, a.Value)
					}
				}
				n.Remaps = append(n.Remaps, inherited()...)
				f.Nodes = append(f.Nodes, n)
				cur = &f.Nodes[len(f.Nodes)-1]
				scopes = append(scopes, scope{elem: "node"})
			case "remap":
				var r Remap
				for _, a := range t.Attr {
					switch strings.ToLower(a.Name.Local) {
					case "from":
						r.From = a.Value
					case "to":
						r.To = a.Value
					}
				}
				if r.From == "" || r.To == "" {
					break // an incomplete remap rewrites nothing
				}
				if cur != nil {
					cur.Remaps = append(cur.Remaps, r)
				} else {
					f.GlobalRemaps = append(f.GlobalRemaps, r)
					if len(scopes) > 0 {
						scopes[len(scopes)-1].remaps = append(scopes[len(scopes)-1].remaps, r)
					}
				}
			case "group", "launch":
				s := scope{elem: strings.ToLower(t.Name.Local)}
				for _, a := range t.Attr {
					if strings.ToLower(a.Name.Local) == "ns" && a.Value != "" {
						nsStack = append(nsStack, strings.Trim(a.Value, "/"))
						s.pushedNS = true
					}
				}
				scopes = append(scopes, s)
			case "arg":
				var name, val string
				for _, a := range t.Attr {
					switch strings.ToLower(a.Name.Local) {
					case "name":
						name = a.Value
					case "default", "value":
						if val == "" || strings.ToLower(a.Name.Local) == "value" {
							val = a.Value // an explicit value wins over a default
						}
					}
				}
				if name != "" && val != "" {
					if f.Args == nil {
						f.Args = map[string]string{}
					}
					f.Args[name] = val
				}
				scopes = append(scopes, scope{elem: "arg"})
			case "include":
				for _, a := range t.Attr {
					if strings.ToLower(a.Name.Local) == "file" {
						f.Includes = append(f.Includes, a.Value)
					}
				}
				scopes = append(scopes, scope{elem: "include"})
			default:
				scopes = append(scopes, scope{elem: strings.ToLower(t.Name.Local)})
			}
		case xml.EndElement:
			name := strings.ToLower(t.Name.Local)
			if name == "node" {
				cur = nil
			}
			for i := len(scopes) - 1; i >= 0; i-- {
				if scopes[i].elem == name {
					if scopes[i].pushedNS && len(nsStack) > 0 {
						nsStack = nsStack[:len(nsStack)-1]
					}
					scopes = scopes[:i]
					break
				}
			}
		}
	}
	return f, nil
}

// subRe matches a roslaunch substitution: $(arg x), $(env X), $(find pkg), …
var subRe = regexp.MustCompile(`\$\(([a-zA-Z_]+)\s+([^)]*)\)`)

// Substitute expands $(arg NAME) using args, leaving every other substitution
// ($(find …), $(env …), $(eval …)) untouched — those depend on the filesystem,
// the environment, or Python evaluation, none of which is knowable from source.
func Substitute(v string, args map[string]string) string {
	return subRe.ReplaceAllStringFunc(v, func(m string) string {
		g := subRe.FindStringSubmatch(m)
		if len(g) == 3 && g[1] == "arg" {
			if val, ok := args[strings.TrimSpace(g[2])]; ok {
				return val
			}
		}
		return m
	})
}

// HasSubstitution reports whether v still contains an unexpanded substitution.
// Such a value is NOT a runtime name: applying it would group endpoints under a
// literal like "$(arg scan_topic)".
func HasSubstitution(v string) bool { return subRe.MatchString(v) }

// joinNS composes a namespace chain.
func joinNS(parent, child string) string {
	child = strings.Trim(child, "/")
	if child == "" {
		return parent
	}
	if parent == "" {
		return child
	}
	return parent + "/" + child
}
