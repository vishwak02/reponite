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

// autoIndexIfEmpty indexes HEAD when the repo has no indexed refs yet, so a
// freshly-mounted MCP server returns real results instead of silently-empty
// ones (the "looks broken" failure the roadmap flagged). No-op once an index
// exists — a prior `reponite index` or a running `reponite watch` owns it.
func autoIndexIfEmpty(st *sqlite.Store, repo, dir string) {
	if len(st.Refs(repo)) > 0 {
		return
	}
	fmt.Fprintf(os.Stderr, "reponite: no index for %q; indexing %s@HEAD on mount...\n", repo, dir)
	if err := processing.IndexDir(st, repo, "HEAD", dir, version.NormVer); err != nil {
		fmt.Fprintln(os.Stderr, "reponite: auto-index failed:", err)
	}
}
