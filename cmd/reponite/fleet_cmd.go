//go:build sqlite && treesitter

package main

import (
	"fmt"

	"github.com/vishwak02/reponite/internal/fleet"
)

// cmdFleet manages the persistent registry: list (default), add, remove, path.
func cmdFleet(args []string) {
	pos := parseCmd("fleet", "fleet [list|add <dir>|remove <dir|repo>|path]", args, nil)
	action := arg(pos, 0, "list")
	path := registryPath()
	if path == "" {
		fail(fmt.Errorf("no config directory found (set %s to choose a registry path)", fleet.EnvPath))
	}

	switch action {
	case "path":
		fmt.Println(path)
		return
	case "add":
		if len(pos) < 2 {
			fail(fmt.Errorf("usage: reponite fleet add <dir>"))
		}
		dir := pos[1]
		st := openStore(dir)
		defer st.Close()
		repo := repoName(dir)
		reg, err := fleet.Load(path)
		if err != nil {
			fail(err)
		}
		if err := fleet.Save(path, reg.Add(fleet.NewEntry(repo, dir, st.ModulePath(repo)))); err != nil {
			fail(err)
		}
		fmt.Printf("registered %s (%s)\n", repo, dir)
		return
	case "remove":
		if len(pos) < 2 {
			fail(fmt.Errorf("usage: reponite fleet remove <dir|repo>"))
		}
		reg, err := fleet.Load(path)
		if err != nil {
			fail(err)
		}
		reg, ok := reg.Remove(pos[1])
		if !ok {
			fail(fmt.Errorf("%q is not registered", pos[1]))
		}
		if err := fleet.Save(path, reg); err != nil {
			fail(err)
		}
		fmt.Printf("unregistered %s\n", pos[1])
		return
	case "list":
		reg, err := fleet.Load(path)
		if err != nil {
			fail(err)
		}
		live, stale := reg.Live(dbRel)
		if len(live) == 0 && len(stale) == 0 {
			fmt.Printf("no repos registered (registry: %s)\nRun `reponite index <dir>` — indexing registers the repo automatically.\n", path)
			return
		}
		fmt.Printf("fleet registry: %s\n", path)
		for _, e := range live {
			mod := e.Module
			if mod == "" {
				mod = "no module manifest"
			}
			fmt.Printf("  %-28s %s  [%s]  indexed %s\n", e.Repo, e.Dir, mod, e.IndexedAt)
		}
		for _, e := range stale {
			fmt.Printf("  %-28s %s  [STALE — no index at this path]\n", e.Repo, e.Dir)
		}
		return
	default:
		fail(fmt.Errorf("unknown action %q (list|add|remove|path)", action))
	}
}
