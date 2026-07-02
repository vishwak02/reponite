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
→ **compat**. See [agent-features.md](agent-features.md).

## Layers & packages

```
interfaces  →  query  →  storage  →  content
                 ↑          ↑
             processing ────┘   (write path: parse, extract, behavior, index)
```

- `internal/content` — `canon()`, the hashes, manifests/dedup (pure).
- `internal/processing` — behavior-hash pass, extractor, indexer (pure);
  tree-sitter parser + `IndexDir` (build tag `treesitter`).
- `internal/query` — `Store` interface, Oracle/diff/root-cause/grep, coordinators (pure).
- `internal/storage` — in-memory `Store` (pure); `sqlite/` adapter (build tag `sqlite`).
- `internal/interfaces` — JSON `_meta` envelopes.
- `cmd/reponite` — CLI; index-backed commands under `sqlite && treesitter`.

## Build tiers & verification

The pure core compiles and is unit-tested anywhere (no external deps). The
adapters are build-tagged and verified in CI:

| Job | Build | Verifies |
|-----|-------|----------|
| `core` | default | the pure packages (95+ tests) |
| `sqlite` | `-tags sqlite` | the SQLite `Store` adapter |
| `treesitter` | `-tags treesitter` | tree-sitter → `content.AST`, extractor, `IndexDir` |
| `e2e` | `-tags "sqlite treesitter"` | index a real repo across two refs, assert a verdict |

## Status

v1 core complete for Go. Deferred: SCIP edge upgrade (confidence → 1.0), the
`brief`/`rootcause`/`ximpact` MCP tools, intent linkage, freshness
(`watch`/`sync`), the shared team server, and additional Tier-1 languages.
