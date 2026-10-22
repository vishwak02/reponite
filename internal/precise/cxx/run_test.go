package cxx

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/vishwak02/reponite/internal/precise"
)

// The test binary is its own worker: Run starts `<binary> cxx-worker`.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "cxx-worker" {
		if err := Serve(os.Stdin, os.Stdout, os.Getenv("REPONITE_LIBCLANG")); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// tree is a repo on disk for Run.
type tree struct {
	t     *testing.T
	root  string
	store *precise.Store
}

func newTree(t *testing.T, files map[string]string) *tree {
	t.Helper()
	if err := Load(os.Getenv("REPONITE_LIBCLANG")); err != nil {
		t.Skip(err)
	}
	base, _ := filepath.EvalSymlinks(t.TempDir())
	tr := &tree{t: t, root: filepath.Join(base, "repo"), store: precise.Open(filepath.Join(base, "store"))}
	for p, src := range files {
		tr.write(p, src)
	}
	return tr
}

func (tr *tree) write(rel, src string) {
	p := filepath.Join(tr.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		tr.t.Fatal(err)
	}
	// a later edit within the clock's resolution still reads as one
	now := time.Now().Add(time.Duration(len(src)) * time.Millisecond)
	_ = os.Chtimes(p, now, now)
}

// input is the tree as a working-tree index gives it: C/C++ files and
// manifests by content, skip naming directories the walk leaves out.
func (tr *tree) input(cachedOnly bool, skip ...string) Input {
	files := map[string]string{}
	_ = filepath.WalkDir(tr.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(tr.root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			for _, s := range skip {
				if rel == s {
					return filepath.SkipDir
				}
			}
			return nil
		}
		ext := filepath.Ext(rel)
		if sourceExt[ext] || ext == ".h" || ext == ".hpp" || filepath.Base(rel) == "package.xml" || filepath.Base(rel) == "compile_commands.json" {
			b, _ := os.ReadFile(p)
			files[rel] = blobID(b)
		}
		return nil
	})
	return Input{
		Files:       files,
		Read:        func(rel string) ([]byte, error) { return os.ReadFile(filepath.Join(tr.root, filepath.FromSlash(rel))) },
		Materialize: func() (string, func(), error) { return tr.root, func() {}, nil },
		CachedOnly:  cachedOnly,
	}
}

func (tr *tree) run(in Input, cfg Config) Output {
	tr.t.Helper()
	exe, err := os.Executable()
	if err != nil {
		tr.t.Fatal(err)
	}
	cfg.Exe, cfg.Store, cfg.Jobs = exe, tr.store, 2
	out, err := Run(context.Background(), in, cfg)
	if err != nil {
		tr.t.Fatal(err)
	}
	return out
}

// callee is where the edge of caller (path:line) for name goes: "path:line",
// "unbound", "external", "" none.
func callee(out Output, caller, name string) string {
	for _, e := range out.Edges {
		if e.CallerPath+":"+itoa(e.CallerLine) != caller || e.Name != name {
			continue
		}
		switch {
		case e.Unbound:
			return "unbound"
		case e.External:
			return "external"
		}
		return e.CalleePath + ":" + itoa(e.CalleeLine)
	}
	return ""
}

