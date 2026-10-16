// rosviz.go reads ROS visualisation configs (rviz `.rviz` files) as endpoints
// of the communication graph. A display subscribes to a topic and a tool
// publishes to one, but neither appears in any source file: the topic name is
// configuration. Without this, `topics` reports zero consumers for a topic that
// every operator looks at, and the only way to learn who reads it is a text
// search. Pure and stdlib-only (ADR-018): a line-oriented reader of the YAML
// rviz writes, no YAML library needed.
package query

import (
	"path/filepath"
	"regexp"
	"strings"
)

// IsVizConfig reports whether path is an rviz display configuration.
func IsVizConfig(path string) bool {
	return strings.EqualFold(filepath.Ext(path), ".rviz")
}

var (
	vizItemRe  = regexp.MustCompile(`^(\s*)-\s+(.*)$`)
	vizKeyRe   = regexp.MustCompile(`^(\s*)(?:-\s+)?([A-Za-z][\w ]*?):\s*(.*?)\s*$`)
	vizValueRe = regexp.MustCompile(`^\s*Value:\s*(.+?)\s*$`)
	vizNameOK  = regexp.MustCompile(`^/?[A-Za-z_~][\w/~]*$`)
)

func vizIndent(ln string) int { return len(ln) - len(strings.TrimLeft(ln, " ")) }

// vizItem returns the line span [start, end) of the YAML list item enclosing
// line i, and the indent its keys sit at. start is -1 outside any item.
func vizItem(lines []string, i int) (start, end, keyIndent int) {
	ind := vizIndent(lines[i])
	start = -1
	for j := i; j >= 0; j-- {
		if m := vizItemRe.FindStringSubmatch(lines[j]); m != nil && len(m[1])+2 <= ind {
			start, keyIndent = j, len(m[1])+2
			break
		}
		if t := strings.TrimSpace(lines[j]); t != "" && vizIndent(lines[j]) < ind-2 && !strings.HasPrefix(t, "-") {
			return -1, -1, 0
		}
	}
	if start < 0 {
		return -1, -1, 0
	}
	end = len(lines)
	for j := start + 1; j < len(lines); j++ {
		t := strings.TrimSpace(lines[j])
		if t == "" {
			continue
		}
		if vizIndent(lines[j]) < keyIndent {
			end = j
			break
		}
	}
	return start, end, keyIndent
}

// inViz reports whether line i sits under a top-level section named name
// (`Tools:`), walking up through shallower keys.
func inViz(lines []string, i int, name string) bool {
	ind := vizIndent(lines[i])
	for j := i - 1; j >= 0; j-- {
		t := strings.TrimSpace(lines[j])
		if t == "" || strings.HasPrefix(t, "-") {
			continue
		}
		if k := vizIndent(lines[j]); k < ind {
			if strings.HasPrefix(t, name+":") {
				return true
			}
			ind = k
		}
	}
	return false
}

// scanVizConfig returns one endpoint per topic an rviz config binds: displays
// are subscribers, tools (under `Tools:`) publishers. Names are the runtime
// names rviz sees on the wire, so they are never put through launch remapping.
func scanVizConfig(repo, path, content string) []CommEndpoint {
	lines := strings.Split(content, "\n")
	var out []CommEndpoint
	for i, ln := range lines {
		m := vizKeyRe.FindStringSubmatch(ln)
		if m == nil || !strings.HasSuffix(m[2], "Topic") {
			continue
		}
		val := strings.Trim(m[3], `"'`)
		if val == "" { // rviz2 nests the name under Value:
			for j := i + 1; j < len(lines) && j <= i+6; j++ {
				if v := vizValueRe.FindStringSubmatch(lines[j]); v != nil {
					val = strings.Trim(v[1], `"'`)
					break
				}
			}
		}
		if val == "" || !vizNameOK.MatchString(val) {
			continue
		}
		class, name := "", ""
		if s, e, ki := vizItem(lines, i); s >= 0 {
			for j := s; j < e; j++ {
				km := vizKeyRe.FindStringSubmatch(lines[j])
				if km == nil || len(km[1]) != ki && !(j == s && len(km[1])+2 == ki) {
					continue
				}
				switch km[2] {
				case "Class":
					class = strings.Trim(km[3], `"'`)
				case "Name":
					name = strings.Trim(km[3], `"'`)
				}
			}
		}
		role, in := RoleSubscriber, "rviz display"
		if inViz(lines, i, "Tools") {
			role, in = RolePublisher, "rviz tool"
		}
		if name != "" {
			in += ` "` + strings.ReplaceAll(name, `"`, `'`) + `"`
		}
		if class != "" {
			in += " (" + class + ")"
		}
		out = append(out, CommEndpoint{
			Repo: repo, Path: path, Line: i + 1, Role: role,
			Name: normalizeTopic(val), Raw: val, MsgType: class, MsgTypeSource: "rviz-display-class",
			Effective: normalizeTopic(val), NameResolution: "as-configured (rviz)",
			In: in, Text: strings.TrimSpace(ln),
		})
	}
	return out
}
