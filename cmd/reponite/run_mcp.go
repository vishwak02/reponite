//go:build sqlite && mcp

package main

import (
	"github.com/vishwak02/reponite/internal/interfaces"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
	"os"
	"strings"
)

func mcpCommand(args []string) {
	installFactsIfBuilt()
	// No dirs named: mount the whole registered fleet (§8B.7), so an agent's
	// MCP config needn't list every repo. Explicit dirs always win.
	dirs := resolveDirs(args)
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
	// the same pins and test filter as every other command
	store, _ = applyPins(store, os.Getenv(refsEnv), refsEnv)
	if noTestsFromEnv() {
		store = &storage.NoTests{Inner: store}
	}
	// read the primary repo's call graph at its working ref while the agent
	// is still starting up, so its first question does not pay for decoding
	// it — one ref of one repo: the rest is read when asked about
	go func() {
		if ref := warmRef(stores[0].Refs(repos[0])); ref != "" {
			stores[0].SymbolsAt(repos[0], ref)
			stores[0].Snapshot(repos[0], ref)
		}
	}()
	ts := &interfaces.ToolServer{Store: store, Repo: repos[0], Intent: newIntentProvider(dirs[0]), ParseSymbols: newSymbolParser(), Ranker: semanticRanker()}
	ts.History = newHistoryProvider(dirs)
	if err := interfaces.ServeStdio(ts); err != nil {
		fail(err)
	}
}

// warmRef is the ref an agent most likely asks about: HEAD, else a branch
// name (not a commit id or a version tag), else none.
func warmRef(refs []string) string {
	for _, r := range refs {
		if r == "HEAD" {
			return r
		}
	}
	for _, r := range refs {
		if strings.HasPrefix(r, "origin/") || r == "main" || r == "master" || r == "devel" || r == "develop" {
			return r
		}
	}
	return ""
}