func TestRunBindsAndCaches(t *testing.T) {
	tr := newTree(t, map[string]string{
		"src/lib.cpp":  "int a() { return 1; }\nint b() { return 2; }\n",
		"src/main.cpp": "#include \"pick.inc\"\nint a();\nint b();\nint main() { return PICK(); }\n",
		"src/pick.inc": "#define PICK a\n",
	})
	out := tr.run(tr.input(false), Config{})
	if out.Units != 2 || out.Cached != 0 || out.Failed != 0 || out.Method != MethodSynth {
		t.Fatalf("first run: %+v", out)
	}
	if got := callee(out, "src/main.cpp:4", "a"); got != "src/lib.cpp:1" {
		t.Errorf("main calls a, defined at src/lib.cpp:1: got %q in %+v", got, out.Edges)
	}
	if out = tr.run(tr.input(true), Config{}); out.Cached != 2 || callee(out, "src/main.cpp:4", "a") != "src/lib.cpp:1" {
		t.Errorf("unchanged: every unit from the store, same edges: %+v", out)
	}
	// a file the walk does not hold (an odd extension) is still part of the
	// unit's identity
	tr.write("src/pick.inc", "#define PICK b\n")
	if out = tr.run(tr.input(true), Config{}); out.Cached != 1 || callee(out, "src/main.cpp:4", "a") != "" {
		t.Errorf("an edited include outside the walked files invalidates the unit: %+v", out)
	}
	if out = tr.run(tr.input(false), Config{}); callee(out, "src/main.cpp:4", "b") != "src/lib.cpp:2" {
		t.Errorf("recompiled against the new include: %+v", out.Edges)
	}
}

// A header added where an include would now find it first invalidates the
// units that include that name; one added elsewhere does not.
func TestRunShadowingHeader(t *testing.T) {
	tr := newTree(t, map[string]string{
		"common/include/util.h": "int common_f();\n#define UTIL common_f\n",
		"common/src/util.cpp":   "int common_f() { return 1; }\n",
		"app/src/local.cpp":     "int local_f() { return 2; }\n",
		"app/src/other.h":       "int other();\n",
		"app/src/main.cpp":      "#include \"util.h\"\n#include \"other.h\"\nint local_f();\nint main() { return UTIL(); }\n",
	})
	out := tr.run(tr.input(false), Config{})
	if got := callee(out, "app/src/main.cpp:4", "common_f"); got != "common/src/util.cpp:1" {
		t.Fatalf("UTIL is common_f: %q %+v", got, out.Edges)
	}
	tr.write("tools/include/tools/unrelated.h", "int unrelated();\n")
	if out = tr.run(tr.input(true), Config{}); out.Cached != out.Units {
		t.Errorf("a header in an unrelated directory keeps every unit cached: %+v", out)
	}
	tr.write("app/src/util.h", "int local_f();\n#define UTIL local_f\n")
	if out = tr.run(tr.input(true), Config{}); callee(out, "app/src/main.cpp:4", "common_f") != "" {
		t.Errorf("a util.h next to the includer shadows the old one: the cached result is stale: %+v", out)
	}
	if out = tr.run(tr.input(false), Config{}); callee(out, "app/src/main.cpp:4", "local_f") != "app/src/local.cpp:1" {
		t.Errorf("recompiled, UTIL is local_f: %+v", out.Edges)
	}
}

// Two programs each define helper(): with one defining unit not read, the
// other program's definition is never taken for the call.
func TestRunAbsentUnitKeepsNameUnbound(t *testing.T) {
	tr := newTree(t, map[string]string{
		"tool_a/helper.cpp": "int helper() { return 1; }\n",
		"tool_a/main.cpp":   "int helper();\nint main() { return helper(); }\n",
		"tool_b/helper.cpp": "int helper() { return 2; }\n",
		"tool_b/main.cpp":   "int helper();\nint main() { return helper(); }\n",
	})
	out := tr.run(tr.input(false), Config{})
	if callee(out, "tool_b/main.cpp:2", "helper") != "unbound" {
		t.Fatalf("two definitions, declared apart: not proven: %+v", out.Edges)
	}
	tr.write("tool_b/helper.cpp", "int helper() { return 3; }\n")
	out = tr.run(tr.input(true), Config{})
	if out.Cached != 3 {
		t.Fatalf("one unit edited: %+v", out)
	}
	if got := callee(out, "tool_b/main.cpp:2", "helper"); got != "unbound" {
		t.Errorf("tool_b's helper is in a unit not read: the call is not proven to tool_a's: %q", got)
	}
}

