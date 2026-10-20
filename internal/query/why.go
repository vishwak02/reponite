// why.go answers "why is this code the way it is?": the commits that changed a
// symbol's text or signature, each with its date, author, subject, a body
// excerpt and the PR/issue it names, plus which indexed refs first shipped the
// change and the symbol's compat history across those refs. The history comes
// from a HistoryProvider (a thin adapter over the repo's git object store —
// `git log -L` on the symbol's line range at each indexed ref's commit, pickaxe
// as the fallback); everything here — choosing ranges, merging per-ref runs,
// classifying a change, ordering refs, budgeting — is pure over the Store and
// the provider, so it is unit-tested in-sandbox with a fake provider (ADR-018).
package query

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"
)

// DefaultWhyBudget is the token budget when the caller passes <= 0.
const DefaultWhyBudget = 1500

// HistoryKind classifies what a commit did to the symbol's lines.
type HistoryKind string

const (
	ChangeIntroduced HistoryKind = "introduced" // the range appears from nothing (file created or lines added)
	ChangeSignature  HistoryKind = "signature"  // the declaration line (name/parameters) changed
	ChangeBody       HistoryKind = "body"       // only the body changed
	ChangeMoved      HistoryKind = "moved"      // the range arrived from another file/location (an older indexed ref has it elsewhere)
	ChangeMention    HistoryKind = "mention"    // pickaxe fallback: the commit added/removed the name in the file
)

// Change is one commit that touched a symbol's lines, as the provider reports it.
type Change struct {
	Commit  string
	Time    int64 // author time, unix seconds
	Author  string
	Subject string
	Body    string
	Kind    HistoryKind
	Paths   []string // file path(s) the range lived in for this commit
	Added   int      // lines added within the range
	Removed int      // lines removed within the range
}

// LineRange is a symbol's location at one commit.
type LineRange struct {
	Commit     string
	Path       string
	Start, End int
	Name       string // bare symbol name (classifies declaration-line edits)
}

// HistoryProvider reads a repo's history from its object store — read-only,
// never a checkout. The production adapter shells out to git
// (processing.GitHistory); tests use a fake.
type HistoryProvider interface {
	// LineHistory: commits reachable from r.Commit that changed lines
	// [r.Start, r.End] of r.Path (as numbered at r.Commit), newest first.
	LineHistory(repo string, r LineRange, limit int) ([]Change, error)
	// Pickaxe: commits reachable from commit that changed the number of
	// occurrences of needle in paths (all paths when empty), newest first.
	Pickaxe(repo, commit, needle string, paths []string, limit int) ([]Change, error)
	// Contains maps each commit to the tips (label -> commit) whose history
	// includes it.
	Contains(repo string, commits []string, tips map[string]string) (map[string][]string, error)
	// CommitTimes returns each commit's committer time (unix seconds).
	CommitTimes(repo string, commits []string) (map[string]int64, error)
}

// WhyCommit is one change in the answer.
type WhyCommit struct {
	Commit  string // full SHA
	Date    string // YYYY-MM-DD (author date, UTC)
	Author  string
	Subject string
	Body    string // excerpt; trailers dropped
	PRs     []string
	Tickets []string
	Kind    HistoryKind
	Path    string // where the range lived in this commit
	Added   int
	Removed int
	FirstIn string // earliest indexed ref (by commit time) containing this commit; "" = none indexed
	InRefs  int    // how many indexed refs contain it
}

// WhyCompat is the symbol at one indexed ref, compared with the previous one
// in commit-time order (the compat Oracle, reused).
type WhyCompat struct {
	Ref     string
	Date    string
	Verdict string // vs the previous ref: added / absent / shape_changed / behavior_changed / compatible; "" for the oldest
	Present bool
}

// WhyResult is the history answer for one symbol.
type WhyResult struct {
	Symbol    string // qualified id: drill-in handle for brief/context
	Repo, Ref string
	Path      string
	StartLine int
	EndLine   int
	Commits   []WhyCommit
	Total     int // commits found (before the budget)
	Compat    []WhyCompat
	Truncated bool
	Next      string // how to get the rest, when truncated
	Note      string
	Meta      Meta
}

// maxWhyCommits bounds one history walk; the budget usually binds first.
const maxWhyCommits = 200

