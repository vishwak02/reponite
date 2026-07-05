package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeMCPServerPreservesAndCreates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "cfg.json")
	if err := os.WriteFile(p, []byte(`{"mcpServers":{"other":{"command":"x"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entry := map[string]interface{}{"command": "/bin/reponite", "args": []interface{}{"mcp", "/repo"}}
	if err := mergeMCPServer(p, "reponite", entry); err != nil {
		t.Fatal(err)
	}
	s := readFile(t, p)
	if !strings.Contains(s, `"reponite"`) || !strings.Contains(s, `"other"`) {
		t.Fatalf("merge must preserve existing servers and add reponite:\n%s", s)
	}
	// creates the file + parent dirs when absent
	p2 := filepath.Join(dir, "sub", "new.json")
	if err := mergeMCPServer(p2, "reponite", entry); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readFile(t, p2), `"reponite"`) {
		t.Fatal("config not created with entry")
	}
}

func TestParseSetupArgsFlagOrder(t *testing.T) {
	cases := []struct {
		name   string
		args   []string
		repo   string
		config string
		client string
		print  bool
	}{
		{"flags first", []string{"--config", "x.json", "."}, ".", "x.json", "claude-desktop", false},
		{"positional first (regression)", []string{".", "--config", "x.json"}, ".", "x.json", "claude-desktop", false},
		{"print after positional", []string{"myrepo", "--print"}, "myrepo", "", "claude-desktop", true},
		{"client flag", []string{".", "--client", "cursor"}, ".", "", "cursor", false},
		{"no args -> defaults", nil, ".", "", "claude-desktop", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repo, config, client, printOnly, ok := parseSetupArgs(c.args)
			if !ok || repo != c.repo || config != c.config || client != c.client || printOnly != c.print {
				t.Fatalf("parseSetupArgs(%v) = (%q,%q,%q,%v,%v); want (%q,%q,%q,%v,true)",
					c.args, repo, config, client, printOnly, ok, c.repo, c.config, c.client, c.print)
			}
		})
	}
}

func TestDefaultClaudeConfigPath(t *testing.T) {
	if p := defaultClaudeConfigPath(); p != "" && !strings.Contains(p, "Claude") {
		t.Fatalf("unexpected config path: %s", p)
	}
}

func TestClientConfigPath(t *testing.T) {
	// claude-code is project-scoped: the .mcp.json next to the repo.
	if p, ok := clientConfigPath("claude-code", "/repo"); !ok || p != filepath.Join("/repo", ".mcp.json") {
		t.Fatalf("claude-code -> %q (ok=%v)", p, ok)
	}
	// known clients resolve; empty/claude alias resolves to the Claude Desktop path.
	for _, c := range []string{"cursor", "windsurf", "claude-desktop", ""} {
		if _, ok := clientConfigPath(c, "/repo"); !ok {
			t.Fatalf("client %q should be known", c)
		}
	}
	// unknown client is reported, not silently defaulted.
	if _, ok := clientConfigPath("emacs", "/repo"); ok {
		t.Fatal("unknown client must return ok=false")
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