// Units the build's database lists compile with its flags; one it does not
// list is compiled with a synthesized context, and labeled so.
func TestRunCompdbAndSynthUnits(t *testing.T) {
	gen := t.TempDir()
	if err := os.MkdirAll(filepath.Join(gen, "cfg"), 0o755); err != nil {
		t.Fatal(err)
	}
	choice := filepath.Join(gen, "cfg", "choice.h")
	if err := os.WriteFile(choice, []byte("#define RUN run_fast\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tr := newTree(t, map[string]string{
		"src/impl.cpp": "int run_fast() { return 1; }\nint run_safe() { return 2; }\n",
		"src/app.cpp":  "#include \"cfg/choice.h\"\nint run_fast();\nint run_safe();\n#ifdef USE_X\nint app() { return RUN(); }\n#endif\n",
		"src/tool.cpp": "int run_safe();\nint tool() { return run_safe(); }\n",
	})
	tr.write("build/compile_commands.json", `[
 {"directory": "`+tr.root+`/build", "file": "`+tr.root+`/src/app.cpp",
  "command": "/usr/bin/c++ -D USE_X -DNAME=\\\"app\\\" -I`+gen+` -o app.o -c `+tr.root+`/src/app.cpp"},
 {"directory": "`+tr.root+`/build", "file": "`+tr.root+`/src/impl.cpp", "arguments": ["c++", "-c", "../src/impl.cpp"]}
]`)
	out := tr.run(tr.input(false), Config{})
	if out.Method != MethodCompdb || out.Synth != 1 {
		t.Fatalf("two units from the database, one synthesized: %+v", out)
	}
	methods := map[string]string{}
	for _, e := range out.Edges {
		methods[e.CallerPath] = e.Method
	}
	if got := callee(out, "src/app.cpp:5", "run_fast"); got != "src/impl.cpp:1" || methods["src/app.cpp"] != MethodCompdb {
		t.Errorf("app is compiled with -D USE_X and the generated header: %q %+v", got, out.Edges)
	}
	if methods["src/tool.cpp"] != MethodSynth {
		t.Errorf("tool.cpp is not in the database: its edges are the synthesized context's: %+v", out.Edges)
	}
	// a generated header outside the repo changes: the unit is not reused
	if err := os.WriteFile(choice, []byte("#define RUN run_safe\n/* regenerated */\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out = tr.run(tr.input(true), Config{}); callee(out, "src/app.cpp:5", "run_fast") != "" {
		t.Errorf("a changed header outside the repo invalidates the unit: %+v", out)
	}
}

// A virtual method called through an object may run an override; one
// called by its qualified name is that method.
func TestRunVirtualCalls(t *testing.T) {
	tr := newTree(t, map[string]string{
		"src/shape.cpp": `struct Shape { virtual int area() const { return 0; } };
struct Square : Shape { int area() const override { return Shape::area() + 4; } };
Shape* make();
int total() { return make()->area(); }
`,
	})
	out := tr.run(tr.input(false), Config{})
	kinds := map[int]bool{}
	for _, e := range out.Edges {
		if e.Name == "area" && !e.Unbound {
			kinds[e.CallerLine] = e.Virtual
		}
	}
	if v, ok := kinds[4]; !ok || !v {
		t.Errorf("make()->area() dispatches virtually: %+v", out.Edges)
	}
	if v, ok := kinds[2]; !ok || v {
		t.Errorf("Shape::area() is that method itself: %+v", out.Edges)
	}
}

func TestSplitCommand(t *testing.T) {
	for cmd, want := range map[string][]string{
		`c++ -DROS_PACKAGE_NAME=\"pkg\" -I/a -c x.cpp`: {"c++", `-DROS_PACKAGE_NAME="pkg"`, "-I/a", "-c", "x.cpp"},
		`c++ "-DX=a b" -D 'Y=1 2' x.cpp`:               {"c++", "-DX=a b", "-D", "Y=1 2", "x.cpp"},
		`cc -DV="\"1.0\"" a\ b.c`:                      {"cc", `-DV="1.0"`, "a b.c"},
	} {
		if got := splitCommand(cmd); !reflect.DeepEqual(got, want) {
			t.Errorf("splitCommand(%s) = %q, want %q", cmd, got, want)
		}
	}
	got := rebaseArgs([]string{"c++", "-D", "FOO=1", "-I/src/repo/inc", "-iquote/src/repo/q", "-idirafter", "/x", "-fopenmp",
		"-fdiagnostics-color", "-Wall", "-o", "a.o", "-c", "/src/repo/a.cpp"}, "/build", "/src/repo", rootMark, nil)
	want := []string{"-D", "FOO=1", "-I" + rootMark + "/inc", "-iquote" + rootMark + "/q", "-idirafter", "/x", "-fopenmp", "-w", "-ferror-limit=0"}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("rebaseArgs = %q, want %q first", got, want)
	}
}

