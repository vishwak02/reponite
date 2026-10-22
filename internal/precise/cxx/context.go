package cxx

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// compileCtx is how each unit of a ref compiles: the build's own commands
// (compile_commands.json) where it lists the unit, else a context
// synthesized from the tree (include directories, package.xml).
type compileCtx struct {
	compdb map[string][]string // unit -> its build's arguments, paths on rootMark (srcMark)
	// srcRoot is the source directory each unit's build command named
	srcRoot map[string]string
	// libPaths are the submodules' files and directories that are not the
	// repo's: a build's -I into a submodule reads them at libMark
	libPaths map[string]bool
	// libAnc are the repo's directories (and "" the root) holding a
	// submodule: a build's -I there reads the submodule at libMark too
	libAnc map[string]bool
	base   func(tu string) []string
	// args is how many of base's last arguments follow the include
	// directories (the system headers, --cpp-arg)
	args int
	// dirs is a synthesized unit's include directories in search order
	// (rootMark/ and libMark/ paths); roots the include directories a
	// repo or submodule file is under. nil without a synthesized context.
	dirs  func(tu string) []string
	roots func(p string) []string
	sys   sysIncludes
}

func newCompileCtx(in Input, cfg Config) *compileCtx {
	repo := repoPaths(in)
	cc := &compileCtx{srcRoot: map[string]string{}}
	cc.compdb = compdbArgs(in, cfg, repo, cc.srcRoot)
	synthArgs(in, cfg, cc)
	if len(in.Libs) > 0 && len(cc.compdb) > 0 {
		cc.libPaths, cc.libAnc = map[string]bool{}, map[string]bool{"": true}
		for p := range in.Libs {
			for d := p; d != "." && d != "/"; d = path.Dir(d) {
				if repo[d] {
					cc.libAnc[d] = true
				} else {
					cc.libPaths[d] = true
				}
			}
		}
	}
	return cc
}

// repoPaths is every repo file and directory of in.
func repoPaths(in Input) map[string]bool {
	repo := map[string]bool{}
	for _, m := range []map[string]string{in.Files, in.Extra} {
		for p := range m {
			for d := p; d != "." && d != "/" && !repo[d]; d = path.Dir(d) {
				repo[d] = true
			}
		}
	}
	return repo
}

func (cc *compileCtx) method(tu string) string {
	if _, ok := cc.compdb[tu]; ok {
		return MethodCompdb
	}
	return MethodSynth
}

// key is what of a unit's arguments its cache key holds: the build's whole
// command, or a synthesized context without its include directories (which
// files those made the unit read is in its manifest).
func (cc *compileCtx) key(tu string) []string {
	if a, ok := cc.compdb[tu]; ok {
		return a
	}
	return cc.base(tu)
}

// full is a unit's arguments for the compiler.
func (cc *compileCtx) full(tu string) []string {
	if a, ok := cc.compdb[tu]; ok {
		if cc.libPaths == nil && !hasSrcMark(a) {
			return a
		}
		// a submodule's directory is empty in the ref's copy: its files are
		// at libMark — an include directory inside one is read there, one
		// above one (the repo root, third_party/) there as well
		// under the build's source directory, not the repo's: a submodule's
		// (at libMark), else where the build left it
		a = append([]string(nil), a...)
		for i, x := range a {
			if j := strings.Index(x, srcMark+"/"); j >= 0 {
				r := x[j+len(srcMark)+1:]
				if cc.libPaths[r] {
					a[i] = x[:j] + libMark + "/" + r
				} else {
					a[i] = x[:j] + cc.srcRoot[tu] + "/" + r
				}
			}
		}
		var out []string
		for i := 0; i < len(a); i++ {
			x := a[i]
			flag, val := "", x
			if includeDirFlags[x] && i+1 < len(a) {
				flag, val = x, a[i+1]
				i++
			} else if f := joinedPath(x); f != "" && includeDirFlags[f] {
				flag, val = f, x[len(f):]
			}
			rel, under := "", val == rootMark
			if !under {
				rel, under = strings.CutPrefix(val, rootMark+"/")
			}
			lib := ""
			switch {
			case !under || flag == "":
			case cc.libPaths[rel]:
				val = libMark + "/" + rel
			case cc.libAnc[rel]:
				lib = strings.TrimSuffix(libMark+"/"+rel, "/")
			}
			if flag == "" {
				out = append(out, val)
				continue
			}
			out = append(out, flag, val)
			if lib != "" {
				out = append(out, flag, lib)
			}
		}
		return out
	}
	args := cc.base(tu)
	out := append([]string(nil), args[:len(args)-cc.args]...)
	for _, d := range cc.dirs(tu) {
		if strings.HasPrefix(d, libMark+"/") {
			out = append(out, "-isystem", d)
		} else {
			out = append(out, "-I", d)
		}
	}
	return append(out, args[len(args)-cc.args:]...)
}

