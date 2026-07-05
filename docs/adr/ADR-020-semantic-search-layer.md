# ADR-020 — Semantic search layer (retrieval ladder rung 3)

Status: accepted (default embedder shipped; neural embedder deferred)

## Context

The retrieval ladder (agent-features.md §10A.2) defines rung 3 as semantic —
"where is the thing that does X" — between structural search and intent. The
base spec named it but left it unspecified; `content.EmbedHash` (with a
model-version domain separator) existed as a foundation but had no consumer, and
`processing.Embedder` was stubbed until M6. The open question was whether the
first cut requires a model/vector store (network, external deps) or can be pure.

## Decision

Ship a **pluggable `query.Embedder`** (`Embed(text) map[string]float64`) with a
**pure stdlib default, `TermEmbedder`**: identifier-aware tokenization
(camelCase / snake_case split, lowercased) into a term-frequency vector, ranked
by cosine similarity against each symbol's `name + body` (the same source spans
grep/brief use). `SemanticSearch` returns the top-N hits with scores.

No model, no network, no external dependency — the whole layer stays in the pure
core and is unit-tested in-sandbox (ADR-018). A higher-recall neural embedder
(bundled | ollama | remote, keyed by `content.EmbedHash` for cache-on-model-
change) is a drop-in behind the same interface and is deferred until there is
demand; nothing on the critical path requires it.

## Consequences

- The semantic rung works today for the common case (term overlap after
  identifier splitting) with zero setup, and degrades honestly (score 0 ⇒ no
  shared terms), never fabricating relevance.
- It is recall-limited vs embeddings (no synonymy/paraphrase); the pluggable
  seam means upgrading is additive, not a rewrite.
- Surfaces: CLI `reponite semsearch <query> [ref] [--limit N]` and MCP
  `reponite_semsearch`.