// A workspace's database lists other packages' src/main.cpp: the repo's
// units take the commands of the source root most of them are under.
func TestCompdbWorkspace(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "compile_commands.json")
	if err := os.WriteFile(db, []byte(`[
 {"directory": "/ws/build/a", "file": "/ws/src/pkg_a/src/main.cpp", "arguments": ["c++", "-DA", "-c", "/ws/src/pkg_a/src/main.cpp"]},
 {"directory": "/ws/build/a", "file": "/ws/src/pkg_a/src/util.cpp", "arguments": ["c++", "-DA", "-c", "/ws/src/pkg_a/src/util.cpp"]},
 {"directory": "/ws/build/b", "file": "/ws/src/pkg_b/src/main.cpp", "arguments": ["c++", "-DB", "-c", "/ws/src/pkg_b/src/main.cpp"]}
]`), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Files: map[string]string{"src/main.cpp": "1", "src/util.cpp": "2"}}
	by := compdbArgs(in, Config{Compdb: db, Args: []string{"-DEXTRA"}}, repoPaths(in), nil)
	if a := strings.Join(by["src/main.cpp"], " "); !strings.Contains(a, "-DA") || strings.Contains(a, "-DB") || !strings.Contains(a, "-DEXTRA") {
		t.Errorf("src/main.cpp takes pkg_a's command and --cpp-arg: %s", a)
	}
}

func TestDefinedNames(t *testing.T) {
	got := map[string]bool{}
	definedNames(`int helper() { return 1; }
Foo::Foo(int x) : x_(x) {}
int Foo::get() const noexcept { return x_; }
int call() { return helper() + other(2); }
int decl(int);
`, got)
	for _, n := range []string{"helper", "Foo::Foo", "Foo::get", "call"} {
		if !got[n] {
			t.Errorf("%s is defined: %v", n, got)
		}
	}
	for _, n := range []string{"other", "decl", "get"} {
		if got[n] {
			t.Errorf("%s is only called or declared: %v", n, got)
		}
	}
}

