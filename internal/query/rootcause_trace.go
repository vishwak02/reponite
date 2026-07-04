// rootcause_trace.go implements reponite_rootcause_trace (architecture ext
// §8A.4 / ADR-015): the agent pastes a stack trace and reponite maps each frame
// to a symbol, then runs the root-cause drill-down along the actual failing
// path — so the agent needn't know the entry symbol. Frame parsing is per
// language (Go panic, Python traceback, Node/JS, Java); mapping uses the same
// package-qualified id scheme as the indexer. Pure over the Store (ADR-018):
// parse + map + reuse RootCause. Frames that map to no indexed symbol are
// reported in Unmapped rather than silently dropped (§13A).
package query

import (
	"regexp"
	"sort"
	"strings"
)

// TraceFrame is one parsed stack frame: a file, a function token, and a line.
type TraceFrame struct {
	File     string
	Function string
	Line     int
}

// MappedFrame is a frame after resolution to a stored symbol (Symbol=="" when
// the frame maps to no indexed symbol).
type MappedFrame struct {
	File     string
	Function string
	Symbol   string
}

// RootCauseTraceResult is the trace-seeded drill-down outcome.
type RootCauseTraceResult struct {
	Frames   []MappedFrame
	Unmapped []string
	Result   RootCauseResult
	Note     string
	Meta     Meta
}

var (
	// Python: `  File "svc.py", line 10, in charge`
	rePy = regexp.MustCompile(`^\s*File "([^"]+)", line (\d+), in (\S+)`)
	// Node/JS: `    at charge (/app/svc.js:10:5)` or `    at /app/svc.js:10:5`
	reJSNamed = regexp.MustCompile(`^\s*at (.+?) \((.+):(\d+):\d+\)\s*$`)
	reJSBare  = regexp.MustCompile(`^\s*at (.+):(\d+):\d+\s*$`)
	// Java: `	at com.app.Svc.charge(Svc.java:10)`
	reJava = regexp.MustCompile(`^\s*at (\S+)\(([^:()]+\.java):(\d+)\)\s*$`)
	// Go: an indented `	/path/file.go:42 +0x1b` file line paired with the
	// preceding `pkg.Func(...)` function line.
	reGoFile = regexp.MustCompile(`^\s+(.+\.go):(\d+)(?:\s+\+0x[0-9a-f]+)?\s*$`)
	reGoFunc = regexp.MustCompile(`^(\S+)\(`)
)

// ParseStackTrace extracts frames from a stack trace in any supported language.
// It is line-oriented and tolerant: unrecognized lines are skipped.
func ParseStackTrace(trace string) []TraceFrame {
	var frames []TraceFrame
	lines := strings.Split(trace, "\n")
	for i := 0; i < len(lines); i++ {
		ln := lines[i]
		if m := rePy.FindStringSubmatch(ln); m != nil {
			frames = append(frames, TraceFrame{File: m[1], Function: m[3], Line: atoi(m[2])})
			continue
		}
		if m := reJSNamed.FindStringSubmatch(ln); m != nil {
			frames = append(frames, TraceFrame{File: m[2], Function: m[1], Line: atoi(m[3])})
			continue
		}
		if m := reJava.FindStringSubmatch(ln); m != nil {
			frames = append(frames, TraceFrame{File: m[2], Function: m[1], Line: atoi(m[3])})
			continue
		}
		if m := reJSBare.FindStringSubmatch(ln); m != nil {
			frames = append(frames, TraceFrame{File: m[1], Line: atoi(m[2])})
			continue
		}
		// Go: this line is the file line; the previous line named the function.
		if m := reGoFile.FindStringSubmatch(ln); m != nil && i > 0 {
			if fn := reGoFunc.FindStringSubmatch(strings.TrimSpace(lines[i-1])); fn != nil {
				frames = append(frames, TraceFrame{File: m[1], Function: fn[1], Line: atoi(m[2])})
			}
		}
	}
	return frames
}

