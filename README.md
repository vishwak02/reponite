<div align="center">

# Reponite

**Ref-aware code intelligence for AI agents and teams.**
One small binary that knows whether a symbol still *exists*, kept its *shape*, and kept its *behavior* — across every ref and repo you index.

[![CI](https://github.com/vishwak02/reponite/actions/workflows/go.yml/badge.svg)](https://github.com/vishwak02/reponite/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/vishwak02/reponite?color=00ADD8)](https://github.com/vishwak02/reponite/releases)
[![Go](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

</div>

---

Structural and semantic code search is a commodity — plenty of tools do it well. Reponite matches that layer and then owns the dimension none of them address: it indexes **many refs** (tags, branches, deployed commits) of **many repositories** as content-addressed, deduplicated snapshots, and answers questions *across* them — most importantly, **did this change break anyone?**

It runs as a **CLI**, an **MCP server** an AI agent mounts, a **web dashboard**, and a **VS Code extension** — all over the same pure core.

## The moat — the Compatibility Oracle

Given a symbol at one ref, Reponite returns, for every other ref and repo, exactly one verdict:

| Verdict | Meaning |
|---|---|
| `absent` | it isn't there |
| `shape_changed` | present, but the signature differs — an **API break** |
| `behavior_changed` | identical signature, but the **resolved call graph underneath differs** — same interface, different behavior (a dependency was patched or regressed) |
| `compatible` | same shape *and* behavior |

That third tier is the whole point, and nothing else computes it. It falls out of a Merkle **`behavior_hash`** over the call graph: a callee's change propagates to every transitive caller. Every verdict carries a **confidence and its provenance** — the Oracle never claims more certainty than it actually computed (a stdlib call it can't see caps the floor; a type-checker-proven edge is `1.0`).

```jsonc
// reponite compat Charge
{
  "symbol": "billing.Charge",
  "verdicts": [
    { "ref": "prod",   "verdict": "behavior_changed", "confidence": 0.9,
      "changed_callees": ["~validateCard"],
      "detail": "identical signature; resolved call graph differs" },
    { "ref": "v1.0.0", "verdict": "absent", "confidence": 1 }
  ],
  "_meta": { "ref": "HEAD" }
}
```

## Features

- **Multi-language** — Go, Python, JavaScript, TypeScript, Java, plus **ROS** interface files (`.msg`/`.srv`/`.action`) indexed as typed contracts.
- **Compatibility Oracle** — `absent` / `shape_changed` / `behavior_changed` / `compatible` across refs and repos, with honest confidence.
- **Root-cause drill-down** — walk a behavior change down to its mutation site; seed it straight from a **pasted stack trace** (`rootcause-trace`).
- **Editing brief** — one token-budgeted bundle (body + callees + callers + covering tests + compat snapshot) that replaces 5–6 file reads for an agent.
- **Cross-repo impact** (`ximpact`) — who across the fleet calls this symbol, and whether its **contract changed**.
- **Retrieval ladder** — `grep` (trigram + regex, each hit fused with its enclosing symbol) → structural → **semantic** (`semsearch`, no model needed) → intent → compat.
- **CI gate** (`ci-check`) — non-zero exit on any exported API break, ready to drop into a PR workflow.
- **Surfaces** — CLI · MCP server (11 tools) · web dashboard (`serve`) · VS Code extension · shared **team/fleet** view across many repos.

## Install

```sh
# prebuilt binary (Linux/macOS, no toolchain needed):
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh

# or from source (Go 1.22+ and a C toolchain for the tree-sitter adapter):
make cli        # → bin/reponite
```

## Quickstart

```sh
reponite index .                     # index HEAD (Go/Python/JS/TS/Java + ROS .msg/.srv/.action)
reponite index . v2.3.0              # index another ref (tag / branch / commit)
reponite compat Charge               # compatibility across every indexed ref (+ changed_callees)
reponite brief Charge                # everything needed to edit Charge, in one bundle
reponite rootcause Charge v1 HEAD    # drill a behavior change to its mutation site
reponite diff v0.1.0 HEAD --changed-only --package internal/query
reponite ci-check --base main --head HEAD    # exit non-zero on any exported API break
reponite grep validateCard           # trigram search, each hit fused with its symbol
reponite semsearch "where we charge a card"  # semantic search — no model/network
reponite ximpact getUserV2           # who across indexed repos depends on this symbol
reponite serve .                     # web dashboard → http://127.0.0.1:8899  (serve a b c = team view)
```

## Use it from an AI agent (MCP)

Reponite is designed to be the single interface an agent reaches for — cheapest rung first, so it can often skip reading files entirely.

```sh
reponite setup .                     # register as an MCP server (--client claude-desktop|claude-code|cursor|windsurf)
reponite mcp .                       # what the agent runs; self-indexes HEAD on first mount
```

It exposes 11 tools: `search`, `grep`, `compat`, `context`, `diff`, `rootcause`, `rootcause_trace`, `brief`, `ximpact`, `semsearch`, `refs`.

## Architecture

The correctness-critical core is **pure Go, standard-library only**, and exhaustively unit-tested in isolation. Everything that touches the outside world is a **thin adapter behind an interface**, compiled in via build tags — so the core stays dependency-free, deterministic, and fast to test ([ADR-018](docs/adr/ADR-018-pure-core-thin-adapters.md)).

```
   surfaces      CLI  ·  MCP server (stdio)  ·  web dashboard (serve)  ·  VS Code
                                        │  query coordinators
 ┌──────────────────────────────────────▼─────────────────────────────────────┐
 │  PURE CORE  (stdlib only, no CGO)                                            │
 │  canon() · three-hash identity · behavior-hash Merkle pass · Compat Oracle   │
 │  diff · rootcause · grep · brief · ximpact · semantic · Store interface      │
 └──────────────────────────────────────┬─────────────────────────────────────┘
                                         │  build-tagged adapters
              tree-sitter (CGO)  ·  SQLite (pure-Go)  ·  go-git  ·  go/types
```

```
cmd/reponite/         CLI entry point
internal/
  content/            canon() + the three hashes + content-addressed manifests
  processing/         extractor, behavior-hash pass, tree-sitter/ROS/git indexers, go/types resolver
  query/              Store interface, Oracle, diff, rootcause, grep, brief, ximpact, semantic, intent
  storage/            in-memory + SQLite stores, MultiStore fleet aggregator
  interfaces/         JSON output, MCP server, web dashboard
editors/vscode/       VS Code extension
docs/adr/             architecture decision records
```

CI verifies every layer independently: `core` (the pure packages), `sqlite`, `treesitter`, `mcp`, and `e2e` (a real repo indexed across two refs, asserting a `behavior_changed` verdict end to end).

## Documentation

- [Architecture overview](docs/architecture.md)
- [Agent-facing features](docs/agent-features.md) — editing brief, root-cause drill-down, cross-repo impact, retrieval ladder
- [Build plan & roadmap](docs/BUILD_PLAN.md)
- [Architecture Decision Records](docs/adr/)

## Status & roadmap

**Shipped** (CI-verified, `v0.2.0`): five languages + ROS contracts · the Oracle · brief · root-cause (+ stack-trace seeding) · cross-repo impact with contract fusion · the retrieval ladder incl. semantic search · `ci-check` · web dashboard · VS Code extension · MCP server · shared team/fleet read view.

**On the roadmap:** a persistent cross-repo registry (`module_path` capture + `global.db` + per-caller signature-skew) for full fleet queries, and SCIP integration to raise cross-file edge confidence to `1.0` for non-Go languages.

## License

[Apache-2.0](LICENSE)
