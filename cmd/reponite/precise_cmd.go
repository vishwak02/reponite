//go:build sqlite && treesitter

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
	"github.com/vishwak02/reponite/internal/precise/cxx"
	"github.com/vishwak02/reponite/internal/precise/pyright"
	"github.com/vishwak02/reponite/internal/precise/tsc"
	"github.com/vishwak02/reponite/internal/processing"
)

// preciseFlags select the compiler-exact tier: off, cached (only results a
// compiler already produced for these exact sources — never slower), or on
// (compile what is not cached).
type preciseFlags struct {
	mode     string
	headers  excludeFlags
	cppArgs  excludeFlags
	compdb   string
	libclang string
	jobs     int
	// node runs the TypeScript and Python drivers' workers; typescript is
	// the compiler for a repo without its own, pyright the Python checker
	node       string
	typescript string
	pyright    string
	langs      string // the drivers to run, comma-separated (cpp, ts, py; "" all)
	// compiled is set when the last ref's driver run parsed translation
	// units (compiled now or read from the store): only then does the ref
	// record that it holds compiler-exact calls
	compiled bool
	// cxx, ts, py: which drivers run (on: all; cached: those with results
	// stored), set by driver
	cxx, ts, py bool
}

// preciseConfig is how a repo was last compiled: a later run without the
// flags reuses them, so the default (cached) index finds the results.
type preciseConfig struct {
	Headers    []string `json:"headers,omitempty"`
	Args       []string `json:"args,omitempty"`
	Compdb     string   `json:"compdb,omitempty"`
	Libclang   string   `json:"libclang,omitempty"`
	Node       string   `json:"node,omitempty"`
	TypeScript string   `json:"typescript,omitempty"`
	Pyright    string   `json:"pyright,omitempty"`
}

func configPath(root, repo string) string {
	return filepath.Join(root, "configs", repo+".json")
}

// settle fills the compile settings the flags left out from the ones the
// repo was last compiled with, and records them when this run compiles.
// The C/C++ settings and the tools of the other drivers settle apart: a
// run naming only --node keeps the remembered headers.
func (p *preciseFlags) settle(root, repo string) {
	given := len(p.headers) > 0 || len(p.cppArgs) > 0 || p.compdb != ""
	tools := p.node != "" || p.typescript != "" || p.pyright != ""
	// absolute: a later run (watch, an MCP server) starts elsewhere
	for i, h := range p.headers {
		if abs, err := filepath.Abs(h); err == nil {
			p.headers[i] = abs
		}
	}
	for _, f := range []*string{&p.compdb, &p.typescript, &p.pyright} {
		if *f != "" {
			if abs, err := filepath.Abs(*f); err == nil {
				*f = abs
			}
		}
	}
	if p.node != "" && strings.ContainsRune(p.node, filepath.Separator) {
		if abs, err := filepath.Abs(p.node); err == nil {
			p.node = abs
		}
	}
	path := configPath(root, repo)
	var cfg preciseConfig
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &cfg)
	}
	if !given {
		p.headers, p.cppArgs, p.compdb = cfg.Headers, cfg.Args, cfg.Compdb
	}
	if p.libclang == "" {
		p.libclang = cfg.Libclang
	}
	if p.node == "" {
		p.node = cfg.Node
	}
	if p.typescript == "" {
		p.typescript = cfg.TypeScript
	}
	if p.pyright == "" {
		p.pyright = cfg.Pyright
	}
	if p.mode == "on" && (given || tools) {
		cfg = preciseConfig{Headers: p.headers, Args: p.cppArgs, Compdb: p.compdb, Libclang: p.libclang,
			Node: p.node, TypeScript: p.typescript, Pyright: p.pyright}
		if b, err := json.MarshalIndent(cfg, "", "  "); err == nil && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
	}
}