// At a git ref a submodule's directory in the copy is empty: a build's -I
// into it reads the submodule's files where they were put.
func TestCompdbIncludeIntoSubmodule(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "compile_commands.json")
	if err := os.WriteFile(db, []byte(`[{"directory": "/src/parent/build", "file": "/src/parent/main.cpp",
  "arguments": ["c++", "-I/src/parent/third_party/sub/include", "-I/src/parent/inc", "-c", "../main.cpp"]}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	cc := newCompileCtx(Input{
		Files: map[string]string{"main.cpp": "1", "inc/a.h": "2", "third_party/README.h": "3"},
		Libs:  map[string]string{"third_party/sub/include/sl/sl.h": "4"},
	}, Config{Compdb: db})
	got := strings.Join(cc.full("main.cpp"), " ")
	if !strings.Contains(got, "-I "+libMark+"/third_party/sub/include") || !strings.Contains(got, "-I "+rootMark+"/inc") {
		t.Errorf("the submodule's include directory is read at libMark, the repo's at rootMark: %q", got)
	}
	if key := strings.Join(cc.key("main.cpp"), " "); !strings.Contains(key, srcMark+"/third_party/sub/include") {
		t.Errorf("the key names the source directory, not where it was copied: %q", key)
	}
}

// The review's compile-context cases: a build directory inside the repo, the
// repo root as an include directory above a submodule, flags taking a value,
// CMake's clang precompiled header, ties between source roots, --cpp-arg after
// the repo's directories.
func TestCompdbForms(t *testing.T) {
	repo := map[string]bool{"src": true, "src/main.cpp": true, "third_party": true, "third_party/README.h": true}
	got := strings.Join(rebaseArgs([]string{"c++", "-I/r/build", "-I/r", "-mllvm", "-inline-threshold=5", "-DFOO=1",
		"-include-pch", "/r/build/pch.pch", "-Xclang", "-include", "-Xclang", "/r/build/cmake_pch.hxx", "-c", "/r/src/main.cpp"},
		"/r/build", "/r", rootMark, repo), " ")
	for _, want := range []string{"-I" + srcMark + "/build", "-I" + rootMark + " ", "-DFOO=1", "-include " + srcMark + "/build/cmake_pch.hxx"} {
		if !strings.Contains(got, want) {
			t.Errorf("rebaseArgs: want %q in %q", want, got)
		}
	}
	if strings.Contains(got, "-mllvm") || strings.Contains(got, "pch.pch") {
		t.Errorf("-mllvm and -include-pch go with their values: %q", got)
	}
	cc := &compileCtx{compdb: map[string][]string{"src/main.cpp": {"-I" + srcMark + "/build", "-I" + rootMark}},
		srcRoot: map[string]string{"src/main.cpp": "/r"}, libPaths: map[string]bool{"third_party/json": true},
		libAnc: map[string]bool{"": true, "third_party": true}}
	full := strings.Join(cc.full("src/main.cpp"), " ")
	if !strings.Contains(full, "-I /r/build") || !strings.Contains(full, "-I "+rootMark+" -I "+libMark) {
		t.Errorf("the build directory is read where the build left it, the root also at the submodules' copy: %q", full)
	}

	dir := t.TempDir()
	db := filepath.Join(dir, "compile_commands.json")
	if err := os.WriteFile(db, []byte(`[
 {"directory": "/ws/b", "file": "/ws/src/aaa/src/main.cpp", "arguments": ["c++", "-DWRONG", "-c", "x"]},
 {"directory": "/ws/b", "file": "/ws/src/zzz/src/main.cpp", "arguments": ["c++", "-DRIGHT", "-c", "x"]},
 {"directory": "/ws/b", "file": "/ws/src/zzz/src/main.cpp", "arguments": ["c++", "-DRIGHT", "-c", "x"]}
]`), 0o644); err != nil {
		t.Fatal(err)
	}
	in := Input{Files: map[string]string{"src/main.cpp": "1"}}
	if by := compdbArgs(in, Config{Compdb: db}, repoPaths(in), nil); by != nil {
		t.Errorf("two source roots holding as many of the repo's files: neither is trusted: %q", by)
	}
}

func TestSynthCppArgLast(t *testing.T) {
	cc := &compileCtx{}
	synthArgs(Input{Files: map[string]string{"include/pkg/conf.h": "1", "src/a.cpp": "2"}}, Config{Args: []string{"-I/opt/pkg"}}, cc)
	full := strings.Join(cc.full("src/a.cpp"), " ")
	if strings.Index(full, "-I/opt/pkg") < strings.Index(full, rootMark+"/include") {
		t.Errorf("--cpp-arg comes after the repo's include directories: %q", full)
	}
}

// writeOut writes a file outside the repo.
func writeOut(t *testing.T, p, src string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A header missing at compile time that appears outside the repo (the
// messages a later build generates), or one added to an earlier include
// directory, recompiles the unit.
func TestRunOutsideHeaders(t *testing.T) {
	ext := t.TempDir()
	tr := newTree(t, map[string]string{
		"src/lib.cpp":  "int aa() { return 1; }\nint bb() { return 2; }\nint gen_fn() { return 3; }\n",
		"src/main.cpp": "#include <cfg/choice.h>\n#include <gen/msg.h>\nint aa();\nint bb();\nint main() { return PICK() + MSG(); }\n",
	})
	writeOut(t, filepath.Join(ext, "ros/include/cfg/choice.h"), "#define PICK aa\n")
	if err := os.MkdirAll(filepath.Join(ext, "overlay/include"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{Headers: []string{filepath.Join(ext, "overlay"), filepath.Join(ext, "ros")}}
	out := tr.run(tr.input(false), cfg)
	if callee(out, "src/main.cpp:5", "aa") != "src/lib.cpp:1" {
		t.Fatalf("PICK is aa: %+v", out.Edges)
	}
	writeOut(t, filepath.Join(ext, "ros/include/gen/msg.h"), "int gen_fn();\n#define MSG gen_fn\n")
	if out = tr.run(tr.input(true), cfg); out.Cached != 1 {
		t.Errorf("a missing header that appears outside the repo invalidates the unit: %+v", out)
	}
	if out = tr.run(tr.input(false), cfg); callee(out, "src/main.cpp:5", "gen_fn") == "" || callee(out, "src/main.cpp:5", "gen_fn") == "unbound" {
		t.Errorf("recompiled with the generated header: MSG() is gen_fn: %+v", out.Edges)
	}
	writeOut(t, filepath.Join(ext, "overlay/include/cfg/choice.h"), "#define PICK bb\n")
	if out = tr.run(tr.input(true), cfg); out.Cached != 1 {
		t.Errorf("a header added to an earlier include directory invalidates the unit: %+v", out)
	}
	if out = tr.run(tr.input(false), cfg); callee(out, "src/main.cpp:5", "bb") != "src/lib.cpp:2" {
		t.Errorf("recompiled against the overlay: %+v", out.Edges)
	}
	// rewritten with the same size and modification time (a copy that keeps them)
	p := filepath.Join(ext, "overlay/include/cfg/choice.h")
	fi, _ := os.Stat(p)
	writeOut(t, p, "#define PICK aa\n")
	_ = os.Chtimes(p, fi.ModTime(), fi.ModTime())
	if out = tr.run(tr.input(true), cfg); out.Cached != 1 {
		t.Errorf("same size and time, new content: the unit is not reused: %+v", out)
	}
}

// __has_include of a header that later appears; a symlinked header pointed
// elsewhere; two refs differing in one header both stay cached.
func TestRunHasIncludeSymlinkAndRefs(t *testing.T) {
	tr := newTree(t, map[string]string{
		"src/lib.cpp":   "int fast() { return 1; }\nint slow() { return 2; }\nint one() { return 3; }\nint two() { return 4; }\n",
		"src/util.h":    "int fast();\nint slow();\nint one();\nint two();\n",
		"src/impl/v1.h": "#define API one\n",
		"src/impl/v2.h": "#define API two\n",
		"src/main.cpp":  "#include \"util.h\"\n#include \"api.h\"\n#if __has_include(\"feature.h\")\n#define IMPL fast\n#else\n#define IMPL slow\n#endif\nint main() { return IMPL() + API(); }\n",
	})
	if err := os.Symlink("impl/v1.h", filepath.Join(tr.root, "src/api.h")); err != nil {
		t.Fatal(err)
	}
	out := tr.run(tr.input(false), Config{})
	if callee(out, "src/main.cpp:8", "slow") != "src/lib.cpp:2" || callee(out, "src/main.cpp:8", "one") != "src/lib.cpp:3" {
		t.Fatalf("slow and one: %+v", out.Edges)
	}
	tr.write("src/feature.h", "// present\n")
	if out = tr.run(tr.input(true), Config{}); out.Cached != 1 {
		t.Errorf("a header a __has_include names appears: the unit is not reused: %+v", out)
	}
	tr.run(tr.input(false), Config{})
	_ = os.Remove(filepath.Join(tr.root, "src/api.h"))
	if err := os.Symlink("impl/v2.h", filepath.Join(tr.root, "src/api.h")); err != nil {
		t.Fatal(err)
	}
	if out = tr.run(tr.input(true), Config{}); out.Cached != 1 {
		t.Errorf("a symlinked header pointed elsewhere: the unit is not reused: %+v", out)
	}
	if out = tr.run(tr.input(false), Config{}); callee(out, "src/main.cpp:8", "two") != "src/lib.cpp:4" {
		t.Errorf("recompiled through the new link: %+v", out.Edges)
	}
	// back to v1: its result, compiled before, is still there
	_ = os.Remove(filepath.Join(tr.root, "src/api.h"))
	if err := os.Symlink("impl/v1.h", filepath.Join(tr.root, "src/api.h")); err != nil {
		t.Fatal(err)
	}
	if out = tr.run(tr.input(true), Config{}); out.Cached != 2 || callee(out, "src/main.cpp:8", "one") != "src/lib.cpp:3" {
		t.Errorf("a ref compiled before keeps its entry beside the newer one: %+v", out)
	}
}

// A unit not read this run that defines the name through a macro still
// keeps another program's definition from being taken; one that defines a
// method of another class does not refuse this class's.
func TestRunAbsentUnitMacroAndOtherClass(t *testing.T) {
	tr := newTree(t, map[string]string{
		"tool_a/helper.cpp": "int helper() { return 1; }\n",
		"tool_a/main.cpp":   "int helper();\nint main() { return helper(); }\n",
		"tool_b/helper.cpp": "#define DEF(n) int n() { return 2; }\nDEF(helper)\n",
		"tool_b/main.cpp":   "int helper();\nint main() { return helper(); }\n",
		"cls/foo.cpp":       "struct Foo { int init(); };\nint Foo::init() { return 1; }\n",
		"cls/bar.cpp":       "struct Bar { int init(); };\nint Bar::init() { return 2; }\n",
		"cls/use.cpp":       "struct Bar { int init(); };\nint use(Bar& b) { return b.init(); }\n",
	})
	tr.run(tr.input(false), Config{})
	tr.write("tool_b/helper.cpp", "#define DEF(n) int n() { return 3; }\nDEF(helper)\n")
	tr.write("cls/foo.cpp", "struct Foo { int init(); };\nint Foo::init() { return 9; }\n")
	out := tr.run(tr.input(true), Config{})
	if got := callee(out, "tool_b/main.cpp:2", "helper"); got != "unbound" {
		t.Errorf("tool_b's helper (a macro's) is in a unit not read: not tool_a's: %q", got)
	}
	if got := callee(out, "cls/use.cpp:2", "init"); got != "cls/bar.cpp:2" {
		t.Errorf("Foo::init's unit left out does not refuse Bar::init: %q", got)
	}
}

// Calls of a virtual method that cannot dispatch: on an object held by
// value, on this in a constructor.
func TestRunVirtualByValue(t *testing.T) {
	tr := newTree(t, map[string]string{
		"src/s.cpp": `struct Shape { virtual int area() const { return 0; } virtual void init() {} Shape() { init(); } };
int byValue(Shape s) { return s.area(); }
int byRef(Shape& s) { return s.area(); }
`,
	})
	out := tr.run(tr.input(false), Config{})
	virt := map[int]bool{}
	for _, e := range out.Edges {
		if !e.Unbound {
			virt[e.CallerLine] = virt[e.CallerLine] || e.Virtual
		}
	}
	if virt[2] {
		t.Errorf("s.area() on a Shape held by value runs Shape::area: %+v", out.Edges)
	}
	if !virt[3] {
		t.Errorf("s.area() through a reference may dispatch: %+v", out.Edges)
	}
	if virt[1] {
		t.Errorf("init() in the constructor runs Shape::init: %+v", out.Edges)
	}
}

func TestUSRClass(t *testing.T) {
	for usr, want := range map[string]string{"c:@N@nav@S@Map@F@floor#": "Map", "c:@F@helper#": "", "c:@S@Box>#T@FT@>1#Tget#": "Box"} {
		if got := usrClass(usr); got != want {
			t.Errorf("usrClass(%s) = %q, want %q", usr, got, want)
		}
	}
}

// A header found through a directory that stops being an include directory,
// and same-named headers whose order a package's dependencies change, each
// invalidate the unit.
func TestRunIncludeRootsAndOrder(t *testing.T) {
	tr := newTree(t, map[string]string{
		"a/x.h":               "int x();\n",
		"a/pkg/util.h":        "#define U one\n",
		"src/lib.cpp":         "int one() { return 1; }\nint fa() { return 2; }\nint fb() { return 3; }\n",
		"src/main.cpp":        "#include \"pkg/util.h\"\nint one();\nint main() { return U(); }\n",
		"pkg_a/package.xml":   "<package><name>pkg_a</name></package>\n",
		"pkg_a/include/cfg.h": "#define C fa\n",
		"pkg_b/package.xml":   "<package><name>pkg_b</name></package>\n",
		"pkg_b/include/cfg.h": "#define C fb\n",
		"pkg_c/package.xml":   "<package><name>pkg_c</name><depend>pkg_a</depend></package>\n",
		"pkg_c/src/use.cpp":   "#include \"cfg.h\"\nint fa();\nint fb();\nint use() { return C(); }\n",
	})
	out := tr.run(tr.input(false), Config{})
	if callee(out, "src/main.cpp:3", "one") != "src/lib.cpp:1" || callee(out, "pkg_c/src/use.cpp:4", "fa") != "src/lib.cpp:2" {
		t.Fatalf("U is one, C is fa (pkg_a first): %+v", out.Edges)
	}
	if err := os.Remove(filepath.Join(tr.root, "a/x.h")); err != nil {
		t.Fatal(err)
	}
	tr.write("pkg_c/package.xml", "<package><name>pkg_c</name><depend>pkg_b</depend></package>\n")
	out = tr.run(tr.input(true), Config{})
	if callee(out, "src/main.cpp:3", "one") != "" {
		t.Errorf("a/ is no include directory now: pkg/util.h is not found, the unit is not reused: %+v", out)
	}
	if callee(out, "pkg_c/src/use.cpp:4", "fa") != "" {
		t.Errorf("pkg_b's cfg.h comes first now: the unit is not reused: %+v", out)
	}
	if out = tr.run(tr.input(false), Config{}); callee(out, "pkg_c/src/use.cpp:4", "fb") != "src/lib.cpp:3" {
		t.Errorf("recompiled, C is fb: %+v", out.Edges)
	}
}

// A virtual call names the class of the object it is made on.
func TestRunVirtualReceiver(t *testing.T) {
	tr := newTree(t, map[string]string{
		"src/s.cpp": `struct Base { virtual int f() { return 0; } };
struct A : Base { int f() override { return 1; } };
struct B : Base { };
int use(B* b) { return b->f(); }
`,
	})
	out := tr.run(tr.input(false), Config{})
	for _, e := range out.Edges {
		if e.CallerLine == 4 && e.Name == "f" {
			if !e.Virtual || e.RecvPath != "src/s.cpp" || e.RecvLine != 3 {
				t.Errorf("b->f() is virtual on a B (line 3): %+v", e)
			}
			return
		}
	}
	t.Errorf("no edge for b->f(): %+v", out.Edges)
}

// A directory holding its own limits.h is reached by quoted includes beside
// it: on the include path it would hide the system's.
func TestIncludeRootsSkipLibcNames(t *testing.T) {
	set := map[string]bool{}
	includeRoots(map[string]string{"gfx/internal/limits.h": "1", "gfx/internal/draw.h": "2", "gfx/include/gfx.h": "3"}, set)
	if set["gfx/internal"] || !set["gfx/include"] {
		t.Errorf("include roots: %v", set)
	}
}
