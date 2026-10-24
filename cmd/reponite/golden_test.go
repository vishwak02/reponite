//go:build sqlite && treesitter && mcp

package main

// golden_test.go pins what the CLI (`reponite <cmd>`, JSON output) and the MCP
// server (`tools/list` and every tool's `tools/call` text) answer for a small
// fixture repo indexed at two refs. The dashboard shares the query layer and
// the JSON formatters with both, so a change made for the web must leave
// these files byte-identical. Regenerate on an intended change with
//
//	go test -tags "sqlite treesitter mcp" ./cmd/reponite/ -run Golden -update
//
// and review the diff of testdata/golden.

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var updateGolden = flag.Bool("update", false, "rewrite testdata/golden from the current build")

// goldenCLI is each pinned command line, run in the fixture repo.
var goldenCLI = []struct{ name, args string }{
	{"repos", "repos"},
	{"refs", "refs"},
	{"search", "search Charge"},
	{"search-tests", "search Charge --tests"},
	{"grep", "grep declined"},
	{"context", "context Charge"},
	{"context-tests", "context Charge --tests"},
	{"brief", "brief Charge"},
	{"compat", "compat Charge"},
	{"diff", "diff v1 HEAD"},
	{"diff-changed", "diff v1 HEAD --changed-only"},
	{"rootcause", "rootcause Charge v1 HEAD"},
	{"ximpact", "ximpact Charge"},
	{"blast-radius", "blast-radius Charge"},
	{"usages", "usages Charge"},
	{"topics", "topics"},
	{"coverage", "coverage"},
	{"links", "links orders/orders.go"},
	{"logsite", "logsite charging order 42"},
	{"impls", "impls Order"},
	{"routes", "routes"},
	{"ui", "ui"},
	{"models", "models"},
	{"config", "config"},
	{"errors", "errors"},
	{"errors-msg", "errors card declined"},
	{"tests", "tests Charge"},
	{"map", "map"},
	{"explain", "explain Charge"},
	{"trace", "trace Charge"},
	{"semsearch", "semsearch validate card"},
	{"investigate", "investigate how is an order charged --json"},
	{"why", "why Charge"},
	{"verify-edit", "verify-edit orders/orders.go"},
}

// goldenMCP is each pinned tool call (arguments as MCP delivers them).
var goldenMCP = []struct {
	name, tool string
	args       map[string]string
}{
	{"repos", "reponite_repos", nil},
	{"refs", "reponite_refs", nil},
	{"search", "reponite_search", map[string]string{"query": "Charge"}},
	{"grep", "reponite_grep", map[string]string{"pattern": "declined"}},
	{"compat", "reponite_compat", map[string]string{"symbol": "Charge"}},
	{"context", "reponite_context", map[string]string{"symbol": "Charge"}},
	{"diff", "reponite_diff", map[string]string{"from": "v1", "to": "HEAD"}},
	{"rootcause", "reponite_rootcause", map[string]string{"symbol": "Charge", "from": "v1", "to": "HEAD"}},
	{"rootcause_trace", "reponite_rootcause_trace", map[string]string{"stacktrace": "panic: card declined\n\tshop/orders/orders.go:22 +0x1d\n", "from": "v1", "to": "HEAD"}},
	{"brief", "reponite_brief", map[string]string{"symbol": "Charge"}},
	{"semsearch", "reponite_semsearch", map[string]string{"query": "validate card"}},
	{"ximpact", "reponite_ximpact", map[string]string{"symbol": "Charge"}},
	{"investigate", "reponite_investigate", map[string]string{"question": "how is an order charged"}},
	{"verify_edit", "reponite_verify_edit", map[string]string{"path": "orders/orders.go", "content": "package orders\n\nfunc Charge() error { return nil }\n"}},
	{"usages", "reponite_usages", map[string]string{"symbol": "Charge"}},
	{"blast_radius", "reponite_blast_radius", map[string]string{"symbol": "Charge"}},
	{"logsite", "reponite_logsite", map[string]string{"line": "charging order 42"}},
	{"impls", "reponite_impls", map[string]string{"base": "Order"}},
	{"routes", "reponite_routes", nil},
	{"topics", "reponite_topics", nil},
	{"links", "reponite_links", map[string]string{"target": "orders/orders.go"}},
	{"coverage", "reponite_coverage", nil},
	{"ui", "reponite_ui", nil},
	{"trace", "reponite_trace", map[string]string{"entry": "Charge"}},
	{"models", "reponite_models", nil},
	{"config", "reponite_config", nil},
	{"map", "reponite_map", nil},
	{"explain", "reponite_explain", map[string]string{"target": "Charge"}},
	{"guide", "reponite_guide", nil},
	{"why", "reponite_why", map[string]string{"symbol": "Charge"}},
	{"errors", "reponite_errors", nil},
	{"tests", "reponite_tests", map[string]string{"symbol": "Charge"}},
}

