// Package storage holds the index: the in-memory store used by tests and the
// demo, the SQLite store (subpackage sqlite) behind the build tag, and the
// views composed over them — the fleet multi-store that answers across every
// registered repo, per-repo ref pins (REPONITE_REFS) and the test-code filter
// (REPONITE_NO_TESTS). Query code depends only on the Store interface.
package storage
