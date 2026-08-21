<h1 align="center">reponite</h1>

<p align="center">
  <b>Code intelligence that answers “did this change break anyone?” — across every branch, tag, and repo.</b>
</p>

<p align="center">
  <a href="https://github.com/vishwak02/reponite/actions/workflows/go.yml"><img src="https://github.com/vishwak02/reponite/actions/workflows/go.yml/badge.svg" alt="CI"></a>
  <a href="https://github.com/vishwak02/reponite/releases"><img src="https://img.shields.io/github/v/release/vishwak02/reponite?color=00ADD8" alt="Release"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.22%2B-00ADD8.svg" alt="Go 1.22+"></a>
  <a href="#languages"><img src="https://img.shields.io/badge/languages-10-00ADD8.svg" alt="10 languages"></a>
  <a href="#ai-agents-mcp"><img src="https://img.shields.io/badge/MCP-17%20tools-6E56CF.svg" alt="17 MCP tools"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-Apache--2.0-blue.svg" alt="License"></a>
</p>

<p align="center">
  <a href="#quickstart">Quickstart</a> ·
  <a href="#the-core-idea">Core idea</a> ·
  <a href="#what-you-can-ask">What you can ask</a> ·
  <a href="#ai-agents-mcp">AI agents</a> ·
  <a href="#command-reference">Commands</a> ·
  <a href="#faq">FAQ</a>
</p>

---

Most code tools answer **“where is this symbol?”**

reponite answers the question you actually have before shipping:

> **“Is this symbol still the same as it was — and if not, who did I just break?”**

It's a **single binary**. Point it at your repos, and it indexes them at every branch, tag,
and commit you care about. Then you can ask about *change* — across time and across
repository boundaries — instead of just about *location*.

```console
$ reponite compat Charge
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

`Charge` looks untouched — same name, same signature. But something it calls changed
underneath it, so its **behavior** changed. That's the class of bug that ships quietly, and
finding it is what reponite is for.

---

## Table of contents

- [Why reponite exists](#why-reponite-exists)
- [Install](#install)
- [Quickstart](#quickstart)
- [The core idea](#the-core-idea) — three hashes, explained simply
- [What you can ask](#what-you-can-ask) — every feature with a real example
- [AI agents (MCP)](#ai-agents-mcp)
- [Web dashboard](#web-dashboard)
- [Working with many repos](#working-with-many-repos)
- [Configuration](#configuration)
- [Command reference](#command-reference)
- [How it works](#how-it-works)
- [Design principles](#design-principles)
- [FAQ](#faq)
- [Contributing](#contributing)

---

## Why reponite exists

Say you change a function. Before you merge, you'd like to know:

| Question | `grep` | LSP / IDE | Sourcegraph | reponite |
|---|:---:|:---:|:---:|:---:|
| Where is this symbol defined? | ~ | ✅ | ✅ | ✅ |
| Who calls it, right now? | ~ | ✅ | ✅ | ✅ |
| Did its **signature** change since the release tag? | ❌ | ❌ | ~ | ✅ |
| Did its **behavior** change even though the signature didn't? | ❌ | ❌ | ❌ | ✅ |
| **Which** callee caused that behavior change? | ❌ | ❌ | ❌ | ✅ |
| Which **other repos** call it, and do they expect the old shape? | ❌ | ❌ | ~ | ✅ |
| What breaks if I save **this edit**, before I compile? | ❌ | ~ | ❌ | ✅ |
| Who reacts when I publish to a **ROS topic**? | ❌ | ❌ | ❌ | ✅ |
| How certain is each of those answers? | ❌ | ❌ | ❌ | ✅ |

The last row is the one that matters most. Every reponite answer carries a **confidence
score and its provenance** — how it was derived. A tool that is confidently wrong about
compatibility is worse than no tool at all, so reponite would rather tell you
*"unresolved, here's what I couldn't prove"* than guess.

---

## Install

**Prebuilt binary** (Linux / macOS — no Go toolchain needed):

```sh
curl -fsSL https://raw.githubusercontent.com/vishwak02/reponite/main/install.sh | sh
```

**From source** (Go 1.22+ and a C compiler, for the tree-sitter parsers):

```sh
git clone https://github.com/vishwak02/reponite
cd reponite
make cli          # → bin/reponite
```

**Verify:**

```sh
reponite version
```

---

## Quickstart

Five commands, about two minutes.

### 1. Index your repo

```sh
cd ~/src/my-project
reponite index .
```

```
indexed my-project@HEAD [module github.com/me/my-project] — refs now: [HEAD]
```

This walks your source, parses it, and builds an index at `.reponite/index.db`.
Vendored directories are skipped automatically. The repo also joins your
[fleet registry](#working-with-many-repos), so later commands can find it.

### 2. Index a second point in history

Compatibility questions need something to compare against:

```sh
reponite index . --git v1.0.0        # a tag
reponite index . --git main          # a branch
reponite index . --git HEAD~20       # a commit
```

`--git` reads the commit's tree directly. **You don't need to check anything out** —
your working directory is untouched.

### 3. Ask if a symbol is still compatible

```sh
reponite compat Charge
```

You get one of [four verdicts](#the-four-verdicts) per ref, each with a confidence score.

### 4. Find out *why* something changed

```sh
reponite rootcause Charge v1.0.0 HEAD
```

This walks down the call graph to the function whose code *actually* changed — not the
one that merely inherited the change from a callee.

### 5. Find out who you're about to break

```sh
reponite blast-radius Charge
```

In-repo callers, callers in **other repos**, the tests that cover it, and whether the
contract has already moved — in one call.

---

## The core idea

Everything in reponite is built on **three hashes**, computed for every symbol at every
indexed ref. Understanding these three lines is understanding the whole tool.

| Hash | The question it answers | How it's built |
|---|---|---|
| `symbol_hash` | **Did the code text change?** | Hash of the normalized source of just this symbol |
| `signature_hash` | **Did the API shape change?** | Hash of the signature only — body excluded |
| `behavior_hash` | **Did behavior change?** | Merkle hash over the resolved call graph: `H(symbol_hash + all callee behavior_hashes)` |

That third one is the interesting one. Because a symbol's `behavior_hash` folds in the
hashes of everything it calls, **a change anywhere below propagates upward automatically**.

```
  validateCard   ← you edited this. symbol_hash changes.
        ▲
     Charge      ← untouched, but its behavior_hash changes, because a callee's did.
        ▲
   CheckoutFlow  ← also untouched, also behavior_changed. And so on, transitively.
