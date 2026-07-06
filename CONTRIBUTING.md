# Contributing to reponite

Thanks for your interest in reponite — ref-aware code intelligence. This guide
covers the build, the invariants that keep the tool honest, and how to add a
language or a feature without breaking the core.

## Prerequisites

- **Go 1.22+** and a **C toolchain** (gcc/clang) — the tree-sitter adapter uses CGO.

## Build & test

The correctness-critical core is **pure Go, standard-library only**, and always
builds and tests with no external dependencies:

```sh
go build ./...    # pure core — zero deps, builds anywhere
go test ./...     # unit tests for the pure core
```

External adapters live behind **build tags** and are fetched on demand:

| tag          | what it adds                                                     |
|--------------|------------------------------------------------------------------|
| `sqlite`     | SQLite store (`modernc.org/sqlite`)                              |
| `treesitter` | tree-sitter parser (CGO) + go-git ref indexing + go/types edges  |
| `mcp`        | MCP server (`mark3labs/mcp-go`)                                  |

```sh
make cli          # full binary with every adapter -> bin/reponite

# Per-adapter checks (mirror CI):
make sqlite | make treesitter | make mcp | make e2e
```

CI (`.github/workflows/go.yml`) runs five jobs — `core` (pure), `sqlite`,
`treesitter`, `mcp`, `e2e`. **Keep all five green.**

## The invariants (load-bearing — read before you touch the core)

These live in [CLAUDE.md](CLAUDE.md) and must never be broken:

1. `norm_ver` is folded into every hash; versions never silently collide.
2. `canon()` is conservative — when unsure, KEEP the difference; never merge distinct code.
3. Storage dedups on `symbol_hash`; only the Oracle consults `behavior_hash`.
4. A ref is real only when its manifest is written **last** (crash-safety).
5. Every edge carries `resolution_method` + `confidence`; `behavior_conf = min` over the subgraph; never overclaim.
6. Correctness-critical logic stays pure/stdlib behind interfaces; external deps live in thin build-tagged adapters (ADR-018).

If a change would weaken one of these, it needs an ADR (see below), not just a PR.

## Adding a language

The extractor is language-agnostic; adding a language is mostly a rule table:

1. Add a `LangRules` entry in [`internal/processing/lang.go`](internal/processing/lang.go)
   (node-type names come from the language's tree-sitter grammar).
2. Bind its grammar in [`internal/processing/parser.go`](internal/processing/parser.go)
   (`grammarForExt`) and add the grammar to the `go get` lines in the `Makefile`.
3. If the language has a module manifest, teach
   [`internal/processing/module.go`](internal/processing/module.go) to read its path,
   and add its import syntax to [`internal/processing/imports.go`](internal/processing/imports.go)
   (for cross-repo `ximpact`).
4. Add a real-grammar parse/extract test (CI, `//go:build treesitter`) and a
   fake-AST test (pure, in-sandbox) so both layers stay pinned.

Non-tree-sitter interface formats (e.g. ROS `.msg`/`.srv`/`.action`) are parsed
by a pure text extractor that `IndexDir`/`IndexGitRef` route to first.

## Tests

- **Unit tests are co-located** with the code they test (`foo.go` + `foo_test.go`),
  Go's convention — internal-package tests need access to unexported symbols.
- **Integration / cross-adapter tests** live in [`internal/e2e/`](internal/e2e/)
  behind `//go:build sqlite && treesitter`.
- New pure logic must be unit-tested in-sandbox; adapter code is verified by CI
  (the pure core builds without a module proxy, adapters do not).

## Architecture Decision Records

Design decisions are recorded under [`docs/adr/`](docs/adr/); see
[`docs/adr/README.md`](docs/adr/README.md) for the log and where each ADR lives
(some are standalone files, some are embedded in `docs/agent-features.md`). A
change that alters a public seam or an invariant should add or amend an ADR.

## Pull requests

- Keep the diff focused; separate mechanical refactors from behavior changes.
- All five CI jobs green; `go vet ./...` clean.
- Update `docs/` and `PROGRESS.md` (the running build log) when you change behavior.
- Open an [issue](https://github.com/vishwak02/reponite/issues) first for anything
  large or invariant-adjacent.

By contributing you agree your work is licensed under the project's
[Apache-2.0](LICENSE) license.
