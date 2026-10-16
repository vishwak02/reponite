# Changelog

All notable changes to reponite are documented here.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this
project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

For the full session-by-session build log, see [PROGRESS.md](PROGRESS.md).

## [Unreleased]

### Wiring outside source code (IndexVer 8)

#### Added

- **`topics`: launch-declared bindings.** A `<remap>` on a launched node whose source
  shows no endpoint for the target — a third-party package that is not indexed, or a
  name built by an expression — is listed under `declared` (role `launch_declared`,
  direction unknown) instead of the topic looking producer-less. A leading namespace
  substitution such as `$(arg ns)` is read as the root and labeled. A remap already
  explaining an indexed endpoint is not repeated.
- **`topics`: rviz configs.** `.rviz` displays are consumers and `Tools:` entries
  producers of the topics they name (`Topic`, `Marker Topic`, `Update Topic`, rviz2
  `Value:`), cited with the display name, class and line.
- **`errors`: names and texts defined as data.** When the queried text sits in a JSON or
  YAML catalogue (error tables, message files), the matching lines are returned under
  `data` with sibling keys and the code files that load the catalogue (`loaded_by`).
- JSON files (≤ 256 KB, lockfiles excepted) and `.rviz` files are stored as content-only
  files, so grep and search see them too. Re-index to pick them up.

- **`config`: keys read through a named constant.** A Go `os.Getenv(name)` /
  `os.LookupEnv(name)` whose argument is a string constant of the same package resolves to
  the constant's value.
- `reponite version` prints the index ruleset (`index_ver`).

#### Changed

- The `topics` note now says that an empty producer list means none was found in the
  indexed code, not that none exists.

### Documentation and releases

- New [docs/INSTALL.md](docs/INSTALL.md) (requirements, release binaries, building from
  source, agent setup, data locations, upgrading, uninstalling, troubleshooting) and
  [docs/USER_GUIDE.md](docs/USER_GUIDE.md) (concepts, every command, configuration, FAQ).
  The README is now a short overview pointing at them; package docs describe what exists.
- Releases build all four binaries again: Intel macOS is cross-built on an Apple-silicon
  runner instead of waiting on an Intel runner (v0.2.0 shipped without `darwin-amd64`).
  Each release publishes `SHA256SUMS` and takes its notes from this changelog.
- `install.sh` verifies the download against `SHA256SUMS`, accepts `REPONITE_VERSION`,
  creates `REPONITE_BINDIR` when needed and warns when it is not on `PATH`.

### History, errors and tests

#### Added

- **`why <symbol> [ref]`** (MCP `reponite_why`): the commits that changed a symbol's
  lines or signature (`git log -L` on its range at each indexed ref's commit, pickaxe
  fallback; moves between files followed through older refs), newest first, with date,
  author, subject, body excerpt, PR/issue/ticket ids, kind, and the first indexed ref
  containing each; plus the symbol's compat history across refs. Read-only git.
- **`errors ["<message>"|Type|status]`** (MCP `reponite_errors`): logsite generalised to
  errors — raise/throw, error values and sentinels, panics, error logs, HTTP error
  responses — ranked against a pasted message (placeholders filled), a type or a status,
  with one hop of callers and the handlers that catch them (by type, base class,
  errors.Is/As, framework handlers, client-side status checks). No argument: a per-repo
  error catalogue.
- **`tests <symbol> [ref]`** (MCP `reponite_tests`): tests exercising a symbol through
  the call graph (≤ N hops), HTTP requests to its route, or test-file references, the
  framework per file, and the command that runs only those tests.
- `--format text` and `--budget` on all three.
### Orientation (map / explain / guide)

#### Added

- **`reponite map`** / `reponite_map`: the first call on an unfamiliar codebase — README
  summary, languages, entry points (mains, CLI commands, HTTP servers with route counts,
  UI roots and page routes, ROS nodes/launch files/plugins, build targets, Dockerfile
  CMD/ENTRYPOINT, compose services, CI jobs, package scripts, Make targets), modules with
  their stated purpose, hub symbols and declared dependencies; without `--repo`, every
  repo plus the known cross-repo links. Token-budgeted with `truncated` + `next`.
- **`reponite explain`** / `reponite_explain`: one bundle to understand a symbol, file,
  `path:line`, directory or `"METHOD /route"`.
- **`reponite guide`** and the `reponite_guide` MCP tool, prompt and resource; every MCP
  tool description now opens with when to use it.
- Project files (READMEs, Dockerfiles, Makefiles, dependency manifests, CI workflows) are
  stored at index time — **IndexVer 6**.

### Production answers (devel)

#### Added

- **`ui [route|component]` / `reponite_ui`: frontend understanding.** No argument lists the
  app's pages (react-router trees and route objects, vue-router and other route tables with
  lazy imports and module-federation exposes, Next.js pages/app, SvelteKit, Nuxt) with the
  component each renders. A route or component returns its budget-bounded component tree with
  props, state, effects, event handlers and the API calls each handler/effect reaches, linked
  to the serving route (`button Save → handleSave → POST /api/orders → OrdersView`); `--uses`
  lists render sites. `--format text`, `--budget`, `truncated`/`next`. `query.UIEdges` exposes
  the render/handler/API edges for other queries.
- **Vue and Svelte single-file components are indexed**, and **JSX elements are call edges**
  (`<CartItem/>` calls `CartItem`), so context/usages see component renders. A path-alias
  import (`@app/components/X`, `$lib/X.svelte`) whose directory is in the repo is repo code.
  `routes` reads client calls on a factory-returned client (`getClient().get(…)`) and in
  `.vue`/`.svelte` files. **IndexVer 6** — reindex frontends.
