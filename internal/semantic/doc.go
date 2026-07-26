// Package semantic holds the build-tagged neural adapter for the retrieval
// ladder's semantic rung (ADR-020). The pure default ranker (term-idf) lives in
// internal/query and needs nothing; this package adds a query.SemanticRanker
// backed by a config-driven embeddings endpoint, compiled only with
// `-tags neural` (ADR-018: the pure core carries no model or network path —
// even a stdlib-only network call is an external dependency in spirit).
package semantic
