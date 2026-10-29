package pyright

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/precise"
)

func TestScanCalls(t *testing.T) {
	src := `import os


@app.route("/x", methods=list())
def handler(a=default(), *, b: int = width()) -> Result[int]:
    """doc: not_a_call()"""
    # comment(1)
    v = os.path.join(a, b) + helper (1)
    s = f"{fmt(v)!r:>{pad()}} {{literal()}}"
    if (cond(v)):
        return Klass(v).run()
    match v:
        case Point(x=0):
            pass
    lambda: inner()
    return obj[0]() or call()()


class Child(make_base()):
    x = field(default=1)
`
	var got []string
	for _, s := range scanCalls(src) {
		got = append(got, fmt.Sprintf("%d:%d %s", s.Line+1, s.Col, s.Name))
	}
	want := []string{
		"8:16 join", "8:29 helper", "9:11 fmt", "9:22 pad", "10:8 cond", "11:15 Klass", "11:24 run",
		"15:12 inner", "16:23 call", "20:8 field",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("sites:\n got %v\nwant %v", got, want)
	}
	// columns count UTF-16 units, as the server does
	if s := scanCalls("x = \"é😀\"; f()\n"); len(s) != 1 || s[0].Col != 11 {
		t.Errorf("utf-16 column: %v", s)
	}
	// a triple-quoted string spanning lines keeps later lines' numbers
	if s := scanCalls("x = '''a\nb'''\ng()\n"); len(s) != 1 || s[0].Line != 2 {
		t.Errorf("after a multi-line string: %v", s)
	}
}

func TestDefAt(t *testing.T) {
	src := "class Order:\n    async def total(self):\n        run = self.start\n        for step in steps:\n            pass\nx.y = 1\n"
	for _, c := range []struct {
		line, col  int
		name, kind string
	}{
		{0, 6, "Order", "class"}, {1, 14, "total", "def"}, {2, 8, "run", "var"}, {3, 12, "step", "var"}, {5, 2, "y", ""}, {1, 3, "", ""},
	} {
		if n, k := defAt(src, c.line, c.col); n != c.name || k != c.kind {
			t.Errorf("defAt(%d:%d) = %q %q, want %q %q", c.line, c.col, n, k, c.name, c.kind)
		}
	}
	if l, c, ok := aliasOf(src, 2, 8); !ok || l != 2 || c != 19 {
		t.Errorf("aliasOf run = self.start: %d:%d %v", l, c, ok)
	}
	for _, s := range []string{"f = g()\n", "f = None\n", "f = a[0]\n", "a, f = g, h\n"} {
		col := strings.Index(s, "f")
		if _, _, ok := aliasOf(s, 0, col); ok {
			t.Errorf("aliasOf %q: not an alias", s)
		}
	}
}

func TestSourceRoots(t *testing.T) {
	files := map[string]string{
		"setup.py": "", "app/__init__.py": "", "app/sub/__init__.py": "",
		"pkgs/nav/src/nav/__init__.py": "", "pkgs/nav/scripts/run.py": "", "tools/x.py": "",
	}
	if got := sourceRoots(files); !reflect.DeepEqual(got, []string{"pkgs/nav/src"}) {
		t.Errorf("source roots: %v", got)
	}
}

// The fake language server: the test binary run with PYRIGHT_FAKE set
// answers definitions from a table (file:line:col -> "file:line:col",
// several separated by ";", "" none), as pyright-langserver would.
func TestMain(m *testing.M) {
	if tab := os.Getenv("PYRIGHT_FAKE"); tab != "" {
		fakeServer(tab)
		return
	}
	os.Exit(m.Run())
}

func fakeServer(tab string) {
	var answers map[string]string
	b, err := os.ReadFile(tab)
	if err != nil || json.Unmarshal(b, &answers) != nil {
		os.Exit(2)
	}
	r := bufio.NewReader(os.Stdin)
	root := ""
	send := func(v any) {
		b, _ := json.Marshal(v)
		fmt.Printf("Content-Length: %d\r\n\r\n%s", len(b), b)
	}
	for {
		n := -1
		for {
			ln, err := r.ReadString('\n')
			if err != nil {
				return
			}
			ln = strings.TrimSpace(ln)
			if ln == "" {
				break
			}
			if v, ok := strings.CutPrefix(ln, "Content-Length:"); ok {
				n, _ = strconv.Atoi(strings.TrimSpace(v))
			}
		}
		body := make([]byte, n)
		if _, err := io.ReadFull(r, body); err != nil {
			return
		}
		var m struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		_ = json.Unmarshal(body, &m)
		switch m.Method {
		case "initialize":
			var p struct {
				RootURI string `json:"rootUri"`
			}
			_ = json.Unmarshal(m.Params, &p)
			root = uriPath(p.RootURI)
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"capabilities": map[string]any{}}})
		case "textDocument/definition":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Position struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"position"`
			}
			_ = json.Unmarshal(m.Params, &p)
			rel, _ := filepath.Rel(root, uriPath(p.TextDocument.URI))
			k := fmt.Sprintf("%s:%d:%d", filepath.ToSlash(rel), p.Position.Line, p.Position.Character)
			var locs []any
			if a := answers[k]; a != "" {
				for _, t := range strings.Split(a, ";") {
					f := strings.Split(t, ":")
					line, _ := strconv.Atoi(f[1])
					col, _ := strconv.Atoi(f[2])
					file := f[0]
					if !filepath.IsAbs(file) {
						file = filepath.Join(root, file)
					}
					pos := map[string]any{"line": line, "character": col}
					locs = append(locs, map[string]any{"uri": fileURI(file), "range": map[string]any{"start": pos, "end": pos}})
				}
			}
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": locs})
		case "shutdown":
			send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
		case "exit":
			return
		default:
			if len(m.ID) > 0 {
				send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": nil})
			}
		}
	}
}

func TestRunWithFakeServer(t *testing.T) {
	lib := filepath.Join(t.TempDir(), "lib.pyi")
	if err := os.WriteFile(lib, []byte("def get(k): ...\nX: int\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in := tree(t, map[string]string{
		"shop/orders.py": `from shop.util import helper, Order


