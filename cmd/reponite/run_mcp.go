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
	ts := &interfaces.ToolServer{Store: st, Repo: repoName(dir)}
	if err := interfaces.ServeStdio(ts); err != nil {
		fail(err)
	}
}
