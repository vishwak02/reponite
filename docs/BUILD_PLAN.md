# reponite — Phased Build Plan

> **Status (2026-07-05):** Phases **1–4** and **5a/5b/6a/6c** are **built and merged to `main`**
> (PRs #3/#4/#5, all CI-green) and **v0.2.0 is released** with 4-platform binaries. Delivered:
> multi-language parsing, auto-index-on-mount, `brief`, `rootcause_trace`, intent linkage,
> `changed_callees`, diff filters, `ci-check`, `ximpact` (name-based), web dashboard (`serve`),
> VS Code extension, semantic search (ADR-020), and ROS `.msg`/`.srv`/`.action` interface compat.
> **Remaining (large infra, not started):** Phase 4 fleet registry (`module_path` + `global.db` +
> Oracle signature-skew fusion), SCIP high-confidence edges (4.3), shared team server (4.2). See
> `PROGRESS.md` for the per-phase log.


Derived from `goat_roadmap.md` (the "GOAT roadmap"), reconciled against the **actual
current state of the tree** (every claim below was verified against source, not the roadmap).
Ordered for **shippable increments**: each phase leaves `main` green and adds standalone value.

Companion docs: `docs/agent-features.md` (specs + embedded ADRs 014–019), `docs/adr/ADR-000`
(built-vs-planned index), `docs/adr/ADR-018` (pure-core/thin-adapter law), `PROGRESS.md`.

---

## Ground truth — what's actually built vs. stubbed (verified)

**Built & CI-green (the moat):** three-hash identity (`SymbolHash`/`SignatureHash`/`BehaviorHash`,
`internal/content/hash.go`), `Canon`, behavior-hash Merkle pass (`behavior.go`, Tarjan SCC +
reverse-topo, `behavior_conf = min`), compat Oracle (4 verdicts), diff, **rootcause core**
(`query.RootCause`/`RootCauseBy`), grep (trigram + regex + enclosing-symbol fusion), `Store`
interface + in-mem + SQLite (WAL, **live per-call reads**, not a snapshot), MCP server with 7 tools,
go/types precise Go edges (conf 1.0), go-git ref indexing, honest direct-vs-transitive confidence.

**Language-agnostic already:** `LangRules` (Go/Python/JS/TS/Java) + `RulesForExt()` in
`internal/processing/lang.go`; the generic `Extract(root, rules, normVer)` in `extract.go`.

**Stubbed / not implemented** (`init/brief/impact/ximpact/why/arch/sync/status/gc/serve`):
- Parsing/indexing is **hardcoded to Go** despite the rules existing (see Phase 1).
- `reponite_brief`, `reponite_rootcause_trace`, `reponite_ximpact` — spec'd, not built.
- Intent linkage (`intent.db`), `external_refs` capture, `module_path` registry — not built.
- Semantic layer — **no detailed spec exists**; only `EmbedHash` (uncalled) + `domainEmbed`.

---

## Constraints that shape every phase (do not fight these)

1. **Pure-core / thin-adapter law (ADR-018, Invariant 6).** Correctness logic stays pure/stdlib
   behind interfaces (`AST`, `Store`, `Embedder`). External deps (tree-sitter, sqlite, go-git,
   vectors) live in build-tagged adapters. Every new feature splits this way.
2. **Hashing invariants (1–5).** `norm_ver` folded into every hash; `Canon` conservative;
   dedup on `symbol_hash`; ref real only when manifest written last; every edge carries
   `resolution_method` + `confidence`, `behavior_conf = min`, never overclaim.
3. **Sandbox has no Go module proxy.** The pure core builds/tests locally; **anything importing
   tree-sitter / sqlite / go-git / x/tools cannot be built or tested in-sandbox** — it is verified
   only by GitHub Actions (jobs: `core`, `sqlite`, `treesitter`, `mcp`, `e2e`). Plan the dev loop
   around "push → CI green," not local runs, for adapter work.
4. **Build-tag composition is a real hazard.** `run_mcp.go` is `//go:build sqlite && mcp`;
   `IndexDir`/`watch` are `//go:build treesitter` / `sqlite && treesitter`. `make cli` builds ALL
   tags into one binary, but `make mcp` (sqlite+mcp only) does **not** see `IndexDir`. Any feature
   that calls indexing from the MCP path (Phase 1b) must be bridged behind a build-tagged shim so
   the `mcp`-only build still compiles.
5. **GitHub is read-only from this environment (403 on push/create).** Cutting tags / publishing
   releases (Phase 1c) must be done by the user or from an authenticated context.

---

## Phase 1 — Make it usable by real users (roadmap Tier 1 blockers)

> Without this reponite is a Go-only tool that silently returns empty on the languages most repos
> are written in. This is the whole "impressive demo → GOAT" gap.

### 1a. Multi-language parsing — THE #1 blocker (Medium)
The abstraction is done; this is plumbing. Files: `internal/processing/{parser,index_ts,index_git,index,extract}.go`,
`internal/processing/lang.go` (rules already correct), `Makefile`.

- **Bind grammars.** `parser.go` imports only `smacker/go-tree-sitter/golang`. Add
  `.../python`, `.../javascript`, `.../typescript/typescript` (+ `.../typescript/tsx`), `.../java`.
  Add them to the `go get` lines in `Makefile` (`treesitter`, `cli`, `e2e` targets) since go.mod has
  no require block.
- **Parser dispatch.** Replace the single `ParseGo` with a grammar picker keyed off `LangRules`
  (parallel to `RulesForExt`): `grammarFor(r LangRules) *sitter.Language` → `Parse(src, r)`.
- **Un-hardcode the file walk.** `index_ts.go:37` and `index_git.go:43` both `strings.HasSuffix(".go")`.
  Replace with `RulesForExt(filepath.Ext(path))` gating; dispatch to the matched rules.
- **Route extraction through rules.** Replace direct `ExtractGo(...)` calls (`index_ts.go`,
  `index_git.go`) with `Extract(root, rules, normVer)`.
- **Generalize `topLevelSpans`** (`index_ts.go:77-96`, Go-specific node switch). Drive off
  `LangRules`. **Subtlety the roadmap misses:** class-based langs (Py/Java/JS/TS) nest methods inside
  classes; spans must descend into class bodies so grep's enclosing-symbol fusion matches what
  `Extract` emits. Keep spans and `Extract` symbol sets consistent.
- **Stop stamping `Lang:"go"`** in `indexFiles` (`index.go:68`); take lang from the matched rules.
- **Confidence expectation:** `TypeResolvedEdges`/`resolve_go.go` (go/types, conf 1.0) stays
  **Go-only by design**. Other langs resolve edges by name-match heuristic (conf 0.5–0.9). That's
  correct per Invariant 5 — honest lower confidence, raised later by SCIP (Phase 6).
- **Tests (CI, `//go:build treesitter`).** Today only Go has real-grammar tests; Py/JS are fake-AST,
  TS/Java only exercise `RulesForExt`. Add a real-grammar parse+extract test per language, and an
  e2e index of a small multi-lang fixture. Verify method→class qualification per language.

**Acceptance:** `reponite index .` on a Python/TS/Java repo produces a non-empty index; per-language
CI tests green; Go behavior unchanged.

### 1b. Auto-index on mount + live freshness (Tiny + Small — roadmap 1.3 + 2.5)
Today `mcpCommand` (`run_mcp.go`) never indexes and never auto-starts `watch`; if you forget
`reponite index .` every tool returns empty with no error. `watch` exists (`run_watch.go`, fsnotify,
300ms debounce) but is a separate process and only triggers on `.go`.

- **Auto-index on mount.** In the MCP startup: `if len(st.Refs(repo)) == 0 { IndexDir(...) }`.
  **Build-tag bridge required** (Constraint 4): `IndexDir` is `treesitter`, `run_mcp.go` is `sqlite&&mcp`.
  Introduce a build-tagged `autoIndex()` shim — real impl under `treesitter`, no-op (log a hint)
  otherwise — so `make mcp` still compiles.
- **Live freshness.** Optionally auto-start the watch loop from the MCP server (same bridge pattern),
  or document that the freshness path is "run `reponite watch` alongside." Storage is already live via
  WAL, so once a watcher re-indexes, the mounted server sees it. Also **generalize watch's `.go`-only
  trigger** (`run_watch.go:79`) to `RulesForExt` (depends on 1a).
- Resolves the `PROGRESS.md` dogfood note: "mounted MCP server is a mount-time snapshot."

**Acceptance:** mounting on an unindexed repo self-indexes; editing a file mid-session is reflected
without restart.

### 1c. Real releases + zero-friction install (Small — roadmap 1.2)
- **Version stamping (bug).** `internal/version/version.go` is a `const Version="0.0.0-dev"` with **no
  build-time override anywhere** — even tagged release binaries self-report `0.0.0-dev`. Add
  `-ldflags "-X .../internal/version.Version=$TAG"` in `release.yml` (and Makefile).
- **Publish all 4 binaries.** `release.yml` on `main` already has the 4-target matrix
  (linux/darwin × amd64/arm64), but the latest tag `v0.1.2` predates it, so only 2 assets exist.
  `install.sh` advertises 4 → installing on Intel Mac / ARM Linux hits "build from source." Fix =
  cut a fresh tag against current `main` (user does this — Constraint 5).
- **Broader `setup`.** `setup.go` targets **Claude Desktop only** (`defaultClaudeConfigPath`).
  Add Cursor / Claude Code / Windsurf / Cline / Continue via a `--client` flag + per-client default
  paths. The config-merge logic (`mergeMCPServer`) is client-agnostic and reusable.

**Acceptance:** `curl … | sh` installs a working binary on all 4 platforms; `reponite version` prints
the real tag; `reponite setup --client cursor` writes the right config.

---

## Phase 2 — Agent-facing flagship reads (roadmap Tier 2 moat-deepeners)

> These make an agent *prefer reponite over reading files*. All pure query assembly over primitives
> that already exist — the ADR-018 sweet spot. Corresponds to milestone M3 in the spec.

### 2a. `is_test` capture (Tiny — prerequisite for brief)
§9A.1. `brief`'s "covering tests" and cleaner search need a persisted `is_test` flag per symbol.
`IsTestName` heuristic already exists in `coordinator.go`; promote it to indexed metadata.

### 2b. Intent linkage — commit → PR/ticket (Medium — roadmap 2.4, §8A.6, ADR-017)
Linkage-only, **no LLM**. Feeds both `brief` and `rootcause`.
- New thin adapter over **go-git** (already a dep): for a mutation-site symbol, `git blame` the span →
  changing commit → parse PR numbers / ticket IDs from commit metadata/message.
- Store `intent` rows keyed `(symbol_hash, commit_hash)` with `summary` NULL; add
  `CREATE INDEX idx_intent_symbol_commit`. LLM summaries/clustering stay deferred (ADR-017).

### 2c. `reponite_brief` — the flagship (Medium — roadmap 2.1, §8C, ADR-014)
One token-budgeted call replacing 5–6 `view_file`s. Pure assembly, priority-fill to `token_budget`
(~3k default), overflow → `_meta.omitted` with fetch handles. Sections in order:
1. Target: full body + signature + path + complexity + `exported`.
2. Callees (depth 1): signature + `body_preview` + `symbol_hash` handle + confidence.
3. Callers (depth 1): signature + path (blast radius).
4. Type context: defs/signatures of param/return types.
5. Covering tests (needs 2a).
6. Intent (needs 2b).
7. Compat snapshot (Oracle across refs) — for exported symbols.

**Only the target ships a full body**; everything else is signature + preview + handle. That
discipline is what turns graph access into token *savings*. Wire CLI `reponite brief` + MCP
`reponite_brief`. Target P50 < 60ms.

### 2d. `reponite_rootcause_trace` — stack trace → root cause (Medium — roadmap 2.2, §8A.4, ADR-015)
Non-trace core is built; add the trace seeding on top.
- **Language-specific frame parser** (Go panic, Python traceback, JS/Java stack) → `file:function`.
- Map each frame to a `symbol_hash` at each ref via `file_paths` + name; unmapped frames →
  `_meta.unmapped_frames`, walk the rest.
- Run the existing drill-down **along the failing path**. `_meta.confidence = min` over the path.

---

## Phase 3 — Output & DX polish (roadmap Tier 3 — small, high daily value)

> Small efforts that materially cut agent token cost and cover the obvious enterprise hook. Can
> interleave with Phase 2.

### 3a. Smarter agent output (Small — roadmap 3.1)
- **`changed_callees` on compat.** `CompatResult` has only free-text `Detail` (behavior change is
  hardcoded `"identical signature; resolved call graph differs"`). The actual differing callees live
  in the `rootcause` walk. Surface them on the verdict so compat→rootcause is one call, not two.
- **Compact output mode.** `output.go` pretty-prints (2-space indent) — a token sink for agents. Add
  a compact/no-indent mode for MCP responses.

### 3b. Filtering & scoping on diff/search (Small — roadmap 3.2)
`diff` currently emits a row for **every** symbol including `unchanged` (~2000 lines of JSON on a real
diff) and has **zero** filter flags. Thread through, at all three layers (`run_full.go:cmdDiff`,
`query.DiffRefs`/`DiffRefsBy`, `reponite_diff` MCP schema): `--changed-only`, `--package` (use
`pkgOf`/`qualify` in `resolve.go`), `--kind`, `--confidence-min`.

### 3c. CI integration (Small — roadmap 3.5)
`reponite ci-check --base main --head <branch>` → non-zero exit if any exported symbol is
`shape_changed` (API break). Ships as a GitHub Action that comments a compat summary on PRs. Pure
reuse of the compat Oracle + diff.

---

## Phase 4 — Cross-repo & fleet (roadmap Tier 2.3 / §8B / ADR-016 — Large)

> The deploy-safety answer: "who across the fleet depends on this symbol?" Capture is cheap and can
> be pulled early; the query needs a global registry (milestone M7).

- **4a. `external_refs` capture at index time (cheap, pull early).** §9A.2. At resolve time, any
  reference resolving *outside* the repo → row in an `external_refs` table `(from_hash, from_repo,
  target_module, target_name, target_signature_hash?, resolution_method, confidence)`. "Data you have
  anyway." Can land alongside Phase 1a's resolver work.
- **4b. `module_path` in registry + global denorm.** Add `module_path` to per-repo `registry.db`;
  denormalize `(module_path → repo, ref)` into `global.db`.
- **4c. `reponite_ximpact` fleet query.** Match a symbol's `(module_path, name, signature_hash)`
  against every repo's `external_refs`; group callers by repo/ref with confidence; **fuse with the
  Oracle** ("4 services call getUserV2; 3 still expect the old signature"). State honest limits in
  `_meta`: source-call-graph only (RPC/HTTP/gRPC/queue invisible), version-skew defaults to each
  caller's pinned ref, most cross-repo edges medium-confidence.

---

## Phase 5 — Distribution surfaces (roadmap Tier 3.3 / 3.4 — Large, MCP is the backend)

- **5a. Web UI / dashboard (3.3):** indexed repos/refs at a glance, compat matrix heatmap, diff
  visualization, interactive call-graph explorer. The demo that sells the tool.
- **5b. VS Code extension (3.4):** inline compat verdicts, behavior-changed highlights, "rootcause"
  as a code action. Pure UI over the existing MCP server.

---

## Phase 6 — Moonshots (roadmap Tier 4 — Large / research)

- **6a. Semantic search layer (4.4).** Retrieval-ladder rung 3 ("where is the thing that does X").
  **Spec gap:** no detailed design exists — only the one-line ladder entry + the deferred `Embedder`
  interface. `EmbedHash`(+`domainEmbed`) exists but is **uncalled**; no embedder, no vector store.
  **Write the spec/ADR first**, then build the adapter (bundled | ollama | remote) behind the
  `Embedder` seam.
- **6b. SCIP high-confidence edges (4.3).** Raise TS/Python/Java edge confidence from ~0.5 to 1.0
  (SCIP from Sourcegraph), matching Go's go/types today. Upgrades the transitive floor fleet-wide.
- **6c. Cross-language / ROS boundary (4.1).** Index `.msg`/`.srv`/`.action` as interface types so
  compat works across ROS packages.
- **6d. Shared team server (4.2).** Org-wide index + fleet compat queries. The `Store` interface
  already abstracts this.

---

## Dependency & sequencing summary

```
Phase 1a (multi-lang) ──┬─> 1b (auto-index/watch generalization)
                        └─> 4a (external_refs capture, pull early)
1c (releases) ── independent, can ship first (fastest win)
2a (is_test) ─> 2c (brief)
2b (intent)  ─> 2c (brief), 2d (rootcause_trace "why")
2d (rootcause_trace) needs rootcause core (built) + per-lang frame parser
3a/3b/3c ── independent, interleave anytime (all reuse built primitives)
4a ─> 4b ─> 4c (ximpact)          5a/5b need only the MCP backend (built)
6a needs a spec first; 6b (SCIP) strengthens 1a's edges; 6c/6d are research
```

**Recommended order to ship value fastest:** 1c (real release, hours) → 1b (auto-index, hours) →
1a (multi-lang, the big unlock) → 3a/3b (cheap agent-UX wins) → 2a→2b→2c→2d (the flagship reads) →
3c (CI hook) → 4a→4b→4c (fleet) → 5.x (surfaces) → 6.x (moonshots).

**Per-phase definition of done:** all 5 CI jobs green (adapters can't be verified in-sandbox);
invariants 1–6 upheld; new pure logic unit-tested; `docs/agent-features.md` + `PROGRESS.md` updated.