def run(cb, d):
    helper(1)
    Order()
    d.get(1)
    cb()
    alias()
    mixed()


alias = helper
`,
		"shop/util.py":  "def helper(v):\n    return v\n\n\nclass Order:\n    pass\n",
		"shop/util.pyi": "def stub(): ...\n",
	})
	answers := map[string]string{
		"shop/orders.py:4:4":  "shop/util.py:0:4",
		"shop/orders.py:5:4":  "shop/util.py:4:6",
		"shop/orders.py:6:6":  lib + ":0:4",
		"shop/orders.py:7:4":  "shop/orders.py:3:8", // a parameter
		"shop/orders.py:8:4":  "shop/orders.py:12:0",
		"shop/orders.py:12:8": "shop/util.py:0:4",
		"shop/orders.py:9:4":  "shop/util.py:0:4;" + lib + ":1:0",
	}
	tab := filepath.Join(t.TempDir(), "answers.json")
	b, _ := json.Marshal(answers)
	if err := os.WriteFile(tab, b, 0o644); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PYRIGHT_FAKE", tab)
	store := precise.Open(t.TempDir())
	cfg := Config{Pyright: exe, Store: store}
	out, err := Run(context.Background(), in, cfg)
	if err != nil {
		t.Fatal(err)
	}
	got := edgeLines(out.Edges)
	want := []string{
		"shop/orders.py:10 mixed -> ?",
		"shop/orders.py:5 helper -> shop/util.py:1 helper",
		"shop/orders.py:6 Order -> shop/util.py:5 Order (class)",
		"shop/orders.py:7 get -> ext",
		"shop/orders.py:8 cb -> ?",
		"shop/orders.py:9 alias -> shop/util.py:1 helper",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("edges:\n got %q\nwant %q", got, want)
	}
	if out.Cached != 0 || out.Sites != 6 {
		t.Errorf("a first run resolves: %+v", out)
	}
	// cached mode answers from the store without the server
	t.Setenv("PYRIGHT_FAKE", "")
	cin := in
	cin.CachedOnly = true
	again, err := Run(context.Background(), cin, Config{Pyright: "/nonexistent", Store: store})
	if err != nil || again.Cached != 1 || !reflect.DeepEqual(edgeLines(again.Edges), want) {
		t.Errorf("cached: %+v %v", again, err)
	}
	// a changed file misses
	cin.Files = map[string]string{}
	for k, v := range in.Files {
		cin.Files[k] = v
	}
	cin.Files["shop/util.py"] = "changed"
	if miss, _ := Run(context.Background(), cin, Config{Store: store}); miss.Cached != 0 || len(miss.Edges) != 0 {
		t.Errorf("a changed file is not cached: %+v", miss)
	}
	// with nothing ever resolved, the default mode does nothing
	if none, _ := Run(context.Background(), cin, Config{Store: precise.Open(t.TempDir())}); none.Units != 1 || len(none.Edges) != 0 {
		t.Errorf("empty store: %+v", none)
	}
}

func TestPartlyUnknown(t *testing.T) {
	for h, want := range map[string]bool{
		"(method) fetch: Unknown | (() -> Literal[1])":   true,
		"(method) fetch: (() -> int) | Unknown":          true,
		"(method) fetch: Any | ((u: str) -> list[dict])": true,
		"(method) def fetch(self: S) -> list[Unknown]":   false,
		"(method) fetch: (() -> int) | (() -> str)":      false,
		"(variable) cb: (x: Unknown | int) -> None":      false,
		"(class) Order": false,
	} {
		if got := partlyUnknown(h); got != want {
			t.Errorf("partlyUnknown(%q) = %v", h, got)
		}
	}
}

func TestConfigEnvironments(t *testing.T) {
	files := map[string]string{
		"run.py": "", "tools/migrate/migrate.py": "", "tools/migrate/step.py": "",
		"pkgs/nav/src/nav/__init__.py": "", "pkgs/nav/src/nav/core.py": "",
	}
	b, err := config(files)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Envs []struct {
			Root       string   `json:"root"`
			ExtraPaths []string `json:"extraPaths"`
		} `json:"executionEnvironments"`
	}
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, e := range cfg.Envs {
		got = append(got, e.Root+" <- "+strings.Join(e.ExtraPaths, ","))
	}
	want := []string{
		"pkgs/nav/src <- pkgs/nav,pkgs,.",
		"tools/migrate <- tools,.,pkgs/nav/src",
		". <- pkgs/nav/src",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("environments:\n got %q\nwant %q", got, want)
	}
}
