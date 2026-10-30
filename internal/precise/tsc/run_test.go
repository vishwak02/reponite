package tsc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/precise"
)

func TestProjects(t *testing.T) {
	files := map[string]string{
		"app/tsconfig.json": "", "app/src/a.ts": "", "app/src/b.tsx": "", "app/web/package.json": "", "app/web/c.js": "",
		"web/package.json": "", "web/app.js": "", "web/lib/x.mjs": "",
		"pkgs/ui/tsconfig.json": "", "pkgs/ui/src/c.ts": "", "pkgs/ui/types.d.ts": "",
		"types/only.d.ts": "",
	}
	var got []string
	for _, p := range projects(files) {
		got = append(got, fmt.Sprintf("%s|%s|%s", orRoot(p.dir), p.config, strings.Join(p.files, ",")))
	}
	want := []string{
		// a package.json under a tsconfig's directory is part of its project
		"app|app/tsconfig.json|app/src/a.ts,app/src/b.tsx,app/web/c.js",
		"pkgs/ui|pkgs/ui/tsconfig.json|pkgs/ui/src/c.ts,pkgs/ui/types.d.ts",
		"web||web/app.js,web/lib/x.mjs",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("projects:\n got %q\nwant %q", got, want)
	}
	// a directory of declarations alone is no project
	if prs := projects(map[string]string{"package.json": "", "index.d.ts": ""}); len(prs) != 0 {
		t.Errorf("declarations only: %v", prs)
	}
}

func TestRawCallDecode(t *testing.T) {
	var cs []rawCall
	if err := json.Unmarshal([]byte(`[["a.ts",3,"f","B","b.ts",7,"f",0],["a.ts",4,"C","B","b.ts",9,"C",1],["a.ts",5,"g","E"],["a.ts",6,"h","U"],["a.ts",7]]`), &cs); err != nil {
		t.Fatal(err)
	}
	var got []call
	for _, c := range cs {
		if x, ok := c.decode(); ok {
			got = append(got, x)
		}
	}
	want := []call{
		{File: "a.ts", Line: 3, Name: "f", Kind: 'B', CalleePath: "b.ts", CalleeLine: 7, CalleeName: "f"},
		{File: "a.ts", Line: 4, Name: "C", Kind: 'B', CalleePath: "b.ts", CalleeLine: 9, CalleeName: "C", Ctor: true},
		{File: "a.ts", Line: 5, Name: "g", Kind: 'E'},
		{File: "a.ts", Line: 6, Name: "h", Kind: 'U'},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("decode:\n got %+v\nwant %+v", got, want)
	}
}

// The fake node: the test binary run with TSC_FAKE set reads requests as
// the worker does and answers each source file with a call of helper bound
// to app/src/util.ts, reporting every project file as read.
func TestMain(m *testing.M) {
	if os.Getenv("TSC_FAKE") != "" {
		fakeWorker()
		return
	}
	os.Exit(m.Run())
}

func fakeWorker() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<26)
	for sc.Scan() {
		var req request
		if json.Unmarshal(sc.Bytes(), &req) != nil {
			os.Exit(2)
		}
		rep := map[string]any{"version": "9.9.9", "probes": map[string]bool{"f:R:app/src/missing.ts": false}, "lists": map[string]string{}}
		var calls []any
		reads := []string{}
		if req.Config != "" {
			reads = append(reads, "R:"+req.Config)
		}
		for _, f := range req.Files {
			reads = append(reads, "R:"+f)
			if f != "app/src/util.ts" {
				calls = append(calls, []any{f, 2, "helper", "B", "app/src/util.ts", 1, "helper", 0}, []any{f, 3, "log", "E"})
			}
		}
		rep["calls"], rep["reads"] = calls, reads
		b, _ := json.Marshal(rep)
		fmt.Println(string(b))
	}
}

