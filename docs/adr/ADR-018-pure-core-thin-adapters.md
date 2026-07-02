# ADR-018 — Pure, dependency-free core behind thin external-dependency adapters

## Context
Two forces point the same way. (1) The correctness-critical logic —
canonicalization, the three hashes, the behavior-hash graph pass, manifest/
dedup set operations, the compat verdict, GC mark-sweep — must be exhaustively
testable, because a compat oracle that is ever confidently wrong is worse than
none. (2) The build sandbox has no Go module proxy, so anything importing
external modules (tree-sitter, modernc.org/sqlite, go-git, usearch) cannot be
compiled or tested there.

## Decision
Keep all correctness-critical logic in **pure, standard-library-only** packages
that depend on narrow interfaces (`AST`, `Store`, `GraphStore`, `Embedder`).
Implement the external-dependency pieces as **thin adapters** behind those
interfaces (parsing, SQLite, git, vectors). Unit-test the core in-sandbox with
in-memory fakes; compile and integration-test the adapters on a real machine
(and in CI, which has full module access).

## Consequences
- The moat's logic is verified continuously in-sandbox against fakes.
- Adapters stay small and mechanical, so deferring their on-machine compilation
  is low-risk.
- This is also just good design: the same seams enable the Cozo/Ollama/Redis
  escape hatches (ADR-006/007/013) and keep the binary honest.
