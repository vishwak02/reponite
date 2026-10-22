// precise.go merges call edges a compiler proved into the inferred call
// graph. A precise driver (clang for C/C++) reports what it saw by LOCATION — the file and line of the calling
// definition and of the called one — so its results do not depend on how
// this indexer names symbols and can be cached across refs by file content.
// indexFiles maps those locations to qids with the same locator queries use,
// and per caller and called name the compiler's verdict replaces the
// inferred one; what the compiler did not see (a macro's call, a construct
// it could not bind) keeps the inferred edge. Pure and stdlib-only (ADR-018).
package processing

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// PreciseCall is one call a compiler saw, located by definitions.
type PreciseCall struct {
	CallerPath string // the calling definition's file and (1-based) line
	CallerLine int
	Name       string // the called name as written (f, not ns::Cls::f)
	CalleePath string // the called definition, when it is the repo's
	CalleeLine int
	External   bool   // bound to a definition outside the repo (a library)
	Unbound    bool   // the compiler could not bind this call (a dependent or broken one)
	Virtual    bool   // bound to a virtual method through an object: overrides may run instead
	RecvPath   string // a virtual call's object's class definition ("" unknown)
	RecvLine   int
}

// PreciseUnit is one compiled unit's calls (a translation unit, a project),
// all proven by one method at one confidence.
type PreciseUnit struct {
	Method string
	Conf   float64
	Calls  []PreciseCall
}

// PreciseCaller is the compiler's verdicts for one caller, by called name.
type PreciseCaller struct {
	Bound    map[string][]string // name -> callee qids
	External map[string]bool     // name bound outside the repo
	Unbound  map[string]bool     // some call of the name the compiler could not bind
	// Virtual is each callee some call bound virtually, with the classes of
	// the objects it was called on (none known: any subclass's override)
	Virtual map[string][]string
	Via     map[string]Proof // callee qid -> the strongest unit that bound it
}

// Proof is how a bound edge was proven.
type Proof struct {
	Method string
	Conf   float64
}

// PreciseEdges is callerQID -> the compiler's verdicts.
type PreciseEdges map[string]*PreciseCaller

// locIndex maps a file line to the innermost definition (qid) spanning it.
type locIndex map[string][]locSpan

type locSpan struct {
	qid        string
	start, end int
}

// at is the qid of the innermost definition of path spanning line, "" none
// — and "" when two different definitions share that innermost span (the
// members a macro generates on one line): the line does not say which.
func (li locIndex) at(path string, line int) string {
	best, size, tie := "", 1<<30, false
	for _, s := range li[path] {
		if s.start > line || line > s.end {
			continue
		}
		switch d := s.end - s.start; {
		case d < size:
			best, size, tie = s.qid, d, false
		case d == size && s.qid != best:
			tie = true
		}
	}
	if tie {
		return ""
	}
	return best
}

// newLocIndex locates every definition of the files the units name, with the
// locator queries use (query.LocateSymbol), so a location maps to the qid a
// query would answer for it.
func newLocIndex(units []PreciseUnit, files []ParsedFile, qidsByPath map[string][]string) locIndex {
	need := map[string]bool{}
	for _, u := range units {
		for _, c := range u.Calls {
			need[c.CallerPath] = true
			if c.CalleePath != "" {
				need[c.CalleePath] = true
			}
			if c.RecvPath != "" {
				need[c.RecvPath] = true
			}
		}
	}
	li := locIndex{}
	for _, f := range files {
		if !need[f.Path] {
			continue
		}
		src := f.Content
		if src == "" && f.Load != nil {
			src, _ = f.Load()
		}
		qf := []query.File{{Path: f.Path, Content: src, Symbols: f.Spans}}
		claimed := map[[2]int]bool{}
		byName := map[string][]string{}
		for _, q := range qidsByPath[f.Path] {
			byName[BaseName(q)] = append(byName[BaseName(q)], q)
			if _, s, e, ok := query.LocateSymbol(qf, q); ok {
				li[f.Path] = append(li[f.Path], locSpan{qid: q, start: s, end: e})
				claimed[[2]int{s, e}] = true
			}
		}
		// overloads share one id (mergeOverload) but each has its own span:
		// a span no id claimed belongs to the id of its name in the file —
		// of the class whose body holds it, when several classes have one
		for _, sp := range f.Spans {
			qs := byName[sp.Name]
			if len(qs) == 0 || claimed[[2]int{sp.StartLine, sp.EndLine}] {
				continue
			}
			if len(qs) > 1 {
				qs = ownedByEnclosing(qs, f.Spans, sp)
			}
			if len(qs) == 1 {
				li[f.Path] = append(li[f.Path], locSpan{qid: qs[0], start: sp.StartLine, end: sp.EndLine})
			}
		}
	}
	return li
}

// ownedByEnclosing keeps the ids of qs that are methods of the innermost
// span strictly enclosing sp (its class), or the free ones when none does.
func ownedByEnclosing(qs []string, spans []query.SymbolSpan, sp query.SymbolSpan) []string {
	owner, size := "", 1<<30
	for _, o := range spans {
		if o.StartLine <= sp.StartLine && o.EndLine >= sp.EndLine && (o.StartLine < sp.StartLine || o.EndLine > sp.EndLine) &&
			o.EndLine-o.StartLine < size {
			owner, size = o.Name, o.EndLine-o.StartLine
		}
	}
	var out []string
	for _, q := range qs {
		if owner != "" && strings.HasSuffix(q, "."+owner+"."+sp.Name) {
			out = append(out, q)
		}
	}
	if owner == "" && len(qs) == 1 {
		out = qs
	}
	return out
}

