// Package tsc is the TypeScript/JavaScript driver of the compiler-exact tier:
// it builds each project of a ref with the TypeScript compiler (the repo's
// own node_modules/typescript, else a configured one), run in a node worker
// process, and reports every call, `new` and JSX element the checker binds,
// located by the call's line and the bound definition's place. reponite
// stays pure Go: without node or typescript the tier is unavailable and
// calls stay inferred. Each project's result is kept in the precise store
// under every file the compiler read or looked for, so a later run (cached
// mode) reuses it without node while those are unchanged.
package tsc

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
)

//go:embed worker.js
var workerJS string

// driverVersion changes whenever what a project's result holds changes —
// the worker's rules too, by its text: results of an older driver are never
// read.
var driverVersion = "tsc-1:" + precise.Key(workerJS)[:12]

// Kind is this driver's directory in the precise store.
const Kind = "ts"

// Method labels, as the indexer's provenance: a call written in TypeScript,
// and one written in JavaScript (checked from its inferred types alone).
const (
	MethodTS = "tsc"
	MethodJS = "tsc-js"
)

// Config is how to run the driver.
type Config struct {
	Node       string // the node binary ("" node on $PATH)
	TypeScript string // a typescript package directory, for a repo without its own
	Jobs       int    // parallel workers (0: one per CPU, at most 4, as free memory allows)
	Store      *precise.Store
	Timeout    time.Duration // per project (0: 5 minutes)
	Log        io.Writer
}

// Input is one ref's TypeScript and JavaScript code.
type Input struct {
	Files map[string]string // every repo input (IsInput) -> its content id (git blob hash)
	Extra map[string]string // inputs the walk excludes (a vendored directory's), by content id
	// Dir is the repo's directory on disk: its node_modules (and the
	// typescript there) are read from it, for a ref read from git too.
	Dir string
	// Read returns a repo file's content: a file the compiler read that is
	// not an input (a JSON module) is identified by it in the cache.
	Read func(rel string) ([]byte, error)
	// Materialize puts the ref's files on disk — only when a project
	// misses the store — and returns their directory and how to remove it.
	Materialize func() (root string, cleanup func(), err error)
	// CachedOnly answers from the store alone, never running node.
	CachedOnly bool
}

// Edge is one call the checker proved, located by the call's line and the
// bound definition (repo-relative).
type Edge struct {
	CallerPath string
	CallerLine int // the line the call is written on
	Name       string
	CalleePath string
	CalleeLine int    // the line of the bound definition's name
	CalleeName string // its own name
	Ctor       bool   // a class constructed (new, a JSX class component)
	External   bool
	Unbound    bool
	Method     string
}

// Output is what the driver proved for one ref.
type Output struct {
	Edges    []Edge
	Projects int // projects (a tsconfig's directory, a package's)
	Cached   int // of them read from the store
	Failed   int // of them not built (no typescript, a worker's crash, a timeout)
	Version  string
	Errs     []string // why some failed
}

var sourceExts = map[string]bool{".ts": true, ".tsx": true, ".mts": true, ".cts": true, ".js": true, ".jsx": true, ".mjs": true, ".cjs": true}

// IsInput reports a file the driver reads: a TypeScript or JavaScript
// source or declaration file, a project's config, a package manifest.
func IsInput(rel string) bool {
	if sourceExts[strings.ToLower(path.Ext(rel))] {
		return true
	}
	b := path.Base(rel)
	return b == "package.json" || b == "jsconfig.json" || (strings.HasPrefix(b, "tsconfig") && strings.HasSuffix(b, ".json"))
}

func isDecl(rel string) bool {
	return strings.HasSuffix(rel, ".d.ts") || strings.HasSuffix(rel, ".d.mts") || strings.HasSuffix(rel, ".d.cts")
}

// project is a set of source files built as one program: those under a
// directory with a tsconfig.json or jsconfig.json (else a package.json, else
// the root) and under no deeper such directory.
type project struct {
	dir    string // "" the root
	config string // its tsconfig.json / jsconfig.json ("" none: the defaults)
	files  []string
}

