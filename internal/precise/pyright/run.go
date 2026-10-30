// Package pyright is the Python driver of the compiler-exact tier: it runs
// the pyright language server (a node program; reponite stays pure Go) on a
// ref's Python files, asks it where each call's name is defined, and
// reports every call it binds, located by the call's line and the bound
// definition's place. Without node or pyright the tier is unavailable and
// calls stay inferred. A ref's result is kept in the precise store under the
// content of every Python file of the ref, so a later run (cached mode)
// reuses it without starting the server while they are unchanged.
//
// The calls are found by a small scanner (scan.go), and each is resolved by
// the server's go-to-definition, which follows imports and re-exports to the
// definition and types receivers from annotations and inference.
package pyright

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
)

// Kind is this driver's directory in the precise store.
const Kind = "py"

// driverVersion changes whenever what a result holds, or the rules that
// make it, change: results of an older driver are never read.
const driverVersion = "pyright-5"

// Config is how to run the driver.
type Config struct {
	Node    string // the node binary ("" node on $PATH), for a pyright package or script
	Pyright string // a pyright package directory, its langserver script, or a langserver executable ("" look for one)
	Store   *precise.Store
	Timeout time.Duration // per ref (0: 30 minutes)
	Log     io.Writer
}

// Input is one ref's Python code.
type Input struct {
	Files map[string]string // every repo input (IsInput) -> its content id (git blob hash)
	Extra map[string]string // inputs the walk excludes (a vendored directory's), by content id
	// Dir is the repo's directory on disk ("" unknown): a pyright in its
	// node_modules is found there.
	Dir  string
	Read func(rel string) ([]byte, error)
	// CachedOnly answers from the store alone, never starting the server.
	CachedOnly bool
}

// Edge is one call pyright proved, located by the call's line and the bound
// definition (repo-relative).
type Edge struct {
	CallerPath string
	CallerLine int // the line the call is written on
	Name       string
	CalleePath string
	CalleeLine int    // the line of the bound definition's name
	CalleeName string // its own name
	Ctor       bool   // a class called: its constructor runs
	External   bool
	Unbound    bool
	// Alias: bound through a variable assigned once from a name (f =
	// helper): another module may assign it again
	Alias bool
	// Virtual: called on an object (x.f(), self.f()), not through super():
	// the bound method, or an override of a class deriving from its class
	Virtual bool
}

// Output is what the driver proved for one ref.
type Output struct {
	Edges   []Edge
	Units   int // 1 when the ref has Python files
	Cached  int
	Failed  int
	Sites   int    // calls resolved now (0 when cached)
	Version string // the engine that proved the cached or new result
}

// IsInput reports a file the driver reads.
func IsInput(rel string) bool {
	switch strings.ToLower(path.Ext(rel)) {
	case ".py", ".pyi":
		return true
	}
	switch path.Base(rel) {
	case "pyrightconfig.json", "pyproject.toml":
		return true
	}
	return false
}

// call is one call of a result.
type call struct {
	File       string
	Line       int // 1-based
	Name       string
	Kind       byte // 'B' bound into the repo, 'E' bound outside it, 'U' not bound
	CalleePath string
	CalleeLine int
	CalleeName string
	Ctor       bool
	Alias      bool // bound through a variable assigned once from a name (f = helper)
	Virtual    bool // a method called on an object: a subclass's override may run
}

// result is a ref's cache entry.
type result struct {
	Engine  string // the engine's identity (its version, or its executable's)
	Version string
	Calls   []call
}

// key is a ref's cache key: the driver and every Python file's content.
func key(in Input) string {
	parts := []string{"result", driverVersion}
	for _, m := range []map[string]string{in.Files, in.Extra} {
		ps := make([]string, 0, len(m))
		for p := range m {
			ps = append(ps, p)
		}
		sort.Strings(ps)
		for _, p := range ps {
			parts = append(parts, p, m[p])
		}
		parts = append(parts, "\x00")
	}
	return precise.Key(parts...)
}

