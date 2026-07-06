//go:build sqlite && treesitter

package main

import (
	"fmt"
	"net/http"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// serveCommand starts the read-only web dashboard + JSON API. With one dir it
// serves that repo; with several it aggregates them into a MultiStore team/fleet
// view (roadmap 4.2) where cross-repo ximpact spans all of them. Bound to
// localhost by default; --addr overrides.
func serveCommand(args []string) {
	addr, dirs := popValue(args, "--addr")
	if addr == "" {
		addr = "127.0.0.1:8899"
	}
	if len(dirs) == 0 {
		dirs = []string{"."}
	}
	var stores []query.Store
	var repos []string
	for _, dir := range dirs {
		st := openStore(dir)
		defer st.Close()
		stores = append(stores, st)
		repos = append(repos, repoName(dir))
	}
	store := stores[0]
	if len(stores) > 1 {
		store = storage.NewMultiStore(stores...)
	}
	h := &interfaces.WebHandler{Store: store, Repo: repos[0], Intent: processing.NewGitIntent(dirs[0])}
	fmt.Printf("reponite serve: http://%s  (repos %v)\n", addr, repos)
	if err := http.ListenAndServe(addr, h.Routes()); err != nil {
		fail(err)
	}
}
