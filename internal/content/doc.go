// Package content implements canonicalization and content-addressing — the
// foundation of reponite's identity model (architecture §4–6).
//
// Pure core (dependency-free, unit-tested in-sandbox):
//   canon.go     — versioned AST canonicalization over a language-agnostic AST
//                  interface (norm_ver); rule table decides keep/normalize/
//                  drop/recurse per node type.
//   hash.go      — the three-hash identity model: symbol_hash (textual),
//                  signature_hash (API shape), behavior_hash (Merkle over the
//                  resolved call graph), plus embed/edge/manifest hashes.
//   blobstore.go — content-addressed blob storage keyed by content hash.
//
// canon() consumes an AST via an interface defined here; the concrete
// tree-sitter adapter lives behind it in internal/processing (ADR-018).
package content
