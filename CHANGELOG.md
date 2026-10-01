# Changelog

All notable changes to reponite are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For the full session-by-session build log, see [PROGRESS.md](PROGRESS.md).

## [Unreleased]

### Cross-boundary questions (devel)

The questions a call graph alone cannot answer: which code printed this log
line, what runs behind a call through a base class or plugin interface, and
which frontend call reaches which backend view.

#### Added

- **`logsite "<log line>"`** (MCP `reponite_logsite`): paste a line from a
  real log and get the code locations whose format string produces it —
  printf `%d/%s/%.2f`, fmt/spdlog/Python `{}`/`{name}`, `%(name)s`, JS
  `${…}`, stream (`<<`) and concatenated literals — with the enclosing
  function and the logging call. The ROS logger name in the line
  (`[ros.<package>.<name>]`) ranks sites in that package first, and a logging
  call outranks a helper (`operator<<`, `toString`) that printed part of it.
- **`impls <Base[::method]>`** (MCP `reponite_impls`): every class deriving
  from a C++ base fleet-wide — including implementations in other repos loaded
  at runtime — with derivation depth, `PLUGINLIB_EXPORT_CLASS` registration
  and each class's own definition of the method; test mocks listed last.
- **`routes [path]`** (MCP `reponite_routes`): HTTP client calls linked to the
  server routes they reach, across repos. Server: Django `path`/`re_path`/`url`
  composed through `include()` prefixes, DRF routers (nested routers,
  `@action`), FastAPI/Flask decorators with router prefixes, Express, Go
  net/http and gin/echo/chi. Client: `fetch`, axios or any client object's
  `get/post/…` (TS generics included), requests/httpx/sessions, with URLs
  resolved from literals, template strings, f-strings, concatenation and named
  URL constants. A client URL malformed as written (a stray brace from a
  template-literal typo) is flagged.

#### Changed

- **C++ member calls are resolved by the receiver's declared type**
  (`receiver-typed`, 0.85): parameters, locals (`auto` from `new`,
  `make_shared<T>`, or another typed expression), member fields of the
  caller's class and its bases, container elements and iterators
  (`v[i]`, `for (auto& x : v)`, `m.find(k)->second`, structured bindings),
  smart pointers, `Foo::Ptr` and typedef/using aliases, method return types,
  lambda parameters and `if (auto x = …)` declarations. A call on a type in an
  external namespace (`ros::Publisher::publish`) or on a container is an
  external leaf, never the repo's same-named method.
- **Virtual dispatch edges** (`virtual-override`, 0.6): a call through a base
  class (including `this->f()` in the base, and pure virtuals) also reaches
  each subclass's override in the repo; mock/fake/stub overrides are targets
  only for test code. A member-name guess never targets test-support code from
  production code.
- **C++ headers named `.h` are parsed as C++** when their content is C++
  (classes, namespaces, templates) — previously their classes and inline
  methods were lost to the C grammar.
- **A function returning a pointer or reference is named after itself**
  (`std::ostream& operator<<` was named `ostream`).
- When one function calls the same target both typed and untyped, the edge
  keeps the stronger resolution.
- Fleet notes are pin-aware: a repo read at its pinned ref is no longer
  reported as "not indexed" at the query's default ref.
- `IndexVer` 4: refs indexed under 3 are re-indexed on the next `index --git`.

### Resolution, storage and multi-version indexing (devel)

Driven by a benchmark of questions with known answers on real C++/ROS and
TypeScript repos: who calls a method across repos, at the versions a
deployment actually runs, and which tags carry a bug.

#### Changed

- **Call resolution uses the call's shape, not just its name.** C/C++:
  `this->f()`/`f()` resolve on the caller's own class, `A::f()` on A,
  `std::`/`boost::`/`ros::`… are external, and a member call on an object of
  unknown type only targets a method — a standard-library member name
  (`erase`, `insert`, `begin`…) stays *ambiguous* rather than being pinned on
  the repo's same-named method at 0.9. A unique method match with an unproven
  receiver is the new `member-name-resolved` (0.7). JS/TS/Python/Java also use
  import bindings: a name imported from an external package, or a call on a
  runtime global (`console`, `Math`, `JSON`, `os`, `System`…), is external;
  imports of the repo's own code (absolute Python packages, `@/` aliases) still
  resolve in-repo.
- **Out-of-class C++ definitions (`void A::f()`) are qualified by their class**
  — two classes' same-named methods in one directory no longer collapse.
- **React components are symbols:** `const X = () => …`, `forwardRef`/`memo`
  wrapped functions and class-field arrows (grep spans included).
- **Storage format v2** (migrated in place on open): names interned, symbol
  records, edge sets, span sets and external refs content-addressed and shared
  across refs, file content deflated. On a mid-size C++ repo: 4 refs 46 MB →
  11 MB; each further patch tag 0.4–1.7 MB (was ~10 MB); indexing ~20% faster.
