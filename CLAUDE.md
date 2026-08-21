# CLAUDE.md — working on reponite

reponite is ref-aware code intelligence (what/why: `README.md`, `docs/architecture.md`).
This file orients an agent building in the repo.

## Prerequisites
- Go 1.22+ and a C toolchain (gcc/clang) — the tree-sitter adapter uses CGO.

## Build & test
The correctness-critical core is **pure Go, standard-library only**, and always builds/tests with no external deps:

    go build ./...
    go test ./...

External adapters live behind **build tags** and are fetched on demand:

| tag         | what it adds                        |
|-------------|-------------------------------------|
| `sqlite`    | SQLite store (`modernc.org/sqlite`) |
| `treesitter`| tree-sitter parser (CGO) + go-git ref indexing + go/types precise edges (pulls a current `golang.org/x/tools`; build with a recent Go) |
| `mcp`       | MCP server (`mark3labs/mcp-go`)     |
| `neural`    | neural semantic ranker (stdlib-only `net/http` client for an OpenAI-compatible embeddings endpoint; tagged because a network path stays out of the pure core — its tests ride the `core` CI job) |

Build the full CLI (all adapters):

    make cli          # -> bin/reponite

Per-adapter checks (mirror CI):

    make sqlite | make treesitter | make mcp | make e2e

## Layout
- `internal/content`   — `canon()`, the three hashes, manifests (pure)
- `internal/processing`— behavior-hash pass, language-agnostic extractor, indexer (pure); tree-sitter parser (`treesitter`)
- `internal/query`     — `Store` interface, Oracle/diff/rootcause/grep, coordinators (pure)
- `internal/fleet`     — the persistent cross-run repo registry serve/mcp mount by default (pure)
- `internal/roslaunch` — ROS launch XML → node/remap table; resolves the RUNTIME topic name (pure, stdlib encoding/xml, no build tag)
- `internal/scip`      — SCIP index reader (stdlib protobuf subset) → symbol monikers for cross-repo edges (pure)
- `internal/storage`   — in-memory `Store` (pure); `storage/sqlite` (`sqlite`)
- `internal/interfaces`— JSON output; MCP server (`mcp`)
- `cmd/reponite`       — CLI; index-backed commands + `mcp`/`watch` under build tags

## CI
`.github/workflows/go.yml` runs 5 jobs: `core` (pure — also builds/tests the stdlib-only `neural` adapter), `sqlite`, `treesitter`, `mcp`, `e2e`.
`release.yml` builds binaries on `v*` tags. Keep all jobs green.

⚠ **Run the full matrix, not just `go test ./...`.** A file's build tag must cover every
helper it uses: a `//go:build sqlite` file calling a `sqlite && treesitter` helper compiles
under `make cli` and breaks `make sqlite`. Two real breaks were caught exactly this way.

## Invariants (do not break)
1. `norm_ver` is folded into every hash; versions never silently collide.
2. `canon()` is conservative — when unsure, KEEP the difference; never merge distinct code.
3. Storage dedups on `symbol_hash`; only the Oracle consults `behavior_hash`.
4. A ref is real only when its manifest is written **last** (crash-safety).
5. Every edge carries `resolution_method` + `confidence`; `behavior_conf = min` over the subgraph; never overclaim.
6. Correctness-critical logic stays pure/stdlib behind interfaces; external deps live in thin build-tagged adapters (ADR-018).
7. A cross-repo identity is `(module ROOT, name)` or a SCIP moniker — never an exact
   import-path match. An import path is the module path PLUS a package path, so exact
   equality silently demotes every multi-package repo to the name-based tier
   (`query.ModuleMatches`).
8. Per-language facts are captured at INDEX time from the path/AST, never inferred from a
   symbol name at query time. `is_test` was a Go name heuristic for months, so `brief`
   silently reported "0 covering tests" for 8 of 10 languages while advertising the
   section (`processing.IsTestPath` → `SymbolRecord.IsTest`).
9. Schema changes go in `migrate()`, never the base schema — including indexes over
   migrated columns. `CREATE TABLE IF NOT EXISTS` is a no-op on an existing table, so a
   base-schema index over a new column breaks `Open` for every already-indexed repo.

## Adding a language
Add a `LangRules` entry in `internal/processing/lang.go`, bind its tree-sitter grammar in `parser.go` (`grammarForExt`), add the grammar to the `go get` lines in the `Makefile`/CI, and add a per-language parse test. The `Extract` engine is language-agnostic; `IndexDir`/`IndexGitRef` dispatch by `RulesForExt`. Bound today: Go, Python, JavaScript, TypeScript, Java, C, C++, Rust, **Shell**. Extension-less files fall back to their shebang (`readSource`/`RulesForShebang`) — a CLI entry point like `installer/rdt` carries no extension and is usually the most valuable file in a tree; only files with NO extension are peeked, so a known-unsupported extension (`.yaml`, `.tf`) still costs nothing. Shell has no signature (no declared parameter list), so its edits read as `behavior_changed`, never `shape_changed`; its callee is the `command_name` node, NEVER a bare `word`, because a command's arguments are `word` nodes too and matching those makes the last argument look like the callee. Four `LangRules` knobs cover the awkward grammars: `DeclNameIn` (the name is nested in a declarator — C/C++ `function_declarator`; the name is the last `DeclNameTypes` leaf BEFORE the parameter list, so `ns::T::m` yields `m` and a parameter/trailing-return type is never mistaken for it), `DeclNameTypes` (name node types valid inside that declarator — C++ in-class methods are `field_identifier`, destructors/operators their own nodes — kept separate from `NameTypes` so member calls don't mis-resolve), `TypeDeclNeedsBody`+`TypeDeclBody` (C/C++ `struct/class/enum` specifiers are only *definitions* with a body child; bare references like `struct Foo x;` and forward declarations produce no symbol/span), and `ScopeDecl` (a block that qualifies nested methods by its own name without being a symbol — a Rust `impl T { ... }`). When a declarator yields no name the callable is anonymous and dropped — a name is never invented from a parameter or body identifier. For cross-repo `ximpact`, add the language's import syntax to `imports.go` and its module manifest to `module.go`. ROS launch/rostest XML (`.launch`/`.test`) is routed to `launch.go` → `internal/roslaunch`: stored for search with one span per `<node>`, and re-parsed at query time by `topics` to resolve remapping (zero new storage, the §8D.5 model). A remap target that keeps an unexpanded substitution is REPORTED, never applied — grouping endpoints under the literal `$(arg scan_topic)` is worse than not remapping. Non-tree-sitter interface formats (e.g. ROS `.msg`/`.srv`/`.action`) are parsed by a pure text extractor (`ros.go`) that `IndexDir`/`IndexGitRef` route to before the tree-sitter path — their signature is the field contract, so the Oracle flags a changed contract as `shape_changed`.

## Cross-repo tiers (§8B)
Three labeled tiers, highest first: `scip-resolved`@0.95 (SCIP moniker — globally unique,
no name guessing) → `import-resolved`@0.75 (import binding resolves to the target's module
root) → `unresolved-external`@0.6 (bare name, the honest fallback). Every caller also
carries `expected_signature` (`current`/`stale`/unknown) comparing the contract captured
when it was indexed against the target's current signature. All three tiers need caller and
target in ONE store: the CLI opens the fleet registry's repos as a MultiStore, and index
time gets the same view via `IndexOptions.Peers`.

## More
`docs/architecture.md`, `docs/agent-features.md`, `docs/adr/`, `CHANGELOG.md`, and
`PROGRESS.md` (running build log).
