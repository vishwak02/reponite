// setup.go implements `reponite setup`: it registers reponite as an MCP server
// in an agent's config so tools like reponite_compat/context/grep appear
// automatically. Pure stdlib (no build tags), so it works in any build and is
// unit-tested in-sandbox.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

func setupCommand(args []string) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	configPath := fs.String("config", "", "MCP client config file (default: Claude Desktop for this OS)")
	printOnly := fs.Bool("print", false, "print the config entry instead of writing it")
	if err := fs.Parse(args); err != nil {
		os.Exit(2)
	}
	repo := "."
	if fs.NArg() > 0 {
		repo = fs.Arg(0)
	}
	absRepo, err := filepath.Abs(repo)
	if err != nil {
		absRepo = repo
	}
	exe, err := os.Executable()
	if err != nil || exe == "" {
		exe = "reponite"
	}
	entry := map[string]interface{}{"command": exe, "args": []interface{}{"mcp", absRepo}}

	if *printOnly {
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mcpServers": map[string]interface{}{"reponite": entry},
		}, "", "  ")
		fmt.Println(string(out))
		return
	}

	path := *configPath
	if path == "" {
		path = defaultClaudeConfigPath()
		if path == "" {
			fmt.Fprintln(os.Stderr, "reponite setup: could not determine the Claude config path; pass --config <file> or use --print")
			os.Exit(1)
		}
	}
	if err := mergeMCPServer(path, "reponite", entry); err != nil {
		fmt.Fprintln(os.Stderr, "reponite setup:", err)
		os.Exit(1)
	}
	fmt.Printf("added the 'reponite' MCP server to %s\nrepo: %s\nRestart your agent to pick it up.\n", path, absRepo)
}

// defaultClaudeConfigPath returns the Claude Desktop config location per OS.
func defaultClaudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", "Claude", "claude_desktop_config.json")
	case "windows":
		if ad := os.Getenv("APPDATA"); ad != "" {
			return filepath.Join(ad, "Claude", "claude_desktop_config.json")
		}
		return filepath.Join(home, "AppData", "Roaming", "Claude", "claude_desktop_config.json")
	default:
		return filepath.Join(home, ".config", "Claude", "claude_desktop_config.json")
	}
}

// mergeMCPServer sets config["mcpServers"][name] = entry, preserving any other
// servers, creating the file (and parents) if needed.
func mergeMCPServer(path, name string, entry map[string]interface{}) error {
	cfg := map[string]interface{}{}
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		if err := json.Unmarshal(b, &cfg); err != nil {
			return fmt.Errorf("existing config %s is not valid JSON: %w", path, err)
		}
	}
	servers, _ := cfg["mcpServers"].(map[string]interface{})
	if servers == nil {
		servers = map[string]interface{}{}
	}
	servers[name] = entry
	cfg["mcpServers"] = servers
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(out, '\n'), 0o644)
}