// sources are the files whose calls are resolved: the repo's .py files.
func sources(in Input) []string {
	var out []string
	for p := range in.Files {
		if strings.EqualFold(path.Ext(p), ".py") {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Run resolves every call of in's Python files (from the store when
// unchanged) and returns the calls pyright bound.
func Run(ctx context.Context, in Input, cfg Config) (Output, error) {
	var out Output
	if len(sources(in)) == 0 {
		return out, nil
	}
	out.Units = 1
	if in.CachedOnly && !cfg.Store.Has(Kind) {
		return out, nil // nothing was ever resolved here: the default costs nothing
	}
	k := key(in)
	var r result
	cached := cfg.Store.Get(Kind, k, &r)
	use := func() (Output, error) {
		out.Cached = 1
		out.Version, out.Edges = r.Version, edges(r.Calls)
		return out, nil
	}
	if in.CachedOnly {
		if !cached {
			return out, nil
		}
		return use()
	}
	eng, err := Find(cfg.Pyright, cfg.Node, in.Dir)
	if err != nil {
		if cached {
			return use() // the engine is gone: what it proved for these sources holds
		}
		out.Failed = 1
		return out, err
	}
	if cached && r.Engine == eng.ID {
		return use()
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	rctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	scratch := filepath.Join(os.TempDir(), "reponite-pyright")
	if cfg.Store != nil && cfg.Store.Dir() != "" {
		scratch = filepath.Join(cfg.Store.Dir(), "tmp")
	}
	calls, sites, err := resolveAll(rctx, in, eng, scratch)
	if err != nil {
		out.Failed = 1
		return out, err
	}
	r = result{Engine: eng.ID, Version: eng.Version, Calls: calls}
	_ = cfg.Store.Put(Kind, k, r)
	out.Sites = sites
	out.Version, out.Edges = r.Version, edges(r.Calls)
	return out, nil
}

func edges(calls []call) []Edge {
	out := make([]Edge, 0, len(calls))
	for _, c := range calls {
		e := Edge{CallerPath: c.File, CallerLine: c.Line, Name: c.Name}
		switch c.Kind {
		case 'B':
			e.CalleePath, e.CalleeLine, e.CalleeName, e.Ctor, e.Alias, e.Virtual = c.CalleePath, c.CalleeLine, c.CalleeName, c.Ctor, c.Alias, c.Virtual
		case 'E':
			e.External = true
		default:
			e.Unbound = true
		}
		out = append(out, e)
	}
	return out
}

// Engine is a pyright language server to run.
type Engine struct {
	Argv    []string // the command, without --stdio
	Version string   // "" unknown
	ID      string   // what identifies its results: the version, else the executable
}

// Find locates the language server: the configured package directory,
// script or executable, else a pyright in the repo's node_modules, else
// pyright-langserver or basedpyright-langserver on $PATH.
func Find(configured, node, repoDir string) (Engine, error) {
	script := func(js, version string) (Engine, error) {
		n, err := findNode(node)
		if err != nil {
			return Engine{}, err
		}
		id := version
		if id == "" {
			id = statID(js)
		}
		return Engine{Argv: []string{n, "--max-old-space-size=6144", js}, Version: version, ID: "pyright " + id}, nil
	}
	pkg := func(dir string) (Engine, bool, error) {
		for _, c := range []string{dir, filepath.Join(dir, "node_modules", "pyright"), filepath.Join(dir, "node_modules", "basedpyright")} {
			js := filepath.Join(c, "langserver.index.js")
			if !isFile(js) {
				js = filepath.Join(c, "dist", "pyright-langserver.js")
			}
			if isFile(js) {
				e, err := script(js, pkgVersion(c))
				return e, true, err
			}
		}
		return Engine{}, false, nil
	}
	if configured != "" {
		abs, err := filepath.Abs(configured)
		if err != nil {
			return Engine{}, err
		}
		fi, err := os.Stat(abs)
		switch {
		case err != nil:
			return Engine{}, fmt.Errorf("pyright %s: not found", configured)
		case fi.IsDir():
			if e, ok, err := pkg(abs); ok {
				return e, err
			}
			return Engine{}, fmt.Errorf("pyright %s: no langserver.index.js or dist/pyright-langserver.js there", configured)
		case strings.HasSuffix(abs, ".js"):
			// the package's version, for a script inside one
			v := pkgVersion(filepath.Dir(abs))
			if v == "" {
				v = pkgVersion(filepath.Dir(filepath.Dir(abs)))
			}
			return script(abs, v)
		default:
			return Engine{Argv: []string{abs}, ID: "exe " + statID(abs)}, nil
		}
	}
	if repoDir != "" {
		if e, ok, err := pkg(repoDir); ok {
			return e, err
		}
	}
	for _, name := range []string{"pyright-langserver", "basedpyright-langserver"} {
		if p, err := exec.LookPath(name); err == nil {
			return Engine{Argv: []string{p}, ID: "exe " + statID(p)}, nil
		}
	}
	return Engine{}, errors.New("pyright not found (--pyright or $REPONITE_PYRIGHT name a pyright package or a pyright-langserver)")
}

func findNode(configured string) (string, error) {
	if configured != "" {
		if !isFile(configured) {
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

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func statID(p string) string {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		real = p
	}
	fi, err := os.Stat(real)
	if err != nil {
		return real
	}
	return real + "@" + precise.StatID(fi)
}

func pkgVersion(dir string) string {
	b, err := os.ReadFile(filepath.Join(dir, "package.json"))
	if err != nil {
		return ""
	}
	var pkg struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(b, &pkg) != nil || pkg.Version == "" {
		return ""
	}
	if pkg.Name == "basedpyright" {
		return "basedpyright " + pkg.Version
	}
	return pkg.Version
}

// sourceRoots are the directories holding top-level packages (a directory
// with an __init__.py whose parent has none), other than the root: where
// `import pkg` finds pkg when the code runs from there (a src/ layout, a
// package of a multi-package repository).
func sourceRoots(files map[string]string) []string {
	init := map[string]bool{}
	for p := range files {
		if path.Base(p) == "__init__.py" {
			init[dirOf(p)] = true
		}
	}
	roots := map[string]bool{}
	for d := range init {
		if d == "" {
			continue
		}
		if parent := dirOf(d); parent != "" && !init[parent] {
			roots[parent] = true
		}
	}
	out := make([]string, 0, len(roots))
	for d := range roots {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func dirOf(p string) string {
	d := path.Dir(p)
	if d == "." {
		return ""
	}
	return d
}

// envRoots are where code is run from that needs an execution environment
// of its own (the root always has one): a directory holding Python files
// that is not a package (a script's directory, sys.path[0] when it runs) or
// a source root, when a module it holds has the name of one the root, a
// source root or a directory above it holds — there the directory's own
// module is the one imported, while the server's default order would find
// the other first. A file belongs to the deepest one above it. Few
// environments keep the server fast: each resolves its imports anew.
func envRoots(files map[string]string) []string {
	init, py := map[string]bool{}, map[string]bool{}
	mods := map[string]map[string]bool{} // directory -> the modules it holds
	add := func(d, m string) {
		if mods[d] == nil {
			mods[d] = map[string]bool{}
		}
		mods[d][m] = true
	}
	for p := range files {
		d := dirOf(p)
		ext := strings.ToLower(path.Ext(p))
		if path.Base(p) == "__init__.py" {
			init[d] = true
			if d != "" {
				add(dirOf(d), path.Base(d)) // a package is a module of its parent
			}
		}
		if ext == ".py" {
			py[d] = true
		}
		if ext == ".py" || ext == ".pyi" {
			add(d, strings.TrimSuffix(path.Base(p), path.Ext(p)))
		}
	}
	roots := sourceRoots(files)
	var cands []string
	for d := range py {
		if d != "" && !init[d] {
			cands = append(cands, d)
		}
	}
	cands = append(cands, roots...)
	set := map[string]bool{"": true}
	for _, d := range cands {
		if set[d] {
			continue
		}
		others := []string{""}
		for x := d; x != ""; {
			x = dirOf(x)
			others = append(others, x)
		}
		for _, r := range roots {
			if r != d {
				others = append(others, r)
			}
		}
	find:
		for m := range mods[d] {
			for _, o := range others {
				if mods[o][m] {
					set[d] = true
					break find
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool {
		di, dj := strings.Count(out[i], "/"), strings.Count(out[j], "/")
		if out[i] == "" || out[j] == "" {
			return out[j] == ""
		}
		if di != dj {
			return di > dj
		}
		return out[i] < out[j]
	})
	return out
}

// config is the pyrightconfig.json the ref's copy is checked with: one
// execution environment per envRoots entry, whose imports resolve from its
// own directory, then the directories above it (as a script run from there
// finds them), then the source roots; the root's from the root, then the
// source roots (then, the server's default, the importing file's own
// directory).
func config(files map[string]string) ([]byte, error) {
	roots := sourceRoots(files)
	type env struct {
		Root       string   `json:"root"`
		ExtraPaths []string `json:"extraPaths"`
	}
	var envs []env
	for _, d := range envRoots(files) {
		e := env{Root: orDot(d), ExtraPaths: []string{}}
		seen := map[string]bool{d: true}
		for x := d; x != ""; {
			x = dirOf(x)
			e.ExtraPaths = append(e.ExtraPaths, orDot(x))
			seen[x] = true
		}
		for _, r := range roots {
			if !seen[r] {
				e.ExtraPaths = append(e.ExtraPaths, orDot(r))
			}
		}
		envs = append(envs, e)
	}
	return json.MarshalIndent(map[string]any{
		"typeCheckingMode":      "off",
		"executionEnvironments": envs,
	}, "", " ")
}

func orDot(d string) string {
	if d == "" {
		return "."
	}
	return d
}

// resolveAll copies the ref's Python files out (beside the store, with the
// config above), runs the server on them and resolves every call site; it
// returns the calls and how many sites it asked about.
func resolveAll(ctx context.Context, in Input, eng Engine, scratch string) ([]call, int, error) {
	if in.Read == nil {
		return nil, 0, errors.New("the ref's files cannot be read")
	}
	// a ref's reader (a git tree) is not safe for concurrent use
	var rmu sync.Mutex
	read := in.Read
	in.Read = func(rel string) ([]byte, error) {
		rmu.Lock()
		defer rmu.Unlock()
		return read(rel)
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return nil, 0, err
	}
	dir, err := os.MkdirTemp(scratch, "py-")
	if err != nil {
		return nil, 0, err
	}
	defer os.RemoveAll(dir)
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, 0, err
	}
	// a partial copy would resolve into wrong results that are then
	// cached: any failure abandons the run
	for _, m := range []map[string]string{in.Files, in.Extra} {
		for rel := range m {
			if ext := strings.ToLower(path.Ext(rel)); ext != ".py" && ext != ".pyi" {
				continue
			}
			b, err := in.Read(rel)
			if err != nil {
				return nil, 0, fmt.Errorf("copying %s out for pyright: %w", rel, err)
			}
			p := filepath.Join(root, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return nil, 0, err
			}
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return nil, 0, err
			}
		}
	}
	cfgJSON, err := config(in.Files)
	if err != nil {
		return nil, 0, err
	}
	if err := os.WriteFile(filepath.Join(root, "pyrightconfig.json"), cfgJSON, 0o644); err != nil {
		return nil, 0, err
	}
	analysis := map[string]any{
		"diagnosticMode":         "openFilesOnly",
		"typeCheckingMode":       "off",
		"autoSearchPaths":        false,
		"useLibraryCodeForTypes": true,
		"autoImportCompletions":  false,
	}
	settings := map[string]any{
		"python":                map[string]any{"analysis": analysis},
		"python.analysis":       analysis,
		"pyright":               map[string]any{},
		"basedpyright":          map[string]any{"analysis": analysis},
		"basedpyright.analysis": analysis,
	}
	srv, err := startServer(ctx, append(append([]string{}, eng.Argv...), "--stdio"), root, settings)
	if err != nil {
		return nil, 0, err
	}
	defer srv.close()
	res := &resolver{in: in, root: root, texts: map[string]string{}}
	var mu sync.Mutex
	var calls []call
	var firstErr error
	nsites := 0
	// a few files at a time: the server answers one request at a time, but
	// keeping it busy hides each round trip
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, rel := range sources(in) {
		if ctx.Err() != nil {
			break
		}
		b, err := in.Read(rel)
		if err != nil {
			continue
		}
		src := string(b)
		sites := scanCalls(src)
		if len(sites) == 0 {
			continue
		}
		nsites += len(sites)
		sem <- struct{}{}
		wg.Add(1)
		go func(rel, src string, sites []site) {
			defer func() { <-sem; wg.Done() }()
			cs, err := res.file(ctx, srv, rel, src, sites)
			mu.Lock()
			defer mu.Unlock()
			if err != nil && firstErr == nil {
				firstErr = err
			}
			calls = append(calls, cs...)
		}(rel, src, sites)
	}
	wg.Wait()
	if firstErr != nil {
		return nil, 0, firstErr
	}
	if ctx.Err() != nil {
		return nil, 0, fmt.Errorf("pyright: %w", ctx.Err())
	}
	sort.Slice(calls, func(i, j int) bool {
		a, b := calls[i], calls[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		if a.CalleePath != b.CalleePath {
			return a.CalleePath < b.CalleePath
		}
		return a.CalleeLine < b.CalleeLine
	})
	return calls, nsites, nil
}

// resolver classifies the server's answers.
type resolver struct {
	in    Input
	root  string
	mu    sync.Mutex
	texts map[string]string // absolute path -> content ("" unreadable)
}

func (r *resolver) text(abs, rel string) string {
	r.mu.Lock()
	t, ok := r.texts[abs]
	r.mu.Unlock()
	if ok {
		return t
	}
	var b []byte
	var err error
	if rel != "" && r.in.Read != nil {
		b, err = r.in.Read(rel)
	} else {
		b, err = os.ReadFile(abs)
	}
	if err == nil {
		t = string(b)
	}
	r.mu.Lock()
	r.texts[abs] = t
	r.mu.Unlock()
	return t
}

// file opens one file in the server, resolves its call sites and closes it.
func (r *resolver) file(ctx context.Context, srv *server, rel, src string, sites []site) ([]call, error) {
	uri := fileURI(filepath.Join(r.root, filepath.FromSlash(rel)))
	if err := srv.conn.notify("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": uri, "languageId": "python", "version": 1, "text": src},
	}); err != nil {
		return nil, err
	}
	defer func() {
		_ = srv.conn.notify("textDocument/didClose", map[string]any{"textDocument": map[string]any{"uri": uri}})
	}()
	out := make([][]call, len(sites))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 64)
	var stopped error
	var smu sync.Mutex
	for i, s := range sites {
		sem <- struct{}{}
		wg.Add(1)
		go func(i int, s site) {
			defer func() { <-sem; wg.Done() }()
			qctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
			defer cancel()
			var raw json.RawMessage
			err := srv.conn.call(qctx, "textDocument/definition", map[string]any{
				"textDocument": map[string]any{"uri": uri},
				"position":     map[string]any{"line": s.Line, "character": s.Col},
			}, &raw)
			if err != nil {
				select {
				case <-srv.conn.done:
					smu.Lock()
					if stopped == nil {
						stopped = err
					}
					smu.Unlock()
				default:
				}
				// a request the server failed or did not answer in time:
				// not proven, the inferred edges stay
				out[i] = []call{{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'U'}}
				return
			}
			cs, alias := r.classify(rel, s, decodeLocations(raw))
			followed := alias != nil
			for depth := 0; alias != nil && depth < 3; depth++ {
				// a variable assigned once from a name (f = helper, run =
				// self.start): the call runs what that name is
				raw = nil
				if srv.conn.call(qctx, "textDocument/definition", map[string]any{
					"textDocument": map[string]any{"uri": alias.URI},
					"position":     map[string]any{"line": alias.Range.Start.Line, "character": alias.Range.Start.Character},
				}, &raw) != nil {
					break
				}
				cs, alias = r.classify(rel, s, decodeLocations(raw))
			}
			if alias != nil {
				cs = []call{{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'U'}}
			} else if followed {
				for k := range cs {
					cs[k].Alias = cs[k].Kind == 'B'
				}
			}
			if s.Member && !s.Self && len(cs) > 0 && cs[0].Kind != 'U' {
				// x.f(): bound only when x's type is known in full — the
				// definitions of a partly unknown value (a parameter that
				// defaults to None) name only the part the checker knows;
				// self and cls are the class's own
				var h struct {
					Contents json.RawMessage `json:"contents"`
				}
				err := srv.conn.call(qctx, "textDocument/hover", map[string]any{
					"textDocument": map[string]any{"uri": uri},
					"position":     map[string]any{"line": s.Line, "character": s.Col},
				}, &h)
				if err != nil || partlyUnknown(hoverText(h.Contents)) {
					cs = []call{{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'U'}}
				}
			}
			if s.Member && !s.Super {
				for k := range cs {
					cs[k].Virtual = cs[k].Kind == 'B' && !cs[k].Ctor
				}
			}
			out[i] = cs
		}(i, s)
	}
	wg.Wait()
	if stopped != nil {
		return nil, stopped
	}
	var cs []call
	for _, x := range out {
		cs = append(cs, x...)
	}
	return cs, nil
}

// hoverText is a hover's contents as text: MarkupContent, a MarkedString
// or a list of them.
func hoverText(raw json.RawMessage) string {
	var mc struct {
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &mc) == nil && mc.Value != "" {
		return stripFence(mc.Value)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) == nil && len(list) > 0 {
		return hoverText(list[0])
	}
	return ""
}

// stripFence drops a markdown code fence around a hover's first line.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		if i := strings.IndexByte(s, '\n'); i >= 0 {
			s = s[i+1:]
		}
	}
	return s
}

// decodeLocations reads a definition answer: a Location, a list of them, or
// of LocationLinks.
func decodeLocations(raw json.RawMessage) []location {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil {
		list = []json.RawMessage{raw}
	}
	var out []location
	for _, m := range list {
		var l location
		if json.Unmarshal(m, &l) == nil && l.URI != "" {
			out = append(out, l)
			continue
		}
		var link struct {
			TargetURI            string `json:"targetUri"`
			TargetSelectionRange struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
			} `json:"targetSelectionRange"`
		}
		if json.Unmarshal(m, &link) == nil && link.TargetURI != "" {
			l.URI = link.TargetURI
			l.Range.Start = link.TargetSelectionRange.Start
			out = append(out, l)
		}
	}
	return out
}

// classify turns the definitions the server named for one call into the
// call's verdict: bound to the repo's functions or classes (one call per
// definition), bound outside it, or unbound — no definition, or one that is
// not a def or class statement (a variable, a parameter, a stub of the
// repo's: what it holds is not proven), which keeps the inferred edges.
//
// A call of a variable assigned once, from a name alone (one definition:
// f = helper), returns where that name is written instead: the caller asks
// for its definition in turn.
func (r *resolver) classify(rel string, s site, locs []location) ([]call, *location) {
	unbound := []call{{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'U'}}
	if len(locs) == 0 {
		return unbound, nil
	}
	var bound []call
	ext := false
	for _, l := range locs {
		abs := uriPath(l.URI)
		if abs == "" {
			return unbound, nil
		}
		line, col := l.Range.Start.Line, l.Range.Start.Character
		relT := ""
		if x, err := filepath.Rel(r.root, abs); err == nil && x != ".." && !strings.HasPrefix(x, ".."+string(filepath.Separator)) && !filepath.IsAbs(x) {
			relT = filepath.ToSlash(x)
		}
		_, inRepo := r.in.Files[relT]
		_, inExtra := r.in.Extra[relT]
		var src string
		if relT != "" && (inRepo || inExtra) {
			src = r.text(abs, relT)
		} else {
			// outside the root, or under it but not one of the ref's files
			// (a working tree's virtualenv): a library's
			src = r.text(abs, "")
		}
		name, kind := defAt(src, line, col)
		if kind == "var" && len(locs) == 1 && inRepo {
			if l2, c2, ok := aliasOf(src, line, col); ok {
				a := location{URI: l.URI}
				a.Range.Start.Line, a.Range.Start.Character = l2, c2
				return unbound, &a
			}
		}
		if kind != "def" && kind != "class" {
			return unbound, nil // not a def or class statement there
		}
		switch {
		case inRepo && strings.EqualFold(path.Ext(relT), ".py"):
			bound = append(bound, call{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'B',
				CalleePath: relT, CalleeLine: line + 1, CalleeName: name, Ctor: kind == "class"})
		case inRepo:
			return unbound, nil // the repo's own stub declares, it does not define
		default:
			ext = true
		}
	}
	if len(bound) > 0 {
		// the repo's definitions; one outside it beside them (a union with
		// a library type) is not an edge of the index
		return bound, nil
	}
	if ext {
		return []call{{File: rel, Line: s.Line + 1, Name: s.Name, Kind: 'E'}}, nil
	}
	return unbound, nil
}
