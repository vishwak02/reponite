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
	"strings"
)

func setupCommand(args []string) {
	repo, configPath, client, printOnly, ok := parseSetupArgs(args)
	if !ok {
		os.Exit(2)
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

	if printOnly {
		out, _ := json.MarshalIndent(map[string]interface{}{
			"mcpServers": map[string]interface{}{"reponite": entry},
		}, "", "  ")
		fmt.Println(string(out))
		return
	}

	path := configPath
	if path == "" {
		p, known := clientConfigPath(client, absRepo)
		if !known {
			fmt.Fprintf(os.Stderr, "reponite setup: unknown --client %q; known: %s. For other clients pass --config <file> or --print.\n", client, strings.Join(knownClients(), ", "))
			os.Exit(1)
		}
		if p == "" {
			fmt.Fprintln(os.Stderr, "reponite setup: could not determine the config path for this OS; pass --config <file> or use --print")
			os.Exit(1)
		}
		path = p
	}
	if err := mergeMCPServer(path, "reponite", entry); err != nil {
		fmt.Fprintln(os.Stderr, "reponite setup:", err)
		os.Exit(1)
	}
	fmt.Printf("added the 'reponite' MCP server to %s\nrepo: %s\nRestart your agent to pick it up.\n", path, absRepo)
}

// clientConfigPath returns the default MCP config path for a known client, or
// ("", false) for an unknown one. Every supported client consumes the same
// {"mcpServers": {name: {command, args}}} shape that mergeMCPServer writes;
// clients with a divergent schema should use --config/--print instead.
func clientConfigPath(client, absRepo string) (string, bool) {
	home, _ := os.UserHomeDir()
	switch client {
	case "claude-desktop", "claude", "":
		return defaultClaudeConfigPath(), true
	case "claude-code":
		// Project scope: the checked-in .mcp.json alongside the repo.
		return filepath.Join(absRepo, ".mcp.json"), true
	case "cursor":
		if home == "" {
			return "", true
		}
		return filepath.Join(home, ".cursor", "mcp.json"), true
	case "windsurf":
		if home == "" {
			return "", true
		}
		return filepath.Join(home, ".codeium", "windsurf", "mcp_config.json"), true
	}
	return "", false
}

// knownClients lists the clients setup can auto-target (mcpServers-shaped config).
func knownClients() []string {
	return []string{"claude-desktop", "claude-code", "cursor", "windsurf"}
}

// parseSetupArgs parses `setup [dir] [--client name] [--config path] [--print]`,
// allowing the positional dir and the flags in any order (Go's flag package
// stops at the first positional, so `setup . --config x` would otherwise drop
// --config). ok is false on a parse error. repo defaults to ".", client to
// "claude-desktop".
func parseSetupArgs(args []string) (repo, configPath, client string, printOnly, ok bool) {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	cfg := fs.String("config", "", "MCP client config file (overrides --client)")
	cl := fs.String("client", "claude-desktop", "MCP client: claude-desktop|claude-code|cursor|windsurf")
	pr := fs.Bool("print", false, "print the config entry instead of writing it")
	repo = "."
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return "", "", "", false, false
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		repo = rest[0] // first positional is the repo dir; keep scanning for trailing flags
		rest = rest[1:]
	}
	return repo, *cfg, *cl, *pr, true
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
