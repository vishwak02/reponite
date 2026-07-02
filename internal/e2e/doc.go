// Package e2e holds end-to-end integration tests that exercise the full stack
// (tree-sitter parse -> extract -> index -> SQLite store -> Oracle) under the
// `sqlite` and `treesitter` build tags. This untagged file just gives the
// package a buildable file in the default build; the tests are tag-gated.
package e2e