// Why assembles a symbol's change history at repo@ref. hp may be nil (no git
// history reachable): the compat history is still returned and Note says why
// commits are missing.
func Why(s Store, hp HistoryProvider, repo, ref, symbol string, budget int) WhyResult {
	if budget <= 0 {
		budget = DefaultWhyBudget
	}
	eff := EffectiveRef(s, repo, ref)
	res := WhyResult{Symbol: symbol, Repo: repo, Ref: eff, Meta: NewMeta(s, repo, ref)}
	names := ResolveSymbol(s, repo, ref, symbol)
	if len(names) == 0 {
		res.Note = fmt.Sprintf("symbol %q not found at %s@%s", symbol, repo, eff)
		return res
	}
	qid := names[0]
	res.Symbol = qid
	if len(names) > 1 {
		res.Meta.Warnings = append(res.Meta.Warnings, fmt.Sprintf("ambiguous %q; using %s (also: %s)", symbol, qid, strings.Join(names[1:], ", ")))
	}
	base := baseName(qid)

	// Indexed refs holding this symbol, with their commits and times.
	type refInfo struct {
		label, commit string
		time          int64
		present       bool
		sym           SymbolRef
	}
	var refs []refInfo
	tips := map[string]string{}
	var commits []string
	for _, r := range s.Refs(repo) {
		ri := refInfo{label: r}
		if m, ok := s.Manifest(repo, r); ok {
			ri.commit = m.Commit
		}
		ri.sym, ri.present = s.SymbolAt(repo, qid, r)
		ri.present = ri.present && ri.sym.Present
		if ri.commit != "" {
			tips[r] = ri.commit
			commits = append(commits, ri.commit)
		}
		refs = append(refs, ri)
	}
	if hp != nil && len(commits) > 0 {
		if times, err := hp.CommitTimes(repo, commits); err == nil {
			for i := range refs {
				refs[i].time = times[refs[i].commit]
			}
		}
	}
	sort.SliceStable(refs, func(i, j int) bool {
		// A working-tree index has no commit time: it is "now", so last.
		if (refs[i].time == 0) != (refs[j].time == 0) {
			return refs[j].time == 0
		}
		if refs[i].time != refs[j].time {
			return refs[i].time < refs[j].time
		}
		return refs[i].label < refs[j].label
	})

	// Compat history: each ref against the previous one in time order.
	var prev *refInfo
	for i := range refs {
		r := &refs[i]
		c := WhyCompat{Ref: r.label, Date: day(r.time), Present: r.present}
		if prev != nil {
			switch {
			case !prev.present && r.present:
				c.Verdict = "added"
			case !r.present && !prev.present:
				c.Verdict = ""
			default:
				c.Verdict = string(Compat(prev.sym, r.sym).Verdict)
			}
		}
		res.Compat = append(res.Compat, c)
		prev = r
	}

	// The range at the asked ref.
	files := s.Files(repo, eff)
	path, span, ok := locateSymbol(files, qid)
	if ok {
		res.Path, res.StartLine, res.EndLine = path, span.StartLine, span.EndLine
	}
	if hp == nil {
		res.Note = "no git history reachable for " + repo + " (repo directory unknown or not a git repository); compat history only"
		return res
	}
	head := tips[eff]
	if head == "" {
		res.Note = "ref " + eff + " was indexed from a working tree (no commit recorded); index it with --git to get history — compat history only"
		return res
	}
	if !ok {
		res.Note = "source span of " + qid + " not indexed at " + eff + "; compat history only"
		return res
	}

	byCommit := map[string]*Change{}
	var notes []string
	add := func(cs []Change) {
		for i := range cs {
			c := cs[i]
			if old, seen := byCommit[c.Commit]; seen {
				// A commit seen from two ranges: keep the stronger kind.
				if kindRank(c.Kind) > kindRank(old.Kind) {
					old.Kind = c.Kind
				}
				continue
			}
			byCommit[c.Commit] = &c
		}
	}
	run := func(lr LineRange) {
		cs, err := hp.LineHistory(repo, lr, maxWhyCommits)
		if err != nil || len(cs) == 0 {
			// -L refuses a range it cannot place (file missing at that commit,
			// range past EOF): fall back to the commits that added or removed
			// the name in that file.
			pk, perr := hp.Pickaxe(repo, lr.Commit, base, []string{lr.Path}, maxWhyCommits)
			if perr == nil && len(pk) > 0 {
				for i := range pk {
					pk[i].Kind = ChangeMention
				}
				add(pk)
				notes = append(notes, "line history unavailable for "+lr.Path+" at "+shortSHA(lr.Commit)+"; used pickaxe on the name (kind=mention)")
			} else if err != nil {
				notes = append(notes, "history failed for "+lr.Path+": "+err.Error())
			}
			return
		}
		add(cs)
	}
	run(LineRange{Commit: head, Path: path, Start: span.StartLine, End: span.EndLine, Name: base})

	// git -L follows a range through edits within a file, not across a move to
	// another file. When an older indexed ref already had the symbol before
	// its apparent introduction, the symbol moved: continue from that ref's
	// own range (at most a few hops, oldest evidence first).
	for hop := 0; hop < 4; hop++ {
		intro := oldestIntro(byCommit)
		if intro == nil {
			break
		}
		var next *refInfo
		var oqid string
		for i := len(refs) - 1; i >= 0; i-- {
			r := &refs[i]
			if r.commit == "" || r.time <= 0 || r.time >= intro.Time || r.commit == head {
				continue
			}
			if r.present {
				next, oqid = r, qid
				break
			}
			// Moved to another directory: the id changed with it, so look the
			// old ref up by the bare name — only when that is unambiguous.
			if alt := ResolveSymbol(s, repo, r.label, base); len(alt) == 1 {
				next, oqid = r, alt[0]
				break
			}
		}
		if next == nil {
			break
		}
		opath, ospan, ok := locateSymbol(s.Files(repo, next.label), oqid)
		if !ok {
			break
		}
		before := len(byCommit)
		run(LineRange{Commit: next.commit, Path: opath, Start: ospan.StartLine, End: ospan.EndLine, Name: base})
		if len(byCommit) == before {
			break
		}
		// Older history exists, so the newer "introduction" was a move.
		intro.Kind = ChangeMoved
		if opath != path {
			notes = append(notes, fmt.Sprintf("moved: at %s it lived in %s (%s); history continues there", next.label, opath, oqid))
		}
		head = next.commit
	}

	all := make([]Change, 0, len(byCommit))
	for _, c := range byCommit {
		all = append(all, *c)
	}
	sort.Slice(all, func(i, j int) bool {
		if all[i].Time != all[j].Time {
			return all[i].Time > all[j].Time
		}
		return all[i].Commit < all[j].Commit
	})
	res.Total = len(all)

	// Which indexed refs contain each change.
	var shas []string
	for _, c := range all {
		shas = append(shas, c.Commit)
	}
	contains := map[string][]string{}
	if len(shas) > 0 && len(tips) > 0 {
		if m, err := hp.Contains(repo, shas, tips); err == nil {
			contains = m
		} else {
			notes = append(notes, "ref containment unavailable: "+err.Error())
		}
	}
	refTime := map[string]int64{}
	for _, r := range refs {
		refTime[r.label] = r.time
	}

	spent := estTokens(res.Symbol) + estTokens(res.Path) + 20 + 12*len(res.Compat)
	for _, c := range all {
		wc := toWhyCommit(c)
		in := contains[c.Commit]
		wc.InRefs = len(in)
		wc.FirstIn = earliest(in, refTime)
		cost := estWhyCommit(wc)
		if spent+cost > budget && len(res.Commits) > 0 {
			res.Truncated = true
			break
		}
		spent += cost
		res.Commits = append(res.Commits, wc)
	}
	if res.Truncated {
		res.Next = fmt.Sprintf("%d of %d commits shown; re-run with --budget %d (MCP: budget=%d) for the rest", len(res.Commits), res.Total, budget*2, budget*2)
	}
	parts := []string{"commits that changed " + baseName(qid) + "'s lines (git log -L at each indexed ref's commit), newest first; kind: introduced / moved / signature (declaration line) / body; first_in = earliest indexed ref containing the change"}
	parts = append(parts, notes...)
	if res.Total == 0 {
		parts = append(parts, "no commit found for the range — shallow clone, or the history is not in this object store")
	}
	res.Note = strings.Join(parts, " — ")
	return res
}