- A multi-package workspace (several manifests at the shallowest depth) no
  longer claims one package's name as the repo's module identity.
- Each ref records the extraction/resolution ruleset it was indexed under
  (`IndexVer`); compat/diff/rootcause warn when compared refs disagree.

#### Added

- `--refs repo=ref,...` (CLI usages/topics; MCP usages, topics, grep, search,
  ximpact, blast_radius, investigate, semsearch) and `REPONITE_REFS` for every
  fleet command: read each repo at its own ref — the combination of versions a
  deployed system runs. Fleet answers now say which repos were read and which
  were not indexed at the requested ref, instead of returning a silent zero.
- `index --git a,b,c`, `--tags GLOB`, `--submodules` (each submodule indexed at
  the commit the revision pins, as `<parent>@<label>`), and resume: a commit
  already indexed under the current ruleset is skipped, or aliased for a new
  label (`AliasRef`) at a few KB.
- `REPONITE_STORE_DIR` keeps indexes outside the repos, so `--git` indexing
  only reads a clone.
- Partly qualified symbols (`Class.method`) resolve on a `.` boundary.

#### Fixed

- `--git` indexing dropped the peer stores (cross-repo contract skew was always
  "unknown"); a `--shared` clone failed with a bare "reference not found".

A correctness pass over the whole tool, driven by dogfooding it against a real
multi-repository robotics fleet, followed by the last of the deferred roadmap.

### Fixed

- **Test detection only worked for Go, so "covering tests" was silently empty
  everywhere else.** `is_test` was a Go NAME heuristic (`Test*`/`Benchmark*`/…), so a C++
  fixture in `test/`, a `test_x.py`, or a `foo.spec.ts` was not a test — and `brief`
  reported **0 covering tests** for 8 of 10 languages while advertising the section, and
  `search --tests` was meaningless. Test-ness is now captured at index time from the file
  PATH (§9A.1 always specified this; the name heuristic was a stand-in), stored per symbol,
  and used by `brief`, `blast-radius`, `context`, and `search`. The Go heuristic remains a
  fallback so pre-migration indexes behave as before. ([#36])
- **`investigate` ranked the same symbol twice.** The ranker scores one entry per symbol
  *span*, so a C++ class declared in a header and defined in a `.cpp` produced two hits
  that resolved to one qualified id — the dossier claimed more findings than it had.
  Deduped by `(repo, qid)`, keeping the best score. ([#36])
- **`investigate` reported no coverage, so it ranked noise confidently.** On a repo it can
  barely read it would return "25 relevant symbols" with a vendored lidar driver at #2.
  It now states how many symbols it ranked and the best similarity achieved, and adds a
  caveat at the *top* of the dossier when the best match is weak. ([#36])
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

- **ROS launch-file remapping is now resolved** — this removes a limitation `topics` used
  to have to state in its own output ("launch-file remapping is NOT resolved"). A node whose
  source subscribes to `scan` may actually receive `raw_scan_front`; the comms graph now
  pairs producers and consumers on the **runtime** name, so remapped endpoints link to the
  right partner instead of dangling. Each keeps its source name, reports `effective`, and
  cites the launch file (`name_resolution: launch-remapped`); `$(arg …)` expands against
  declared defaults. A target that keeps an unexpanded substitution is **reported, not
  applied** — grouping endpoints under the literal `$(arg scan_topic)` would be worse than
  not remapping. On `rr_io_amr`: 397 launch files parsed, **51 remaps applied**, 35
  unexpanded reported, 0 groups keyed on a literal. ([#37])
- **Shell/Bash language support** (the 10th language) — `.sh`, `.bash`, `.zsh`, `.ksh`, plus
  **extension-less scripts identified by their shebang**, since a CLI's entry point rarely
  has an extension and is usually the most valuable file in the tree (`#!/usr/bin/env
  python3` and `#!/usr/bin/node` are recognized too). Found by dogfooding: a shell-based CLI
  repo indexed 22 of ~460 files — Python only — and every shell tree was invisible. It now
  indexes 60. ([#35])
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
- **Index footprint reporting** — `index` now prints the file count, test-file count, and
  the directories that contributed most. A repo vendoring third-party code under a
  non-standard path was previously invisible: `rr_sootballs` turned out to be **3,600 of
  4,774 files** from one vendored firmware tree, discoverable before only by accident.
  ([#36])
- **`context` now returns `caller_edges`** — callers as objects carrying `is_test`,
  mirroring the existing `callee_edges` and matching how `brief` returns neighbors, so an
  agent parsing both no longer meets two shapes for the same idea. ([#36])

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
[#35]: https://github.com/vishwak02/reponite/pull/35
[#36]: https://github.com/vishwak02/reponite/pull/36
[#37]: https://github.com/vishwak02/reponite/pull/37