// projects groups a ref's source files.
func projects(files map[string]string) []project {
	conf, pkg := map[string]string{}, map[string]bool{}
	for p := range files {
		d := path.Dir(p)
		if d == "." {
			d = ""
		}
		switch path.Base(p) {
		case "tsconfig.json":
			conf[d] = p
		case "jsconfig.json":
			if _, ok := conf[d]; !ok {
				conf[d] = p
			}
		case "package.json":
			pkg[d] = true
		}
	}
	by := map[string]*project{}
	for p := range files {
		if !sourceExts[strings.ToLower(path.Ext(p))] {
			continue
		}
		dir, ok := nearest(p, func(d string) bool { _, ok := conf[d]; return ok })
		if !ok {
			dir, _ = nearest(p, func(d string) bool { return pkg[d] })
		}
		pr := by[dir]
		if pr == nil {
			pr = &project{dir: dir, config: conf[dir]}
			by[dir] = pr
		}
		pr.files = append(pr.files, p)
	}
	var out []project
	for _, pr := range by {
		own := false
		for _, f := range pr.files {
			if !isDecl(f) {
				own = true
				break
			}
		}
		if !own {
			continue
		}
		sort.Strings(pr.files)
		out = append(out, *pr)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].dir < out[j].dir })
	return out
}

// nearest is the deepest directory above p (the root "" last) that is.
func nearest(p string, is func(string) bool) (string, bool) {
	d := path.Dir(p)
	for {
		if d == "." {
			d = ""
		}
		if is(d) {
			return d, true
		}
		if d == "" {
			return "", false
		}
		d = path.Dir(d)
	}
}

// call is one call of a project's result, as the worker reported it.
type call struct {
	File       string
	Line       int
	Name       string
	Kind       byte // 'B' bound into the repo, 'E' bound outside it, 'U' not bound
	CalleePath string
	CalleeLine int
	CalleeName string
	Ctor       bool
}

// result is a project's cache entry.
type result struct {
	Version string
	Calls   []call
}

// manifest is everything a project's result depends on: each file the
// compiler read (repo files by content, others by size, times and inode),
// each file or directory it looked for, each directory it listed.
type manifest struct {
	Reads  map[string]string
	Probes map[string]bool
	Lists  map[string]string
	Result string
}

type manifests struct {
	List []manifest
}

const maxManifests = 8

// Run builds every project of in (from the store when unchanged) and
// returns the calls the checker bound.
func Run(ctx context.Context, in Input, cfg Config) (Output, error) {
	prs := projects(in.Files)
	out := Output{Projects: len(prs)}
	if len(prs) == 0 {
		return out, nil
	}
	if in.CachedOnly && !cfg.Store.Has(Kind) {
		return out, nil // nothing was ever built here: the default costs nothing
	}
	v := newValidator(in)
	keys := make([]string, len(prs))
	ms := make([]manifests, len(prs))
	results := make([]*result, len(prs))
	var todo []int
	for i, pr := range prs {
		keys[i] = precise.Key(append([]string{"manifest", driverVersion, pr.dir, pr.config}, pr.files...)...)
		if cfg.Store.Get(Kind, keys[i], &ms[i]) {
			for _, m := range ms[i].List {
				var r result
				if v.valid(m) && cfg.Store.Get(Kind, m.Result, &r) {
					results[i] = &r
					break
				}
			}
		}
		if results[i] != nil {
			out.Cached++
			continue
		}
		todo = append(todo, i)
	}
	if len(todo) > 0 && !in.CachedOnly {
		if err := buildAll(ctx, in, cfg, prs, todo, keys, ms, results, v, &out); err != nil {
			return out, err
		}
	}
	for i, r := range results {
		if r == nil {
			continue
		}
		if out.Version == "" {
			out.Version = r.Version
		}
		out.Edges = append(out.Edges, edges(prs[i], r)...)
	}
	return out, nil
}

