//go:build sqlite

package main

import (
	"path/filepath"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/query"
)

// newHistoryProvider maps each mounted directory's repo name to that
// directory, so `why` reads the matching git object store. The working
// directory comes first and wins a name clash, like the fleet view.
func newHistoryProvider(dirs []string) query.HistoryProvider {
	m := map[string]string{}
	for _, d := range dirs {
		repo := repoName(d)
		if _, seen := m[repo]; seen {
			continue
		}
		if abs, err := filepath.Abs(d); err == nil {
			d = abs
		}
		m[repo] = d
	}
	return processing.NewGitHistory(m)
}

// historyDirs is the working directory plus every live registered repo.
func historyDirs() []string {
	dirs, _, _ := fleetDirs()
	return append([]string{"."}, dirs...)
}
