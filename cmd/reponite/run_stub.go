//go:build !sqlite || !treesitter

package main

// indexBackedCommand is the default (no-adapter) stub: index-backed commands
// require the sqlite + treesitter build tags. Build with `make cli`.
func indexBackedCommand(cmd string, args []string) {
	notImplemented(cmd)
}
