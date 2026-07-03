# Reponite

[![CI](https://github.com/vishwak02/reponite/actions/workflows/go.yml/badge.svg)](https://github.com/vishwak02/reponite/actions/workflows/go.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)

**Ref-aware code intelligence for AI agents and teams.** One small binary, local or shared, honest about what it knows.

Structural and semantic code search is a commodity — several tools do it well. Reponite matches that layer and then owns the dimension none of them address: it indexes **many refs** (tags, branches, deployed commits) of **many repositories** as content-addressed, deduplicated snapshots, and can tell you whether a symbol still **exists**, kept its **shape**, and kept its **behavior** across all of them.

## The moat: the Compatibility Oracle

Given a symbol at one ref, Reponite returns — for every other ref and repo — exactly one of:

- **absent** — it isn't there.
- **shape-changed** — present, but the signature differs (an API break).
- **behavior-changed** — identical signature, but the *resolved call graph underneath differs* (a dependency was patched or regressed). Same interface, different behavior.
- **compatible** — same shape and behavior.

That third tier is the whole point, and nothing else computes it. It falls out of a Merkle `behavior_hash` over the call graph: a callee's change propagates to every transitive caller. Every verdict carries a confidence and its provenance — the Oracle never claims more certainty than it actually computed.

## Quickstart

```sh
# one-line install (Linux/macOS, no Go needed):
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
# or build from source (needs Go 1.22+ and a C toolchain):
make cli                               # builds bin/reponite (all adapters)

reponite demo                          # in-memory end-to-end run — no repo needed
reponite index .                       # index this repo's Go files at HEAD
reponite index . v2.3.0                # index another ref
reponite compat Charge                 # Charge's compatibility across indexed refs
reponite grep validateCard             # trigram search; each hit fused with its symbol

# use it from an AI agent (dogfooding):
reponite setup .                       # register reponite as an MCP server for your agent
reponite mcp .                         # (what the agent runs) serve the tools over stdio
```

`reponite demo` prints the flagship verdict as JSON:

```json
{
  "symbol": "Charge",
  "verdicts": [
    { "repo": "billing", "ref": "prod", "verdict": "behavior_changed",
      "confidence": 1, "detail": "identical signature; resolved call graph differs" },
    { "repo": "billing", "ref": "v1.0.0", "verdict": "absent", "confidence": 1 }
  ],
  "_meta": { "ref": "HEAD", "warnings": ["billing@v1.0.0 not indexed"] }
}
```

## The retrieval ladder

Reponite is meant to be the single interface an agent uses for *anything* about the code — it exposes a spectrum, cheapest rung first: **grep** (trigram-prefiltered literal/regex, each hit fused with its enclosing symbol) → **structural** (callers/callees/impact) → **semantic** → **intent** → **compat**. Grep alone often lets an agent skip reading files entirely. See [docs/agent-features.md](docs/agent-features.md).

## How it's built

The correctness-critical core is **pure Go, standard-library only** — canonicalization, the three-hash identity model, the behavior-hash graph pass, the Oracle verdicts, diff, root-cause, grep, content-addressed dedup, and the query coordinators — and is exhaustively unit-tested (95+ tests). The external-dependency pieces are **thin adapters** behind interfaces: a SQLite store (`modernc.org/sqlite`, pure Go, no CGO) and a tree-sitter parser. See [ADR-018](docs/adr/ADR-018-pure-core-thin-adapters.md).

CI verifies every layer independently — `core` (the pure packages), `sqlite`, `treesitter`, and `e2e` (a real repo indexed across two refs, asserting a `behavior_changed` verdict end to end).

## Layout

```
cmd/reponite/            CLI (version, demo; index/compat/diff/grep/search under build tags)
internal/content/        canon() + the three hashes + content-addressed manifests
internal/processing/     behavior-hash graph pass, extractor, indexer, tree-sitter parser
internal/query/          Store interface, Oracle (compat), diff, root-cause, grep, coordinators
internal/storage/        in-memory Store; sqlite/ SQLite adapter
internal/interfaces/     JSON output envelopes
docs/                    architecture, agent features, ADRs, adapter contracts
```

## Docs

- [Architecture overview](docs/architecture.md)
- [Agent-facing features](docs/agent-features.md) — editing brief, root-cause drill-down, cross-repo impact, grep retrieval ladder
- [Architecture Decision Records](docs/adr/)
- [Adapter contracts](docs/adapters/)
- [PROGRESS.md](PROGRESS.md) — the running build log

## Status & limitations

The v1 core is complete and CI-verified for **Go**: identity → behavior → Oracle → retrieval → content-addressing → indexer → CLI. Honest limitations today: one Tier-1 language (Go); cross-file call edges are name-based (medium confidence) until SCIP resolution lands; freshness (`watch`/`sync`), the shared team server, and additional languages are on the roadmap (see PROGRESS.md).

## License

[Apache-2.0](LICENSE).
