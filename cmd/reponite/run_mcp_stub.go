//go:build !sqlite || !mcp

package main

func mcpCommand(args []string) { notImplemented("mcp") }