func hasSrcMark(a []string) bool {
	for _, x := range a {
		if strings.Contains(x, srcMark+"/") {
			return true
		}
	}
	return false
}

// srcMark stands for the build's source directory in a path the repo does
// not hold (an untracked build/ directory, a submodule at a git ref).
const srcMark = "\x00SRC"

// includeDirFlags name an include directory.
var includeDirFlags = map[string]bool{"-I": true, "-isystem": true, "-iquote": true, "-idirafter": true}

// --- compile_commands.json ---

// compdbArgs reads a compile_commands.json (cfg.Compdb, else one in the tree
// or its build directory) and maps each unit it lists to its build's
// arguments, its paths moved onto rootMark; nil when there is none.
func compdbArgs(in Input, cfg Config, repo map[string]bool, srcRoot map[string]string) map[string][]string {
	read := func(p string) ([]byte, error) { return os.ReadFile(p) }
	cands := []string{cfg.Compdb}
	if cfg.Compdb == "" && in.Read != nil {
		read = in.Read
		cands = []string{"compile_commands.json", "build/compile_commands.json"}
	}
	type entry struct {
		Directory string   `json:"directory"`
		File      string   `json:"file"`
		Arguments []string `json:"arguments"`
		Command   string   `json:"command"`
	}
	for _, p := range cands {
		if p == "" {
			continue
		}
		data, err := read(p)
		if err != nil {
			continue
		}
		var entries []entry
		if json.Unmarshal(data, &entries) != nil || len(entries) == 0 {
			continue
		}
		// a workspace's database lists every package: this repo's units are
		// those under the repo's own directory, else under the one source
		// root most of the repo's files map to (another package's
		// src/main.cpp is not this one's); when two tie, neither is trusted
		type hit struct {
			e        entry
			rel, src string
		}
		var hits []hit
		for _, e := range entries {
			file := e.File
			if !filepath.IsAbs(file) {
				file = filepath.Join(e.Directory, file)
			}
			rel, src := relOf(file, in.Files)
			if rel == "" {
				continue
			}
			hits = append(hits, hit{e, rel, src})
		}
		rels := map[string]map[string]bool{}
		for _, h := range hits {
			if rels[h.src] == nil {
				rels[h.src] = map[string]bool{}
			}
			rels[h.src][h.rel] = true
		}
		best, tie := "", false
		if in.Dir != "" {
			if d, err := filepath.EvalSymlinks(in.Dir); err == nil {
				d = filepath.ToSlash(filepath.Clean(d))
				for src := range rels {
					if s, err := filepath.EvalSymlinks(src); err == nil && filepath.ToSlash(filepath.Clean(s)) == d {
						best = src
					}
				}
			}
		}
		if best == "" {
			for src, rs := range rels {
				switch n := len(rs); {
				case best == "" || n > len(rels[best]):
					best, tie = src, false
				case n == len(rels[best]):
					tie = true
				}
			}
			if tie {
				continue
			}
		}
		byRel := map[string][]string{}
		for _, h := range hits {
			if h.src != best {
				continue
			}
			args := h.e.Arguments
			if len(args) == 0 {
				args = splitCommand(h.e.Command)
			}
			if _, dup := byRel[h.rel]; dup {
				continue // the first command for a unit, as tools read the database
			}
			byRel[h.rel] = append(rebaseArgs(args, h.e.Directory, h.src, rootMark, repo), cfg.Args...)
			if srcRoot != nil {
				srcRoot[h.rel] = h.src
			}
		}
		if len(byRel) > 0 {
			return byRel
		}
	}
	return nil
}

