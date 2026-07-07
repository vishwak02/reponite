//go:build sqlite && mcp && treesitter

package main

import (
	"fmt"
	"os"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
	"github.com/vishwak02/reponite/internal/version"
)

// newIntentProvider gives the MCP server git-backed change provenance for
// reponite_brief (real under the treesitter tag; see the stub for other builds).
func newIntentProvider(dir string) query.IntentProvider { return processing.NewGitIntent(dir) }

// autoIndexOnMount keeps a mounted MCP server's index current. With no index yet
// it indexes HEAD synchronously, so the first tool calls aren't silently empty.
// If an index already exists (e.g. from a prior session) it refreshes HEAD in
// the BACKGROUND — so restarting the agent picks up the current working tree
// without a manual `reponite index` and without blocking startup. This fixes the
// stale-mount trap (an agent got confident answers about outdated code). Note:
// a background refresh briefly re-clears the ref, so that repo's results can be
// momentarily partial right after mount; `reponite watch` gives continuous
// mid-session freshness.
func autoIndexOnMount(st *sqlite.Store, repo, dir string) {
	if len(st.Refs(repo)) == 0 {
		fmt.Fprintf(os.Stderr, "reponite: no index for %q; indexing %s@HEAD on mount...\n", repo, dir)
		if err := processing.IndexDir(st, repo, "HEAD", dir, version.NormVer); err != nil {
			fmt.Fprintln(os.Stderr, "reponite: auto-index failed:", err)
		}
		return
	}
	go func() {
		if err := processing.IndexDir(st, repo, "HEAD", dir, version.NormVer); err != nil {
			fmt.Fprintln(os.Stderr, "reponite: background refresh failed:", err)
			return
		}
		fmt.Fprintf(os.Stderr, "reponite: refreshed %s@HEAD on mount\n", repo)
	}()
}