func buildAll(ctx context.Context, in Input, cfg Config, prs []project, todo []int, keys []string, ms []manifests,
	results []*result, v *validator, out *Output) error {
	node, err := FindNode(cfg.Node)
	if err != nil {
		out.Failed += len(todo)
		return err
	}
	tsdirs := make([]string, len(prs))
	var runnable []int
	for _, i := range todo {
		if tsdirs[i] = typescriptFor(in.Dir, prs[i].dir, cfg.TypeScript); tsdirs[i] == "" {
			out.Failed++
			out.Errs = append(out.Errs, fmt.Sprintf("%s: no typescript (none in the repo's node_modules, and --typescript not given)", orRoot(prs[i].dir)))
			continue
		}
		runnable = append(runnable, i)
	}
	if len(runnable) == 0 {
		return nil
	}
	if in.Materialize == nil {
		out.Failed += len(runnable)
		return errors.New("the ref's files cannot be put on disk")
	}
	dir, cleanup, err := in.Materialize()
	if err != nil {
		out.Failed += len(runnable)
		return err
	}
	defer cleanup()
	root, err := filepath.Abs(dir)
	if err == nil {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil {
		out.Failed += len(runnable)
		return err
	}
	linkNodeModules(root, in.Dir, prs, runnable)
	isolated := true
	if in.Dir != "" {
		if abs, err := filepath.Abs(in.Dir); err == nil {
			if real, err := filepath.EvalSymlinks(abs); err == nil && real == root {
				isolated = false // the working tree itself
			}
		}
	}
	repo := make([]string, 0, len(in.Files)+len(in.Extra))
	for p := range in.Files {
		repo = append(repo, p)
	}
	for p := range in.Extra {
		repo = append(repo, p)
	}
	sort.Strings(repo)
	replies := runPool(ctx, node, cfg, len(runnable), func(k int) request {
		i := runnable[k]
		return request{Root: root, TS: tsdirs[i], Config: prs[i].config, Files: prs[i].files, Repo: repo, Isolated: isolated}
	})
	for k, rep := range replies {
		i := runnable[k]
		if rep == nil || rep.Err != "" {
			out.Failed++
			if rep != nil {
				msg := rep.Err
				if j := strings.IndexByte(msg, '\n'); j > 0 {
					msg = msg[:j]
				}
				out.Errs = append(out.Errs, orRoot(prs[i].dir)+": "+msg)
			}
			continue
		}
		r := &result{Version: rep.Version}
		for _, c := range rep.Calls {
			if x, ok := c.decode(); ok {
				r.Calls = append(r.Calls, x)
			}
		}
		results[i] = r
		m, known := v.record(rep)
		if !known {
			continue // some file it read cannot be identified next time: not cached
		}
		m.Result = m.digest(keys[i])
		if cfg.Store.Put(Kind, m.Result, r) == nil {
			set := manifests{List: []manifest{m}}
			for _, old := range ms[i].List {
				if old.Result != m.Result && len(set.List) < maxManifests {
					set.List = append(set.List, old)
				}
			}
			_ = cfg.Store.Put(Kind, keys[i], set)
		}
	}
	return nil
}

func orRoot(d string) string {
	if d == "" {
		return "."
	}
	return d
}

// edges turns a project's calls into located edges.
func edges(pr project, r *result) []Edge {
	var out []Edge
	seen := map[Edge]bool{}
	for _, c := range r.Calls {
		e := Edge{CallerPath: c.File, CallerLine: c.Line, Name: c.Name, Method: MethodTS}
		switch strings.ToLower(path.Ext(c.File)) {
		case ".js", ".jsx", ".mjs", ".cjs":
			e.Method = MethodJS
		}
		switch c.Kind {
		case 'B':
			e.CalleePath, e.CalleeLine, e.CalleeName, e.Ctor = c.CalleePath, c.CalleeLine, c.CalleeName, c.Ctor
		case 'E':
			e.External = true
		default:
			e.Unbound = true
		}
		if !seen[e] {
			seen[e] = true
			out = append(out, e)
		}
	}
	return out
}

// typescriptFor is the typescript package a project is built with: the
// repo's own (a node_modules/typescript at the project's directory or one
// above it, up to the repo's root), else the configured one.
func typescriptFor(repoDir, dir, configured string) string {
	if repoDir != "" {
		d := dir
		for {
			cand := filepath.Join(repoDir, filepath.FromSlash(d), "node_modules", "typescript")
			if fi, err := os.Stat(filepath.Join(cand, "lib", "typescript.js")); err == nil && !fi.IsDir() {
				return cand
			}
			if d == "" {
				break
			}
			if d = path.Dir(d); d == "." {
				d = ""
			}
		}
	}
	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return ""
		}
		if fi, err := os.Stat(filepath.Join(abs, "lib", "typescript.js")); err == nil && !fi.IsDir() {
			return abs
		}
		// the node_modules directory holding it, or its lib directory
		for _, c := range []string{filepath.Join(abs, "typescript"), filepath.Dir(abs)} {
			if fi, err := os.Stat(filepath.Join(c, "lib", "typescript.js")); err == nil && !fi.IsDir() {
				return c
			}
		}
	}
	return ""
}