```

Two consequences fall straight out:

**1. You can detect silent regressions.** Same signature, different behavior — the change
that passes code review because the diff looks local.

**2. Root-cause becomes cheap.** Given a behavior change, ask of each symbol:

- Did its **own** `symbol_hash` change? → it's a **mutation site**. The origin.
- Only its `behavior_hash` changed? → it was **carried along**. Keep walking down.

No re-analysis, no heuristics — just comparing hashes you already stored.

### The four verdicts

```sh
reponite compat <symbol>
```

| Verdict | Meaning | What to do |
|---|---|---|
| `compatible` | Same shape *and* same behavior | Safe to ship |
| `shape_changed` | The signature changed | **API break** — callers won't compile |
| `behavior_changed` | Same signature, different call graph underneath | **Silent regression risk** — review carefully |
| `absent` | The symbol doesn't exist at that ref | New, removed, or renamed |

### Storage stays small

Content is addressed by hash, so **identical code across refs is stored once**. Indexing
20 tags of a repo costs roughly what one tag costs, plus whatever genuinely differs.

---

## What you can ask

Every command below prints JSON, so it pipes cleanly into `jq` or a script.

<details open>
<summary><b>Search — four rungs, cheapest first</b></summary>

Pick the cheapest rung that answers your question.

```sh
# 1. Exact / regex. Trigram-prefiltered, and each hit is fused with its enclosing symbol.
reponite grep validateCard
reponite grep "TODO|FIXME"                 # real regex, alternation included
reponite grep "TODO|FIXME" --limit -1      # everything (default caps at 50)
reponite grep "TODO|FIXME" --offset 50     # page through a big result

# 2. Symbol names.
reponite search Charge

# 3. Meaning — "where is the thing that does X". No model or network required.
reponite semsearch "where we charge a card"