// preciseEdges turns located units into per-caller verdicts. A call whose
// caller or callee does not map to a definition is dropped: its caller keeps
// the inferred edge for that name. isType reports a qid that is a type: a
// reference in a class body outside any method has no calling function.
func preciseEdges(units []PreciseUnit, li locIndex, isType func(string) bool) PreciseEdges {
	out := PreciseEdges{}
	for _, u := range units {
		for _, c := range u.Calls {
			from := li.at(c.CallerPath, c.CallerLine)
			if from == "" || c.Name == "" || isType != nil && isType(from) {
				continue
			}
			pc := out[from]
			if pc == nil {
				pc = &PreciseCaller{Bound: map[string][]string{}, External: map[string]bool{}, Unbound: map[string]bool{},
					Virtual: map[string][]string{}, Via: map[string]Proof{}}
				out[from] = pc
			}
			switch {
			case c.Unbound:
				pc.Unbound[c.Name] = true
			case c.External:
				pc.External[c.Name] = true
			case c.CalleePath != "":
				to := li.at(c.CalleePath, c.CalleeLine)
				if to == from && (c.CalleePath != c.CallerPath || c.CalleeLine != c.CallerLine) {
					// a definition nested in the caller that this index does
					// not hold (a local class's method, inside a test body):
					// the caller's own span contains it, but it is not the caller
					to = ""
				}
				if to == "" || isType != nil && isType(to) {
					// the repo's, but not a function this index holds (a
					// line inside a class body only, a nested definition)
					pc.Unbound[c.Name] = true
					continue
				}
				if !containsStr(pc.Bound[c.Name], to) {
					pc.Bound[c.Name] = append(pc.Bound[c.Name], to)
				}
				if p, ok := pc.Via[to]; !ok || u.Conf > p.Conf {
					pc.Via[to] = Proof{u.Method, u.Conf}
				}
				if c.Virtual {
					recv := ""
					if c.RecvPath != "" {
						if r := li.at(c.RecvPath, c.RecvLine); r != "" && isType != nil && isType(r) {
							recv = r
						}
					}
					rs, seen := pc.Virtual[to]
					switch {
					case !seen:
						pc.Virtual[to] = nil
						if recv != "" {
							pc.Virtual[to] = []string{recv}
						}
					case len(rs) > 0 && recv != "" && !containsStr(rs, recv):
						pc.Virtual[to] = append(rs, recv)
					case recv == "":
						pc.Virtual[to] = nil // a call on an object of unknown class: any override
					}
				}
			}
		}
	}
	return out
}

// calleeBase is the called name an edge stands for: the last segment of a
// qid (pkg.Cls.f) or of an unresolved name (std::abs, (ambiguous).f).
func calleeBase(name string) string {
	if i := strings.LastIndex(name, "::"); i >= 0 {
		name = name[i+2:]
	}
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	return name
}

// mergePrecise applies the compiler's verdicts for one caller to its inferred
// callees: a name the compiler bound (into the repo or out of it) at every
// call keeps only the compiler's targets (and, for a virtual call, every
// override overridesOf finds for them, of the classes the objects called on
// derive from); a name it could not bind somewhere
// keeps the inferred edges beside the proven ones; a name it never saw keeps
// the inferred edges alone.
func mergePrecise(inferred []query.Callee, pc *PreciseCaller, nodeSet map[string]bool, overridesOf func(qid string, recvs []string) []query.Callee) []query.Callee {
	if pc == nil {
		return inferred
	}
	decided := func(name string) bool {
		return (len(pc.Bound[name]) > 0 || pc.External[name]) && !pc.Unbound[name]
	}
	out := make([]query.Callee, 0, len(inferred))
	at := map[string]int{}
	add := func(c query.Callee) {
		if i, ok := at[c.Name]; ok {
			if c.Confidence > out[i].Confidence {
				out[i] = c
			}
			return
		}
		at[c.Name] = len(out)
		out = append(out, c)
	}
	for _, c := range inferred {
		// a name the compiler decided is its verdict, overrides included: it
		// tells a virtual call (whose overrides are added below) from one
		// that is not
		if !decided(calleeBase(c.Name)) {
			add(c)
		}
	}
	names := make([]string, 0, len(pc.Bound)+len(pc.External))
	for n := range pc.Bound {
		names = append(names, n)
	}
	for n := range pc.External {
		if len(pc.Bound[n]) == 0 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		repo := false
		for _, q := range pc.Bound[n] {
			if nodeSet[q] {
				p := pc.Via[q]
				add(query.Callee{Name: q, ResolutionMethod: p.Method, Confidence: p.Conf})
				repo = true
				if recvs, virt := pc.Virtual[q]; virt && overridesOf != nil {
					// the call runs whichever override the object's class has
					for _, o := range overridesOf(q, recvs) {
						add(o)
					}
				}
			}
		}
		if !repo && pc.External[n] && !pc.Unbound[n] {
			add(query.Callee{Name: n, ResolutionMethod: MethodExternal, Confidence: ConfExternal})
		}
	}
	return out
}

// preciseInputs are the files a C/C++ precise driver reads: sources, headers
// and the manifests that say how they build.
func preciseInput(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".c", ".cc", ".cpp", ".cxx", ".c++", ".h", ".hh", ".hpp", ".hxx", ".h++", ".inl", ".ipp", ".tpp", ".ixx",
		".inc", ".def", ".tcc", ".cuh":
		return true
	case "":
		// an extensionless header (<Eigen/Dense>) under an include directory
		return strings.HasPrefix(rel, "include/") || strings.Contains(rel, "/include/")
	}
	switch path.Base(rel) {
	case "package.xml", "compile_commands.json":
		return true
	}
	return false
}

// blobID is git's id of a file's content (sha1 of "blob <len>\0" + content),
// so a working tree's files and a ref's share precise-cache entries.
func blobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}
