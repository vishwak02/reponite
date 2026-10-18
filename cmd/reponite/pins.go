//go:build sqlite

package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// refsEnv pins repos to refs fleet-wide: "repoA=ref1,repoB=ref2".
const refsEnv = "REPONITE_REFS"

// noTestsEnv drops test code from every answer when set to 1/true/yes/on.
const noTestsEnv = "REPONITE_NO_TESTS"

// noTestsFromEnv is the environment's half of --no-tests, for servers that
// take no per-command flags (mcp).
func noTestsFromEnv() bool {
	switch strings.ToLower(os.Getenv(noTestsEnv)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// applyPins reads each repo at its pinned ref when a query names HEAD (every
// command's default). refs is "repo=ref,…" (from --refs or REPONITE_REFS).
// With no pins, a repo indexed only at tags is read at its newest one rather
// than as empty; explicit pins describe one deployed system, so repos outside
// them stay out. An explicit other ref (a compat target, `context X 5.6.7`) is
// read as asked. It returns the store and how many repos got a default pin.
func applyPins(st query.Store, refs, source string) (query.Store, int) {
	pins := map[string]string{}
	if refs != "" {
		var err error
		if pins, err = storage.ParsePins(refs); err != nil {
			fail(fmt.Errorf("%s: %w", source, err))
		}
	}
	defaulted := 0
	if len(pins) == 0 {
		pins, defaulted = storage.DefaultPins(st, pins)
	}
	if len(pins) == 0 {
		return st, 0
	}
	return &storage.Pinned{Inner: st, Pins: pins, Only: "HEAD"}, defaulted
}