# 4. Compatibility (the sections above).
reponite compat Charge
```

Every grep hit tells you which **function** it landed in, not just the line — so a match is
one hop from the call graph rather than a dead end.

Counts are precise, and they mean exactly one thing each: `total` is every matching line
(ground truth, independent of paging), `matches` is the window you asked for, `truncated`
means more exist *after* this window, and `scanned` is how many candidate **files** were
examined. No approximations hiding behind a number.

</details>

<details>
<summary><b>Usages — every call site, and whether it's real</b></summary>

```sh
reponite usages validateCard
```

```jsonc
{
  "symbol": "validateCard",
  "total": 12,
  "usages": [
    { "path": "billing/charge.go", "line": 44, "in": "Charge",
      "text": "return validateCard(card)", "confirmed": true },
    { "path": "docs/notes.md",     "line": 7,  "in": "",
      "text": "// validateCard() is called from Charge", "confirmed": false }
  ]
}
```

`confirmed: true` means the enclosing function is a **resolved caller in the call graph**.
`false` means it's a lexical match — a comment, a string, or a dynamic call that static
analysis can't prove. Both are shown, clearly separated. Nothing is dropped and nothing is
overstated.

</details>

<details>
<summary><b>Verify edit — what breaks, before you save</b></summary>

Pass a file's *proposed* content and find out what it breaks — no saving, no compiling.

```sh
reponite verify-edit internal/query/coordinator.go
```

```jsonc
{
  "path": "internal/query/coordinator.go",
  "safe": false,
  "changed": ["reposFor"],
  "impacts": [{
    "symbol": "reposFor",
    "kind": "signature_changed",
    "breaks": [
      { "path": "internal/query/grep.go",     "line": 153, "in": "GrepRepo",    "confirmed": true },
      { "path": "internal/query/semantic.go", "line": 173, "in": "SemanticSearch", "confirmed": true }
    ]
  }]
}
```

`safe: false` means at least one **confirmed** caller breaks. This is the tightest feedback
loop reponite offers, and it's why an AI agent can edit a shared symbol without guessing.

</details>

<details>
<summary><b>Cross-repo impact — who else depends on this</b></summary>

```sh
reponite ximpact GetUser
```

```jsonc
{
  "target": "GetUser",
  "modules": ["github.com/acme/api"],
  "callers": [
    { "repo": "web",     "caller": "svc.Handle",  "resolution_method": "scip-resolved",
      "confidence": 0.95, "expected_signature": "stale" },
    { "repo": "worker",  "caller": "job.Sync",    "resolution_method": "import-resolved",
      "confidence": 0.75, "expected_signature": "current" }
  ],
  "stale_callers": 1,
  "contract_changed": true
}
```

Two things to notice.

**Callers are matched in tiers, and each says which tier it came from.** Highest first:

| Tier | `resolution_method` | Confidence | How the match was made |
|---|---|:---:|---|
| 0 | `scip-resolved` | 0.95 | A globally unique [SCIP](#scip-symbol-precise-cross-repo-edges) symbol identity — no name guessing |
| 1 | `import-resolved` | 0.75 | The caller's import resolves to this module, and the name matches |
| 2 | `unresolved-external` | 0.6 | Name match only — the honest fallback |

**`expected_signature` is per caller.** `stale` means that caller was last indexed against
an *older* shape of the symbol — it still expects the old contract. `stale_callers: 1` is
the "1 of 2 callers hasn't caught up" number you want before a deploy. A caller reponite
couldn't determine reads `""`: unknown, never guessed.

</details>

<details>
<summary><b>Root cause — from symptom to the actual mutation</b></summary>

```sh
reponite rootcause Charge v1.0.0 HEAD
```

Or skip the guessing entirely and paste a stack trace:

```sh
reponite rootcause-trace crash.log --from v1.0.0 --to HEAD
```

reponite maps each frame to a symbol, walks only the **failing path**, and returns the
mutation sites on it — with the commit and PR that introduced each one, where git history
can supply it. Go, Python, JavaScript, and Java traces are understood.

</details>

<details>
<summary><b>Investigate — “how does X work?” in one answer</b></summary>

```sh
reponite investigate "how does the picking workflow decide which item to grab"
```

Returns a single cited markdown dossier: the most relevant symbols across **all** your
repos, each with a body preview and its callers and callees, filled to a token budget.
It replaces the search → read → search → read loop, which matters most when the thing
asking is an AI agent paying for every round trip.

</details>

<details>
<summary><b>Editing brief — everything needed to change one symbol</b></summary>

```sh
reponite brief Charge --budget 3000
```

One bundle: the full body, its callees (with previews), its callers, the tests that cover
it, the reason it last changed, and its compatibility across refs. Sections are filled in
priority order until the token budget runs out, and anything dropped is **listed by name**
so you know what you're missing rather than silently getting less.

</details>

<details>
<summary><b>ROS topic graph — the edges no call graph contains</b></summary>

In a robotics system, a publisher and a subscriber live in **different processes**, joined
only by a topic-name string that the middleware resolves at runtime. There is no
source-level edge between them, so no call graph can ever contain one.

```sh
reponite topics                # the whole communication map
reponite topics /cmd_vel       # just this one: who publishes, who reacts
```

```jsonc
{
  "family": "topic", "name": "scan", "connected": true, "confidence": 0.9,
  "producers": [{ "repo": "driver", "path": "src/lidar.cpp", "line": 42,
                  "in": "Lidar::init", "msg_type": "sensor_msgs::LaserScan",
                  "msg_type_source": "template" }],
  "consumers": [{ "repo": "planner", "path": "nav.py", "line": 88,
                  "in": "Nav.__init__", "msg_type": "LaserScan",
                  "msg_type_source": "positional-arg" }]
}
```

Supports **roscpp, rospy, rclcpp, and rclpy** — publishers, subscribers, services, and
actions. Message types are recovered even where ROS 1 hides them: `subscribe()` carries no
template, so the type comes from the callback's parameter, and `msg_type_source` tells you
which method was used. Confidence rises to 0.9 when both ends agree on the type.

Honest about its limits, in the output itself: launch-file remapping is **not** resolved,
and a dynamic (non-literal) topic name is *counted as unresolved* rather than guessed.

</details>

<details>
<summary><b>CI gate — fail the build on an API break</b></summary>

```sh
reponite ci-check --base origin/main --head HEAD
```

Exits non-zero if any **exported** symbol was removed or changed shape. "Exported" is
decided per language (Go's capitalization, the leading-underscore convention elsewhere),
not by one hardcoded rule. Behavior changes are reported but don't fail the build — they
aren't contract breaks.

</details>

---

## AI agents (MCP)

This is what reponite was built for. An AI agent can read, understand, and safely modify
your code **without opening source files** — spending a fraction of the tokens.

```sh
reponite setup .    # register with Claude Desktop, Claude Code, Cursor, Windsurf…
```

Then the agent has 17 tools:

| Tool | What the agent gets |
|---|---|
| `reponite_investigate` | One cited dossier answering “how does X work?” |
| `reponite_search` | Symbols by name, fleet-wide |
| `reponite_semsearch` | Symbols by meaning |
| `reponite_grep` | Regex, with each hit's enclosing symbol |
| `reponite_usages` | Every call site, graph-confirmed |
| `reponite_context` | Direct callers and callees |
| `reponite_brief` | Token-budgeted bundle for editing a symbol |
| `reponite_blast_radius` | Everything that could break, pre-edit |
| `reponite_verify_edit` | What a proposed edit breaks, pre-save |
| `reponite_compat` | Compatibility verdicts across refs |
| `reponite_diff` | Symbol-level delta between two refs |
| `reponite_rootcause` | Walk a behavior change to its origin |
| `reponite_rootcause_trace` | Same, seeded from a stack trace |
| `reponite_ximpact` | Fleet callers + per-caller contract skew |
| `reponite_topics` | ROS publisher ↔ subscriber graph |
| `reponite_repos` | What's indexed and where |
| `reponite_refs` | A repo's indexed refs |

Every response is **token-budgeted** and carries a `_meta` envelope with confidence,
freshness, and provenance — so the agent knows how far to trust each answer instead of
treating everything as equally certain.

A miss returns **“did you mean…?”** rather than an empty result, so an agent that
misremembers a name recovers in one step instead of concluding the symbol doesn't exist.

---

## Web dashboard

```sh
reponite serve        # every registered repo → http://127.0.0.1:8899
reponite serve .      # just this one
```

Seven views: **Overview** (what's indexed, and the database behind it) · **Explore**
(search → editing brief) · **Diff** · **Impact** (fleet callers and contract skew) ·
**Topics** (ROS graph) · **Usages** · **Verify** (paste an edit, see what breaks).

Every view is deep-linkable, so a URL is shareable with a teammate. Light and dark themes.
No external assets — the dashboard works offline.

---

## Working with many repos

Most interesting questions cross repository boundaries. reponite keeps a **fleet registry**
so you don't re-type your directory list on every command.

```sh
reponite index ~/src/api       # indexing registers the repo automatically
reponite index ~/src/web