// relOf finds the repo file an absolute build path names (the longest
// path suffix that is a repo file) and the source root it implies.
func relOf(abs string, files map[string]string) (rel, srcRoot string) {
	abs = filepath.ToSlash(filepath.Clean(abs))
	parts := strings.Split(abs, "/")
	for i := 1; i < len(parts); i++ {
		r := strings.Join(parts[i:], "/")
		if _, ok := files[r]; ok {
			return r, strings.Join(parts[:i], "/")
		}
	}
	return "", ""
}

// rebaseArgs keeps a build command's preprocessor, language and target
// flags, its source-tree paths moved from srcRoot to root; the compiler, the
// output, warnings and the input file are dropped. A path under srcRoot the
// repo does not hold (an untracked build/ directory's generated headers, a
// submodule) is put on srcMark: the compiler reads it where the build left
// it, or in the submodule's copy.
func rebaseArgs(args []string, dir, srcRoot, root string, repo map[string]bool) []string {
	move := func(p string) string {
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		p = filepath.ToSlash(filepath.Clean(p))
		if srcRoot != "" && (p == srcRoot || strings.HasPrefix(p, srcRoot+"/")) {
			rel := strings.TrimPrefix(strings.TrimPrefix(p, srcRoot), "/")
			if rel == "" || repo == nil || repo[rel] {
				return root + strings.TrimPrefix(p, srcRoot)
			}
			return srcMark + "/" + rel
		}
		return p
	}
	args = expandResponseFiles(args, dir, 0)
	var out []string
	for i := 1; i < len(args); i++ {
		a := args[i]
		next := func() (string, bool) {
			if i+1 < len(args) {
				i++
				return args[i], true
			}
			return "", false
		}
		switch {
		case a == "-Xclang" && i+3 < len(args) && args[i+1] == "-include" && args[i+2] == "-Xclang":
			// CMake's precompiled header for clang: the header itself
			out = append(out, "-include", move(args[i+3]))
			i += 3
		case a == "-o" || a == "-MF" || a == "-MT" || a == "-MQ" || a == "-Xclang" || a == "-Xpreprocessor" || a == "-arch" ||
			a == "-mllvm" || a == "-include-pch" || a == "-Xlinker" || a == "-Xassembler":
			next()
		case pathFlags[a]:
			if v, ok := next(); ok {
				out = append(out, a, move(v))
			}
		case a == "-D" || a == "-U" || a == "-x" || a == "-target":
			if v, ok := next(); ok {
				out = append(out, a, v)
			}
		case strings.HasPrefix(a, "--sysroot="):
			out = append(out, "--sysroot="+move(a[len("--sysroot="):]))
		case strings.HasPrefix(a, "-include-pch"):
		case joinedPath(a) != "":
			f := joinedPath(a)
			out = append(out, f+move(a[len(f):]))
		case strings.HasPrefix(a, "-D") || strings.HasPrefix(a, "-U") || strings.HasPrefix(a, "-std=") ||
			strings.HasPrefix(a, "-x") || strings.HasPrefix(a, "--target=") || strings.HasPrefix(a, "-stdlib=") ||
			strings.HasPrefix(a, "-m") || strings.HasPrefix(a, "-O") || a == "-pthread" || a == "-ansi" ||
			a == "-nostdinc" || a == "-nostdinc++" || a == "-undef":
			out = append(out, a)
		case strings.HasPrefix(a, "-f") && !strings.Contains(a, "diagnostics") && !strings.Contains(a, "color"):
			out = append(out, a) // -fopenmp, -fno-exceptions: macros the code tests
		}
	}
	out = append(out, "-w", "-ferror-limit=0")
	out = append(out, simdFlags()...)
	for _, d := range builtinIncludes() {
		out = append(out, "-isystem", d)
	}
	return out
}

