<div align="center">

# Reponite

[![CI](https://github.com/vishwak02/reponite/actions/workflows/go.yml/badge.svg)](https://github.com/vishwak02/reponite/actions/workflows/go.yml)
[![Release](https://img.shields.io/github/v/release/vishwak02/reponite?color=00ADD8)](https://github.com/vishwak02/reponite/releases)
[![Go 1.22+](https://img.shields.io/badge/go-1.22%2B-00ADD8.svg)](go.mod)
[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**Code intelligence for AI agents — across every branch, tag, and repo you care about.**

*One small binary. No cloud. No LLM required.*

</div>

---

## The one thing no other tool does

Every code search tool can tell you *where* something lives.  
Reponite tells you *whether it changed* — and **whether that change broke anyone**.

```sh
reponite compat Charge
```

```json
{
  "symbol": "billing.Charge",
  "verdicts": [
    {
      "ref": "prod",
      "verdict": "behavior_changed",
      "confidence": 0.9,
      "changed_callees": ["~validateCard"],
      "detail": "identical signature; resolved call graph differs"
    },
    { "ref": "v1.0.0", "verdict": "absent", "confidence": 1 }
  ]
}
```

That `behavior_changed` verdict — with honest confidence — is something nothing else computes.

---

## Why it matters

Structural code search is a commodity. Reponite does that, then goes further:

| Question | Other tools | Reponite |
|---|---|---|
| Where is `Charge` defined? | ✅ | ✅ |
| Did its signature change between `v1` and `main`? | ❌ | ✅ |
| Did its *behavior* change even if the signature didn't? | ❌ | ✅ |
| Which services across the fleet still expect the old shape? | ❌ | ✅ |
| What's the root cause — the actual mutation site, not just the symptom? | ❌ | ✅ |

---

## Install

```sh
# One-liner (Linux/macOS — no Go toolchain needed):
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh

# Or build from source (Go 1.22+):
make cli   # → bin/reponite
```

---

## Core commands

```sh
# Index your repo (Go, Python, JS, TS, Java, ROS)
reponite index .
reponite index . v2.3.0          # index a specific tag/branch/commit

# The Oracle: did this symbol break anything?
reponite compat Charge           # verdict across every indexed ref

# Root cause: what actually changed and why?
reponite rootcause Charge v1 HEAD

# Editing brief: everything you need to touch a symbol, in one bundle
reponite brief Charge

# Cross-repo impact: who across the fleet calls this?
reponite ximpact getUserV2

# Search (three rungs of the same ladder)
reponite grep validateCard       # exact/regex, result fused with its symbol
reponite semsearch "where we charge a card"   # semantic — no model or network
reponite diff v0.1.0 HEAD --changed-only

# CI gate: fail on any exported API break
reponite ci-check --base main --head HEAD

# Web dashboard
reponite serve .                 # → http://127.0.0.1:8899
```

---

## Built for AI agents (MCP)

Reponite exposes **11 MCP tools** so an agent can reach for the cheapest rung first — and often skip reading files entirely.

```sh
reponite setup .    # register as MCP server (Claude, Cursor, Windsurf, ...)
reponite mcp .      # what the agent runs; auto-indexes HEAD on first mount
```

The retrieval ladder an agent sees:

```
grep → structural → semantic → intent → compat
 ↑                                        ↑
cheapest                              most powerful
(exact match, no tokens wasted)     (cross-ref behavior verdicts)
```

Every response is **token-budgeted** and carries a `_meta` block with confidence, freshness, and provenance — so the agent knows exactly how much to trust it.

---

## The four verdicts

The **Compatibility Oracle** answers one question across every indexed ref and repo:

| Verdict | What it means |
|---|---|
| `compatible` | same shape *and* same behavior |
| `shape_changed` | signature differs → **API break** |
| `behavior_changed` | signature identical, but the call graph underneath changed → **silent regression** |
| `absent` | symbol doesn't exist at that ref |

Every verdict comes with **confidence and its provenance**. Reponite never claims more certainty than it computed.

---

## How it works (briefly)

Three hashes, computed over every symbol in every indexed ref:

- **`symbol_hash`** — did the code text change? (storage dedup key)
- **`signature_hash`** — did the API shape change?
- **`behavior_hash`** — did the resolved call graph change? (Merkle hash: a callee's change propagates to every transitive caller)

The behavior hash is what makes `behavior_changed` possible. It's also what makes root-cause cheap: a symbol whose `symbol_hash` changed is a **mutation site**; a symbol whose `behavior_hash` alone changed is merely **carried along**. Walk the frontier, find the origin.

---

## Languages supported

Go · Python · JavaScript · TypeScript · Java · **ROS** (`.msg` / `.srv` / `.action` — indexed as typed contracts)

---

## Architecture (the 30-second version)

```
  CLI  ·  MCP server  ·  web dashboard  ·  VS Code extension
                        │
  ┌─────────────────────▼───────────────────────────────────┐
  │  PURE CORE  (stdlib only, zero CGO)                      │
  │  canon() · three-hash identity · behavior-hash Merkle    │
  │  Compat Oracle · diff · rootcause · grep · brief         │
  └─────────────────────┬───────────────────────────────────┘
                        │  thin build-tagged adapters
          tree-sitter (CGO)  ·  SQLite  ·  go-git  ·  go/types
```

The correctness-critical core is **pure Go, standard-library only** — no external dependencies, no CGO, exhaustively unit-tested. External tools live in thin, isolated adapters. See [ADR-018](docs/adr/ADR-018-pure-core-thin-adapters.md).

---

## Contributing

```sh
go build ./...    # pure core — no deps, builds anywhere
go test ./...     # 95+ unit tests
make cli          # full binary (all adapters)
make sqlite | make treesitter | make mcp | make e2e   # per-adapter (mirrors CI)
```

Open an [issue](https://github.com/vishwak02/reponite/issues) to ask a question or propose a change. Keep all CI jobs green. The invariants in [CLAUDE.md](CLAUDE.md) are load-bearing — please read them before sending a PR.

---

## More

- [Architecture deep-dive](docs/architecture.md)
- [Agent-facing features](docs/agent-features.md) — brief, root-cause, cross-repo impact, retrieval ladder
- [Architecture Decision Records](docs/adr/)

---

## License

[Apache-2.0](LICENSE) © reponite contributors
