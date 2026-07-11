# Reponite — Architecture Extension (Agent-Facing Reads)

> **Implementation status (2026-07-13):** `reponite_brief` (§8C), `reponite_rootcause_trace` (§8A.4),
> `reponite_ximpact` (§8B, **module-path precise**), the grep layer (§10A) and the semantic rung
> (§10A.2, ADR-020, now **IDF-ranked**) are built and merged. Added since: **fleet mount** (multi-repo
> MCP MultiStore) with fleet-wide `search`/`grep`/`semsearch` + self-healing "did you mean" (§ agent
> UX), **`reponite_repos`** (fleet orientation), **`reponite_blast_radius`** (pre-edit macro),
> **`reponite_investigate`** (one cited dossier answering "how does X work?", §2),
> **`reponite_usages`** (call sites with lines, graph-verified), **`reponite_verify_edit`** (§3
> read/write loop — what breaks if you save a proposed edit, before compiling), and
> **`reponite_topics`** (§8D — ROS protocol-aware topic/action edges: publishers ↔ subscribers
> linked by name across the fleet, the runtime graph the call graph can't see). Intent linkage
> (§8A.6) ships as a git-blame provider. Deferred: a persistent cross-run `global.db` registry (the
> `serve`/`mcp` MultiStore aggregates a multi-dir fleet today), per-caller signature-skew,
> a neural embedder behind the `Embedder` seam,
> and SCIP-grade
> cross-boundary confidence (Phase 6b). See `docs/BUILD_PLAN.md` and `PROGRESS.md` for the log.

*Extends the base architecture. Adds four capabilities that turn Reponite's index into concrete "faster / fewer tokens / minutes-to-answer" wins for coding, explanation, and debugging agents: an editing-brief bundle, a root-cause drill-down, cross-repo impact, and a lexical/grep retrieval layer (the base of a retrieval ladder, §10A). Section numbers slot into the base spec (e.g. §8A extends §8). ADRs continue from ADR-013. Read alongside the base spec and the two build-plan docs.*

**Design stance carried forward:** none of these features add a new source of truth or weaken any invariant. They are (a) query-time assembly over the existing three-hash/graph/manifest core, plus (b) a few small pieces of extra indexing (`is_test`, `external_refs`, and a raw-file trigram index for grep) captured cheaply when the indexer already has the data. The "never lie" principle (§Design Principles) governs all four: every bundled fact keeps its own `resolution_method`/`confidence`, and confidence propagates to the aggregate.

---

## §8A — Root-Cause Drill-Down (debugging flagship)

### 8A.1 The question
"It worked at ref A, broke at ref B — *what actually changed, and why*." The Oracle (§8) tells you a symbol's behavior changed; root-cause tells you the **origin** of that change and its cause, in one bounded walk.

### 8A.2 The computable definition (why this is uniquely ours)
The three-hash model (§6) makes "origin vs. merely affected" a precise, cheap distinction:

- A symbol is a **root cause** between refs A and B if its own `symbol_hash` differs (its text changed) OR its `signature_hash`/outgoing-edge set differs. It is a *mutation site*.
- A symbol is **merely affected** if its `symbol_hash` is identical across A and B but its `behavior_hash` differs — i.e. it changed *only* because a callee did. This is propagation, not cause.

So a debugging walk = descend the target's transitive callee subgraph (intersected across both refs) in reverse-topological order, and collect the **frontier of symbol_hash-changed nodes**. Those are the true origins; everything above them on the path is propagation.

### 8A.3 Output
For each origin symbol, ordered by proximity to the target and by confidence:
- `symbol_hash` diff (old/new `body_preview`, full body via handle), and `signature_diff` if the shape changed.
- The edge that changed (callee added/removed/retargeted), with its `resolution_method`/`confidence`.
- **Cause:** the intent record for the changing commit — PR number / ticket ID / commit (from `intent.db`, keyed on `(symbol_hash, commit_hash)`; §9.2). This is the "why," and it needs only the *linkage* half of intent, not LLM summaries (see §8A.6, ADR-017).
- Path from target → origin (the propagation chain).
- `_meta.confidence = min` over every edge on the traversal path — a drill-down through heuristic edges is labeled, never asserted.

### 8A.4 Stack-trace seeding — the "minutes" path
`reponite_rootcause_trace(stacktrace, from_ref, to_ref, repo?)`: parse a stack trace (language-specific frame → `file:function`), map each frame to a `symbol_hash` at each ref via `file_paths` + name (§9.1), then run the drill-down **along the actual failing path**. The agent pastes the trace; it needn't know the entry symbol. This is the difference between "narrow it in minutes" and "go read the diff yourself."

### 8A.5 Cost & bounds
Pure query-time graph walk over indexed `symbol_hash`/`behavior_hash`/`edges` + an intent lookup. No new indexing. Bounded by `max_depth` (default = the `behavior_hash` horizon, §6.4). Both refs must be indexed — dedup (§4) makes that cheap. Target P50 < 150ms typical, < 500ms deep.

### 8A.6 Dependency on minimal intent
The "why" requires the commit→PR/ticket linkage in `intent.db`. This promotes a **linkage-only** intent layer onto the critical path (rows with `commit_hash` + `pr_numbers`/`ticket_ids`, `summary` NULL). LLM summaries and clustering stay deferred (ADR-017).

---

## §8B — Cross-Repo Impact (change-safety flagship)

### 8B.1 The question
"Who across the fleet depends on this symbol." The Oracle answers cross-repo *compatibility* (is my symbol the same over there, §8); cross-repo impact answers *who over there calls it* — the question an agent must answer before changing an exported API.

### 8B.2 Identity across the repo boundary
`symbol_hash` embeds `repo` (§6), so it never matches across repos — that is correct for storage but useless for cross-repo linkage. Linkage instead keys on how a caller actually names a dependency: **`(module/package path, exported symbol name, signature_hash?)`**. When indexing repo A, any reference that resolves *outside* A (an import to another module) is recorded as an external reference (§9A.2). At query time, a symbol's `(module_path, name, signature_hash)` is matched against every repo's external references via the global registry.

### 8B.3 Output & fusion with the Oracle
`reponite_ximpact(symbol, repo, ref?)` returns callers grouped by repo and ref, each with `resolution_method`/`confidence`. Fused with the Oracle: for each caller it also reports whether *that caller's expected signature* still matches — e.g. "4 services call `getUserV2`; 3 still expect the old signature." That fusion is the actual deploy-safety answer.

### 8B.4 Confidence is the whole game here
SCIP resolves within a repo, not across the boundary (unless the dependency is module-resolved/vendored/monorepo). So most cross-repo edges are **medium confidence, name/path-based**, and labeled as such; only module-resolved links are high. Consistent with "never lie."

### 8B.5 Honest limitations (state in output)
- This is **source-call-graph** impact, not RPC topology — HTTP/gRPC/queue calls between services are invisible.
- **Version skew:** which ref of the caller repo? Default to each caller repo's production/pinned ref, stated in `_meta`.
- Cross-repo *behavior* propagation stays deferred (§8.4, §26). This feature is impact (*who calls*), not behavior (*does it still behave the same*).

### 8B.6 Split build (important)
The `external_refs` **capture** is cheap and happens at resolve time from the first structural session (data you have anyway). The fleet-wide **query** needs the global registry and lands in the team/cross-repo milestone. Capture early, query later.

### 8B.7 As built (2026-07)
Both halves shipped. **Capture:** a caller file's import bindings (`imports.go`, per language) resolve its qualified/from-imported call sites (`Symbol.QualifiedCalls`) to `(module_path, name)` external references at index time (`resolveExternalRefs`, `import-resolved`@0.75), stored per-ref in `external_refs` and cleared on reindex. A repo's `module_path` is detected from its ecosystem manifest (`module.go`: `go.mod`/`package.json`/`pyproject.toml`/`pom.xml`, root-most wins). **Query:** `XImpact` fuses tier 1 module-resolved callers (match `external_refs` on the target's own `module_path` — so a same-named symbol in an unrelated module does **not** collide) with tier 2 the original name-based `unresolved-external` scan (fallback, deduped by caller); each caller carries its `resolution_method` so the precise and heuristic tiers are distinguishable. The `serve` **MultiStore** fans `ExternalRefsTo` across several per-repo stores for a live multi-dir fleet view. **Still deferred:** a persistent cross-run `global.db` (so separately-invoked CLI indexes share a registry without `serve`); recording each caller's `target_signature_hash` for per-caller expected-signature skew (§8B.3 currently detects contract drift across the *target's* definition refs, not per caller); SCIP to lift cross-boundary edges above name/path confidence (§8B.4, Phase 6b).

---

## §8C — Editing-Brief Bundle (coding-agent flagship)

### 8C.1 The question
"I'm about to edit symbol X — give me exactly what I need, and nothing else." Replaces 5 separate calls (`context` + `callers` + `callees` + `why` + `impact`) with one token-budgeted bundle.

### 8C.2 Contents, in priority order
1. **Target:** full body, signature, path, complexity, `exported` (you're editing it, so full body is warranted).
2. **Callees (depth 1):** signature + `body_preview` + fetch handle + confidence — what it relies on.
3. **Callers (depth 1):** signature + path — immediate blast radius.
4. **Type context:** definitions/signatures of types in the target's parameters/returns.
5. **Covering tests:** test symbols that reference the target (via `is_test`, §9A.1).
6. **Intent:** linkage (PR/ticket) + summary if present.
7. **Compat snapshot:** for exported symbols, the Oracle verdict across production refs — so the agent knows the API is fleet-load-bearing *before* touching it.

### 8C.3 Token-budgeted assembly (the mechanism that makes "fewer tokens" a guarantee)
`reponite_brief(symbol, ref?, repo?, token_budget?, include?)`. The tool fills sections in the priority order above until `token_budget` (default ~3k) is reached, then truncates lower-priority sections and records what was dropped in `_meta.omitted` with a `token_estimate`. Everything except the target is signature + `body_preview` + a `symbol_hash` handle for on-demand full body — that discipline is what turns graph access into an actual token saving instead of a bigger payload.

### 8C.4 Cost
Pure assembly over existing primitives (a few point queries + one bounded reverse-edge scan + one compat lookup) plus `is_test` filtering. No new indexing beyond `is_test`. Target P50 < 60ms, single repo.

---

## §8D — ROS Communication Graph (`reponite_topics`) — the robotics-fleet flagship

### 8D.1 The question
"Who reacts when I publish to `/cmd_vel`?" / "Where does this subscriber's data come from?" In a ROS system the answer is **not in anyone's call graph.** A publisher and a subscriber run in different processes and are joined only by a *topic name string* that the middleware resolves at runtime — so there is no source-level edge between them to resolve (contrast §8B, which links by import binding). This is the single most-asked cross-node question in a robotics monorepo/fleet, and it is exactly the edge §6's call graph structurally cannot contain.

### 8D.2 The computable definition
An **endpoint** is a call to a client-library comms primitive bound to a name literal: `advertise`/`create_publisher`/`rospy.Publisher` (publisher), `subscribe`/`create_subscription`/`rospy.Subscriber` (subscriber), the `advertiseService`/`create_service`/`serviceClient`/`create_client`/`ServiceProxy` family (services), and `rclcpp_action`/`ActionServer`/`ActionClient` (actions). Across every idiom the bound name is the **first quoted string** in the call — because the message *type* is either a C++ template (`<T>`, unquoted) or a Python positional identifier (unquoted), never the first literal. Two endpoints **link** when they share a name (within a family: topic / service / action) and sit on opposite sides (producer ↔ consumer). Names are normalized by stripping a single leading `/` so an absolute `/scan` links a relative `scan`.

### 8D.3 Output
`reponite_topics(topic?, repo?, ref?)` — no `topic`: the whole comms map, connected edges first; with `topic`: that one name's producers and consumers. Each endpoint carries `repo`, `path`, `line`, `role`, the normalized `name` + `raw` (as-written), the C++ template `msg_type` when captured, and the enclosing symbol `in` (a hop back into the call graph). Each group carries a `connected` flag and a `confidence`.

### 8D.4 Confidence & honest limits (stated in the result)
Name-string linkage is **medium confidence (0.75)**, bumped to 0.9 only when a producer and consumer share a captured message type, and 0.6 for a one-sided (dangling) endpoint. Namespace/launch-file **remapping is not resolved**, dynamic (non-literal) topic names are counted as `unresolved` rather than guessed, and this is *source-idiom* inference, not DDS/rosmaster wire truth — a name match is a strong hint, never asserted as a proven connection. Consistent with "never lie."

### 8D.5 Cost
Pure query-time text scan over the file content the Store already holds (§10A's raw blobs) — **zero new indexing**, like `grep`/`usages`. Idioms are gated by file language (C++ idioms only in C/C++ files, Python only in `.py`) so a JavaScript observer's `.subscribe(...)` never masquerades as a ROS edge. Fleet-wide by default via the `serve`/`mcp` MultiStore.

---

## §9A — Data-model additions

### 9A.1 `nodes.is_test` (feeds §8C covering tests)
```sql
ALTER TABLE nodes ADD COLUMN is_test BOOLEAN NOT NULL DEFAULT 0;
-- set at parse/resolve time: *_test.go, test framework markers, per-language heuristics
CREATE INDEX idx_nodes_is_test ON nodes(is_test) WHERE is_test = 1;
-- covering tests(target) = edges where to_hash = target AND from node.is_test = 1
```

### 9A.2 `external_refs` (feeds §8B cross-repo impact)
```sql
CREATE TABLE external_refs (
  from_hash         TEXT NOT NULL REFERENCES nodes(symbol_hash), -- caller symbol in this repo
  from_repo         TEXT NOT NULL,
  target_module     TEXT NOT NULL,   -- module/package path the reference resolves to
  target_name       TEXT NOT NULL,   -- exported symbol name
  target_signature_hash TEXT,        -- if resolvable (SCIP) → higher-confidence match
  resolution_method TEXT NOT NULL,   -- scip | treesitter | heuristic
  confidence        REAL NOT NULL DEFAULT 0.6,
  PRIMARY KEY (from_hash, target_module, target_name)
);
CREATE INDEX idx_extref_target ON external_refs(target_module, target_name);
```
Repo module identity: add `module_path TEXT` to the per-repo registry (`registry.db`) and denormalize `(module_path → repo, ref)` into `global.db` so a symbol's `(module_path, name)` resolves to candidate caller repos fleet-wide.

**As built:** the SQLite adapter keys `external_refs` by `(repo, ref, from_name, target_module, target_name)` with `resolution_method`+`confidence` (symbols are keyed by package-qualified id, not `symbol_hash`, so `from_name` is the caller's qid), indexed on `(target_module, target_name)`; `target_signature_hash` is not yet captured (per-caller skew deferred, §8B.7). `module_path` lives in a per-repo `repo_modules(repo, module_path)` table; the cross-run `global.db` denorm is deferred in favor of the runtime `serve` MultiStore.

### 9A.3 Intent linkage promoted (feeds §8A cause + §8C intent)
No schema change — the base `intent` table (§9.2) already has `commit_hash`, `pr_numbers`, `ticket_ids`. The change is that **linkage rows are populated on the critical path with `summary` NULL**, independent of any LLM. Add:
```sql
CREATE INDEX idx_intent_symbol_commit ON intent(symbol_hash, commit_hash);
```

---

## §14A — New interface surface

### CLI
```
reponite brief <symbol> [--ref R] [--budget N]        Editing-brief bundle
reponite rootcause <symbol> --from A --to B           Behavior-change origin walk
reponite rootcause --trace <file> --from A --to B     Seed the walk from a stack trace
reponite ximpact <symbol> [--ref R]                   Cross-repo reverse dependencies
```

### MCP tools
```
reponite_brief(symbol, ref?, repo?, token_budget?, include?)      → token-budgeted bundle + _meta
reponite_rootcause(symbol, from_ref, to_ref, repo?, max_depth?)   → ordered change origins + cause + _meta
reponite_rootcause_trace(stacktrace, from_ref, to_ref, repo?)     → path-scoped origins + _meta
reponite_ximpact(symbol, repo?, ref?)                             → fleet callers, compat-fused + _meta
```
`reponite_impact` (§14.2) gains an optional `cross_repo?` flag that delegates to `ximpact`. All responses carry the standard `_meta` envelope (§10.3) with aggregate confidence.

---

## §13A — New failure / degradation modes
| Situation | Behavior |
|---|---|
| Brief exceeds `token_budget` | Truncate lowest-priority sections; list them in `_meta.omitted` with handles to fetch on demand |
| Root-cause: target behavior unchanged A→B | Return fast "no behavioral change"; no walk |
| Root-cause through heuristic edges | Origins returned with `_meta.confidence < 1.0` and `resolution: partly heuristic` |
| Stack-trace frame maps to no indexed symbol | Skip frame, note in `_meta.unmapped_frames`; walk the frames that did map |
| Cross-repo match name/path only (no SCIP) | Return at medium confidence, labeled; never asserted as proven |
| Caller repo ref ambiguous | Default to its production/pinned ref; state the chosen ref in `_meta` |
| Target repo/callee repo not indexed | Report as "unknown (not indexed)", never as "no callers" |

---

## §18A — Performance targets
| Operation | Target |
|---|---|
| `reponite brief` (1 symbol, 1 repo) | P50 < 60ms |
| `reponite rootcause` (typical depth) | P50 < 150ms |
| `reponite rootcause --trace` | P50 < 250ms |
| `reponite ximpact` (fleet, indexed) | P50 < 200ms |

---

## Architecture Decision Records (continued)

### ADR-014 — Agent-shaped bundle reads with token-budgeted assembly
Exposing atomic primitives (`context`/`callers`/`callees`/`why`/`impact`) forces an agent into multi-call assembly, spending round-trips and tokens and often falling back to reading source. **Decision:** add `reponite_brief`, a single call that assembles the minimal-complete editing context in a caller-specified `token_budget`, filling by priority and truncating with explicit `omitted` handles; only the target ships a full body, everything else is signature + preview + fetch handle. **Consequences:** the "fewer tokens" promise becomes a bounded guarantee rather than a hope; no new indexing beyond `is_test`. Cost: a priority/truncation policy to maintain.

### ADR-015 — Root-cause via the three-hash frontier + stack-trace seeding
A behavior-changed verdict says *that* something changed, not *what* or *why*. **Decision:** define origin precisely — a symbol whose `symbol_hash`/`signature_hash`/edges changed between refs is a mutation site; a symbol whose `behavior_hash` alone changed is merely affected — and return the frontier of mutation sites with their diff and causing PR/ticket, optionally seeded from a stack trace along the failing path. **Consequences:** minutes-to-cause debugging that only the three-hash model makes computable; reuses `diff` + intent linkage; confidence = min over the path. Cost: needs intent linkage on the critical path (ADR-017).

### ADR-016 — Cross-repo impact via a name/path/signature external-reference index
`symbol_hash` deliberately can't match across repos, so cross-repo linkage can't use it. **Decision:** capture `external_refs` (module path + name + optional signature_hash + confidence) at resolve time, and query them fleet-wide via the global registry; fuse with the Oracle to report which callers still expect the old shape. Cross-repo edges are mostly name/path-based and labeled medium confidence. **Consequences:** the "who across the fleet calls this" question is answerable and honest about certainty; capture is cheap and early, the query lands with the cross-repo milestone. Explicitly *not* cross-repo behavior propagation (still deferred, §8.4). Cost: needs per-repo `module_path`; blind to RPC-level calls.

### ADR-017 — Promote linkage-only intent to the critical path; defer summaries & clustering
`reponite_brief` and `reponite_rootcause` both consume the commit→PR/ticket linkage; nothing on the critical path needs LLM summaries or Leiden clusters. **Decision:** populate `intent` linkage rows (PR/ticket/commit, `summary` NULL) on the critical path with no LLM dependency; keep LLM summaries and clustering (`reponite arch`, `reponite why` full text) deferred and lazy as before. **Consequences:** the debugging "why" and the brief's provenance work without an LLM; the heavy, optional intent stays optional. This refines the earlier "cut intent" stance: cut the *expensive* half, keep the *linkage* half.

---

## Prioritization decision (recorded)
The open question was: is v1 **moat-first** (compat + debugging), or is **"explain how a component works"** co-equal? Two of the three additions (root-cause, cross-repo impact) and most of the brief lean on the three-hash/graph core, not on intent summaries or clustering — so they **reinforce moat-first**. Recommended answer: **moat-first, with linkage-only intent on the critical path** (ADR-017); full component-level explanation (intent summaries + clustering) stays a later, demand-driven milestone. Day-one demo: the behavior-diff / root-cause debugging story plus the editing brief. *(Override this and the session order in the build-plan docs reshuffles to pull intent-summaries and clustering forward.)*

---

## Build integration — where these land in the session map
Slots into `reponite-claude-build-plan.md`. Capture-early / query-late is deliberate.

| Piece | Session | Milestone | Note |
|---|---|---|---|
| `nodes.is_test` capture | extend **S1.4** | M1 | node property at resolve time; near-zero cost |
| `external_refs` capture | extend **S1.4** | M1 | record out-of-repo references now; query later |
| `module_path` in registry | extend **S2.2** | M2 | needed to match external refs across repos |
| Linkage-only intent (`intent.db` rows via git blame/commit) | new **S3.5** | M3 | feeds brief + root-cause; no LLM |
| `reponite_rootcause` (+ `_trace`) | new **S3.6** | M3 | needs `diff` (S3.2) + behavior + intent (S3.5) |
| `reponite_brief` | new **S3.7** | M3 | needs struct + compat (S3.3) + intent (S3.5) + `is_test` |
| Register new tools in MCP | extend **S3.4** | M3 | expose brief/rootcause over MCP |
| `reponite_ximpact` fleet query | new **M7 session** | M7 | needs global registry; consumes S1.4's `external_refs` |

Net effect on the plan: ~3 new M3 sessions (S3.5–S3.7), two cheap extensions to S1.4/S2.2, and one M7 query session — the shippable-Oracle count moves from ~19 to ~22 sessions, and the day-one demo gains the root-cause and brief stories.

---

## §10A — Lexical Search Layer (`grep`) — the retrieval ladder's base

### 10A.1 Motivation
Agents constantly need exact, non-semantic lookups: "does this string exist,"
"every `os.Getenv` call," "grep this error message," "files matching
`**/*.yaml`," "where is this magic constant." Today an agent shells out to
`grep`/`rg` over a live checkout — which fails for refs not checked out, ignores
freshness and confidence, and returns unbounded output it must then read.
Reponite already stores every ref's file content, so it can serve exact search
across *any indexed ref and the whole fleet* from one always-fresh server. This
is what makes Reponite the single retrieval interface — not just code mapping,
but "any information from the code."

### 10A.2 The retrieval ladder
Expose a spectrum so the agent picks the cheapest rung that answers its question:
1. **Lexical (`grep`)** — exact string / regex / glob. Cheapest, most precise for presence & location.
2. **Structural** — symbols, callers/callees, impact.
3. **Semantic** — "where is the thing that does X."
4. **Intent** — provenance / why.
5. **Compat (Oracle)** — cross-ref / cross-repo verdicts.
Grep is the bottom rung and frequently eliminates file-reading entirely — which
is where the token savings actually come from.

### 10A.3 Mechanism — trigram-indexed regex (à la Zoekt / Code Search)
A trigram index over *unique* file content (content-addressed → indexed once no
matter how many refs contain the file) produces candidate files for any query
with literal atoms; the engine intersects candidates with the requested ref's
manifest file set, then verifies with Go `regexp` over the raw blobs, extracting
matching lines + line numbers. Substring/literal queries use trigrams directly;
regex with no literal atoms (e.g. `.*`) can't prefilter → a bounded, streamed
scan, labeled as such. Path/glob filters use the per-ref `file_paths`;
structured filters (`lang`, `is_test`, `exported`, `kind`) narrow before scan.

### 10A.4 Graph fusion (the part plain grep can't do)
Every hit is annotated with its **enclosing symbol** (via `file_contents`
start/end lines → `symbol_hash`) and that symbol's quick facts (exported? caller
count?). So a lexical match is one hop from the graph: *"match in `Charge()`
[exported, 12 callers]."* Grep that knows what it hit — and can hand straight
off to `impact` / `brief` / `compat` on that symbol.

### 10A.5 Token-lean output & scope
Matches are grouped by file/symbol, each with line number and the matching line
(± configurable context), a total count, and truncation to a result/token budget
with an omitted count — never a raw dump. Search one ref, several refs, or a repo
group; every response carries ref + freshness + confidence `_meta`. Because
content is deduped, searching many refs doesn't multiply cost for shared files.
Raw-source authorization (§15) governs results, so secrets in `.env`/config are
gated exactly like source.

### §9A.4 — Data additions for lexical search
- **Raw file blob.** Store each file's raw bytes as a content-addressed blob
  (`raw_hash`) alongside its canonical identity; `files.raw_hash` links them.
  Grep scans raw bytes (what the ref actually contains); canonical form remains
  for identity/dedup. Deduped by `raw_hash`.
- **Trigram index.** A trigram index over raw file text keyed by `raw_hash`
  (SQLite FTS5 `trigram` tokenizer, or a dedicated trigram posting table). Built
  at index time, deduped by content.

### §14A — Lexical interface
- CLI: `reponite grep <pattern> [--ref R] [--repos …] [--glob G] [--lang L] [-C n] [--fixed]`
- MCP: `reponite_grep(pattern, ref?, repos?, glob?, lang?, is_test?, context?, fixed?, limit?, token_budget?)` → grouped matches + enclosing symbol + `_meta`

### §13A — Failure modes (lexical)
| Situation | Behavior |
|---|---|
| Regex with no literal atoms | Bounded streamed scan; if truncated, `_meta` flags partial + total seen |
| Invalid regex | Error with the compile message; no partial output |
| Binary / oversized file | Skipped, counted in `_meta.skipped` |
| Too many matches | Truncate to `token_budget`/`limit`; report total match count |

### §18A — Performance (lexical)
| Operation | Target |
|---|---|
| `grep` literal, 1 repo/ref (trigram-prefiltered) | P50 < 50ms |
| `grep` fleet, 20 repos | P50 < 300ms |
| `grep` pathological regex (no atoms) | bounded / streamed, labeled |

### ADR-019 — Trigram lexical layer as the retrieval ladder's base
**Context.** Agents need exact lookups as often as structural ones; making them
shell `grep` over a checkout is ref-blind, stale-blind, unbounded, and
disconnected from the graph. **Decision.** Add a trigram-indexed regex/literal
search over content-addressed raw file blobs, ref- and fleet-scoped, fused with
the symbol graph, exposed as `reponite_grep`. **Consequences.** Reponite becomes
the single retrieval interface (exact → structural → semantic → intent →
compat); lexical hits link one hop to the graph; responses are fresh,
confidence-tagged, and token-bounded. **Cost.** Store raw file text (small,
deduped) + a trigram index; a pathological no-atom regex needs a bounded scan
fallback. This also serves the YAML/HTML/.env case (§ prior discussion): those
files may lack a behavior_hash, but grep + graph fusion makes them fully
retrievable and linkable.

### Build integration (lexical)
| Piece | Session | Milestone | Note |
|---|---|---|---|
| Raw file blob + trigram index capture | extend **S1.2** | M1 | store raw bytes + build trigram at index time |
| `reponite grep` (HEAD, 1 repo) + graph-fused enclosing symbol | new **S1.7** | M1 | independent of the Oracle; ship early — highest-utility agent primitive |
| Ref-aware + fleet `grep --repos` | extend **M7 session** | M7 | needs coordinator + global registry |

Grep is deliberately pulled early (M1): it depends only on stored file content +
nodes, not on refs or the Oracle, and it is the primitive an agent reaches for
most.