func kindRank(k HistoryKind) int {
	switch k {
	case ChangeIntroduced, ChangeMoved:
		return 3
	case ChangeSignature:
		return 2
	case ChangeBody:
		return 1
	}
	return 0
}

func oldestIntro(m map[string]*Change) *Change {
	var oldest *Change
	for _, c := range m {
		if oldest == nil || c.Time < oldest.Time || c.Time == oldest.Time && c.Commit < oldest.Commit {
			oldest = c
		}
	}
	if oldest == nil || oldest.Kind != ChangeIntroduced {
		return nil
	}
	return oldest
}

func earliest(labels []string, t map[string]int64) string {
	best := ""
	for _, l := range labels {
		if best == "" || t[l] < t[best] || t[l] == t[best] && l < best {
			best = l
		}
	}
	return best
}

func toWhyCommit(c Change) WhyCommit {
	link := ParseIntentMessage(c.Commit, c.Subject+"\n"+c.Body)
	wc := WhyCommit{Commit: c.Commit, Date: day(c.Time), Author: c.Author, Subject: c.Subject,
		Body: BodyExcerpt(c.Body, 240), PRs: link.PRs, Tickets: link.Tickets, Kind: c.Kind,
		Added: c.Added, Removed: c.Removed}
	if len(c.Paths) > 0 {
		wc.Path = c.Paths[len(c.Paths)-1]
	}
	return wc
}

