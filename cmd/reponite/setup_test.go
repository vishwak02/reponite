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

func TestDefaultClaudeConfigPath(t *testing.T) {
	if p := defaultClaudeConfigPath(); p != "" && !strings.Contains(p, "Claude") {
		t.Fatalf("unexpected config path: %s", p)
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
