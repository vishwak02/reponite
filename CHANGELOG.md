# Changelog

All notable changes to reponite are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For the full session-by-session build log, see [PROGRESS.md](PROGRESS.md).

## [Unreleased]

A correctness pass over the whole tool, driven by dogfooding it against a real
multi-repository robotics fleet, followed by the last of the deferred roadmap.

### Fixed

- **The CI gate passed when it couldn't see the ref.** `ci-check --base <typo>` exited
  **0** with "no exported API breaks": an unindexed base produces an empty diff, which is
  indistinguishable from a clean one. A typo'd ref — or a CI job that forgot to index the
  base — turned the gate into a rubber stamp. It now exits **2** ("couldn't check",
  distinct from 1 "checked, and it breaks") and names the refs it lacks. ([#34])
- **Fleet `grep` returned a silent zero at an unindexed ref.** The single-repo path said
  "(ref not indexed)"; the fleet path had lost that, so `0 matches` looked like a real
  answer. It now names the repos missing the ref, or says nothing was searched at all.
  ([#34])
- **`brief`, `context`, and `blast-radius` answered blank for a symbol in another fleet
  repo.** Since `search` became fleet-wide, looking up a symbol it found would return an
  empty target — which reads as "this symbol is empty", not "I looked in the wrong repo".
  They now resolve the owning repo (reporting which one), refuse on ambiguity, and offer
  "did you mean…?" on a miss. ([#34])
- **`grep` under-matched on regex alternation.** `grep "TODO|FIXME"` returned *zero*
  matches while `grep TODO` returned 517: the pattern was matched literally, and the
  trigram prefilter then looked for trigrams of the raw alternation string, which exist in
  no file. Patterns are now regexes by default (`--fixed` opts into literal matching), and
  a regex prefilters through its required literals — an alternation ORs its branches'
  candidate sets rather than intersecting across them. ([#18])
- **The "module-path precise" cross-repo tier never fired on real repos.** It compared a
  caller's import path (`github.com/acme/api/pkg/user` — module path *plus* package path)
  to the target's module root by exact equality, so every multi-package repo's callers
  silently fell back to name-based matching at 0.6 confidence. Matching is now
  exact-or-slash-prefixed, with a guard so a sibling module like `apiv2` can't collide.
  ([#32])
- **Opening a pre-existing index failed after upgrading.** An index over a column added by
  a migration was declared in the base schema, where `CREATE TABLE IF NOT EXISTS` is a
  no-op on an existing table — so the column didn't exist yet and `Open` errored for every
  previously indexed repo. ([#32])
- **C++ symbols were attributed to the wrong name.** An endpoint inside an in-class method
  was reported against a parameter's *type* (`in=NodeHandle`) or a member variable, because
  in-class definitions name via a node type that name resolution deliberately excluded.
  The declarator is now authoritative, and when it yields no name the callable is dropped
  rather than given an invented one. ([#23])
- **`grep` truncated silently with no way to page**, and `usages` inherited that cap —
  meaning `verify_edit` and `blast_radius` were reasoning over a truncated list of call
  sites. ([#22])

### Added

- **SCIP support** — drop an `index.scip` at a repo root and cross-repo callers are matched
  by globally unique symbol moniker (`scip-resolved`, 0.95) instead of by name. Reads the
  protobuf with a small standard-library decoder: no new dependency, no build tag. ([#30])
- **Persistent fleet registry** — indexing registers the repo, so `serve`, `mcp`, and
  cross-repo queries mount your whole fleet from any directory. New `reponite fleet`
  command. ([#29], [#32])
- **Per-caller signature skew** — `ximpact` now reports which individual callers still
  expect the *old* shape of a symbol (`expected_signature: stale`, plus a `stale_callers`
  count), not just that the contract moved. ([#27], [#32])
- **Optional neural semantic search** behind a `SemanticRanker` seam. The pure identifier-
  aware ranker remains the default; an OpenAI-compatible embeddings endpoint can be
  configured, results always name the ranker that produced them, and a failed endpoint
  falls back with the failure recorded. Embeddings are cached by content hash. ([#28],
  [#32])
- **Index-time exclusion** — a default vendored set, `.reponiteignore` (gitignore syntax),
  and `--exclude` globs, applied once at index time so every surface benefits. ([#25])
- **ROS 1 subscriber message types** recovered from the callback's parameter, since
  `subscribe()` carries no template. Fleet-wide, this took subscriber endpoints with an
  unknown type from 459 of 503 down to 119. ([#24])
- **`Usages` and `Verify` dashboard views**, over API endpoints that previously had no UI.
  ([#26])
- **`grep` paging** — `--limit` / `--offset` with a deterministic order, plus a `/api/grep`
  endpoint. ([#22])
- **`--local`** on every fleet-wide command, to scope back to the current repository.
  ([#32])

### Changed

- `reponite_semsearch` now returns an envelope (`{ranker, hits, note, _meta}`) rather than a
  bare array, so a ranking always states how it was produced. ([#28])
- `grep` treats its pattern as a regex by default on every surface. ([#18])

## [0.2.0]

### Added

- **`reponite_topics`** — the ROS communication graph: publishers paired with subscribers
  (and service/action clients with servers) by name across the fleet. These are runtime
  edges that no call graph can contain, because the two ends live in different processes.
  ([#17])
- **`reponite_verify_edit`** — pass a file's proposed content and get back what breaks
  before saving or compiling. ([#16])
- **`reponite_usages`** — every call site with its exact line and enclosing function,
  cross-checked against the call graph. ([#15])
- **Fleet intelligence** — multi-repo mounts, fleet-wide search, self-healing "did you
  mean…?" on a miss, and the `blast_radius` pre-edit macro. ([#14])
- **Module-path-precise `ximpact`** — `external_refs` captured from import bindings, and
  per-repo module identity. ([#11])
- **C, C++, and Rust** language support.

## [0.1.2]

### Added

- **ROS interface compatibility** — `.msg`, `.srv`, and `.action` files indexed as typed
  contracts, so adding a field is correctly reported as `shape_changed`. ([#5])
- **`ximpact` contract fusion** — resolve the target's own definitions and report whether
  its contract moved. ([#7])
- **`MultiStore`** fleet aggregator and a repo-aware team server. ([#8])

## [0.1.1]

### Added

- **Web dashboard** and **semantic search** (identifier-aware, no model required). ([#4])
- **Multi-language parsing** (Go, Python, JavaScript, TypeScript, Java), the agent-facing
  reads (`brief`, `rootcause_trace`), and the `ci-check` CI gate. ([#3])

## [0.1.0]

First release.

### Added

- The three-hash identity model — `symbol_hash`, `signature_hash`, `behavior_hash` — and
  the Compatibility Oracle's four verdicts.
- Root-cause drill-down to the mutation site.
- Trigram-backed `grep`, fused with each hit's enclosing symbol.
- Honest edge confidence: every call-graph edge carries its `resolution_method`.
- Content-addressed storage, so indexing many refs costs unique content, not refs × size.
- MCP server, CLI, and the SQLite + tree-sitter adapters.

[Unreleased]: https://github.com/vishwak02/reponite/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/vishwak02/reponite/compare/v0.1.2...v0.2.0
[0.1.2]: https://github.com/vishwak02/reponite/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/vishwak02/reponite/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/vishwak02/reponite/releases/tag/v0.1.0
[#3]: https://github.com/vishwak02/reponite/pull/3
[#4]: https://github.com/vishwak02/reponite/pull/4
[#5]: https://github.com/vishwak02/reponite/pull/5
[#7]: https://github.com/vishwak02/reponite/pull/7
[#8]: https://github.com/vishwak02/reponite/pull/8
[#11]: https://github.com/vishwak02/reponite/pull/11
[#14]: https://github.com/vishwak02/reponite/pull/14
[#15]: https://github.com/vishwak02/reponite/pull/15
[#16]: https://github.com/vishwak02/reponite/pull/16
[#17]: https://github.com/vishwak02/reponite/pull/17
[#18]: https://github.com/vishwak02/reponite/pull/18
[#22]: https://github.com/vishwak02/reponite/pull/22
[#23]: https://github.com/vishwak02/reponite/pull/23
[#24]: https://github.com/vishwak02/reponite/pull/24
[#25]: https://github.com/vishwak02/reponite/pull/25
[#26]: https://github.com/vishwak02/reponite/pull/26
[#27]: https://github.com/vishwak02/reponite/pull/27
[#28]: https://github.com/vishwak02/reponite/pull/28
[#29]: https://github.com/vishwak02/reponite/pull/29
[#30]: https://github.com/vishwak02/reponite/pull/30
[#32]: https://github.com/vishwak02/reponite/pull/32
[#34]: https://github.com/vishwak02/reponite/pull/34
