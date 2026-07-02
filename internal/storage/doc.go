// Package storage owns persistence: the SQLite relational/graph/FTS store, the
// content-addressed vector store + per-ref HNSW graphs, per-ref manifests, and
// the ref/global registries (architecture §4, §9, §11).
//
// The store is exposed behind a Store interface (pure-core code depends only on
// the interface); the concrete modernc.org/sqlite + usearch adapters are thin
// and built on a real machine (ADR-018). Planned files: schema.go migrate.go
// sqlite.go nodes.go edges.go symref.go paths.go files.go manifest.go
// registry.go globalreg.go vectors.go graphs.go gc.go.
package storage
