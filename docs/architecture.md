# Reponite — Architecture

A concise overview of how Reponite is designed. For decisions and rationale see
[the ADRs](adr/); for the agent-facing feature designs see
[agent-features.md](agent-features.md).

## Thesis

Structural + semantic code search is a commodity. Reponite matches that and then
owns the dimension nothing else does: it indexes **many refs of many repos** as
content-addressed, deduplicated snapshots, and answers whether a symbol still
**exists**, kept its **shape**, and kept its **behavior** across all of them —
each answer carrying a confidence, never overclaiming.

## Design principles

- **Never lie.** Every edge carries `resolution_method` + `confidence`; every
  answer carries a `_meta` block; a verdict inherits the minimum confidence of
  its evidence. A compatibility oracle that is ever confidently wrong is worse
  than none.
- **Storage proportional to *unique* content.** Content-addressing (Git's model)
  means "index N refs" grows with unique content, not N.
- **Pure core, thin adapters.** All correctness-critical logic is pure and
  standard-library only; external dependencies (SQLite, tree-sitter) live in thin
  adapters behind interfaces ([ADR-018](adr/ADR-018-pure-core-thin-adapters.md)).

## The three-hash identity model

A code-intelligence server must answer three different "is this the same?"
questions, so Reponite computes three hashes over a canonical (`canon()`) form:

- `symbol_hash` — *same text?* Storage dedup key; excludes ref and path, so
  identical code dedupes across refs and survives file moves.
- `signature_hash` — *same API shape?* Body-independent; drives the
  shape-changed verdict.
- `behavior_hash` — *same behavior?* A Merkle hash over the resolved call graph:
  `H(symbol_hash + norm_ver + sorted(callee behavior_hashes))`. A callee's change
  propagates to every transitive caller. This is what makes the behavior-changed
  verdict possible.

`canon()` is a versioned (`norm_ver`), language-aware transform over the AST that
drops formatting and comments (comments feed a separate `doc_hash`) while keeping
identifiers, literals, operators, and structure — conservative by default: when
unsure, keep the difference.

## Content-addressed refs

A ref owns no content — it owns a **manifest**: a set of blob hashes plus
metadata. A manifest diff is a set operation; dedup accounting shows storage
scales with unique content; GC's mark phase is set subtraction. All pure
(`internal/content/manifest.go`).

## The Compatibility Oracle

A compat query is a pure comparison over per-ref symbol history — absent /
shape-changed / behavior-changed / compatible — never a re-analysis. Fused across
a fleet, it answers "which deployed services still expect the old shape/behavior."
Root-cause drill-down then walks the call graph to the *mutation-site frontier*
(the symbols whose own text/signature/edges changed) versus symbols merely
carried along by a callee — a distinction only the three-hash model makes cheap.

## The retrieval ladder

Reponite is the single retrieval interface for an agent, exposing the cheapest
rung that answers a question: **grep** (trigram-prefiltered literal/regex, each
hit fused with its enclosing symbol) → **structural** → **semantic** → **intent**
→ **compat**. Semantic ranking sits behind the `SemanticRanker` seam: the pure
term/IDF ranker is the default, and a build-tagged neural adapter can replace it,
with every result naming the ranker that produced it (ADR-020). See
[agent-features.md](agent-features.md).

## Crossing the repo boundary

`symbol_hash` embeds the repo, so it deliberately cannot match across repos —
correct for storage, useless for linkage. Cross-repo edges are therefore resolved
in three labeled tiers, highest first:

| Tier | Method | Confidence | Basis |
|---|---|:---:|---|
| 0 | `scip-resolved` | 0.95 | A SCIP moniker: a globally unique symbol identity two indexers agree on |
| 1 | `import-resolved` | 0.75 | The caller's import binding resolves to the target's module **root** |
| 2 | `unresolved-external` | 0.6 | Bare-name match — the honest fallback |

Tier 1 matches a module root against an import path (`<root>/pkg/user`), never by
exact equality; tier 0 needs no module comparison at all, because a moniker either
is the symbol or it is not. Each caller also carries `expected_signature`, comparing
the contract captured when that caller was indexed against the target's current
signature — "which callers still expect the old shape" (§8B.3).

## Layers & packages

```
interfaces  →  query  →  storage  →  content
                 ↑          ↑
             processing ────┘   (write path: parse, extract, behavior, index)
```

- `internal/content` — `canon()`, the hashes, manifests/dedup (pure).
- `internal/processing` — behavior-hash pass, extractor, indexer, index-time
  exclusion (pure); tree-sitter parser + `IndexDir`/`IndexGitRef` (build tag
  `treesitter`).
- `internal/query` — `Store` interface, Oracle/diff/root-cause/grep/semantic/
  ximpact/topics, coordinators (pure).
- `internal/storage` — in-memory `Store` and the `MultiStore` fleet aggregator
  (pure); `sqlite/` adapter (build tag `sqlite`).
- `internal/scip` — SCIP index reader: a standard-library protobuf subset that
  yields symbol monikers for cross-repo edges (pure, no build tag — it is only
  file parsing).
- `internal/fleet` — the persistent cross-run repo registry `serve`/`mcp` mount
  by default (pure).
- `internal/semantic` — neural embeddings client for the `SemanticRanker` seam
  (build tag `neural`; tagged because it opens a network path, not because of a
  dependency — it is standard library only).
- `internal/interfaces` — JSON `_meta` envelopes, MCP dispatch, web dashboard.
- `cmd/reponite` — CLI; index-backed commands under `sqlite && treesitter`.

## Build tiers & verification

The pure core compiles and is unit-tested anywhere (no external deps). The
adapters are build-tagged and verified in CI:

| Job | Build | Verifies |
|-----|-------|----------|
| `core` | default | the pure packages (205 tests) + the `neural` adapter, which is standard-library only |
| `sqlite` | `-tags sqlite` | the SQLite `Store` adapter, including its migrations |
| `treesitter` | `-tags treesitter` | tree-sitter → `content.AST`, extractor, `IndexDir` |
| `mcp` | `-tags "sqlite mcp"` | the MCP server builds against the SDK |
| `e2e` | `-tags "sqlite treesitter"` | index a real repo across two refs, assert a verdict |

## Status

Every item on the original roadmap has shipped: the three-hash core and the four
verdicts, root cause (including stack-trace seeding), the editing brief,
cross-repo impact through all three resolution tiers with per-caller contract
skew, the retrieval ladder (grep → structural → semantic → compat) with an
optional neural ranker, the ROS communication graph, verify-edit, index-time
exclusion, the persistent fleet registry, and nine languages.

Known limitations, all reported in the output rather than hidden:

- Cross-repo **behavior** propagation is out of scope — impact answers *who
  calls*, not *does it still behave the same* over there (§8.4).
- RPC/HTTP/queue calls between services are invisible; this is source-call-graph
  impact (§8B.5). The ROS graph is the one runtime-edge exception.
- ROS launch-file/namespace remapping is not resolved, and a non-literal topic
  name is counted as unresolved rather than guessed (§8D.4).
- SCIP's *relationship* graph (implementations, overrides) is not consumed yet —
  only definitions and references.
