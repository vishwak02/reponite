//go:build sqlite && mcp

package main

import (
	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func mcpCommand(args []string) {
	dirs := args
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	var stores []query.Store
	var repos []string
	for _, dir := range dirs {
		st := openStore(dir)
		defer st.Close()
		repo := repoName(dir)
		autoIndexOnMount(st, repo, dir) // index on first mount; refresh HEAD in the background otherwise
		stores = append(stores, st)
		repos = append(repos, repo)
	}
	store := stores[0]
	if len(stores) > 1 {
		store = storage.NewMultiStore(stores...)
	}
	ts := &interfaces.ToolServer{Store: store, Repo: repos[0], Intent: newIntentProvider(dirs[0])}
	if err := interfaces.ServeStdio(ts); err != nil {
		fail(err)
	}
}
