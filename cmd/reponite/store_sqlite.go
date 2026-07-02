//go:build sqlite

package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/vishwak02/reponite/internal/storage/sqlite"
)

const dbRel = ".reponite/index.db"

func fail(err error) {
	fmt.Fprintln(os.Stderr, "reponite:", err)
	os.Exit(1)
}

func openStore(baseDir string) *sqlite.Store {
	dbPath := filepath.Join(baseDir, dbRel)
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