func (p *preciseFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&p.mode, "precise", "cached", "compiler-exact calls (C/C++, TypeScript/JavaScript, Python): off | cached (reuse results already compiled, never compile) | on (compile what is not cached)")
	fs.Var(&p.headers, "cpp-headers", "header trees the C/C++ build sees, for --precise (a ROS install, a copied /usr/include, generated messages); repeatable")
	fs.Var(&p.cppArgs, "cpp-arg", "extra compiler argument for --precise (repeatable, comma-separable), e.g. -DCONFIG_X=1")
	fs.StringVar(&p.compdb, "compdb", "", "the build's compile_commands.json for --precise (default: one at the tree root or build/)")
	fs.StringVar(&p.libclang, "libclang", os.Getenv("REPONITE_LIBCLANG"), "libclang to load for --precise (default: the usual library names)")
	fs.IntVar(&p.jobs, "precise-jobs", 0, "parallel compiler workers for --precise (default: one per CPU, at most what free memory holds at about 1 GB each)")
	fs.StringVar(&p.node, "node", os.Getenv("REPONITE_NODE"), "node binary for the TypeScript and Python drivers of --precise (default: node on $PATH)")
	fs.StringVar(&p.typescript, "typescript", os.Getenv("REPONITE_TYPESCRIPT"), "typescript package directory for --precise, for a repo without node_modules/typescript")
	fs.StringVar(&p.langs, "precise-langs", "", "the languages --precise covers, comma-separated: cpp, ts (TypeScript/JavaScript), py (default: all)")
	fs.StringVar(&p.pyright, "pyright", os.Getenv("REPONITE_PYRIGHT"), "pyright for --precise: its package directory, its langserver script, or a pyright-langserver/basedpyright-langserver executable (default: one on $PATH)")
}

// preciseMark is what a ref records beside its exclude filter when its
// index holds compiler-exact calls: a later --precise on run of it is then
// done, and a default run does not redo it.
const preciseMark = "|precise"

// preciseRoot is where compiled results are kept: $REPONITE_PRECISE_DIR,
// else beside the stores ($REPONITE_STORE_DIR/.precise), else the user cache.
func preciseRoot() string {
	if d := os.Getenv("REPONITE_PRECISE_DIR"); d != "" {
		return d
	}
	root := os.Getenv(storeDirEnv)
	if root == "" {
		d, err := os.UserCacheDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(d, "reponite")
	}
	return filepath.Join(root, ".precise")
}

// driver is the precise driver the flags select — C/C++ (clang),
// TypeScript/JavaScript (tsc) and Python (pyright), each on its own files —
// nil when off, or when cached and nothing was ever compiled.
func (p *preciseFlags) driver(repo string) func(processing.PreciseSource) []processing.PreciseUnit {
	switch p.mode {
	case "off", "":
		return nil
	case "cached", "on":
	default:
		fail(fmt.Errorf("--precise %q: want off, cached or on", p.mode))
	}
	root := preciseRoot()
	if root == "" {
		if p.mode == "on" {
			fmt.Fprintln(os.Stderr, "reponite: --precise on: no directory to keep compiled results in (set REPONITE_PRECISE_DIR): calls stay inferred")
		}
		return nil
	}
	store := precise.Open(root)
	on := p.mode == "on"
	p.cxx, p.ts, p.py = on || store.Has("cpp"), on || store.Has(tsc.Kind), on || store.Has(pyright.Kind)
	if p.langs != "" {
		want := map[string]bool{}
		for _, l := range strings.Split(p.langs, ",") {
			switch l = strings.TrimSpace(strings.ToLower(l)); l {
			case "cpp", "c", "c++", "cxx":
				want["cpp"] = true
			case "ts", "js", "typescript", "javascript":
				want["ts"] = true
			case "py", "python":
				want["py"] = true
			case "":
			default:
				fail(fmt.Errorf("--precise-langs %q: want cpp, ts or py", l))
			}
		}
		p.cxx, p.ts, p.py = p.cxx && want["cpp"], p.ts && want["ts"], p.py && want["py"]
	}
	if !p.cxx && !p.ts && !p.py {
		return nil // nothing was ever compiled: the default index does no extra work
	}
	p.settle(root, repo)
	return func(src processing.PreciseSource) []processing.PreciseUnit {
		p.compiled = false
		var units []processing.PreciseUnit
		if p.cxx {
			units = append(units, p.runCxx(store, only(src, processing.PreciseInputCxx))...)
		}
		if p.ts {
			units = append(units, p.runTS(store, only(src, tsc.IsInput))...)
		}
		if p.py {
			units = append(units, p.runPy(store, only(src, pyright.IsInput))...)
		}
		return units
	}
}

