//go:build sqlite && mcp && !treesitter

package main

import (
	"fmt"
	"os"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

// newIntentProvider is nil in builds without the git-backed indexer; brief then
// omits the intent section rather than failing.
func newIntentProvider(dir string) query.IntentProvider { return nil }

// autoIndexOnMount is a no-op in MCP builds without the tree-sitter indexer
// (`-tags "sqlite mcp"`); it warns so an empty index isn't mistaken for a broken
// server. Build with `make cli` (adds -tags treesitter) for index/refresh on mount.
func autoIndexOnMount(st *sqlite.Store, repo, dir string) {
	if len(st.Refs(repo)) > 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "reponite: no index for %q and this build lacks the indexer; run `reponite index %s` (or use the full `make cli` binary).\n", repo, dir)
}
