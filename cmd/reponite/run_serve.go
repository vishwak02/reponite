//go:build sqlite && treesitter

package main

import (
	"fmt"
	"net/http"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
)

// serveCommand starts the read-only web dashboard + JSON API over the repo's
// index. Bound to localhost by default; --addr overrides.
func serveCommand(args []string) {
	addr, args := popValue(args, "--addr")
	if addr == "" {
		addr = "127.0.0.1:8899"
	}
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	st := openStore(dir)
	defer st.Close()
	repo := repoName(dir)
	h := &interfaces.WebHandler{Store: st, Repo: repo, Intent: processing.NewGitIntent(dir)}
	fmt.Printf("reponite serve: http://%s  (repo %s)\n", addr, repo)
	if err := http.ListenAndServe(addr, h.Routes()); err != nil {
		fail(err)
	}
}
