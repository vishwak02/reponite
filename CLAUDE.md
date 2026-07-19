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

Build the full CLI (all adapters):

    make cli          # -> bin/reponite

Per-adapter checks (mirror CI):

    make sqlite | make treesitter | make mcp | make e2e

## Layout
- `internal/content`   — `canon()`, the three hashes, manifests (pure)
- `internal/processing`— behavior-hash pass, language-agnostic extractor, indexer (pure); tree-sitter parser (`treesitter`)
- `internal/query`     — `Store` interface, Oracle/diff/rootcause/grep, coordinators (pure)
- `internal/storage`   — in-memory `Store` (pure); `storage/sqlite` (`sqlite`)
- `internal/interfaces`— JSON output; MCP server (`mcp`)
- `cmd/reponite`       — CLI; index-backed commands + `mcp`/`watch` under build tags

## CI
`.github/workflows/go.yml` runs 5 jobs: `core` (pure), `sqlite`, `treesitter`, `mcp`, `e2e`.
`release.yml` builds binaries on `v*` tags. Keep all jobs green.

## Invariants (do not break)
1. `norm_ver` is folded into every hash; versions never silently collide.
2. `canon()` is conservative — when unsure, KEEP the difference; never merge distinct code.
3. Storage dedups on `symbol_hash`; only the Oracle consults `behavior_hash`.
4. A ref is real only when its manifest is written **last** (crash-safety).
5. Every edge carries `resolution_method` + `confidence`; `behavior_conf = min` over the subgraph; never overclaim.
6. Correctness-critical logic stays pure/stdlib behind interfaces; external deps live in thin build-tagged adapters (ADR-018).

## Adding a language
Add a `LangRules` entry in `internal/processing/lang.go`, bind its tree-sitter grammar in `parser.go` (`grammarForExt`), add the grammar to the `go get` lines in the `Makefile`/CI, and add a per-language parse test. The `Extract` engine is language-agnostic; `IndexDir`/`IndexGitRef` dispatch by `RulesForExt`. Bound today: Go, Python, JavaScript, TypeScript, Java, **C, C++, Rust**. Four `LangRules` knobs cover the awkward grammars: `DeclNameIn` (the name is nested in a declarator — C/C++ `function_declarator`; the name is the last `DeclNameTypes` leaf BEFORE the parameter list, so `ns::T::m` yields `m` and a parameter/trailing-return type is never mistaken for it), `DeclNameTypes` (name node types valid inside that declarator — C++ in-class methods are `field_identifier`, destructors/operators their own nodes — kept separate from `NameTypes` so member calls don't mis-resolve), `TypeDeclNeedsBody`+`TypeDeclBody` (C/C++ `struct/class/enum` specifiers are only *definitions* with a body child; bare references like `struct Foo x;` and forward declarations produce no symbol/span), and `ScopeDecl` (a block that qualifies nested methods by its own name without being a symbol — a Rust `impl T { ... }`). When a declarator yields no name the callable is anonymous and dropped — a name is never invented from a parameter or body identifier. For cross-repo `ximpact`, add the language's import syntax to `imports.go` and its module manifest to `module.go`. Non-tree-sitter interface formats (e.g. ROS `.msg`/`.srv`/`.action`) are parsed by a pure text extractor (`ros.go`) that `IndexDir`/`IndexGitRef` route to before the tree-sitter path — their signature is the field contract, so the Oracle flags a changed contract as `shape_changed`.

## More
`docs/architecture.md`, `docs/agent-features.md`, `docs/adr/`, and `PROGRESS.md` (running build log).
