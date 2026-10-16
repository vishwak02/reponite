package roslaunch

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ParamValue is one parameter a launch file sets, after $(arg …) expansion:
// a `<param name value>`, or one key of a `<rosparam>` YAML load. It is how a
// node whose source reads its topic NAME from a parameter gets that name at
// runtime, so `topics` can link it instead of dropping it as dynamic.
type ParamValue struct {
	Name  string // normalized: no leading "~" or "/", namespace joined with "/"
	Value string
	Pkg   string // the <node pkg> it is private to; "" for a global param
	Via   string // citation, "path:line" (the YAML file's line for a file load)
}

// IsParamFile reports whether path is a YAML file that may hold ROS
// parameters (a rosparam load target). Only these are stored at index time.
func IsParamFile(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".yaml", ".yml":
		return true
	}
	return false
}

// NormParam normalizes a parameter name for comparison: private (~x) and
// absolute (/x) spellings of the same key compare equal. Namespace resolution
// is a runtime concern this layer does not attempt; callers label the result.
func NormParam(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "~")
	return strings.Trim(name, "/")
}

var (
	findRe     = regexp.MustCompile(`^\$\(find\s+([\w.-]+)\)(/\S*)$`)
	evalFindRe = regexp.MustCompile(`^\$\(eval\s+find\(\s*['"]([\w.-]+)['"]\s*\)\s*\+\s*['"]([^'"]+)['"]\s*\)$`)
)

// FindRef splits a file reference written as `$(find pkg)/rel` or
// `$(eval find('pkg') + '/rel')` into the package and the path inside it.
func FindRef(v string) (pkg, rel string, ok bool) {
	v = strings.TrimSpace(v)
	if m := findRe.FindStringSubmatch(v); m != nil {
		return m[1], strings.TrimPrefix(m[2], "/"), true
	}
	if m := evalFindRe.FindStringSubmatch(v); m != nil {
		return m[1], strings.TrimPrefix(m[2], "/"), true
	}
	return "", "", false
}

// YAMLLookup resolves a rosparam file reference to the stored file it names:
// (repo-relative path, content, ok).
type YAMLLookup func(pkg, rel string) (string, string, bool)

// LaunchParams expands one parsed launch file's <param> and <rosparam>
// declarations into name/value pairs. A value that keeps an unexpanded
// substitution is dropped (it is not a runtime value) and counted, and a
// rosparam file that cannot be located is returned as written, so the caller
// can say what it could not read instead of implying there was nothing.
func LaunchParams(f File, lookup YAMLLookup) (vals []ParamValue, unresolved []string) {
	for _, p := range f.Params {
		v := Substitute(p.Value, f.Args)
		if v == "" || HasSubstitution(v) {
			continue
		}
		vals = append(vals, ParamValue{Name: NormParam(joinNS(p.Ns, NormParam(p.Name))), Value: strings.TrimSpace(v), Pkg: p.Pkg,
			Via: f.Path + ":" + strconv.Itoa(p.Line)})
	}
	for _, r := range f.Rosparams {
		if r.Command != "" && r.Command != "load" {
			continue
		}
		prefix := r.Ns
		if r.Param != "" {
			prefix = joinNS(prefix, NormParam(r.Param))
		}
		if r.File == "" {
			// Inline YAML. A bare scalar under param="x" is that param's value.
			text := strings.TrimSpace(r.Inline)
			if text == "" {
				continue
			}
			flat := FlattenYAML(text)
			if len(flat) == 0 && r.Param != "" && !strings.Contains(text, "\n") {
				vals = append(vals, ParamValue{Name: NormParam(prefix), Value: unquote(text), Pkg: r.Pkg,
					Via: f.Path + ":" + strconv.Itoa(r.Line)})
				continue
			}
			for _, k := range sortedYAMLKeys(flat) {
				vals = append(vals, ParamValue{Name: NormParam(joinNS(prefix, k)), Value: flat[k].Value, Pkg: r.Pkg,
					Via: f.Path + ":" + strconv.Itoa(r.Line)})
			}
			continue
		}
		ref := Substitute(r.File, f.Args)
		pkg, rel, ok := FindRef(ref)
		if !ok || lookup == nil {
			unresolved = append(unresolved, ref)
			continue
		}
		p, content, ok := lookup(pkg, rel)
		if !ok {
			unresolved = append(unresolved, ref)
			continue
		}
		flat := FlattenYAML(content)
		for _, k := range sortedYAMLKeys(flat) {
			vals = append(vals, ParamValue{Name: NormParam(joinNS(prefix, k)), Value: flat[k].Value, Pkg: r.Pkg,
				Via: p + ":" + strconv.Itoa(flat[k].Line) + " via " + f.Path + ":" + strconv.Itoa(r.Line)})
		}
	}
	return vals, unresolved
}

