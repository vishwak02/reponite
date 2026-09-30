//go:build treesitter

package processing

import "testing"

func TestParseGitmodules(t *testing.T) {
	got := parseGitmodules(`[submodule "lib_core"]
	path = lib_core
	url = git@github.com:org/lib_core.git
[submodule "deploy/manifests"]
	path = deployment/manifests
	branch = main
`)
	if got["lib_core"] != "lib_core" || got["deployment/manifests"] != "deploy/manifests" || len(got) != 2 {
		t.Fatalf("got %v", got)
	}
}
