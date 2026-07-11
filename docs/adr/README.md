# Architecture Decision Records

This log lists every ADR reponite refers to and **where it lives**. Not every
ADR is a standalone file: the agent-facing feature decisions (014–019) are
embedded inline in [`../agent-features.md`](../agent-features.md) next to the
spec they justify, and a few early platform decisions are captured inline in the
[ADR-000 interface index](ADR-000-interface-index.md) as the seams they govern.
The numbering is therefore intentionally sparse as standalone files — it is not
a set of missing documents.

| ADR | Title | Status | Location |
|-----|-------|--------|----------|
| 000 | Interface index (living document) | living | [`ADR-000-interface-index.md`](ADR-000-interface-index.md) |
| 006 | Bounded transitive-closure traversal (`GraphStore`) | planned seam | inline in [ADR-000](ADR-000-interface-index.md) |
| 007 | Pluggable `Embedder` (bundled \| ollama \| remote) | partly shipped (ADR-020) | inline in [ADR-000](ADR-000-interface-index.md); see [ADR-020](ADR-020-semantic-search-layer.md) |
| 012 | `Authenticator` seam (token/mTLS now, OIDC later) | planned seam | inline in [ADR-000](ADR-000-interface-index.md) |
| 013 | `WorkQueue` seam (in-memory now, Redis/NATS later) | planned seam | inline in [ADR-000](ADR-000-interface-index.md) |
| 014 | Agent-shaped bundle reads with token-budgeted assembly (`brief`) | shipped | [`../agent-features.md` §ADR-014](../agent-features.md) |
| 015 | Root-cause via the three-hash frontier + stack-trace seeding | shipped | [`../agent-features.md` §ADR-015](../agent-features.md) |
| 016 | Cross-repo impact via a name/path/signature external-reference index | shipped | [`../agent-features.md` §ADR-016](../agent-features.md) |
| 017 | Promote linkage-only intent to the critical path; defer summaries | shipped (linkage) | [`../agent-features.md` §ADR-017](../agent-features.md) |
| 019 | Trigram lexical layer as the retrieval ladder's base (`grep`) | shipped | [`../agent-features.md` §ADR-019](../agent-features.md) |
| 018 | Pure core / thin build-tagged adapters | shipped (invariant 6) | [`ADR-018-pure-core-thin-adapters.md`](ADR-018-pure-core-thin-adapters.md) |
| 020 | Semantic search layer (pluggable embedder) | shipped (default `TermEmbedder`) | [`ADR-020-semantic-search-layer.md`](ADR-020-semantic-search-layer.md) |

Numbers 001–005, 008–011 were consolidated into the base architecture spec
(`../architecture.md`) rather than split into separate records; they are listed
here only so the sequence reads as deliberate.

## Conventions

- A change that alters a **public seam** (an interface in the ADR-000 index) or
  an **invariant** (see [CLAUDE.md](../../CLAUDE.md)) should add or amend an ADR.
- New standalone ADRs continue from the highest number in use; feature-local
  decisions may instead be embedded next to their spec in `../agent-features.md`,
  and must be added to the table above either way.
