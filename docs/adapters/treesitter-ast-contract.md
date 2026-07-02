# Adapter contract: tree-sitter → `content.AST`

The `canon()` logic (internal/content/canon.go) is tested in-sandbox against a
fake AST. In production a thin adapter wraps tree-sitter nodes to satisfy
`content.AST` (ADR-018). This is the contract that adapter MUST meet so the
sandbox-verified canon behavior holds unchanged on real parse trees.

## Interface mapping
| `content.AST` | tree-sitter source |
|---|---|
| `Type() string` | `ts_node_type` — the grammar symbol name for named nodes; the literal token text for anonymous nodes (operators/punctuation). canon relies on this: composites use rule names, operators like `<=` arrive as anonymous nodes whose `Type()` == `"<="`. |
| `Text() string` | for a **leaf** node, the exact source slice `source[StartByte:EndByte]` — verbatim, NO normalization. For composites canon recurses and ignores `Text()`. |
| `Children() []AST` | **all** children in source order via `ts_node_child(i)` (count `ts_node_child_count`), i.e. INCLUDING anonymous tokens — canon keeps operators/keywords, so they must be present. Do NOT use named-only children. |
| `IsNamed() bool` | `ts_node_is_named`. |

## Rules the adapter must respect
- **Whitespace:** tree-sitter emits no whitespace nodes, so canon needs no whitespace handling — this is *why* reformat-invariance holds. Do not synthesize whitespace nodes.
- **Comments:** appear as nodes (tree-sitter "extras"). canon drops them from identity and routes them to `DocText`/doc_hash. Go's comment node type is `comment`; canon's `isComment()` also accepts `line_comment`/`block_comment` for other Tier-1 grammars. When adding a language, confirm its comment node type names are recognized.
- **Imports (Go):** canon sorts the children of `import_spec_list`. This special-case is Go-only today; other languages' import containers (TS import statements, Python `import_from_statement`, …) are currently kept in source order (conservative, safe) until a per-language rule is added (M8).
- **Byte fidelity:** `Text()` must be exact source bytes. The adapter performs ZERO canonicalization — canon owns all normalization.
- **Determinism & grammar pinning:** pin grammar versions. A grammar upgrade that renames/re-shapes node types changes canon output and is therefore a `norm_ver` bump (invariant 1), not a silent change.

## Non-goals
The adapter is a pure structural bridge: no normalization, no filtering beyond faithfully exposing the parse tree. All identity policy lives in canon.go, versioned by `norm_ver`.