func TestRunWithFakeWorker(t *testing.T) {
	ts := filepath.Join(t.TempDir(), "typescript")
	if err := os.MkdirAll(filepath.Join(ts, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(ts, "lib", "typescript.js"), []byte("//"), 0o644)
	_ = os.WriteFile(filepath.Join(ts, "package.json"), []byte(`{"version":"9.9.9"}`), 0o644)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	in := Input{
		Files:       map[string]string{"app/tsconfig.json": "c1", "app/src/app.ts": "a1", "app/src/util.ts": "u1", "web/package.json": "p1", "web/main.js": "m1"},
		Materialize: func() (string, func(), error) { return t.TempDir(), func() {}, nil },
	}
	store := precise.Open(t.TempDir())
	t.Setenv("TSC_FAKE", "1")
	cfg := Config{Node: exe, TypeScript: ts, Jobs: 2, Store: store}
	out, err := Run(context.Background(), in, cfg)
	if err != nil {
		t.Fatal(err)
	}
	lines := func(es []Edge) []string {
		var s []string
		for _, e := range es {
			s = append(s, fmt.Sprintf("%s:%d %s %s %v %v %s:%d", e.CallerPath, e.CallerLine, e.Name, e.Method, e.External, e.Unbound, e.CalleePath, e.CalleeLine))
		}
		sort.Strings(s)
		return s
	}
	want := []string{
		"app/src/app.ts:2 helper tsc false false app/src/util.ts:1",
		"app/src/app.ts:3 log tsc true false :0",
		"web/main.js:2 helper tsc-js false false app/src/util.ts:1",
		"web/main.js:3 log tsc-js true false :0",
	}
	if got := lines(out.Edges); !reflect.DeepEqual(got, want) || out.Projects != 2 || out.Cached != 0 || out.Version != "9.9.9" {
		t.Fatalf("first run: %+v\n%q", out, got)
	}
	// cached mode reads the store, never starting node
	t.Setenv("TSC_FAKE", "")
	cin := in
	cin.CachedOnly = true
	again, err := Run(context.Background(), cin, Config{Node: "/nonexistent", Store: store})
	if err != nil || again.Cached != 2 || !reflect.DeepEqual(lines(again.Edges), want) {
		t.Fatalf("cached: %+v %v", again, err)
	}
	// a file the project read changed: that project misses, the other holds
	cin.Files = map[string]string{}
	for k, v := range in.Files {
		cin.Files[k] = v
	}
	cin.Files["app/src/util.ts"] = "u2"
	part, _ := Run(context.Background(), cin, Config{Store: store})
	if part.Cached != 1 || len(part.Edges) != 2 {
		t.Errorf("one project changed: %+v", part)
	}
	// a file a project looked for and did not find appears: web's files are
	// the same, but it looked for that one too (the fake reports it for
	// every project), so it misses as well
	cin.Files["app/src/util.ts"] = "u1"
	cin.Files["app/src/missing.ts"] = "x"
	if m, _ := Run(context.Background(), cin, Config{Store: store}); m.Cached != 0 {
		t.Errorf("a probed file appeared: %+v", m)
	}
	// with nothing ever built, the default mode does nothing
	if none, _ := Run(context.Background(), cin, Config{Store: precise.Open(t.TempDir())}); none.Cached != 0 || len(none.Edges) != 0 {
		t.Errorf("empty store: %+v", none)
	}
}

// TestRealTSC runs the real worker ($REPONITE_NODE or node on $PATH, with
// $REPONITE_TYPESCRIPT; skipped without them).
func TestRealTSC(t *testing.T) {
	node, err := FindNode(os.Getenv("REPONITE_NODE"))
	if err != nil {
		t.Skip(err)
	}
	ts := os.Getenv("REPONITE_TYPESCRIPT")
	if TypeScriptVersion(ts) == "" {
		t.Skip("no typescript ($REPONITE_TYPESCRIPT)")
	}
	dir := t.TempDir()
	files := map[string]string{
		"tsconfig.json": `{"compilerOptions": {"jsx": "react-jsx", "strict": true}}`,
		"src/util.ts": `export function helper(n: number): number { return n; }
export class Order {
  total(): number { return helper(1); }
}
export const twice = (n: number) => helper(n) * 2;
`,
		"src/app.tsx": `import { helper as h, Order, twice } from "./util";
export function run(o: Order, cb: () => void, x: any) {
  h(1);
  new Order().total();
  twice(2);
  cb();
  x.anything();
  return [1].map((v) => v + 1);
}
`,
	}
	in := Input{Files: map[string]string{}, Dir: dir}
	for rel, src := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
		in.Files[rel] = precise.BlobID([]byte(src))
	}
	in.Materialize = func() (string, func(), error) { return dir, func() {}, nil }
	out, err := Run(context.Background(), in, Config{Node: node, TypeScript: ts, Store: precise.Open(t.TempDir())})
	if err != nil || out.Failed > 0 {
		t.Fatalf("%v %+v", err, out)
	}
	var got []string
	for _, e := range out.Edges {
		s := fmt.Sprintf("%s:%d %s -> ", e.CallerPath, e.CallerLine, e.Name)
		switch {
		case e.Unbound:
			s += "?"
		case e.External:
			s += "ext"
		default:
			s += fmt.Sprintf("%s:%d %s", e.CalleePath, e.CalleeLine, e.CalleeName)
			if e.Ctor {
				s += " (class)"
			}
		}
		got = append(got, s)
	}
	sort.Strings(got)
	all := strings.Join(got, "\n") + "\n"
	t.Logf("%s\n%s", out.Version, all)
	for _, w := range []string{
		"src/app.tsx:3 h -> src/util.ts:1 helper",
		"src/app.tsx:4 Order -> src/util.ts:2 Order (class)",
		"src/app.tsx:4 total -> src/util.ts:3 total",
		"src/app.tsx:5 twice -> src/util.ts:5 twice",
		"src/app.tsx:6 cb -> ?",
		"src/app.tsx:7 anything -> ?",
		"src/app.tsx:8 map -> ext",
		"src/util.ts:3 helper -> src/util.ts:1 helper",
	} {
		if !strings.Contains(all, w+"\n") {
			t.Errorf("missing %q", w)
		}
	}
}