// pathFlags take a path as the next argument.
var pathFlags = map[string]bool{"-I": true, "-isystem": true, "-iquote": true, "-idirafter": true,
	"-include": true, "-imacros": true, "-isysroot": true, "--sysroot": true, "-iwithprefix": true}

// joinedPath is the flag of a path flag written joined (-I/x, -isystem/x),
// "" for another argument.
func joinedPath(a string) string {
	for _, f := range []string{"-isystem", "-iquote", "-idirafter", "-isysroot", "-include", "-imacros", "-I"} {
		if strings.HasPrefix(a, f) && len(a) > len(f) {
			return f
		}
	}
	return ""
}

// expandResponseFiles replaces each @file argument with the arguments the
// file holds (relative to dir), as a compiler driver reads them.
func expandResponseFiles(args []string, dir string, depth int) []string {
	var out []string
	for i, a := range args {
		if i == 0 || !strings.HasPrefix(a, "@") || depth > 3 {
			out = append(out, a)
			continue
		}
		p := a[1:]
		if !filepath.IsAbs(p) {
			p = filepath.Join(dir, p)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		more := expandResponseFiles(append([]string{""}, splitCommand(string(b))...), dir, depth+1)
		out = append(out, more[1:]...)
	}
	return out
}

// splitCommand splits a shell command line into its words, as the shell
// that ran the build did: quotes and backslash escapes removed.
func splitCommand(cmd string) []string {
	var out []string
	var cur strings.Builder
	word := false
	for i := 0; i < len(cmd); i++ {
		ch := cmd[i]
		switch {
		case ch == '\\' && i+1 < len(cmd):
			i++
			if cmd[i] != '\n' { // a backslash-newline joins lines
				cur.WriteByte(cmd[i])
			}
			word = true
		case ch == '\'':
			j := strings.IndexByte(cmd[i+1:], '\'')
			if j < 0 {
				j = len(cmd) - i - 1
			}
			cur.WriteString(cmd[i+1 : i+1+j])
			i += j + 1
			word = true
		case ch == '"':
			for i++; i < len(cmd) && cmd[i] != '"'; i++ {
				if cmd[i] == '\\' && i+1 < len(cmd) && strings.IndexByte("\\\"$`\n", cmd[i+1]) >= 0 {
					i++
				}
				cur.WriteByte(cmd[i])
			}
			word = true
		case ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r':
			if word {
				out = append(out, cur.String())
				cur.Reset()
				word = false
			}
		default:
			cur.WriteByte(ch)
			word = true
		}
	}
	if word {
		out = append(out, cur.String())
	}
	return out
}

// --- a synthesized context ---

var reXMLName = regexp.MustCompile(`<name>\s*([^<\s]+)\s*</name>`)
var reXMLDep = regexp.MustCompile(`<(?:build_)?depend>\s*([^<\s]+)\s*</`)

func isHeader(p string) bool {
	return strings.HasSuffix(p, ".h") || strings.HasSuffix(p, ".hpp") || strings.HasSuffix(p, ".hh") || strings.HasSuffix(p, ".hxx")
}

// includeRoots adds to set every directory holding headers directly ("x.hpp"
// next to its sources) and every directory named include.
func includeRoots(files map[string]string, set map[string]bool) {
	for p := range files {
		if !isHeader(p) {
			continue
		}
		dir := path.Dir(p)
		set[dir] = true
		parts := strings.Split(dir, "/")
		for i, s := range parts {
			if s == "include" {
				set[strings.Join(parts[:i+1], "/")] = true
			}
		}
	}
}

// synthArgs sets cc's context the way a catkin/ament build lists include
// directories — the unit's own package's, then those of the packages it
// depends on (package.xml), then the rest of the repo's, then the
// submodules' — plus the header trees given. A unit outside any package sees
// the repo's include directories nearest it first.
func synthArgs(in Input, cfg Config, cc *compileCtx) {
	incSet, libSet := map[string]bool{}, map[string]bool{}
	includeRoots(in.Files, incSet)
	includeRoots(in.Extra, incSet)
	includeRoots(in.Libs, libSet)
	pkgOfDir := map[string]string{} // package dir -> name
	deps := map[string][]string{}   // package dir -> dependency names
	for p := range in.Files {
		if path.Base(p) != "package.xml" || in.Read == nil {
			continue
		}
		data, err := in.Read(p)
		if err != nil {
			continue
		}
		if m := reXMLName.FindSubmatch(data); m != nil {
			dir := path.Dir(p)
			pkgOfDir[dir] = string(m[1])
			for _, d := range reXMLDep.FindAllSubmatch(data, -1) {
				deps[dir] = append(deps[dir], string(d[1]))
			}
		}
	}
	dirOfPkg := map[string]string{}
	for d, n := range pkgOfDir {
		dirOfPkg[n] = d
	}
	packageOf := func(p string) string {
		for d := path.Dir(p); ; d = path.Dir(d) {
			if _, ok := pkgOfDir[d]; ok {
				return d
			}
			if d == "." || d == "/" {
				return ""
			}
		}
	}
	type incDir struct {
		dir   string
		pkg   string
		parts []string
	}
	var incs []incDir
	for d := range incSet {
		incs = append(incs, incDir{d, packageOf(d + "/x"), strings.Split(d, "/")})
	}
	sort.Slice(incs, func(i, j int) bool { return incs[i].dir < incs[j].dir })
	var libIncs []string
	for d := range libSet {
		libIncs = append(libIncs, libMark+"/"+d)
	}
	sort.Strings(libIncs)

	cc.sys = systemIncludes(cfg.Headers)
	cc.base = func(tu string) []string {
		args := []string{"-x", "c++", "-std=c++17"}
		if strings.HasSuffix(tu, ".c") {
			args = []string{"-x", "c", "-std=c11"}
		}
		args = append(args, "-w", "-ferror-limit=0")
		args = append(args, cc.sys.flags...)
		args = append(args, cc.sys.tail...)
		return append(args, cfg.Args...) // after the repo's directories, as given
	}
	cc.args = len(cc.sys.tail) + len(cfg.Args)
	// the order depends only on the unit's directory: computed once for each
	var mu sync.Mutex
	byDir := map[string][]string{}
	cc.dirs = func(tu string) []string {
		dir := path.Dir(tu)
		mu.Lock()
		defer mu.Unlock()
		if d, ok := byDir[dir]; ok {
			return d
		}
		// tiers: own package 0, its dependencies 1.. (breadth first), others last
		tier := map[string]int{}
		if own := packageOf(tu); own != "" {
			queue, order := []string{own}, 0
			tier[own] = 0
			for len(queue) > 0 {
				d := queue[0]
				queue = queue[1:]
				order++
				for _, dep := range deps[d] {
					if dd, ok := dirOfPkg[dep]; ok {
						if _, seen := tier[dd]; !seen {
							tier[dd] = order
							queue = append(queue, dd)
						}
					}
				}
			}
		}
		tuParts := strings.Split(dir, "/")
		type ranked struct {
			dir          string
			tier, shared int
		}
		rs := make([]ranked, len(incs))
		for i, d := range incs {
			t, ok := tier[d.pkg]
			if !ok || d.pkg == "" {
				t = 1 << 20
			}
			n := 0
			for n < len(d.parts) && n < len(tuParts) && d.parts[n] == tuParts[n] {
				n++
			}
			rs[i] = ranked{d.dir, t, n}
		}
		sort.SliceStable(rs, func(i, j int) bool {
			if rs[i].tier != rs[j].tier {
				return rs[i].tier < rs[j].tier
			}
			return rs[i].shared > rs[j].shared
		})
		out := make([]string, 0, len(rs)+len(libIncs))
		for _, r := range rs {
			out = append(out, rootMark+"/"+r.dir)
		}
		out = append(out, libIncs...)
		byDir[dir] = out
		return out
	}
	cc.roots = func(p string) []string {
		set, prefix := incSet, rootMark+"/"
		if rel, ok := strings.CutPrefix(p, libMark+"/"); ok {
			set, prefix, p = libSet, libMark+"/", rel
		}
		var out []string
		for d := path.Dir(p); d != "." && d != "/"; d = path.Dir(d) {
			if set[d] {
				out = append(out, prefix+d)
			}
		}
		return out
	}
}

// --- the compiler's own and the system's headers ---

var builtinOnce struct {
	sync.Once
	dirs []string
}

// builtinIncludes is the compiler's own headers (stddef.h, stdarg.h, the
// intrinsics), which libclang finds only when its resource directory was
// installed beside it: the loaded libclang's version's, else GCC's newest.
func builtinIncludes() []string {
	builtinOnce.Do(func() {
		major := ""
		if m := regexp.MustCompile(`version (\d+)`).FindStringSubmatch(Version()); m != nil {
			major = m[1]
		}
		var pats []string
		if major != "" {
			pats = []string{"/usr/lib/llvm-" + major + "/lib/clang/" + major + "*/include", "/usr/lib/clang/" + major + "*/include",
				"/usr/local/lib/clang/" + major + "*/include", "/usr/lib64/clang/" + major + "*/include",
				"/opt/homebrew/opt/llvm/lib/clang/" + major + "*/include", "/usr/local/opt/llvm/lib/clang/" + major + "*/include"}
		} else {
			pats = []string{"/usr/lib/llvm-*/lib/clang/*/include", "/usr/lib/clang/*/include", "/usr/local/lib/clang/*/include"}
		}
		// another version's intrinsics call builtins this libclang lacks:
		// GCC's headers, with the SIMD macros undefined, are the safer choice
		pats = append(pats, "/usr/lib/gcc/*-linux-gnu/*/include", "/usr/lib/gcc/*/*/include")
		for _, pat := range pats {
			if m, _ := filepath.Glob(pat); len(m) > 0 {
				sortVersions(m)
				builtinOnce.dirs = []string{m[len(m)-1]}
				return
			}
		}
	})
	return builtinOnce.dirs
}

// simdFlags undefine the SIMD macros when the builtin headers are GCC's:
// its intrinsics headers call GCC's builtins, which clang does not have, and
// code choosing SIMD paths by these macros takes its scalar one.
func simdFlags() []string {
	b := builtinIncludes()
	if len(b) == 0 || !strings.Contains(b[0], "/gcc/") {
		return nil
	}
	var out []string
	for _, m := range []string{"__MMX__", "__SSE__", "__SSE2__", "__SSE3__", "__SSSE3__", "__SSE4_1__", "__SSE4_2__", "__AVX__", "__AVX2__", "__FMA__"} {
		out = append(out, "-U"+m)
	}
	return out
}

// sortVersions sorts paths by the version numbers in them (llvm-9 before
// llvm-18, gcc/9 before gcc/13), then by name.
func sortVersions(ps []string) {
	key := func(p string) []int {
		var out []int
		for _, m := range regexp.MustCompile(`\d+`).FindAllString(p, -1) {
			n, _ := strconv.Atoi(m)
			out = append(out, n)
		}
		return out
	}
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := key(ps[i]), key(ps[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k] != b[k] {
				return a[k] < b[k]
			}
		}
		if len(a) != len(b) {
			return len(a) < len(b)
		}
		return ps[i] < ps[j]
	})
}