reponite serve                 # mounts the whole fleet, from any directory
reponite mcp                   # so does the agent mount
reponite ximpact GetUser       # and so do cross-repo queries

reponite fleet list            # what's registered (stale entries flagged)
reponite fleet add <dir>       # register an already-indexed repo
reponite fleet remove <dir|repo>
reponite fleet path            # where the registry file lives
```

The registry is a small JSON file holding **metadata only** — repo, directory, module path,
last-index time. Your code and index stay in each repo's own `.reponite/index.db`.

Naming directories explicitly always beats the registry, and a registered repo whose index
has been deleted or moved is **reported as stale**, never silently skipped. Any command can
take `--local` to ignore the fleet and answer for the current repo alone.

### SCIP: symbol-precise cross-repo edges

Cross-repo links normally match on `(module path, name)` — precise about the module, but
still a name match. If you generate a **SCIP index** (`scip-go`, `scip-typescript`,
`scip-python`, …) and leave `index.scip` at a repo root, reponite reads it and every symbol
gains a globally unique **moniker**:

```
scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser().
```

Two repos indexed independently produce the *same* string for the same symbol, so callers
are matched **symbol to symbol** — no name guessing at all — and are labeled
`scip-resolved` at 0.95 confidence.

If you have no SCIP index, nothing changes: the existing tiers answer exactly as before.
A corrupt index is reported and skipped rather than silently trusted. And because monikers
match by exact string, a version mismatch can only ever *lose* a match (falling back one
tier), never invent one.

---

## Configuration

### Ignoring vendored code

Vendored trees flood search results with third-party noise, so exclusion happens **at index
time** and every surface benefits at once. Three layers, all gitignore syntax:

1. **Always on:** `vendor/`, `third_party/`, `node_modules/`, `.git/`, `testdata/`, and every dot-directory.
2. **`.reponiteignore`** at the repo root:
   ```gitignore
   # a monorepo bundling upstream ROS
   external/
   *.generated.go
   !important.generated.go
   ```
3. **`--exclude`** on the command line (repeatable, comma-separable):
   ```sh
   reponite index . --exclude "external/**" --exclude "*.pb.go"
   ```

Supported: `#` comments · `!` negation (last match wins; an excluded *directory* can't be
re-included from below, as in git) · trailing `/` for directories only · leading `/` to
anchor at the root · `*` `?` `[...]` within a path segment · `**` across segments.

