// store.go defines the read interface the query layer depends on. Following the
// Go idiom "the consumer defines the interface", it lives here in query, so the
// pure query logic (compat/diff/rootcause/grep coordinators) composes with any
// backend without importing a concrete store. Implementations: internal/storage.Mem
// (pure, in-memory, for tests and dev) and internal/storage/sqlite (production,
// on-machine). See ADR-018.
package query

import "github.com/vishwak02/reponite/internal/content"

// Store is the read surface the query layer needs.
type Store interface {
	// Repos lists indexed repositories.
	Repos() []string
	// Refs lists the indexed refs of a repo.
	Refs(repo string) []string
	// SymbolAt returns a symbol's identity at a ref (ref_history); ok is false
	// when the symbol is not recorded at that ref.
	SymbolAt(repo, symbol, ref string) (SymbolRef, bool)
	// SymbolsAt returns every symbol present at a ref, keyed by name (for diff).
	SymbolsAt(repo, ref string) map[string]SymbolRef
	// Snapshot returns the call-graph snapshot at a ref (for root-cause).
	Snapshot(repo, ref string) RefSnapshot
	// Files returns the files at a ref (for grep).
	Files(repo, ref string) []File
	// Manifest returns a ref's content manifest.
	Manifest(repo, ref string) (content.Manifest, bool)
}
