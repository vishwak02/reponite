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

No model, no network, no external dependency — the default stays in the pure
core and is unit-tested in-sandbox (ADR-018).

**Revision (2026-07-19): the seam moved up a level.** The original `Embedder`
(text → sparse term vector) could not host a dense neural model, because
`SemanticSearch` applies IDF weighting *after* embedding and IDF is a property of
the whole corpus, not of one text. The strategy seam is therefore
`query.SemanticRanker` (`RankerName() string`, `Rank(query, docs, limit)`), with
`TermIDFRanker` wrapping the original TF×IDF+cosine logic as the default;
`Embedder`/`TermEmbedder` remain as that ranker's inner tokenizer seam.

The neural adapter ships in `internal/semantic` behind `-tags neural`: an
OpenAI-compatible `/v1/embeddings` client (Ollama, OpenAI, LiteLLM, vLLM),
configured by `REPONITE_EMBED_ENDPOINT` / `REPONITE_EMBED_MODEL`. It is tagged
because it opens a **network path**, not because of a dependency — it is standard
library only. Embeddings are cached by `(model, sha256(text))` so a long-lived
`serve`/`mcp` embeds each symbol once.

Two honesty properties are part of the contract: every result carries the
`ranker` that actually produced it, and a failing adapter falls back to
`term-idf` with the failure recorded in `note` — a degraded ranking is never
presented as a normal one.

## Consequences

- The semantic rung works today for the common case (term overlap after
  identifier splitting) with zero setup, and degrades honestly (score 0 ⇒ no
  shared terms), never fabricating relevance.
- It is recall-limited vs embeddings (no synonymy/paraphrase); the pluggable
  seam means upgrading is additive, not a rewrite.
- Surfaces: CLI `reponite semsearch <query> [ref] [--limit N]` and MCP
  `reponite_semsearch`.