func estWhyCommit(c WhyCommit) int {
	return 18 + estTokens(c.Subject) + estTokens(c.Body) + estTokens(c.Author) + estTokens(c.Path) +
		2*len(c.PRs) + 3*len(c.Tickets)
}

func day(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format("2006-01-02")
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

var trailerLine = regexp.MustCompile(`^(?i)(signed-off-by|co-authored-by|reviewed-by|acked-by|tested-by|change-id|cc):`)

// BodyExcerpt is a commit body without trailers or blank runs, cut to max
// characters on a word boundary.
func BodyExcerpt(body string, max int) string {
	var kept []string
	for _, l := range strings.Split(body, "\n") {
		l = strings.TrimSpace(l)
		if l == "" || trailerLine.MatchString(l) {
			continue
		}
		kept = append(kept, l)
	}
	s := strings.Join(kept, " ")
	if len(s) <= max {
		return s
	}
	cut := strings.LastIndexByte(s[:max], ' ')
	if cut < max/2 {
		cut = max
	}
	return s[:cut] + " …"
}

// ParseLineLog parses `git log -L` output produced with the record format
// "\x1e%H\x1f%at\x1f%an\x1f%s\x1f%b\x1d" followed by each commit's range
// patch. name (the symbol's bare name) classifies a declaration-line edit.
func ParseLineLog(out, name string) []Change {
	var res []Change
	nameRe := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\b`)
	for _, rec := range strings.Split(out, "\x1e") {
		if strings.TrimSpace(rec) == "" {
			continue
		}
		hdr, patch, _ := strings.Cut(rec, "\x1d")
		f := strings.SplitN(hdr, "\x1f", 5)
		if len(f) < 5 {
			continue
		}
		c := Change{Commit: strings.TrimSpace(f[0]), Author: f[2], Subject: strings.TrimSpace(f[3]), Body: strings.TrimSpace(f[4])}
		fmt.Sscan(f[1], &c.Time)
		c.Kind, c.Paths, c.Added, c.Removed = classifyPatch(patch, nameRe)
		res = append(res, c)
	}
	return res
}

var hunkHdr = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// declLines is how far into the range (from its first line) an edit still
// counts as the declaration: decorators/annotations and a wrapped parameter
// list sit there.
const declLines = 3

func classifyPatch(patch string, nameRe *regexp.Regexp) (HistoryKind, []string, int, int) {
	kind := ChangeBody
	var paths []string
	added, removed := 0, 0
	created := false
	oldLn, newLn, oldStart, newStart := 0, 0, 0, 0
	for _, l := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			if i := strings.LastIndex(l, " b/"); i >= 0 {
				paths = appendUnique(paths, l[i+3:])
			}
		case strings.HasPrefix(l, "--- "):
			if strings.TrimSpace(l[4:]) == "/dev/null" {
				created = true
			}
		case strings.HasPrefix(l, "+++ "):
		case strings.HasPrefix(l, "@@"):
			m := hunkHdr.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			fmt.Sscan(m[1], &oldStart)
			fmt.Sscan(m[3], &newStart)
			oldLn, newLn = oldStart, newStart
			if m[2] == "0" { // the range had no lines before this commit
				created = true
			}
		case strings.HasPrefix(l, "+"):
			added++
			if newLn-newStart < declLines && nameRe.MatchString(l) {
				kind = ChangeSignature
			}
			newLn++
		case strings.HasPrefix(l, "-"):
			removed++
			if oldLn-oldStart < declLines && nameRe.MatchString(l) {
				kind = ChangeSignature
			}
			oldLn++
		default:
			oldLn++
			newLn++
		}
	}
	if created {
		kind = ChangeIntroduced
	}
	return kind, paths, added, removed
}

func appendUnique(xs []string, x string) []string {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

// locateSymbol finds qid's file and span at a ref, disambiguating a method by
// its class: of several same-named spans in the package, the one nested in a
// span named after the qid's class segment, or defined out of class as
// `Class::name` (C++) or on a `Class` receiver (Go), wins — `pkg.Cart.add` is
// Cart's add, not the first `add` in the directory.
func locateSymbol(files []File, qid string) (string, SymbolSpan, bool) {
	base, owner := baseName(qid), qidOwner(qid)
	bestPath, bestSpan, bestScore := "", SymbolSpan{}, -1
	for _, f := range files {
		p := pkgFromPath(f.Path)
		var score int
		switch {
		case p != "" && strings.HasPrefix(qid, p+"."):
			score = 2 * len(p)
		case p == "" && !strings.Contains(qid, "/"):
			score = 0
		default:
			continue
		}
		// the file's own module named in the id (dir.module.Class.f): this
		// file, not a same-named class in a sibling module
		if stem := fileModuleStem(f.Path); stem != "" {
			mod := stem
			if p != "" {
				mod = p + "." + stem
			}
			switch {
			case strings.HasPrefix(qid, mod+"."):
				score += 2
			case qid == mod && stem == base:
				score += 2 // dir.main for main.py's main(): the id names its file
			default:
				// dir.f names the directory's own module (index.ts, __init__.py),
				// not card.ts beside it, whose f is dir.card.f
				score--
			}
		}
		for _, sp := range f.Symbols {
			if sp.Name != base {
				continue
			}
			sc := score
			switch {
			case owner != "" && ownedBy(f, sp, owner, base):
				sc++
			case owner == "" && looksLikeMethod(f, sp, base):
				sc-- // a free function's id: prefer a span that is not some type's method
			}
			// Python: a later definition of the name replaces an earlier one
			// (typing.overload stubs come first, the implementation last)
			later := sc == bestScore && f.Path == bestPath && strings.HasSuffix(f.Path, ".py")
			if sc > bestScore || later {
				bestPath, bestSpan, bestScore = f.Path, sp, sc
			}
		}
	}
	return bestPath, bestSpan, bestScore >= 0
}

// qidOwner is the class segment of a qid ("dir.Class.method" -> "Class"), ""
// for a free function ("dir.func"). A directory is never an owner.
func qidOwner(qid string) string {
	rest := strings.TrimSuffix(qid, "."+baseName(qid))
	if rest == qid {
		return ""
	}
	seg := rest[strings.LastIndex(rest, "/")+1:]
	if i := strings.LastIndex(seg, "."); i >= 0 {
		return seg[i+1:]
	}
	if !strings.Contains(rest, "/") {
		return seg // a root-level "Class.method" (or a top-level dir; spans decide)
	}
	return ""
}

// looksLikeMethod: the span's first line declares base as some type's method
// (Go "func (r T) base(", C++ "T::base(").
func looksLikeMethod(f File, sp SymbolSpan, base string) bool {
	if strings.HasSuffix(f.Path, ".rs") {
		return rustImplOwner(f, sp) != "" // a fn in an impl block
	}
	if (fileModuleStem(f.Path) != "" || isCLikePath(f.Path)) && nestedInClass(f, sp) {
		return true // a def/method inside a class body (C++: an inline member; namespaces have no span)
	}
	line := declHead(f, sp)
	return strings.Contains(line, "::"+base+"(") || strings.Contains(line, ") "+base+"(") && strings.HasPrefix(strings.TrimSpace(line), "func (")
}

// ownedBy reports whether span sp (named base) in f belongs to owner: a class
// it is nested in or defined out of (Class::name), or — in Python and
// JavaScript, whose module is the file — the module the file is.
func ownedBy(f File, sp SymbolSpan, owner, base string) bool {
	if strings.HasSuffix(f.Path, ".rs") {
		if o := rustImplOwner(f, sp); o != "" {
			return o == owner
		}
	}
	if fileModuleStem(f.Path) == owner && !nestedInClass(f, sp) {
		return true
	}
	for _, o := range f.Symbols {
		if o.Name == owner && o.StartLine <= sp.StartLine && o.EndLine >= sp.EndLine && o != sp {
			return true
		}
	}
	line := declHead(f, sp)
	return scopedDef(line, owner, base) || hasWordAt(line, owner+") "+base)
}

// scopedDef: line defines base out of class owner — owner::base, or a class
// template's owner<T, U>::base.
func scopedDef(line, owner, base string) bool {
	if hasWordAt(line, owner+"::"+base) {
		return true
	}
	for from := 0; ; {
		i := strings.Index(line[from:], owner+"<")
		if i < 0 {
			return false
		}
		at := from + i
		from = at + 1
		if at > 0 && isIdentByte(line[at-1]) {
			continue
		}
		depth, j := 0, at+len(owner)
		for ; j < len(line); j++ {
			if line[j] == '<' {
				depth++
			} else if line[j] == '>' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if rest := strings.TrimLeft(line[min(j+1, len(line)):], " "); strings.HasPrefix(rest, "::"+base) {
			r := rest[len("::"+base):]
			if r == "" || !isIdentByte(r[0]) {
				return true
			}
		}
	}
}

// hasWordAt: s holds w not preceded by an identifier character (Client)
// Scan is not mockWiFiClient) Scan).
func hasWordAt(s, w string) bool {
	for from := 0; ; {
		i := strings.Index(s[from:], w)
		if i < 0 {
			return false
		}
		at := from + i
		if at == 0 || !isIdentByte(s[at-1]) {
			return true
		}
		from = at + 1
	}
}

// definitionCount is how many definitions in qid's package carry its name and
// class — more than one for C++/Java overloads, which share one id (their
// edges are merged). Declarations (a line ending in ";") do not count.
func definitionCount(files []File, qid string) int {
	base, owner := baseName(qid), qidOwner(qid)
	dir := ""
	for _, f := range files {
		if p := pkgFromPath(f.Path); p != "" && len(p) > len(dir) && strings.HasPrefix(qid, p+".") {
			dir = p
		}
	}
	n := 0
	for _, f := range files {
		if pkgFromPath(f.Path) != dir {
			continue
		}
		for _, sp := range f.Symbols {
			if sp.Name != base || owner != "" && !ownedBy(f, sp, owner, base) || owner == "" && looksLikeMethod(f, sp, base) {
				continue
			}
			if !strings.HasSuffix(strings.TrimSpace(lineAt(f.Content, sp.StartLine)), ";") {
				n++
			}
		}
	}
	return n
}

// LocateSymbol finds the file and line span defining qid among files (the
// spans the index stored), disambiguating methods by class like every query
// does. ok is false when the symbol's source is not indexed.
func LocateSymbol(files []File, qid string) (path string, start, end int, ok bool) {
	p, sp, ok := locateSymbol(files, qid)
	return p, sp.StartLine, sp.EndLine, ok
}

// fileModuleStem is the module segment a Python/JavaScript file adds to its
// symbols' ids (the indexer's pkgOfFile): its base name without extension,
// other dots as underscores; "" for other languages and for __init__.py and
// index.* (a directory's own module).
func fileModuleStem(p string) string {
	b := path.Base(p)
	i := strings.Index(b, ".")
	if i < 0 {
		return ""
	}
	ext := b[i:]
	switch strings.ToLower(path.Ext(b)) {
	case ".py", ".ts", ".tsx", ".js", ".jsx", ".mjs", ".cjs", ".vue", ".svelte":
	case ".rs":
		if s := b[:i]; s == "mod" || s == "lib" || s == "main" {
			return ""
		}
	default:
		return ""
	}
	stem := b[:i]
	if strings.Count(ext, ".") > 1 {
		stem += "_" + strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(ext, "."), path.Ext(ext)), ".", "_")
	}
	if stem == "__init__" || stem == "index" {
		return ""
	}
	return stem
}

// nestedInClass: another span strictly encloses sp (a method in a class body).
func nestedInClass(f File, sp SymbolSpan) bool {
	for _, o := range f.Symbols {
		if o != sp && o.StartLine <= sp.StartLine && o.EndLine >= sp.EndLine && (o.StartLine < sp.StartLine || o.EndLine > sp.EndLine) {
			return true
		}
	}
	return false
}

// declHead is a definition's head: from its first line through the line
// that opens its parameter list (at most four lines), joined by spaces. A
// C++ return type often sits on its own line above Class::name(.
func declHead(f File, sp SymbolSpan) string {
	var parts []string
	for n := sp.StartLine; n < sp.StartLine+4 && (sp.EndLine == 0 || n <= sp.EndLine); n++ {
		ln := lineAt(f.Content, n)
		parts = append(parts, strings.TrimSpace(ln))
		if strings.Contains(ln, "(") {
			break
		}
	}
	return strings.Join(parts, " ")
}

// isCLikePath: a C or C++ source or header.
func isCLikePath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx":
		return true
	}
	return false
}
