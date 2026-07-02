//go:build sqlite && mcp

package main

import "github.com/vishwak02/reponite/internal/interfaces"

func mcpCommand(args []string) {
	st := openStore(".")
	defer st.Close()
	ts := &interfaces.ToolServer{Store: st, Repo: repoName(".")}
	if err := interfaces.ServeStdio(ts); err != nil {
		fail(err)
	}
}