`index --git <rev>` reads `.reponiteignore` **from the indexed tree**, so a historical ref
is filtered by the rules that existed then.

### Environment variables

| Variable | Purpose | Default |
|---|---|---|
| `REPONITE_FLEET` | Fleet registry path | `$XDG_CONFIG_HOME/reponite/fleet.json`, else `~/.config/reponite/fleet.json` |
| `REPONITE_EMBED_ENDPOINT` | Optional embeddings endpoint for neural semantic search | unset — the built-in ranker is used |
| `REPONITE_EMBED_MODEL` | Model name for that endpoint | unset |
| `REPONITE_EMBED_API_KEY` | Bearer token, if the endpoint needs one | unset |

### Optional: neural semantic search

Semantic search works out of the box with **no model and no network** — an identifier-aware
ranker that splits `validateCardNumber` into `validate card number` and weights rare terms
higher. That's the default, and it's often enough.

For better recall on paraphrased queries, point it at any OpenAI-compatible embeddings
endpoint (Ollama, OpenAI, LiteLLM, vLLM):

```sh
export REPONITE_EMBED_ENDPOINT=http://localhost:11434/v1/embeddings
export REPONITE_EMBED_MODEL=nomic-embed-text
reponite semsearch "where we charge a card"
```

Results always name the ranker that produced them (`term-idf` or `neural:<model>`).
**If the endpoint is down, the search falls back to the built-in ranker and says so** in
the result — you never silently get a worse ranking that looks like a normal one.
Embeddings are cached by content hash, so a long-running server embeds each symbol once.

---

## Command reference

```
reponite <command> [arguments] [flags]
```

### Indexing

| Command | Description |
|---|---|
| `index [dir] [ref]` | Index a directory. `--git <rev>` indexes a commit's tree without checking it out. `--exclude <glob>` skips paths. |
| `refs` | List a repo's indexed refs |
| `repos` | Every indexed repo with its module and per-ref statistics |
| `fleet list\|add\|remove\|path` | Manage the cross-run repo registry |
| `watch [dir]` | Re-index HEAD automatically when sources change |

### Understanding code