type sysIncludes struct {
	flags []string // -nostdinc++ when a tree brings its own C++ library; -U SIMD
	dirs  []string
	tail  []string // -isystem for each of dirs
}

// systemIncludes reads header trees: a copied /usr/include (its own C++
// library, used instead of the host's), and trees holding include/
// directories (a ROS install, generated messages).
func systemIncludes(trees []string) sysIncludes {
	s := sysIncludes{flags: simdFlags()}
	builtins := builtinIncludes()
	for _, t := range trees {
		if fi, err := os.Stat(filepath.Join(t, "c++")); err == nil && fi.IsDir() {
			// a /usr/include: its libstdc++, the newest version it holds
			ents, _ := os.ReadDir(filepath.Join(t, "c++"))
			var vers []string
			for _, e := range ents {
				if e.IsDir() && e.Name() != "" && e.Name()[0] >= '0' && e.Name()[0] <= '9' {
					vers = append(vers, e.Name())
				}
			}
			sortVersions(vers)
			if len(vers) > 0 {
				v := vers[len(vers)-1]
				s.flags = append(s.flags, "-nostdinc++")
				s.dirs = append(s.dirs, filepath.Join(t, "c++", v))
				if m, _ := filepath.Glob(filepath.Join(t, "*-linux-gnu", "c++", v)); len(m) > 0 {
					s.dirs = append(s.dirs, m[0])
				}
				s.dirs = append(s.dirs, filepath.Join(t, "c++", v, "backward"))
			}
			s.dirs = append(s.dirs, builtins...) // before the C library, as a compiler orders them
			builtins = nil
			s.dirs = append(s.dirs, t)
			for _, sub := range []string{"eigen3", "opencv4"} {
				if fi, err := os.Stat(filepath.Join(t, sub)); err == nil && fi.IsDir() {
					s.dirs = append(s.dirs, filepath.Join(t, sub))
				}
			}
			if m, _ := filepath.Glob(filepath.Join(t, "*-linux-gnu")); len(m) > 0 {
				s.dirs = append(s.dirs, m[0])
			}
			continue
		}
		_ = filepath.WalkDir(t, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			if strings.Count(strings.TrimPrefix(p, t), string(filepath.Separator)) > 6 {
				return filepath.SkipDir
			}
			if d.Name() == "include" {
				s.dirs = append(s.dirs, p)
				for _, sub := range []string{"eigen3", "xmlrpcpp"} {
					if fi, err := os.Stat(filepath.Join(p, sub)); err == nil && fi.IsDir() {
						s.dirs = append(s.dirs, filepath.Join(p, sub))
					}
				}
				return filepath.SkipDir
			}
			return nil
		})
	}
	s.dirs = append(s.dirs, builtins...)
	for _, d := range s.dirs {
		s.tail = append(s.tail, "-isystem", d)
	}
	return s
}

// libclangNames are the names libclang is installed under, the unversioned
// (the distribution's default) first, then the newest version first.
func libclangNames() []string {
	out := []string{"libclang.so", "libclang.so.1"}
	for v := 25; v >= 7; v-- {
		out = append(out, fmt.Sprintf("libclang-%d.so.1", v), fmt.Sprintf("libclang.so.%d", v),
			fmt.Sprintf("/usr/lib/llvm-%d/lib/libclang.so.1", v), fmt.Sprintf("/usr/lib/llvm-%d/lib/libclang-%d.so.1", v, v))
	}
	return append(out, "libclang.dylib", "/Library/Developer/CommandLineTools/usr/lib/libclang.dylib",
		"/Applications/Xcode.app/Contents/Developer/Toolchains/XcodeDefault.xctoolchain/usr/lib/libclang.dylib",
		"/opt/homebrew/opt/llvm/lib/libclang.dylib", "/usr/local/opt/llvm/lib/libclang.dylib")
}