// input is the files the selected drivers read (IndexOptions.PreciseInput).
func (p *preciseFlags) input(rel string) bool {
	return p.cxx && processing.PreciseInputCxx(rel) || p.ts && tsc.IsInput(rel) || p.py && pyright.IsInput(rel)
}

// only is src with the files one driver reads.
func only(src processing.PreciseSource, is func(string) bool) processing.PreciseSource {
	pick := func(m map[string]string) map[string]string {
		if m == nil {
			return nil
		}
		out := map[string]string{}
		for k, v := range m {
			if is(k) {
				out[k] = v
			}
		}
		return out
	}
	src.Files, src.Extra = pick(src.Files), pick(src.Extra)
	return src
}

func (p *preciseFlags) runCxx(store *precise.Store, src processing.PreciseSource) []processing.PreciseUnit {
	if len(src.Files) == 0 {
		return nil
	}
	out, err := cxx.Run(context.Background(), cxx.Input{
		Files: src.Files, Extra: src.Extra, Dir: src.Dir, Read: src.Read, Materialize: src.Materialize, CachedOnly: p.mode == "cached",
		LoadLibs: src.Libs, LibDir: processing.LibDir,
	}, cxx.Config{
		Libclang: p.libclang, Jobs: p.jobs, Headers: p.headers, Args: p.cppArgs, Compdb: p.compdb,
		Store: store, Log: os.Stderr,
	})
	if err != nil {
		// cached mode reaches here only with compiled results stored
		fmt.Fprintf(os.Stderr, "reponite: compiler-exact C/C++ calls unavailable: %v\n", err)
		return nil
	}
	if out.Units > 0 && out.Failed < out.Units && (p.mode == "on" || out.Cached == out.Units) {
		p.compiled = true
	}
	if out.Units > 0 && (p.mode == "on" || out.Cached > 0) {
		how := out.Method
		if out.Method == cxx.MethodCompdb && out.Synth > 0 {
			how = fmt.Sprintf("%s; %d not in the build's commands: %s", cxx.MethodCompdb, out.Synth, cxx.MethodSynth)
		}
		fmt.Fprintf(os.Stderr, "reponite: clang (%s): %d translation units — %d cached, %d failed, %d with errors\n",
			how, out.Units, out.Cached, out.Failed, out.Errors)
	}
	if len(out.Edges) == 0 {
		return nil
	}
	// one unit per compile method: a unit the build's commands compiled
	// proves its calls at full confidence, a synthesized one a little less
	compdb := processing.PreciseUnit{Method: processing.MethodClang, Conf: processing.ConfClang}
	synth := processing.PreciseUnit{Method: processing.MethodClangSynth, Conf: processing.ConfClangSynth}
	for _, e := range out.Edges {
		c := processing.PreciseCall{CallerPath: e.CallerPath, CallerLine: e.CallerLine, CallerName: e.CallerName, Name: e.Name,
			CalleePath: e.CalleePath, CalleeLine: e.CalleeLine, External: e.External, Unbound: e.Unbound, Virtual: e.Virtual,
			RecvPath: e.RecvPath, RecvLine: e.RecvLine}
		if e.Method == cxx.MethodCompdb {
			compdb.Calls = append(compdb.Calls, c)
		} else {
			synth.Calls = append(synth.Calls, c)
		}
	}
	var units []processing.PreciseUnit
	for _, u := range []processing.PreciseUnit{compdb, synth} {
		if len(u.Calls) > 0 {
			units = append(units, u)
		}
	}
	return units
}

