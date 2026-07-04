//go:build sqlite && treesitter

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/vishwak02/reponite/internal/processing"
	"github.com/vishwak02/reponite/internal/version"
)

// watchCommand indexes HEAD once, then re-indexes on source-file changes
// (any indexed language, debounced), so a mounted `reponite mcp` server
// (reading via SQLite WAL) always serves fresh results without a manual
// `reponite index`.
func watchCommand(args []string) {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}
	repo := repoName(dir)
	st := openStore(dir)
	defer st.Close()

	var mu sync.Mutex
	reindex := func() {
		mu.Lock()
		defer mu.Unlock()
		if err := processing.IndexDir(st, repo, "HEAD", dir, version.NormVer); err != nil {
			fmt.Fprintln(os.Stderr, "reindex error:", err)
			return
		}
		fmt.Printf("reindexed %s@HEAD\n", repo)
	}
	reindex() // initial

	w, err := fsnotify.NewWatcher()
	if err != nil {
		fail(err)
	}
	defer w.Close()

	// watch dir + subdirs (fsnotify is non-recursive); skip hidden/vendor.
	_ = filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || !info.IsDir() {
			return nil
		}
		if p != dir {
			if b := info.Name(); strings.HasPrefix(b, ".") || b == "vendor" || b == "node_modules" {
				return filepath.SkipDir
			}
		}
		_ = w.Add(p)
		return nil
	})
	fmt.Printf("watching %s (Ctrl-C to stop)\n", dir)

	var debounce *time.Timer
	for {
		select {
		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			if ev.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
				continue
			}
			if ev.Op&fsnotify.Create != 0 {
				if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
					_ = w.Add(ev.Name)
				}
			}
			if _, ok := processing.RulesForExt(filepath.Ext(ev.Name)); !ok {
				continue
			}
			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(300*time.Millisecond, reindex)
		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			fmt.Fprintln(os.Stderr, "watch error:", err)
		}
	}
}