// RootCauseTrace maps each frame of a stack trace to a symbol at the `to` ref,
// then drills down from every traced frame whose behavior changed between the
// refs, merging the mutation-site frontier (§8A.4). Unmapped frames are noted.
func RootCauseTrace(s Store, repo, from, to, trace string) RootCauseTraceResult {
	res := RootCauseTraceResult{Meta: Meta{Repo: repo, Ref: to}}
	frames := ParseStackTrace(trace)
	if len(frames) == 0 {
		res.Note = "no stack frames parsed from the trace"
		return res
	}
	fromSnap := s.Snapshot(repo, from)
	toSnap := s.Snapshot(repo, to)

	var changed []string
	for _, f := range frames {
		qid, ok := resolveFrame(s, repo, to, f.File, f.Function)
		mf := MappedFrame{File: f.File, Function: f.Function}
		if !ok {
			res.Unmapped = append(res.Unmapped, frameLabel(f))
			res.Frames = append(res.Frames, mf)
			continue
		}
		mf.Symbol = qid
		res.Frames = append(res.Frames, mf)
		if bf, okf := fromSnap.Symbols[qid]; okf {
			if tf, okt := toSnap.Symbols[qid]; okt && bf.BehaviorHash != tf.BehaviorHash {
				changed = append(changed, qid)
			}
		}
	}

	if len(changed) == 0 {
		res.Note = "no traced frame changed behavior between " + from + " and " + to
		return res
	}

	// Merge the mutation frontier reachable from any changed frame on the path,
	// keeping the shallowest depth seen for each origin.
	byName := map[string]Origin{}
	for _, tgt := range changed {
		for _, o := range RootCause(tgt, fromSnap, toSnap).Origins {
			if ex, ok := byName[o.Name]; !ok || o.Depth < ex.Depth {
				byName[o.Name] = o
			}
		}
	}
	origins := make([]Origin, 0, len(byName))
	for _, o := range byName {
		origins = append(origins, o)
	}
	sort.Slice(origins, func(i, j int) bool {
		if origins[i].Depth != origins[j].Depth {
			return origins[i].Depth < origins[j].Depth
		}
		return origins[i].Name < origins[j].Name
	})
	res.Result = RootCauseResult{Target: changed[0], Changed: true, Origins: origins}
	if len(origins) == 0 {
		res.Result.Note = "behavior changed on the path but no internal mutation found; origin is likely a cross-repo/unindexed callee (§8.4)"
	}
	return res
}

// resolveFrame maps a frame's (file, function) to a stored qualified id at a
// ref. It resolves by bare function name and, when that is ambiguous, uses the
// frame's file to pick the candidate in the matching package.
func resolveFrame(s Store, repo, ref, file, function string) (string, bool) {
	fn := bareFuncName(function)
	if fn == "" {
		return "", false
	}
	cands := ResolveSymbol(s, repo, ref, fn)
	if len(cands) == 0 {
		return "", false
	}
	if len(cands) == 1 || file == "" {
		return cands[0], true
	}
	if pkg, ok := frameFilePkg(s.Files(repo, ref), file, fn); ok {
		for _, c := range cands {
			if (pkg == "" && c == fn) || (pkg != "" && strings.HasPrefix(c, pkg+".")) {
				return c, true
			}
		}
	}
	return cands[0], true
}

// frameFilePkg returns the package of the indexed file that matches the frame's
// file path and defines a span named fn.
func frameFilePkg(files []File, file, fn string) (string, bool) {
	for _, f := range files {
		if !pathMatches(f.Path, file) {
			continue
		}
		for _, sp := range f.Symbols {
			if sp.Name == fn {
				return pkgFromPath(f.Path), true
			}
		}
	}
	return "", false
}

// pathMatches reports whether an indexed (repo-relative) path corresponds to a
// stack-frame path, which may be absolute, have an extra prefix, or (Java) be a
// bare filename.
func pathMatches(indexed, frame string) bool {
	if indexed == frame || strings.HasSuffix(frame, "/"+indexed) || strings.HasSuffix(indexed, "/"+frame) {
		return true
	}
	return baseFile(indexed) == baseFile(frame)
}

func baseFile(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

// bareFuncName strips language-specific decoration from a frame's function token
// down to the declared name: Go "pkg.(*T).Method" -> "Method", Java
// "com.app.Svc.charge" -> "charge", JS "Object.method" -> "method", trailing
// "()" removed.
func bareFuncName(fn string) string {
	fn = strings.TrimSuffix(strings.TrimSpace(fn), "()")
	if i := strings.LastIndex(fn, "."); i >= 0 {
		fn = fn[i+1:]
	}
	return strings.Trim(fn, "()*")
}

func frameLabel(f TraceFrame) string {
	if f.Function != "" {
		return f.Function + " (" + f.File + ")"
	}
	return f.File
}

func atoi(s string) int {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}