| Command | Description |
|---|---|
| `investigate <question…>` | One cited dossier answering a plain-language question |
| `search <substring>` | Find symbols by name |
| `semsearch <query>` | Find symbols by meaning |
| `grep <pattern> [ref]` | Regex/literal search, fused with the enclosing symbol |
| `context <symbol> [ref]` | Direct callers and callees |
| `brief <symbol> [ref]` | Token-budgeted bundle for editing a symbol |
| `usages <symbol>` | Every call site, with call-graph confirmation |
| `topics [name]` | ROS publisher ↔ subscriber / service / action graph |

### Changing code safely

| Command | Description |
|---|---|
| `compat <symbol> [ref]` | Compatibility verdicts across every indexed ref |
| `diff <from> <to>` | Symbol-level delta. `--changed-only`, `--package`, `--confidence-min` |
| `rootcause <symbol> <from> <to>` | Walk a behavior change to its mutation site |
| `rootcause-trace <file\|-> --from --to` | Same, seeded from a stack trace |
| `ximpact <symbol>` | Cross-repo callers with per-caller contract skew |
| `blast-radius <symbol> [ref]` | Callers + fleet callers + tests + contract state |
| `verify-edit <path>` | What breaks if this file's current content is saved |
| `ci-check --base <ref> --head <ref>` | Non-zero exit on an exported API break |

### Serving

| Command | Description |
|---|---|
| `serve [dir…]` | Web dashboard + JSON API. `--addr` to change the bind address |
| `mcp [dir…]` | MCP server over stdio, for an AI agent |
| `setup [dir]` | Register reponite in your agent's config file |

Every command accepts `--help`. Fleet-wide commands accept `--local`.

### Languages

Go · Python · JavaScript · TypeScript · Java · C · C++ · Rust · **Shell** — plus **ROS
interface files** (`.msg`, `.srv`, `.action`), where the field list *is* the contract, so
adding a field is correctly reported as `shape_changed`.

**Shell** covers `.sh`, `.bash`, `.zsh`, `.ksh` and — importantly — **extension-less scripts
identified by their shebang**, because a CLI's entry point (`installer/rdt`, `bin/deploy`) is
usually the most valuable file in the tree and rarely has an extension. `#!/usr/bin/env
python3` and `#!/usr/bin/node` are recognized too. Shell functions have no declared
parameter list, so a shell edit is reported as `behavior_changed` and never
`shape_changed` — the honest reading for a language with no signature.

---

## How it works

```
    CLI          MCP server        Web dashboard      VS Code extension
      └───────────────┴─────────┬────────┴───────────────────┘
                                │
   ┌────────────────────────────▼──────────────────────────────┐
   │  PURE CORE — Go standard library only, no CGO, 205 tests   │
   │                                                            │
   │  canon()            normalize source → stable identity     │
   │  three hashes       symbol · signature · behavior          │
   │  behavior Merkle    propagate change up the call graph     │
   │  Compat Oracle      the four verdicts + confidence         │
   │  root cause · diff · grep · brief · ximpact · topics       │
   └────────────────────────────┬──────────────────────────────┘
                                │  thin, build-tagged adapters
        ┌───────────────┬───────┴───────┬───────────────┐
   tree-sitter        SQLite         go-git        embeddings
     (parsing)        (storage)      (refs)         (optional)
```

The **entire correctness-critical core is pure Go with zero dependencies**. Parsing,
storage, git access, and embeddings live in thin adapters behind interfaces, each enabled
by a build tag. That means `go build ./... && go test ./...` runs anywhere, instantly, with
nothing to install — and the logic that decides whether your API broke has no third-party
code in it at all. ([ADR-018](docs/adr/ADR-018-pure-core-thin-adapters.md))

| Build tag | Adds |
|---|---|
| *(none)* | The pure core — builds and tests anywhere |
| `sqlite` | Persistent index storage |
| `treesitter` | Real parsers, git ref indexing, Go type-checked edges |
| `mcp` | The MCP server |
| `neural` | Optional embeddings client for semantic search |

---

## Design principles

**Never lie.** Every edge carries how it was resolved and how confident that is. A
verdict inherits the *minimum* confidence of its evidence. When something can't be
resolved, it's **counted and reported** — never quietly dropped or guessed. Almost every
design decision here falls out of that one rule.