func TestGoldenCLIAndMCP(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("needs the go tool")
	}
	root := t.TempDir()
	bin := filepath.Join(root, "reponite")
	build := exec.Command("go", "build", "-tags", "sqlite treesitter mcp", "-o", bin, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	env := goldenEnv(root)
	shop := filepath.Join(root, "shop")
	run := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env = dir, env
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("reponite %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	// v1 then the working tree as HEAD, under the one repo name "shop"
	copyTree(t, filepath.Join("testdata", "shop", "v1"), shop)
	run(root, "index", "shop", "v1")
	if err := os.RemoveAll(shop); err != nil {
		t.Fatal(err)
	}
	copyTree(t, filepath.Join("testdata", "shop", "v2"), shop)
	run(root, "index", "shop", "HEAD")

	norm := func(s string) string { return strings.ReplaceAll(s, root, "<root>") }
	for _, c := range goldenCLI {
		cmd := exec.Command(bin, strings.Fields(c.args)...)
		cmd.Dir, cmd.Env = shop, env
		out, _ := cmd.Output() // a non-zero exit (ci-style verdicts) is pinned by its output
		checkGolden(t, filepath.Join("cli", c.name+".json"), norm(string(out)))
	}

	srv := startMCP(t, bin, root, env)
	defer srv.close()
	list := srv.call(t, "tools/list", map[string]any{})
	checkGolden(t, filepath.Join("mcp", "tools_list.json"), norm(string(list)))
	for _, c := range goldenMCP {
		args := map[string]any{}
		for k, v := range c.args {
			args[k] = v
		}
		res := srv.call(t, "tools/call", map[string]any{"name": c.tool, "arguments": args})
		var r struct {
			Content []struct {
				Type, Text string
			}
			IsError bool `json:"isError"`
		}
		if err := json.Unmarshal(res, &r); err != nil {
			t.Fatalf("%s: %v\n%s", c.tool, err, res)
		}
		var b strings.Builder
		if r.IsError {
			b.WriteString("isError\n")
		}
		for _, ct := range r.Content {
			b.WriteString(ct.Text)
			b.WriteByte('\n')
		}
		checkGolden(t, filepath.Join("mcp", c.name+".txt"), norm(b.String()))
	}
}

// goldenEnv isolates the binary from the user's registry, stores and pins.
func goldenEnv(root string) []string {
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "REPONITE_") || strings.HasPrefix(kv, "XDG_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		"REPONITE_STORE_DIR="+filepath.Join(root, "store"),
		"REPONITE_FLEET="+filepath.Join(root, "fleet.json"),
		"XDG_CACHE_HOME="+filepath.Join(root, "cache"),
		"XDG_CONFIG_HOME="+filepath.Join(root, "config"))
}

func checkGolden(t *testing.T, rel, got string) {
	t.Helper()
	path := filepath.Join("testdata", "golden", rel)
	if *updateGolden {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%s: %v (run with -update to create it)", rel, err)
	}
	if string(want) != got {
		t.Errorf("%s changed:\n%s", rel, firstDiff(string(want), got))
	}
}

// firstDiff shows the first differing line of two outputs with its neighbours.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}
		if i < len(g) {
			gl = g[i]
		}
		if wl != gl {
			return fmt.Sprintf("line %d\n- %s\n+ %s", i+1, wl, gl)
		}
	}
	return "(lengths differ)"
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, p)
		dst := filepath.Join(to, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// mcpProc is `reponite mcp` over stdio, spoken to in newline-delimited JSON-RPC.
type mcpProc struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Reader
	id  int
}

func startMCP(t *testing.T, bin, root string, env []string) *mcpProc {
	t.Helper()
	cmd := exec.Command(bin, "mcp", "shop")
	cmd.Dir, cmd.Env = root, env
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// a mounted HEAD is re-indexed in the background on start; answer only
	// once that is done, so no call reads a ref while it is being rewritten
	refreshed := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			if strings.Contains(sc.Text(), "refreshed shop@HEAD") {
				close(refreshed)
				break
			}
		}
		io.Copy(io.Discard, stderr)
	}()
	select {
	case <-refreshed:
	case <-time.After(2 * time.Minute):
		t.Fatal("mcp: the mount refresh never finished")
	}
	p := &mcpProc{cmd: cmd, in: in, out: bufio.NewReaderSize(out, 1<<20)}
	p.call(t, "initialize", map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "golden", "version": "0"},
	})
	p.send(t, map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"})
	return p
}

func (p *mcpProc) send(t *testing.T, msg map[string]any) {
	t.Helper()
	b, _ := json.Marshal(msg)
	if _, err := p.in.Write(append(b, '\n')); err != nil {
		t.Fatal(err)
	}
}

// call sends one request and returns its result, skipping notifications.
func (p *mcpProc) call(t *testing.T, method string, params map[string]any) json.RawMessage {
	t.Helper()
	p.id++
	p.send(t, map[string]any{"jsonrpc": "2.0", "id": p.id, "method": method, "params": params})
	for {
		line, err := p.out.ReadBytes('\n')
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		var r struct {
			ID     *int            `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  json.RawMessage `json:"error"`
		}
		if json.Unmarshal(line, &r) != nil || r.ID == nil || *r.ID != p.id {
			continue
		}
		if r.Error != nil {
			t.Fatalf("%s: %s", method, r.Error)
		}
		return r.Result
	}
}

func (p *mcpProc) close() {
	p.in.Close()
	done := make(chan struct{})
	go func() { p.cmd.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		p.cmd.Process.Kill()
	}
}