// YAMLScalar is one flattened YAML scalar and the line it was written on.
type YAMLScalar struct {
	Value string
	Line  int
}

// FlattenYAML reads the block-mapping subset of YAML that ROS parameter files
// use — nested `key: scalar` maps — into "a/b/c" -> scalar. It is deliberately
// small (stdlib only, no YAML dependency): sequences, flow collections, block
// scalars and anchors are skipped rather than guessed, so an unsupported
// construct yields a missing key, never a wrong value.
func FlattenYAML(content string) map[string]YAMLScalar {
	out := map[string]YAMLScalar{}
	type level struct {
		indent int
		key    string
	}
	var stack []level
	skipIndent := -1 // inside a block scalar or sequence item deeper than this
	seqIndent := -1  // the indent of the sequence being skipped
	for i, raw := range strings.Split(content, "\n") {
		line := strings.TrimRight(raw, " \t\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if trimmed == "---" || trimmed == "..." {
			stack = stack[:0]
			skipIndent = -1
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if skipIndent >= 0 {
			if indent > skipIndent {
				continue
			}
			skipIndent = -1
		}
		isItem := strings.HasPrefix(trimmed, "- ") || trimmed == "-"
		if seqIndent >= 0 {
			if indent > seqIndent || indent == seqIndent && isItem {
				continue
			}
			seqIndent = -1
		}
		for len(stack) > 0 && stack[len(stack)-1].indent >= indent {
			stack = stack[:len(stack)-1]
		}
		if isItem {
			seqIndent = indent // a sequence: skip it and its children
			continue
		}
		key, val, ok := splitYAMLKey(trimmed)
		if !ok {
			continue
		}
		val = stripYAMLComment(val)
		if val == "" {
			stack = append(stack, level{indent, key})
			continue
		}
		switch val[0] {
		case '|', '>':
			skipIndent = indent // block scalar: its lines are not keys
			continue
		case '{', '[', '&', '*', '!':
			continue
		}
		parts := make([]string, 0, len(stack)+1)
		for _, l := range stack {
			parts = append(parts, l.key)
		}
		parts = append(parts, key)
		out[strings.Join(parts, "/")] = YAMLScalar{Value: unquote(val), Line: i + 1}
	}
	return out
}

func splitYAMLKey(s string) (key, val string, ok bool) {
	if s[0] == '"' || s[0] == '\'' {
		q := s[0]
		end := strings.IndexByte(s[1:], q)
		if end < 0 {
			return "", "", false
		}
		key = s[1 : end+1]
		rest := strings.TrimSpace(s[end+2:])
		if !strings.HasPrefix(rest, ":") {
			return "", "", false
		}
		return key, strings.TrimSpace(rest[1:]), true
	}
	i := strings.Index(s, ":")
	for i >= 0 && i+1 < len(s) && s[i+1] != ' ' && s[i+1] != '\t' {
		// "a:b" inside a key is not a separator; YAML needs ": " or a trailing ":".
		j := strings.Index(s[i+1:], ":")
		if j < 0 {
			return "", "", false
		}
		i += 1 + j
	}
	if i <= 0 {
		return "", "", false
	}
	key = strings.TrimSpace(s[:i])
	if strings.ContainsAny(key, " \t{}[]") {
		return "", "", false
	}
	return key, strings.TrimSpace(s[i+1:]), true
}

func stripYAMLComment(v string) string {
	if v == "" || v[0] == '"' || v[0] == '\'' {
		return v
	}
	if i := strings.Index(v, " #"); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
		return v[1 : len(v)-1]
	}
	return v
}

func sortedYAMLKeys(m map[string]YAMLScalar) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