**Be conservative when unsure.** The normalizer that decides whether two pieces of code are
"the same" may under-normalize (calling identical code different) but must never
over-normalize (calling different code identical). A missed dedup costs disk. A wrong merge
costs you a wrong verdict.

**Storage grows with unique content, not with refs.** Git's model: index 50 tags, store
what actually differs.

**Pure core, thin adapters.** Correctness-critical logic depends on nothing external.

---

## FAQ

<details>
<summary><b>How is this different from an LSP or my IDE?</b></summary>

An LSP answers questions about the code **as it is right now, in one repo**. reponite
answers questions about how code **changed between two points in time, across many repos** —
and attaches a confidence score to each answer. They complement each other; reponite isn't
trying to be your editor's jump-to-definition.
</details>

<details>
<summary><b>Do I need to check out old branches to compare against them?</b></summary>

No. `reponite index . --git v1.0.0` reads the commit's tree straight from the git object
store. Your working directory is never touched, so you can index twenty tags without a
single checkout.
</details>

<details>
<summary><b>How big does the index get?</b></summary>

Roughly proportional to your unique source content, not to the number of refs. Content is
addressed by hash, so a file that didn't change between two tags is stored once. Vendored
directories are excluded by default, which usually matters more than anything else.
</details>

<details>
<summary><b>Does it send my code anywhere?</b></summary>

No. Everything is local. The only outbound network path in the entire tool is the
*optional* embeddings endpoint for neural semantic search, which is off unless you set
`REPONITE_EMBED_ENDPOINT` — and it's behind its own build tag precisely so that "no network"
is enforced by the compiler rather than by convention.
</details>

<details>
<summary><b>What does `confidence: 0.6` actually mean?</b></summary>

Each call-graph edge is scored by how it was resolved: `1.0` type-checker proven ·
`0.95` SCIP symbol-resolved across repos · `0.9` uniquely name-resolved · `0.75`
import-resolved across repos · `0.6` external/opaque · `0.5` ambiguous. A verdict takes the
**minimum** over its evidence, so a single weak edge caps the whole answer. `compat` also
reports `direct_confidence` (this symbol's own edges), which is often much higher than the
transitive floor.
</details>

<details>
<summary><b>My language isn't supported. How hard is it to add?</b></summary>

Usually one table entry plus a grammar binding. The extractor is language-agnostic and
driven by a `LangRules` table — see [Adding a language](CLAUDE.md#adding-a-language).
</details>

<details>
<summary><b>Why does grep return a `scanned` count that isn't the number of matches?</b></summary>

`scanned` is candidate **files** examined after the trigram prefilter; `total` is matching
**lines**. They're deliberately separate numbers because conflating them would misrepresent
how much was actually searched.
</details>

<details>
<summary><b>Is it safe to run against a monorepo with vendored dependencies?</b></summary>

Yes — `vendor/`, `third_party/`, and `node_modules/` are excluded by default. For anything
else (a bundled upstream tree, say), add a `.reponiteignore`. See
[Configuration](#ignoring-vendored-code).
</details>

---

## Contributing

```sh
go build ./...     # pure core — zero dependencies, builds anywhere
go test ./...      # 205 tests, no setup required
make cli           # full binary with every adapter

# Per-adapter checks (these mirror CI exactly):
make sqlite | make treesitter | make mcp | make e2e | make neural
```

Please read [CONTRIBUTING.md](CONTRIBUTING.md) first. The
[invariants in CLAUDE.md](CLAUDE.md#invariants-do-not-break) are load-bearing — breaking one
is how a code-intelligence tool starts lying to people.

Good first contributions: a new language (mostly one table entry), a new ROS client-library
idiom, or a dashboard view over an API endpoint that doesn't have one yet.

Found a bug? [Open an issue](https://github.com/vishwak02/reponite/issues/new/choose) — a
wrong answer with high confidence is the most valuable bug report you can file.

---

## Documentation

| Document | Contents |
|---|---|
| [docs/architecture.md](docs/architecture.md) | The design, layer by layer |
| [docs/agent-features.md](docs/agent-features.md) | Full spec for the agent-facing features |
| [docs/adr/](docs/adr/) | Architecture Decision Records — why things are the way they are |
| [CONTRIBUTING.md](CONTRIBUTING.md) | Development workflow |
| [CHANGELOG.md](CHANGELOG.md) | What changed, by release |
| [PROGRESS.md](PROGRESS.md) | The full build log, session by session |

---

## License

[Apache-2.0](LICENSE) © reponite contributors
