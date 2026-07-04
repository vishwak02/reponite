//go:build sqlite && mcp

package main

import "github.com/vishwak02/reponite/internal/interfaces"

func mcpCommand(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	st := openStore(dir)
	defer st.Close()
	repo := repoName(dir)
	autoIndexIfEmpty(st, repo, dir) // self-index on first mount so tools aren't silently empty
	ts := &interfaces.ToolServer{Store: st, Repo: repo, Intent: newIntentProvider(dir)}
	if err := interfaces.ServeStdio(ts); err != nil {
		fail(err)
	}
}