func (p *preciseFlags) runTS(store *precise.Store, src processing.PreciseSource) []processing.PreciseUnit {
	if len(src.Files) == 0 {
		return nil
	}
	out, err := tsc.Run(context.Background(), tsc.Input{
		Files: src.Files, Extra: src.Extra, Dir: src.Dir, Materialize: src.Materialize, CachedOnly: p.mode == "cached",
	}, tsc.Config{Node: p.node, TypeScript: p.typescript, Jobs: p.jobs, Store: store, Log: os.Stderr})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: compiler-exact TypeScript calls unavailable: %v\n", err)
	}
	if out.Projects > 0 && out.Failed < out.Projects && (p.mode == "on" || out.Cached == out.Projects) {
		p.compiled = true
	}
	if out.Projects > 0 && (p.mode == "on" || out.Cached > 0) {
		ver := ""
		if out.Version != "" {
			ver = " " + out.Version
		}
		fmt.Fprintf(os.Stderr, "reponite: tsc (TypeScript%s): %d projects — %d cached, %d failed\n", ver, out.Projects, out.Cached, out.Failed)
		for _, e := range out.Errs {
			fmt.Fprintf(os.Stderr, "reponite: tsc: %s\n", e)
		}
	}
	ts := processing.PreciseUnit{Method: processing.MethodTSC, Conf: processing.ConfTSC}
	js := processing.PreciseUnit{Method: processing.MethodTSCJS, Conf: processing.ConfTSCJS}
	for _, e := range out.Edges {
		c := processing.PreciseCall{CallerPath: e.CallerPath, CallerLine: e.CallerLine, Name: e.Name,
			CalleePath: e.CalleePath, CalleeLine: e.CalleeLine, CalleeName: e.CalleeName, Ctor: e.Ctor,
			External: e.External, Unbound: e.Unbound}
		if e.Method == tsc.MethodJS {
			js.Calls = append(js.Calls, c)
		} else {
			ts.Calls = append(ts.Calls, c)
		}
	}
	var units []processing.PreciseUnit
	for _, u := range []processing.PreciseUnit{ts, js} {
		if len(u.Calls) > 0 {
			units = append(units, u)
		}
	}
	return units
}

func (p *preciseFlags) runPy(store *precise.Store, src processing.PreciseSource) []processing.PreciseUnit {
	if len(src.Files) == 0 {
		return nil
	}
	out, err := pyright.Run(context.Background(), pyright.Input{
		Files: src.Files, Extra: src.Extra, Dir: src.Dir, Read: src.Read, CachedOnly: p.mode == "cached",
	}, pyright.Config{Node: p.node, Pyright: p.pyright, Store: store, Log: os.Stderr})
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: compiler-exact Python calls unavailable: %v\n", err)
	}
	if out.Units > 0 && out.Failed == 0 && (p.mode == "on" || out.Cached == out.Units) {
		p.compiled = true
	}
	if out.Units > 0 && (p.mode == "on" || out.Cached > 0) && err == nil {
		ver := ""
		if out.Version != "" {
			ver = " " + out.Version
		}
		how := fmt.Sprintf("%d calls resolved", out.Sites)
		if out.Cached > 0 {
			how = "cached"
		}
		fmt.Fprintf(os.Stderr, "reponite: pyright%s: %s\n", ver, how)
	}
	u := processing.PreciseUnit{Method: processing.MethodPyright, Conf: processing.ConfPyright}
	alias := processing.PreciseUnit{Method: processing.MethodPyrightAlias, Conf: processing.ConfPyrightAlias}
	for _, e := range out.Edges {
		c := processing.PreciseCall{CallerPath: e.CallerPath, CallerLine: e.CallerLine, Name: e.Name,
			CalleePath: e.CalleePath, CalleeLine: e.CalleeLine, CalleeName: e.CalleeName, Ctor: e.Ctor,
			External: e.External, Unbound: e.Unbound}
		if e.Alias {
			alias.Calls = append(alias.Calls, c)
		} else {
			u.Calls = append(u.Calls, c)
		}
	}
	var units []processing.PreciseUnit
	for _, x := range []processing.PreciseUnit{u, alias} {
		if len(x.Calls) > 0 {
			units = append(units, x)
		}
	}
	return units
}

// cachedOptions is the default tier for a re-index no command line chose
// (watch, an MCP server's refresh): results already compiled are reused.
func cachedOptions(repo string) processing.IndexOptions {
	// decided at each re-index: a long-running watch sees results compiled
	// after it started
	var mu sync.Mutex
	var pf *preciseFlags
	var at time.Time
	flags := func() *preciseFlags {
		mu.Lock()
		defer mu.Unlock()
		if pf == nil || time.Since(at) > time.Second {
			pf = &preciseFlags{mode: "cached", libclang: os.Getenv("REPONITE_LIBCLANG")}
			pf.cxx, pf.ts, pf.py = false, false, false
			if root := preciseRoot(); root != "" {
				st := precise.Open(root)
				pf.cxx, pf.ts, pf.py = st.Has("cpp"), st.Has(tsc.Kind), st.Has(pyright.Kind)
			}
			at = time.Now()
		}
		return pf
	}
	return processing.IndexOptions{
		PreciseInput: func(rel string) bool { return flags().input(rel) },
		PreciseDriver: func(src processing.PreciseSource) []processing.PreciseUnit {
			f := &preciseFlags{mode: "cached", libclang: os.Getenv("REPONITE_LIBCLANG")}
			if d := f.driver(repo); d != nil {
				return d(src)
			}
			return nil
		},
	}
}

