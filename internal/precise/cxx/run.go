package cxx

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
)

// driverVersion changes whenever what a unit's Result holds changes: cached
// results of an older driver are then never read.
const driverVersion = "cxx-8"

// Method labels, as the indexer's provenance (processing.MethodClang…).
const (
	MethodCompdb = "clang-compdb"
	MethodSynth  = "clang-synth"
)

// Config is how to run the driver.
type Config struct {
	Exe      string   // the binary to run as `<Exe> cxx-worker` (the running reponite)
	Libclang string   // libclang to load ("" the usual names)
	Jobs     int      // parallel workers (0: one per CPU, as many as free memory holds)
	Headers  []string // header trees the build sees: ROS, a sysroot's /usr/include, generated messages
	Args     []string // extra compiler arguments
	Compdb   string   // compile_commands.json ("" look for one in the tree)
	Store    *precise.Store
	Timeout  time.Duration // per translation unit (0: 2 minutes)
	Log      io.Writer     // progress (nil: none)
}

// Input is one ref's C/C++ code.
type Input struct {
	Files map[string]string // every repo file -> its content id (git blob hash): cache validity
	// Extra are repo files a unit may read that are not units (an excluded
	// directory's headers), by content id.
	Extra map[string]string
	// Read returns a repo file's content (package.xml, compile_commands.json,
	// a header outside Files).
	Read func(rel string) ([]byte, error)
	// Materialize puts the ref's C/C++ sources on disk — only when a unit
	// misses the store — and returns their directory and how to remove it.
	Materialize func() (root string, cleanup func(), err error)
	// CachedOnly answers from the store alone: a unit not cached is not
	// compiled (its callers keep inferred edges).
	CachedOnly bool
	// Libs are files the build compiles against that are not the repo's (its
	// pinned submodules), by path; Materialize puts them under LibDir(root).
	Libs   map[string]string
	LibDir func(root string) string
	// Dir is the repo's directory on disk (a ref's too: where its build
	// database's paths point), "" unknown.
	Dir string
	// LoadLibs gives Libs when they are first needed (nil Libs): a ref
	// whose units were never compiled does not read its submodules.
	LoadLibs func() map[string]string
}

// libMark stands for the directory Materialize puts Libs in.
const libMark = "\x00LIBS"

// rootMark stands for the sources' directory in compile arguments, so a
// unit's cache key does not depend on where a ref was copied out to.
const rootMark = "\x00ROOT"

func withRoot(args []string, root, libs string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = strings.ReplaceAll(strings.ReplaceAll(a, rootMark, root), libMark, libs)
	}
	return out
}

// Edge is one call clang proved, located by definitions (repo-relative).
type Edge struct {
	CallerPath string
	CallerLine int
	CallerName string // the calling definition's own name ("" unknown)
	Name       string
	CalleePath string
	CalleeLine int
	External   bool
	Unbound    bool
	Virtual    bool   // a virtual method called through an object
	RecvPath   string // the object's class definition ("" unknown)
	RecvLine   int
	Method     string // how its unit was compiled (MethodCompdb, MethodSynth)
}

// Output is what the driver proved for one ref.
type Output struct {
	Method string // MethodCompdb when the build's commands compiled any unit
	Edges  []Edge
	Units  int // translation units
	Synth  int // of them compiled without the build's commands
	Cached int // of them read from the store
	Failed int // of them not parsed (crash, timeout, unparseable)
	Errors int // of them parsed with error diagnostics
}

