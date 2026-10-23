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

	"github.com/vishwak02/reponite/internal/precise"
	"github.com/vishwak02/reponite/internal/precise/cxx"
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
	// compiled is set when the last ref's driver run parsed translation
	// units (compiled now or read from the store): only then does the ref
	// record that it holds compiler-exact calls
	compiled bool
}

// preciseConfig is how a repo was last compiled: a later run without the
// flags reuses them, so the default (cached) index finds the results.
type preciseConfig struct {
	Headers  []string `json:"headers,omitempty"`
	Args     []string `json:"args,omitempty"`
	Compdb   string   `json:"compdb,omitempty"`
	Libclang string   `json:"libclang,omitempty"`
}

func configPath(root, repo string) string {
	return filepath.Join(root, "configs", repo+".json")
}

// settle fills the compile settings the flags left out from the ones the
// repo was last compiled with, and records them when this run compiles.
func (p *preciseFlags) settle(root, repo string) {
	given := len(p.headers) > 0 || len(p.cppArgs) > 0 || p.compdb != ""
	// absolute: a later run (watch, an MCP server) starts elsewhere
	for i, h := range p.headers {
		if abs, err := filepath.Abs(h); err == nil {
			p.headers[i] = abs
		}
	}
	if p.compdb != "" {
		if abs, err := filepath.Abs(p.compdb); err == nil {
			p.compdb = abs
		}
	}
	path := configPath(root, repo)
	if p.mode == "on" && given {
		cfg := preciseConfig{Headers: p.headers, Args: p.cppArgs, Compdb: p.compdb, Libclang: p.libclang}
		if b, err := json.MarshalIndent(cfg, "", "  "); err == nil && os.MkdirAll(filepath.Dir(path), 0o755) == nil {
			_ = os.WriteFile(path, b, 0o644)
		}
		return
	}
	if given {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var cfg preciseConfig
	if json.Unmarshal(b, &cfg) != nil {
		return
	}
	p.headers, p.cppArgs, p.compdb = cfg.Headers, cfg.Args, cfg.Compdb
	if p.libclang == "" {
		p.libclang = cfg.Libclang
	}
}

func (p *preciseFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&p.mode, "precise", "cached", "compiler-exact C/C++ calls: off | cached (reuse results already compiled, never compile) | on (compile what is not cached)")
	fs.Var(&p.headers, "cpp-headers", "header trees the C/C++ build sees, for --precise (a ROS install, a copied /usr/include, generated messages); repeatable")
	fs.Var(&p.cppArgs, "cpp-arg", "extra compiler argument for --precise (repeatable, comma-separable), e.g. -DCONFIG_X=1")
	fs.StringVar(&p.compdb, "compdb", "", "the build's compile_commands.json for --precise (default: one at the tree root or build/)")
	fs.StringVar(&p.libclang, "libclang", os.Getenv("REPONITE_LIBCLANG"), "libclang to load for --precise (default: the usual library names)")
	fs.IntVar(&p.jobs, "precise-jobs", 0, "parallel compiler workers for --precise (default: one per CPU, at most what free memory holds at about 1 GB each)")
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

// driver is the C/C++ precise driver the flags select, nil when off.
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
	if p.mode == "cached" && !store.Has("cpp") {
		return nil // nothing was ever compiled: the default index does no extra work
	}
	p.settle(root, repo)
	return func(src processing.PreciseSource) []processing.PreciseUnit {
		p.compiled = false
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
		p.compiled = out.Units > 0 && out.Failed < out.Units && (p.mode == "on" || out.Cached == out.Units)
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
}

// cachedDriver is the default tier for a re-index no command line chose
// (watch, an MCP server's refresh): results already compiled are reused.
func cachedDriver(repo string) func(processing.PreciseSource) []processing.PreciseUnit {
	// decided at each re-index: a long-running watch sees results compiled
	// after it started
	return func(src processing.PreciseSource) []processing.PreciseUnit {
		pf := &preciseFlags{mode: "cached", libclang: os.Getenv("REPONITE_LIBCLANG")}
		if d := pf.driver(repo); d != nil {
			return d(src)
		}
		return nil
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
		var libclang string
		parseCmd("precise status", "precise status [--libclang PATH]", args, func(fs *flag.FlagSet) {
			fs.StringVar(&libclang, "libclang", os.Getenv("REPONITE_LIBCLANG"), "libclang to load")
		})
		b, n := store.Size()
		fmt.Printf("store: %s (%d entries, %.1f MB)\n", store.Dir(), n, float64(b)/(1<<20))
		if err := cxx.Load(libclang); err != nil {
			fmt.Printf("C/C++: unavailable — %v\n", err)
		} else {
			fmt.Printf("C/C++: %s — `reponite index --precise on` compiles; the default (cached) reuses what was compiled\n", cxx.Version())
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
