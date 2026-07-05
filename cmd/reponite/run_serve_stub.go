//go:build !(sqlite && treesitter)

package main

func serveCommand(args []string) { notImplemented("serve") }