var sourceExt = map[string]bool{".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".c++": true}

// Run parses every translation unit of in (from the store when unchanged)
// and links what they bound into located call edges.
func Run(ctx context.Context, in Input, cfg Config) (Output, error) {
	var tus []string
	for p := range in.Files {
		if sourceExt[strings.ToLower(path.Ext(p))] {
			tus = append(tus, p)
		}
	}
	sort.Strings(tus)
	out := Output{Units: len(tus)}
	if len(tus) == 0 {
		return out, nil
	}
	if in.CachedOnly && !cfg.Store.Has("cpp") {
		return out, nil // nothing was ever compiled here: the default costs nothing
	}
	if err := Load(cfg.Libclang); err != nil {
		return out, err
	}
	cc := newCompileCtx(in, cfg)
	out.Method = MethodSynth
	for _, tu := range tus {
		if cc.method(tu) == MethodCompdb {
			out.Method = MethodCompdb
		} else {
			out.Synth++
		}
	}
	clangVer := Version()
	headerID := precise.Key(append([]string{"headers"}, cfg.Headers...)...)

	results := make([]Result, len(tus))
	ok := make([]bool, len(tus))
	var todo []int
	mkeys := make([]string, len(tus))
	ms := make([]manifests, len(tus))
	found := make([]bool, len(tus))
	nfound := 0
	for i, tu := range tus {
		// the key: the unit's own source and flags; what else it read is
		// in its manifest
		mkeys[i] = precise.Key("manifest", driverVersion, tu, in.Files[tu], cc.method(tu), precise.Key(cc.key(tu)...), clangVer, headerID)
		if found[i] = cfg.Store.Get("cpp", mkeys[i], &ms[i]); found[i] {
			nfound++
		}
	}
	if in.CachedOnly && nfound == 0 {
		return out, nil // nothing of this ref was compiled
	}
	if in.Libs == nil && in.LoadLibs != nil {
		in.Libs = in.LoadLibs()
		cc = newCompileCtx(in, cfg) // the same keys; submodule include paths added
	}
	v := newValidator(in, cc)
	for i, tu := range tus {
		if found[i] {
			for _, m := range ms[i].List {
				if v.valid(m, tu) && cfg.Store.Get("cpp", m.Result, &results[i]) {
					ok[i] = true
					break
				}
			}
		}
		if ok[i] {
			out.Cached++
			continue
		}
		todo = append(todo, i)
	}
	if len(todo) > 0 && !in.CachedOnly && in.Materialize != nil {
		dir, cleanup, err := in.Materialize()
		if err != nil {
			return out, err
		}
		defer cleanup()
		root, err := filepath.Abs(dir)
		if err == nil {
			root, err = filepath.EvalSymlinks(root)
		}
		if err != nil {
			return out, err
		}
		libs := ""
		if in.LibDir != nil {
			libs = in.LibDir(root)
		}
		replies := runPool(ctx, cfg, len(todo), func(k int) Request {
			tu := tus[todo[k]]
			return Request{Root: root, File: filepath.Join(root, filepath.FromSlash(tu)), Args: withRoot(cc.full(tu), root, libs)}
		})
		for k, rep := range replies {
			i := todo[k]
			if rep == nil || rep.Err != "" {
				out.Failed++
				if cfg.Log != nil && rep != nil {
					fmt.Fprintf(cfg.Log, "reponite: clang: %s: %s\n", tus[i], rep.Err)
				}
				continue
			}
			if cfg.Log != nil && os.Getenv("REPONITE_CXX_DEBUG") != "" && len(rep.Diags) > 0 {
				fmt.Fprintf(cfg.Log, "reponite: clang: %s: %d errors, first: %s\n", tus[i], rep.Errors, rep.Diags[0])
			}
			if !readsItself(rep.Result, tus[i]) {
				// the unit did not see its own source as the repo's (a broken
				// root): use nothing of it, and never cache it
				out.Failed++
				continue
			}
			m, known := v.record(rep.Result, tus[i], root, libs)
			r := rep.Result
			r.Others, r.Missing = nil, nil // in the manifest
			results[i], ok[i] = r, true
			if !known {
				continue // some file it read cannot be identified next time: not cached
			}
			// cache it under the exact sources it read
			parts := []string{"result", mkeys[i]}
			for _, inc := range sortedKeys(m.Includes) {
				parts = append(parts, inc, m.Includes[inc])
			}
			for _, x := range sortedKeys(m.External) {
				parts = append(parts, x, m.External[x])
			}
			m.Result = precise.Key(append(parts, m.Probe, m.Roots, m.Dirs, m.Looked)...)
			if cfg.Store.Put("cpp", m.Result, r) == nil {
				// beside the other refs' entries for this unit
				set := manifests{List: []manifest{m}}
				for _, old := range ms[i].List {
					if old.Result != m.Result && len(set.List) < maxManifests {
						set.List = append(set.List, old)
					}
				}
				_ = cfg.Store.Put("cpp", mkeys[i], set)
				_ = cfg.Store.Put("cpp", latestKey(in, tus[i]), m)
			}
		}
	}
	var all []Result
	var methods []string
	var absent []string
	for i, tu := range tus {
		if !ok[i] {
			absent = append(absent, tu)
			continue
		}
		all = append(all, results[i])
		methods = append(methods, cc.method(tu))
		if results[i].Errors > 0 {
			out.Errors++
		}
	}
	var prior [][]manifest
	for i := range tus {
		if !ok[i] {
			prior = append(prior, ms[i].List)
		}
	}
	out.Edges = link(all, methods, definedIn(in, cfg.Store, absent, prior))
	return out, nil
}

// definedIn is what the units not read this run (not cached, failed) may
// define, nil when every unit was read: a call bound to the only definition
// the read units show may be another program's when one of them defines it
// too. A unit compiled before is known by the functions (USRs) its last
// results defined; one never compiled by every name its text and the repo
// headers it includes hold (a definition a macro writes names it only as an
// argument).
func definedIn(in Input, store *precise.Store, absent []string, prior [][]manifest) func(Call) bool {
	if len(absent) == 0 {
		return nil
	}
	usrs, names := map[string]bool{}, map[string]bool{}
	for k, tu := range absent {
		ms := prior[k]
		var last manifest
		if store.Get("cpp", latestKey(in, tu), &last) {
			ms = append(ms, last)
		}
		for _, m := range ms {
			var r Result
			if store.Get("cpp", m.Result, &r) {
				for _, d := range r.Defs {
					usrs[d.USR] = true
				}
			}
		}
		if in.Read != nil {
			textNames(in, tu, names, map[string]bool{}, 0)
		}
	}
	return func(c Call) bool {
		if c.USR != "" && usrs[c.USR] {
			return true
		}
		cls := usrClass(c.USR)
		return names[cls+"::"+c.Name] || names[c.Name]
	}
}

// latestKey names the manifest a unit of this repo was last compiled with,
// whatever its content was: an edited unit is known by what it defined.
func latestKey(in Input, tu string) string {
	return precise.Key("latest", driverVersion, in.Dir, tu)
}

var reIdent = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
var reQuotedInclude = regexp.MustCompile(`(?m)^\s*#\s*include\s*"([^"]+)"`)

// textNames adds what a repo file, and the repo headers it includes by a
// quoted name, seem to define (definedNames) to names.
func textNames(in Input, rel string, names, seen map[string]bool, depth int) {
	if seen[rel] || depth > 8 {
		return
	}
	seen[rel] = true
	b, err := in.Read(rel)
	if err != nil {
		return
	}
	definedNames(string(b), names)
	for _, m := range reQuotedInclude.FindAllStringSubmatch(string(b), -1) {
		for _, cand := range []string{path.Join(path.Dir(rel), m[1]), m[1]} {
			if _, ok := in.Files[cand]; ok {
				textNames(in, cand, names, seen, depth+1)
			}
		}
		for p := range in.Files {
			if strings.HasSuffix(p, "/"+m[1]) && !seen[p] {
				textNames(in, p, names, seen, depth+1)
			}
		}
	}
}

// readsItself reports whether a unit's result shows its own source file as
// one of the repo's: the include list holds every repo file the unit read,
// the source among them.
func readsItself(r Result, tu string) bool {
	for _, inc := range r.Includes {
		if inc == tu {
			return true
		}
	}
	for _, d := range r.Defs {
		if d.Path == tu {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// link turns every unit's bound calls into located edges: a USR names one
// definition, or — two programs of the repo defining one function each —
// the one whose first declaration the call saw; otherwise (or when a unit
// not read may define the name: elsewhere) the call is not proven, and its
// caller keeps the inferred edge. methods is each result's compile method.
func link(results []Result, methods []string, elsewhere func(Call) bool) []Edge {
	type at struct {
		path string
		line int
	}
	defs := map[string]map[at]Def{}
	for _, r := range results {
		for _, d := range r.Defs {
			if defs[d.USR] == nil {
				defs[d.USR] = map[at]Def{}
			}
			defs[d.USR][at{d.Path, d.Line}] = d
		}
	}
	seen := map[Edge]bool{}
	var out []Edge
	type ownerName struct {
		path  string
		line  int
		owner string // the definition's name: two may share a line
		name  string
	}
	for ri, r := range results {
		method := methods[ri]
		// a name referenced more often than it was called in a definition:
		// some call of it clang dropped (error recovery) — not proven
		calls, refs := map[ownerName]int{}, map[ownerName]int{}
		for _, c := range r.Calls {
			calls[ownerName{c.OwnerPath, c.OwnerLine, c.OwnerName, c.Name}]++
		}
		for _, x := range r.Refs {
			refs[ownerName{x.OwnerPath, x.OwnerLine, x.OwnerName, x.Name}]++
		}
		for k, n := range refs {
			if n > calls[k] {
				e := Edge{CallerPath: k.path, CallerLine: k.line, CallerName: k.owner, Name: k.name, Unbound: true, Method: method}
				if !seen[e] {
					seen[e] = true
					out = append(out, e)
				}
			}
		}
		for _, c := range r.Calls {
			e := Edge{CallerPath: c.OwnerPath, CallerLine: c.OwnerLine, CallerName: c.OwnerName, Name: c.Name, Method: method}
			switch c.Kind {
			case 'E':
				e.External = true
			case 'U':
				e.Unbound = true
			case 'B', 'V':
				e.Virtual = c.Kind == 'V'
				if e.Virtual {
					e.RecvPath, e.RecvLine = c.RecvPath, c.RecvLine
				}
				var cands []Def
				for _, d := range defs[c.USR] {
					cands = append(cands, d)
				}
				if len(cands) > 1 {
					var same []Def
					for _, d := range cands {
						if d.DeclPath == c.DeclPath && d.DeclLine == c.DeclLine {
							same = append(same, d)
						}
					}
					cands = same
				}
				if len(cands) == 1 && (elsewhere == nil || !elsewhere(c)) {
					e.CalleePath, e.CalleeLine = cands[0].Path, cands[0].Line
				} else {
					e.Unbound, e.Virtual, e.RecvPath, e.RecvLine = true, false, "", 0 // no definition the units showed, or several
				}
			}
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CallerPath != b.CallerPath {
			return a.CallerPath < b.CallerPath
		}
		if a.CallerLine != b.CallerLine {
			return a.CallerLine < b.CallerLine
		}
		if a.CallerName != b.CallerName {
			return a.CallerName < b.CallerName
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Method < b.Method
	})
	return out
}

// runPool runs n requests over cfg.Jobs long-lived workers; a nil reply is a
// unit whose worker died or timed out.
func runPool(ctx context.Context, cfg Config, n int, req func(int) Request) []*Reply {
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = defaultJobs()
	}
	if jobs > n {
		jobs = n
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	replies := make([]*Reply, n)
	next := make(chan int)
	var wg sync.WaitGroup
	for w := 0; w < jobs; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var wk *worker
			for k := range next {
				if wk == nil {
					var err error
					if wk, err = startWorker(ctx, cfg); err != nil {
						replies[k] = &Reply{Err: err.Error()}
						continue
					}
				}
				rep, err := wk.do(req(k), timeout)
				if err != nil {
					wk.kill()
					wk = nil
					replies[k] = &Reply{Err: err.Error()}
					continue
				}
				replies[k] = rep
			}
			if wk != nil {
				wk.close()
			}
		}()
	}
	for k := 0; k < n; k++ {
		select {
		case next <- k:
		case <-ctx.Done():
		}
	}
	close(next)
	wg.Wait()
	return replies
}

type worker struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startWorker(ctx context.Context, cfg Config) (*worker, error) {
	exe := cfg.Exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	cmd := exec.CommandContext(ctx, exe, "cxx-worker")
	// a worker recovers from a crash inside libclang (its handlers are its
	// own), unlike the indexing process, which turned them off
	var env []string
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "LIBCLANG_DISABLE_CRASH_RECOVERY=") {
			env = append(env, e)
		}
	}
	cmd.Env = append(env, "REPONITE_LIBCLANG="+cfg.Libclang)
	cmd.Stderr = io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &worker{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}, nil
}

func (w *worker) do(req Request, timeout time.Duration) (*Reply, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	type res struct {
		rep *Reply
		err error
	}
	done := make(chan res, 1)
	go func() {
		if _, err := w.in.Write(append(b, '\n')); err != nil {
			done <- res{nil, err}
			return
		}
		line, err := w.out.ReadBytes('\n')
		if err != nil {
			done <- res{nil, fmt.Errorf("worker died on %s: %v", req.File, err)}
			return
		}
		var rep Reply
		if err := json.Unmarshal(line, &rep); err != nil {
			done <- res{nil, err}
			return
		}
		done <- res{&rep, nil}
	}()
	select {
	case r := <-done:
		return r.rep, r.err
	case <-time.After(timeout):
		return nil, errors.New("timed out parsing " + req.File)
	}
}

func (w *worker) kill() {
	_ = w.cmd.Process.Kill()
	_ = w.cmd.Wait()
}

func (w *worker) close() {
	_ = w.in.Close()
	_ = w.cmd.Wait()
}

// workerMemory is what one worker may hold while parsing a large unit (a ROS
// node with Boost and Eigen): libclang keeps the whole AST.
const workerMemory = 1 << 30

// defaultJobs is one worker per CPU, as many as the free memory holds with
// room to spare: a worker per CPU on a many-core machine can exhaust memory.
func defaultJobs() int {
	n := runtime.NumCPU()
	if avail := memAvailable(); avail > 0 {
		if m := int(avail * 6 / 10 / workerMemory); m < n {
			n = m
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// memAvailable is the memory the system can give without swapping (Linux
// /proc/meminfo MemAvailable), 0 when unknown.
func memAvailable() int64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if rest, ok := strings.CutPrefix(ln, "MemAvailable:"); ok {
			f := strings.Fields(rest)
			if len(f) > 0 {
				if kb, err := strconv.ParseInt(f[0], 10, 64); err == nil {
					return kb << 10
				}
			}
		}
	}
	return 0
}
