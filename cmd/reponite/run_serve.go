//go:build sqlite && treesitter

package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
	"github.com/vishwak02/reponite/internal/version"
)

// serveCommand starts the read-only web dashboard + JSON API. With one dir it
// serves that repo; with several it aggregates them into a MultiStore team/fleet
// view (roadmap 4.2) where cross-repo ximpact spans all of them. With NO dir it
// mounts every repo in the persistent fleet registry (§8B.7). Bound to
// localhost by default; --addr overrides.
func serveCommand(args []string) {
	installFactsIfBuilt()
	var addr, refs string
	dirs := parseCmd("serve", "serve [<dir>...] [--addr host:port] [--refs repo=ref,...]", args, func(fs *flag.FlagSet) {
		fs.StringVar(&addr, "addr", "127.0.0.1:8899", "listen address")
		fs.StringVar(&refs, "refs", os.Getenv("REPONITE_REFS"), "read each repo at its own ref (repo=ref,...), e.g. the versions a deployed system runs; default $REPONITE_REFS")
	})
	if addr == "" {
		addr = "127.0.0.1:8899"
	}
	// No dirs named: mount the whole registered fleet (§8B.7). Explicit dirs
	// always win; stale registry entries are reported by resolveDirs.
	dirs = resolveDirs(dirs)
	var stores []query.Store
	var repos []string
	repoStores := map[string]query.Store{}
	for _, dir := range dirs {
		st := openStore(dir)
		defer st.Close()
		stores = append(stores, st)
		repo := repoName(dir)
		repos = append(repos, repo)
		repoStores[repo] = st // concrete per-repo store, for the Overview DB view
	}
	store := stores[0]
	if len(stores) > 1 {
		store = storage.NewMultiStore(stores...)
	}
	// HEAD (the dashboard's default ref) means "each repo at its pin"
	store, defaulted := applyPins(store, refs, "--refs")
	if defaulted > 0 {
		fmt.Printf("reponite serve: %d repo(s) have no HEAD indexed and are read at their newest indexed ref (--refs repo=ref chooses)\n", defaulted)
	}
	// the index files: any write to them (a re-index beside the server)
	// changes every answer's ETag
	var dbPaths []string
	for _, dir := range dirs {
		dbPaths = append(dbPaths, dbPathFor(dir))
	}
	h := &interfaces.WebHandler{
		Store: store, Repo: repos[0], Intent: processing.NewGitIntent(dirs[0]), RepoStores: repoStores,
		ParseSymbols: func(path, content string) []query.EditedSymbol {
			return processing.ParseEditedSymbols(path, content, version.NormVer)
		},
		Ranker:      semanticRanker(),
		History:     newHistoryProvider(dirs),
		Compact:     true,
		DataVersion: func() string { return interfaces.FileVersion(dbPaths...) },
	}
	// Bounded reads and an idle limit keep a stalled client from holding a
	// connection; the write bound is generous, since a first fleet-wide answer
	// on a large index can take a while to compute.
	srv := &http.Server{
		Addr:              addr,
		Handler:           h.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       time.Minute,
		WriteTimeout:      5 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	fmt.Printf("reponite serve: http://%s  (repos %v)\n", addr, repos)
	if err := srv.ListenAndServe(); err != nil {
		fail(err)
	}
}
