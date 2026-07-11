<p align="center">
  <img src="https://img.shields.io/badge/reponite-code%20intelligence-00ADD8?style=for-the-badge&logo=go&logoColor=white" alt="reponite" height="60"/>
</p>

<h3 align="center">Code intelligence for AI agents — across every branch, tag, and repo.</h3>

<p align="center">
  <a href="https://github.com/vishwak02/reponite/actions/workflows/go.yml"><img src="https://github.com/vishwak02/reponite/actions/workflows/go.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vishwak02/reponite/releases"><img src="https://img.shields.io/github/v/release/vishwak02/reponite?color=00ADD8" alt="Release"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.22%2B-00ADD8.svg" alt="Go 1.22+"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
</p>

<p align="center">
  <a href="#install">Install</a> •
  <a href="#quickstart">Quickstart</a> •
  <a href="#ai-agents-mcp">MCP / AI Agents</a> •
  <a href="#how-it-works">How it works</a> •
  <a href="#contributing">Contributing</a>
</p>

---

**Reponite** is a single binary that indexes your codebase across refs (tags, branches, commits) and answers the question every developer and AI agent eventually needs to ask:

> *"Did this symbol change — and did that change break anyone?"*

```sh
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
reponite index .
reponite compat Charge
```

```json
{
  "symbol": "billing.Charge",
  "verdicts": [
    { "ref": "prod",   "verdict": "behavior_changed", "confidence": 0.9,
      "changed_callees": ["~validateCard"],
      "detail": "identical signature; resolved call graph differs" },
    { "ref": "v1.0.0", "verdict": "absent", "confidence": 1 }
  ]
}
```

---

## Why Reponite

Most code tools answer *"where is it?"*  
Reponite answers *"is it still the same — and if not, who is broken?"*

| | Other tools | Reponite |
|---|:---:|:---:|
| Find where a symbol is defined | ✅ | ✅ |
| Did its API signature change between two refs? | ❌ | ✅ |
| Did its *behavior* change even if the signature didn't? | ❌ | ✅ |
| Which services across the fleet still expect the old shape? | ❌ | ✅ |
| Root cause — actual mutation site, not just the symptom? | ❌ | ✅ |
| Works across branches, tags, and multiple repos? | ❌ | ✅ |

---

## Install

**One-liner** (Linux / macOS, no Go toolchain needed):

```sh
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
```

**From source** (Go 1.22+):

```sh
make cli   # → bin/reponite
```

---

## Quickstart

```sh
# 1. Index your repo
reponite index .                      # indexes HEAD
reponite index . v2.3.0              # index a tag, branch, or commit

# 2. Check if a symbol is still compatible across refs
reponite compat Charge

# 3. Find the root cause of a behavior change
reponite rootcause Charge v1 HEAD

# 4. Get everything you need to safely edit a symbol
reponite brief Charge

# 5. Who across the fleet depends on this symbol?
reponite ximpact getUserV2
```

---

## Features

- 🔍 **Compatibility Oracle** — `absent` / `shape_changed` / `behavior_changed` / `compatible` across every indexed ref and repo, with honest confidence scores
- 🧬 **Root-cause drill-down** — walks a behavior change to its exact mutation site; can be seeded directly from a pasted stack trace
- 📦 **Editing brief** — one token-budgeted bundle (body + callees + callers + tests + compat snapshot) replacing 5–6 file reads for an agent
- 🌐 **Cross-repo impact** — who across the fleet calls a symbol, and whether their expected contract still matches (module-path precise)
- 🧭 **Investigate** — ask "how does X work?" in plain language, get one cited dossier of the relevant symbols fleet-wide (replaces the search→brief→context loop)
- 🎯 **Usages** — every call site of a symbol with its exact line + enclosing function, cross-checked against the call graph (`confirmed` vs lexical)
- 💥 **Blast radius** — before an edit, the in-repo + fleet callers, covering tests, and cross-ref contract state in one call
- 🤖 **ROS topic graph** — the runtime edges no call graph contains: pairs publishers with subscribers (and service/action clients with servers) by name across the fleet, so you can answer "who reacts when I publish `/cmd_vel`?" — roscpp · rospy · rclcpp · rclpy
- 🚀 **Fleet-aware** — mount many repos at once; `search`/`grep`/`semsearch` default fleet-wide, and misses return "did you mean …?" instead of empty
- 🔎 **Retrieval ladder** — `grep` (trigram + regex, fused with enclosing symbol) → structural → semantic (IDF-ranked, no model needed) → compat
- 🚦 **CI gate** — `ci-check` exits non-zero on any exported API break (per-language "exported" rule), drops straight into a PR workflow
- 🗣️ **Multi-language** — Go, Python, JavaScript, TypeScript, Java, C, C++, Rust, and **ROS** interface files (`.msg`/`.srv`/`.action`)
- 📡 **Four surfaces** — CLI · MCP server (16 tools) · web dashboard · VS Code extension

