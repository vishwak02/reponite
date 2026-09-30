//go:build treesitter

package processing

import "testing"

func TestParseGitmodules(t *testing.T) {
	got := parseGitmodules(`[submodule "rr_gbc"]
	path = rr_gbc
	url = git@github.com:org/rr_gbc.git
[submodule "deploy/manifests"]
	path = deployment/rr_manifests
	branch = main
`)
	if got["rr_gbc"] != "rr_gbc" || got["deployment/rr_manifests"] != "deploy/manifests" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
