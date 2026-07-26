//go:build sqlite

package main

import (
	"fmt"
	"os"

	"github.com/vishwak02/reponite/internal/fleet"
)

// registryPath is the persistent fleet registry location (§8B.7): every repo
// this machine has indexed, so serve/mcp can mount the fleet without being
// handed each directory again.
func registryPath() string { return fleet.DefaultPath() }

// registerRepo records a freshly indexed repo in the fleet registry. Best
// effort with a VISIBLE warning: indexing succeeded, so a registry write
// failure must not fail the command — but it must not be silent either, or the
// user later wonders why `serve` doesn't see this repo.
func registerRepo(repo, dir, module string) {
	path := registryPath()
	if path == "" {
		return // no discoverable home; run registry-less
	}
	reg, err := fleet.Load(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: fleet registry unreadable (%v); %s not registered\n", err, repo)
		return
	}
	if err := fleet.Save(path, reg.Add(fleet.NewEntry(repo, dir, module))); err != nil {
		fmt.Fprintf(os.Stderr, "reponite: could not update the fleet registry (%v)\n", err)
	}
}

// fleetDirs resolves the directories a fleet-wide command should mount when the
// user named none: every live registered repo. Stale entries (index deleted or
// moved) are RETURNED, not dropped, so the caller can say so — an agent must
// never read "3 repos mounted" while a fourth silently vanished.
func fleetDirs() (dirs []string, stale []fleet.Entry, err error) {
	path := registryPath()
	if path == "" {
		return nil, nil, nil
	}
	reg, err := fleet.Load(path)
	if err != nil {
		return nil, nil, err
	}
	live, stale := reg.Live(dbRel)
	return fleet.Dirs(live), stale, nil
}

// resolveDirs turns a command's positional dirs into the set to mount: explicit
// dirs always win; with none, the registry provides the fleet; with an empty
// registry it falls back to the working directory (previous behavior). It
// reports stale registry entries on stderr rather than hiding them.
func resolveDirs(args []string) []string {
	if len(args) > 0 {
		return args
	}
	dirs, stale, err := fleetDirs()
	if err != nil {
		fmt.Fprintf(os.Stderr, "reponite: fleet registry unreadable (%v); using the current directory\n", err)
		return []string{"."}
	}
	for _, e := range stale {
		fmt.Fprintf(os.Stderr, "reponite: registered repo %s has no index at %s — skipping (reponite fleet remove %s)\n", e.Repo, e.Dir, e.Repo)
	}
	if len(dirs) == 0 {
		return []string{"."}
	}
	return dirs
}
