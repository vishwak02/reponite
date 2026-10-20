package query

import (
	"regexp"
	"strings"
)

// Framework-wired callers: methods a framework calls because of how a class
// is declared, not because any code calls them. Django REST framework calls a
// serializer's get_<field> for each SerializerMethodField, and a serializer's
// to_representation/validate/create/update when a view (serializer_class =
// S) or a parent serializer (field = S(...)) uses it. The call graph has no
// edge for any of these; context names the declaring class, and the views
// and serializers that use it, as callers labelled with the wiring.

var (
	fwMethodField = regexp.MustCompile(`(?m)^\s+(\w+)\s*=\s*(?:serializers\.)?SerializerMethodField\(([^)]*)\)`)
	fwMethodName  = regexp.MustCompile(`method_name\s*=\s*['"](\w+)['"]`)
	fwSerializers = map[string]bool{"to_representation": true, "to_internal_value": true, "validate": true,
		"create": true, "update": true, "save": true}
)

// frameworkCallers are sym's framework-wired callers in repo at ref.
func frameworkCallers(s Store, repo, ref, sym string) []CallerEdge {
	cls, m, ok := classOf(sym)
	if !ok || !strings.HasSuffix(symFile(s, repo, ref, sym), ".py") {
		return nil
	}
	clsQID := strings.TrimSuffix(sym, "."+m)
	files := s.Files(repo, ref)
	body := classSource(files, clsQID)
	if body == "" {
		return nil
	}
	var out []CallerEdge
	field := ""
	for _, f := range fwMethodField.FindAllStringSubmatch(body, -1) {
		getter := "get_" + f[1]
		if mm := fwMethodName.FindStringSubmatch(f[2]); mm != nil {
			getter = mm[1]
		}
		if getter == m {
			field = f[1]
		}
	}
	serializerHook := fwSerializers[m] || strings.HasPrefix(m, "validate_")
	if field == "" && !serializerHook {
		return nil
	}
	if field != "" {
		out = append(out, CallerEdge{Name: clsQID, Via: "drf SerializerMethodField " + field})
	}
	// the views and parent serializers rendering this serializer run it
	use := regexp.MustCompile(`(?m)^\s+(?:serializer_class\s*=\s*` + regexp.QuoteMeta(cls) + `\b|(\w+)\s*=\s*(?:\w+\.)?` + regexp.QuoteMeta(cls) + `\()`)
	for _, f := range files {
		if !strings.HasSuffix(f.Path, ".py") || !strings.Contains(f.Content, cls) {
			continue
		}
		for _, loc := range use.FindAllStringSubmatchIndex(f.Content, -1) {
			line := strings.Count(f.Content[:loc[0]], "\n") + 1
			owner := enclosingClassAt(f, line)
			if owner == "" {
				continue
			}
			via := "drf serializer_class"
			if fn := enclosingDefAt(f, line); fn != "" {
				owner += "." + fn // s = S(...) inside a method: that method renders it
				via = "drf serializer built"
			} else if loc[2] >= 0 {
				via = "drf nested serializer field " + f.Content[loc[2]:loc[3]]
			}
			q := qidAtPath(s, repo, ref, f.Path, owner)
			if q == "" || q == clsQID {
				continue
			}
			out = append(out, CallerEdge{Name: q, IsTest: testCode(f.Path, q), Via: via})
		}
	}
	return out
}

// symFile is the path of the file defining sym, "" when not located.
func symFile(s Store, repo, ref, sym string) string {
	p, _, _, ok := LocateSymbol(s.Files(repo, ref), sym)
	if !ok {
		return ""
	}
	return p
}

// classSource is a class's source text, "" when not located.
func classSource(files []File, clsQID string) string {
	p, start, end, ok := LocateSymbol(files, clsQID)
	if !ok {
		return ""
	}
	for _, f := range files {
		if f.Path == p {
			return sliceLines(f.Content, start, end)
		}
	}
	return ""
}

// enclosingClassAt is the innermost span holding line that is a class.
func enclosingClassAt(f File, line int) string {
	best, size := "", 1<<30
	for _, sp := range f.Symbols {
		if sp.StartLine <= line && line <= sp.EndLine && sp.EndLine-sp.StartLine < size {
			if l := strings.TrimSpace(lineAt(f.Content, sp.StartLine)); strings.HasPrefix(l, "class ") {
				best, size = sp.Name, sp.EndLine-sp.StartLine
			}
		}
	}
	return best
}

// enclosingDefAt is the innermost def (a method) holding line, "" when the
// line is in a class body itself.
func enclosingDefAt(f File, line int) string {
	best, size, isDef := "", 1<<30, false
	for _, sp := range f.Symbols {
		if sp.StartLine <= line && line <= sp.EndLine && sp.EndLine-sp.StartLine < size {
			l := strings.TrimSpace(lineAt(f.Content, sp.StartLine))
			best, size = sp.Name, sp.EndLine-sp.StartLine
			isDef = !strings.HasPrefix(l, "class ") && !strings.HasPrefix(l, "@")
		}
	}
	if !isDef {
		return ""
	}
	return best
}

// qidAtPath is the id of the class named name defined in path.
func qidAtPath(s Store, repo, ref, path, name string) string {
	mod := pkgFromPath(path)
	if stem := fileModuleStem(path); stem != "" {
		if mod != "" {
			mod += "."
		}
		mod += stem
	}
	q := name
	if mod != "" {
		q = mod + "." + name
	}
	if _, ok := s.SymbolAt(repo, q, ref); ok {
		return q
	}
	return ""
}
