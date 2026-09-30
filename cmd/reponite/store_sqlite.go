//go:build sqlite

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

const dbRel = ".reponite/index.db"

// storeDirEnv relocates every index out of the repos themselves:
// $REPONITE_STORE_DIR/<repo>/index.db. Indexing a git revision (--git) only
// READS the repo's object store, so with this set a clone that must not be
// written to — a teammate's checkout, a shared workspace — is never touched.
const storeDirEnv = "REPONITE_STORE_DIR"

// dbPathFor is where the index of the repo at baseDir lives.
func dbPathFor(baseDir string) string {
	if d := os.Getenv(storeDirEnv); d != "" {
		return filepath.Join(d, repoName(baseDir), "index.db")
	}
	return filepath.Join(baseDir, dbRel)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "reponite:", err)
	os.Exit(1)
}

func openStore(baseDir string) *sqlite.Store {
	dbPath := dbPathFor(baseDir)
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		fail(err)
	}
	st, err := sqlite.Open(dbPath)
	if err != nil {
		fail(err)
	}
	return st
}

func repoName(dir string) string {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return filepath.Base(dir)
	}
	return filepath.Base(abs)
}