// TypeScriptVersion is the version of the typescript package at dir, ""
// when there is none.
func TypeScriptVersion(dir string) string {
	d := typescriptFor("", "", dir)
	if d == "" {
		return ""
	}
	b, err := os.ReadFile(filepath.Join(d, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return ""
	}
	return pkg.Version
}

// FindNode is the node binary to run: the configured one, else node on
// $PATH.
func FindNode(configured string) (string, error) {
	if configured != "" {
		if fi, err := os.Stat(configured); err != nil || fi.IsDir() {
			return "", fmt.Errorf("node %s: not found", configured)
		}
		return configured, nil
	}
	p, err := exec.LookPath("node")
	if err != nil {
		return "", errors.New("node not found on $PATH (--node or $REPONITE_NODE name one)")
	}
	return p, nil
}

// linkNodeModules gives a ref copied out of git the node_modules of the
// repo's directory on disk, at the root and at each project's directory:
// the libraries a build reads are not the repo's sources.
func linkNodeModules(root, repoDir string, prs []project, which []int) {
	if repoDir == "" {
		return
	}
	abs, err := filepath.Abs(repoDir)
	if err != nil {
		return
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil && real == root {
		return // the working tree itself
	}
	dirs := map[string]bool{"": true}
	for _, i := range which {
		for d := prs[i].dir; ; {
			dirs[d] = true
			if d == "" {
				break
			}
			if d = path.Dir(d); d == "." {
				d = ""
			}
		}
	}
	for d := range dirs {
		src := filepath.Join(abs, filepath.FromSlash(d), "node_modules")
		dst := filepath.Join(root, filepath.FromSlash(d), "node_modules")
		if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
			continue
		}
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if os.MkdirAll(filepath.Dir(dst), 0o755) == nil {
			_ = os.Symlink(src, dst)
		}
	}
}

// request is one project for a worker to build.
type request struct {
	Root string `json:"root"`
	// Isolated: Root is a ref's copy in a temporary directory, not the
	// repo's directory (what is beside and above it is not the repo's)
	Isolated bool     `json:"isolated"`
	TS       string   `json:"ts"`
	Config   string   `json:"config"`
	Files    []string `json:"files"`
	Repo     []string `json:"repo"`
}

// reply is a worker's answer to one request.
type reply struct {
	Version string            `json:"version"`
	Calls   []rawCall         `json:"calls"`
	Reads   []string          `json:"reads"`
	Probes  map[string]bool   `json:"probes"`
	Lists   map[string]string `json:"lists"`
	Err     string            `json:"err"`
}

// rawCall is [file, line, name, kind] or, bound, [file, line, name, "B",
// calleePath, calleeLine, calleeName, ctor].
type rawCall []json.RawMessage

func (c rawCall) decode() (call, bool) {
	var x call
	var kind string
	if len(c) < 4 || json.Unmarshal(c[0], &x.File) != nil || json.Unmarshal(c[1], &x.Line) != nil ||
		json.Unmarshal(c[2], &x.Name) != nil || json.Unmarshal(c[3], &kind) != nil || kind == "" {
		return x, false
	}
	x.Kind = kind[0]
	if x.Kind == 'B' {
		var ctor int
		if len(c) < 8 || json.Unmarshal(c[4], &x.CalleePath) != nil || json.Unmarshal(c[5], &x.CalleeLine) != nil ||
			json.Unmarshal(c[6], &x.CalleeName) != nil || json.Unmarshal(c[7], &ctor) != nil {
			return x, false
		}
		x.Ctor = ctor == 1
	}
	return x, true
}

// runPool runs n requests over the configured number of node workers; a nil
// reply is a project whose worker died or timed out.
func runPool(ctx context.Context, node string, cfg Config, n int, req func(int) request) []*reply {
	jobs := cfg.Jobs
	if jobs <= 0 {
		jobs = DefaultJobs()
	}
	if jobs > n {
		jobs = n
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	replies := make([]*reply, n)
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
					if wk, err = startWorker(ctx, node); err != nil {
						replies[k] = &reply{Err: err.Error()}
						continue
					}
				}
				rep, err := wk.do(req(k), timeout)
				if err != nil {
					wk.kill()
					wk = nil
					replies[k] = &reply{Err: err.Error()}
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

// DefaultJobs is one worker per CPU, at most 4, as many as free memory
// holds at about 1 GB each.
func DefaultJobs() int {
	n := runtime.NumCPU()
	if n > 4 {
		n = 4
	}
	if avail := precise.MemAvailable(); avail > 0 {
		if m := int(avail * 6 / 10 / (1 << 30)); m < n {
			n = m
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

type worker struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
}

func startWorker(ctx context.Context, node string) (*worker, error) {
	cmd := exec.CommandContext(ctx, node, "--max-old-space-size=4096", "-e", workerJS)
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

func (w *worker) do(req request, timeout time.Duration) (*reply, error) {
	b, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	type res struct {
		rep *reply
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
			done <- res{nil, fmt.Errorf("worker died building %s: %v", orRoot(path.Dir(req.Config)), err)}
			return
		}
		var rep reply
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
		return nil, errors.New("timed out")
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