---

## The four verdicts

The **Compatibility Oracle** gives you one of four verdicts for every indexed ref and repo:

| Verdict | What happened |
|---|---|
| `compatible` | Same shape *and* same behavior — safe to ship |
| `shape_changed` | Signature changed → **API break** |
| `behavior_changed` | Signature unchanged, but the call graph underneath changed → **silent regression** |
| `absent` | Symbol doesn't exist at that ref |

Every verdict carries a **confidence score and full provenance**. Reponite never claims more certainty than it computed.

---

## Search

Three rungs. Pick the cheapest one that answers your question:

```sh
# Exact / regex — result fused with its enclosing symbol
reponite grep validateCard

# Semantic — no model, no network
reponite semsearch "where we charge a card"

# Structural diff across refs
reponite diff v0.1.0 HEAD --changed-only --package internal/query
```

---

## AI agents (MCP)

Reponite exposes **16 MCP tools** so an agent can read, understand, and safely change code — including ROS runtime wiring — without opening source files.

```sh
reponite setup .   # register as MCP server (Claude, Cursor, Windsurf, ...)
reponite mcp .     # what the agent runs — auto-indexes HEAD on first mount
```

**Tools:** `investigate` · `search` · `semsearch` · `grep` · `usages` · `topics` · `context` · `brief` · `compat` · `diff` · `rootcause` · `rootcause_trace` · `ximpact` · `blast_radius` · `repos` · `refs`

Every response is **token-budgeted** and carries a `_meta` envelope with confidence, freshness, and provenance — the agent always knows how much to trust it.

---

## Web dashboard

```sh
reponite serve .            # single-repo view → http://127.0.0.1:8899
reponite serve repo-a repo-b repo-c   # shared team/fleet view
```

---

## How it works

Three hashes, computed over every symbol at every indexed ref:

| Hash | Question it answers |
|---|---|
| `symbol_hash` | Did the code text change? (storage dedup key — identical code across refs stored once) |
| `signature_hash` | Did the API shape change? |
| `behavior_hash` | Did the call graph change? (Merkle: a callee's change propagates to every transitive caller) |

The `behavior_hash` is what makes `behavior_changed` possible.  
It's also what makes root-cause fast: a symbol whose `symbol_hash` changed is a **mutation site**; a symbol whose `behavior_hash` alone changed is merely **carried along**. Walk the frontier, find the origin.

```
  CLI  ·  MCP server  ·  web dashboard  ·  VS Code extension
                      │
  ┌───────────────────▼─────────────────────────────────────┐
  │  PURE CORE  (stdlib only, zero CGO, 95+ unit tests)      │
  │  canon() · three-hash identity · behavior-hash Merkle    │
  │  Compat Oracle · diff · rootcause · grep · brief         │
  └───────────────────┬─────────────────────────────────────┘
                      │  thin build-tagged adapters
        tree-sitter (CGO)  ·  SQLite  ·  go-git  ·  go/types
```

The correctness-critical core is **pure Go, standard-library only** — no external dependencies, no CGO, fully deterministic. See [ADR-018](docs/adr/ADR-018-pure-core-thin-adapters.md).

---

## Contributing

```sh
go build ./...    # pure core — zero deps, builds anywhere
go test ./...     # 95+ unit tests
make cli          # full binary (all adapters)

# Per-adapter checks (mirrors CI):
make sqlite | make treesitter | make mcp | make e2e
```

Open an [issue](https://github.com/vishwak02/reponite/issues) to ask a question or propose a feature.  
Send a pull request for fixes. Keep all CI jobs green.

The invariants in [CLAUDE.md](CLAUDE.md) are load-bearing — please read them before sending a PR.

---

## Documentation

- [Architecture overview](docs/architecture.md)
- [Agent-facing features](docs/agent-features.md) — brief, root-cause, cross-repo impact, retrieval ladder
- [Architecture Decision Records](docs/adr/)

---

## License

[Apache-2.0](LICENSE) © reponite contributors
