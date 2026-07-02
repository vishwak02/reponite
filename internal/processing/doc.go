// Package processing is the write path: parsing, cross-file resolution, the
// behavior-hash pass, delta processing, ref indexing, and embedding
// (architecture §6.3, §7, §12).
//
// The behavior-hash pass (behavior.go: reverse-topological, SCC-condensed,
// memoized) is pure core over resolved edges and is unit-tested in-sandbox.
// Adapters that pull external deps are thin: parser.go (tree-sitter), scip.go
// (SCIP indexers), embedder.go (bundled/Ollama/remote). See ADR-018.
package processing
