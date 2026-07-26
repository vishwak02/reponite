//go:build sqlite && treesitter

package main

import (
	"fmt"
	"os"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

// fleetStore is a set of opened per-repo stores presented as one query.Store.
// Discovery commands (search, grep, usages, ximpact, …) read the whole fleet
// so their answers match what the MCP server and dashboard return; per-repo
// commands (compat, diff, brief, …) keep using openStore directly.
type fleetStore struct {
	query.Store
	closers []*sqlite.Store
	// Names is the repo list this store aggregates. It is deliberately NOT
	// called Repos: that would shadow the embedded query.Store.Repos() method.
	Names []string
	Local string // the repo that owns the working directory
}

// Close releases every opened store.
func (f *fleetStore) Close() {
	for _, s := range f.closers {
		s.Close()
	}
}

// openFleet opens the working directory's store plus every live repo in the
// registry, aggregated into one MultiStore. This is what makes cross-repo
// answers actually cross-repo from the CLI: ximpact's SCIP/module tiers and
// per-caller skew can only match a caller in repo A to a definition in repo B
// when both are in the SAME store.
//
// local=true (the --local flag) restricts to the working directory, which is
// also the automatic behavior when no registry exists. Stale registry entries
// are reported, never silently skipped.
func openFleet(local bool) *fleetStore {
	localRepo := repoName(".")
	if local {
		st := openStore(".")
		return &fleetStore{Store: st, closers: []*sqlite.Store{st}, Names: []string{localRepo}, Local: localRepo}
	}

	dirs, stale, err := fleetDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: fleet registry unreadable (%v); using this repo only\n", err)
	}
	for _, e := range stale {
		fmt.Fprintf(os.Stderr, "reponite: registered repo %s has no index at %s — skipping (reponite fleet remove %s)\n", e.Repo, e.Dir, e.Repo)
	}

	f := &fleetStore{Local: localRepo}
	seen := map[string]bool{}
	// The working directory comes first so it stays the default repo for
	// commands that need one, even when the registry lists others.
	for _, dir := range append([]string{"."}, dirs...) {
		repo := repoName(dir)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		st := openStore(dir)
		f.closers = append(f.closers, st)
		f.Names = append(f.Names, repo)
	}
	stores := make([]query.Store, 0, len(f.closers))
	for _, s := range f.closers {
		stores = append(stores, s)
	}
	if len(stores) == 1 {
		f.Store = stores[0]
	} else {
		f.Store = storage.NewMultiStore(stores...)
	}
	return f
}

// openPeers opens every registered repo EXCEPT the one at dir, as a read-only
// view for index-time cross-repo lookups (§8B.3 contract capture). Returns an
// empty fleetStore (nil Store) when nothing else is registered, which callers
// treat as "no peer view" rather than an error.
func openPeers(dir string) *fleetStore {
	self := repoName(dir)
	dirs, _, err := fleetDirs()
	if err != nil || len(dirs) == 0 {
		return &fleetStore{}
	}
	f := &fleetStore{Local: self}
	seen := map[string]bool{self: true}
	for _, d := range dirs {
		repo := repoName(d)
		if seen[repo] {
			continue
		}
		seen[repo] = true
		st := openStore(d)
		f.closers = append(f.closers, st)
		f.Names = append(f.Names, repo)
	}
	if len(f.closers) == 0 {
		return &fleetStore{}
	}
	stores := make([]query.Store, 0, len(f.closers))
	for _, s := range f.closers {
		stores = append(stores, s)
	}
	if len(stores) == 1 {
		f.Store = stores[0]
	} else {
		f.Store = storage.NewMultiStore(stores...)
	}
	return f
}

// fleetNote describes the scope a fleet-wide command actually searched, so a
// result is never mistaken for a narrower or wider one than it is.
func (f *fleetStore) fleetNote() string {
	if len(f.Names) <= 1 {
		return ""
	}
	return fmt.Sprintf("searched %d registered repos: %v", len(f.Names), f.Names)
}