// cmdPrecise reports and maintains the compiler-exact tier's store.
func cmdPrecise(args []string) {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	store := precise.Open(preciseRoot())
	switch sub {
	case "status":
		var libclang, node, typescript, pyr string
		parseCmd("precise status", "precise status [--libclang PATH] [--node PATH] [--typescript DIR] [--pyright PATH]", args, func(fs *flag.FlagSet) {
			fs.StringVar(&libclang, "libclang", os.Getenv("REPONITE_LIBCLANG"), "libclang to load")
			fs.StringVar(&node, "node", os.Getenv("REPONITE_NODE"), "node binary")
			fs.StringVar(&typescript, "typescript", os.Getenv("REPONITE_TYPESCRIPT"), "typescript package directory")
			fs.StringVar(&pyr, "pyright", os.Getenv("REPONITE_PYRIGHT"), "pyright package, script or langserver")
		})
		b, n := store.Size()
		fmt.Printf("store: %s (%d entries, %.1f MB)\n", store.Dir(), n, float64(b)/(1<<20))
		if err := cxx.Load(libclang); err != nil {
			fmt.Printf("C/C++: unavailable — %v\n", err)
		} else {
			fmt.Printf("C/C++: %s — `reponite index --precise on` compiles; the default (cached) reuses what was compiled\n", cxx.Version())
		}
		nodeBin, nodeErr := tsc.FindNode(node)
		switch v := tsc.TypeScriptVersion(typescript); {
		case nodeErr != nil:
			fmt.Printf("TypeScript/JavaScript: unavailable — %v\n", nodeErr)
		case v != "":
			fmt.Printf("TypeScript/JavaScript: TypeScript %s (node %s); a repo's own node_modules/typescript is preferred\n", v, nodeBin)
		default:
			fmt.Printf("TypeScript/JavaScript: node %s; only repos with their own node_modules/typescript (--typescript names one for the others)\n", nodeBin)
		}
		if eng, err := pyright.Find(pyr, node, ""); err != nil {
			fmt.Printf("Python: unavailable — %v\n", err)
		} else {
			v := eng.Version
			if v == "" {
				v = eng.Argv[0]
			}
			fmt.Printf("Python: pyright %s\n", v)
		}
	case "gc":
		var max string
		parseCmd("precise gc", "precise gc [--max-size 2G]", args, func(fs *flag.FlagSet) {
			fs.StringVar(&max, "max-size", "2G", "keep at most this much (K, M, G suffixes), least recently used removed first")
		})
		limit, err := parseSize(max)
		if err != nil {
			fail(err)
		}
		fmt.Printf("removed %d entries\n", store.Prune(limit))
	default:
		fail(fmt.Errorf("precise %q: want status or gc", sub))
	}
}

var reSize = regexp.MustCompile(`^\s*([0-9]+(?:\.[0-9]+)?)\s*([kKmMgGtT]?)(i?[bB]?)\s*$`)

// parseSize reads a size like 500M, 2G, 2GB, 1.5G or 512Mi (binary units);
// anything else is an error, never a smaller number.
func parseSize(s string) (int64, error) {
	m := reSize.FindStringSubmatch(s)
	if m == nil {
		return 0, fmt.Errorf("--max-size %q: want a size like 500M, 2G or 1.5GB", s)
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0, fmt.Errorf("--max-size %q: %v", s, err)
	}
	mult := map[string]float64{"": 1, "k": 1 << 10, "m": 1 << 20, "g": 1 << 30, "t": 1 << 40}[strings.ToLower(m[2])]
	return int64(n * mult), nil
}