- **`trace <entry> [ref]`** (MCP `reponite_trace`): "what happens when …" as one compact
  tree. The entry is a symbol, `"VERB /path"`, `path:line`, a CLI command (Django management
  commands, click/typer, argparse, cobra, commander, `case "name":` dispatch) or a ROS topic;
  the walk goes down (or `--direction up`) across repos, crossing client → route handler
  (Django/DRF method dispatch and framework hooks, FastAPI/Flask, Express, Go), publish →
  subscriber callback, base method → implementations (C++, Python, TS/JS, Java) and imports of
  other indexed repos. Nodes carry side effects (HTTP out with its route, DB/ORM, publish,
  file writes, subprocesses, log lines, raised errors), edges their resolution method and
  confidence; repeated subtrees collapse, cycles are marked, `--depth`/`--budget` cuts are
  reported with a `next` hint; `--format text` renders an indented tree.
- **`models [model] [ref]`** and MCP **`reponite_models`**: the data layer. An ER summary
  (tables first, ranked by connectedness), or one model's fields, relations both ways,
  serializers, the routes whose handler touches it, and its writers and readers (ORM call
  sites with the chained operation, related managers, raw SQL naming the table fleet-wide),
  token-budgeted with `truncated`/`next`. Django, DRF, SQLAlchemy/SQLModel, Pydantic,
  dataclasses, TypeORM/MikroORM/sequelize-typescript, Prisma, GORM, SQL DDL across
  migrations, protobuf.
- **`config [key] [ref]`** and MCP **`reponite_config`**: every env var, CLI flag, settings
  key, feature flag and ROS parameter read (with its default) joined to where it is set
  (.env, compose, k8s/Helm, Dockerfile, CI, launch files, settings modules, .properties,
  exports, flags passed in scripts); keys read-never-set and set-never-read flagged.
- `--format text` on both.

#### Changed

- **Index ruleset 6**: `.sql`, `.proto`, `.prisma`, `.env*`, `Dockerfile`, `.properties` and
  CI workflow YAML (`.github/workflows`, `.circleci`, `.gitlab-ci.yml`; ≤ 256 KB) are stored.

#### Added

- **`topics` resolves names read from parameters.** A publisher/subscriber/service
  whose name is a variable, a config-struct member or an inline param read is traced
  to the parameter (`get_ros_param`, `param()`, `getParam`, `declare_parameter`,
  `get_parameter`, rospy `get_param`; same file, then same package) and the parameter
  to its value: launch `<param>`/`<rosparam>` for the node's package (with `$(arg)`,
  `$(find pkg)` and `$(eval find(...))` file references followed), any launch file in
  the repo, the call's default, then a package YAML. Labeled
  `param:<name>=<value> (<file:line>)` with `name_confidence` below a literal's;
  disagreeing sources are listed per candidate as `ambiguous`; unresolvable ones are
  reported under `unresolved_endpoints` instead of disappearing. Test launch files
  never supply a production value.
- **`--no-tests` / `REPONITE_NO_TESTS=1`** on usages, context, impls, topics, routes,
  compat, rootcause, grep, search and semsearch: a store view that hides test symbols,
  test files and test callers by the indexer's own classification.
- **C++ symbol spellings** (`Class::method`, `ns::Class::method`) in context, brief,
  compat, rootcause and blast-radius; `--repo` to pick among fleet candidates.
- **semsearch term-index cache** next to each repo's index, keyed by commit, manifest,
  ruleset and tokenizer version: a warm fleet query drops from ~40 s to well under a
  second (cold ~3x faster: one line split per file, an allocation-free tokenizer).

#### Changed

- **Index ruleset 5**: YAML parameter files (≤ 256 KB) are stored, so existing refs
  re-index on the next `index --git` of the same commit.
- `compat` and `rootcause` answer for the fleet repo that defines the symbol, like
  `context`, instead of only the working directory.
- A symbol miss now says which miss it is — no ref matched (with each repo's indexed
  refs), or not defined in any searched repo — with near matches and their repos;
  several defining repos print the candidates instead of failing.
- semsearch says plainly that the default ranking is keyword (term-idf), not semantic,
  and why no neural ranker ran.

#### Fixed

- Under `REPONITE_REFS`, answers reported `ref: "HEAD"`; they now report the pinned
  label actually read (`_meta.ref`, and `_meta.refs` per repo fleet-wide), and hits
  carry their repo's ref.
- Fleet coverage notes ignored pins ("nothing was searched" over a result with
  matches): the fleet store did not expose the pinning view to coverage checks.
- Pins no longer override an explicitly requested ref, so brief/blast-radius/compat
  compare against a repo's other refs instead of the pin against itself.

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

- **Cross-repo virtual dispatch in `context`**: a call through a base class
  also lists each override in another repo (`xrepo-override`, 0.6), and an
  override lists the callers in other repos that reach it through one of its
  bases (`xrepo-base-call`, with `via` naming the base method). Each repo is
  indexed alone, so these hops — a platform calling a plugin interface whose
  implementation another repo loads at runtime — were invisible from both
  sides. Mocks are excluded unless `--tests`.
- **`routes --gateway <caddy.json>`** follows each request through a reverse
  proxy: a mount (`/svc*` → `localhost:8003`) is bound to the indexed repo
  whose name carries the mount's words (or `--upstream host:port=repo`), a
  client is matched by the mount its path, the upstream it dials, or its
  client object goes through, and only against the routes of the repo behind
  it (`via_gateway` in the output). Hosted upstreams bind no repo; on a tie
  without a gateway hop, a client's own service wins. Minified bundles are
  skipped as clients.

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
