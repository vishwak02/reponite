# ADR-000 — Interface index (living document)

Recall surface for every build session: the public seams of each package, so a
session can honor a contract without opening the implementing files. Updated at
the end of any session that adds/changes a public signature. (⟳ planned / ✓ implemented)

## Cross-cutting seams
- ✓ `content.AST` — language-agnostic AST node consumed by `canon()`; interface defined (S0.3). Tree-sitter adapter implements it on-machine (ADR-018).
- ⟳ `storage.Store` — persistence behind an interface (nodes/edges/manifests/registry). SQLite adapter only impl at launch.
- ⟳ `storage.GraphStore` — bounded transitive-closure traversal (ADR-006); recursive-CTE impl, Cozo escape hatch later.
- ⟳ `processing.Embedder` — bundled | ollama | remote (ADR-007). Stubbed until M6.
- ⟳ `interfaces.Authenticator` — token/mTLS now, OIDC later (ADR-012).
- ⟳ `storage.WorkQueue` — in-memory channel now, Redis/NATS later (ADR-013).

## content  (✓ S0.2 hashes, ✓ S0.3 canon)
- ✓ `content.Hash` — "sha256:<hex>"
- ✓ `content.SymbolIdentity{Repo, Lang, Kind, Signature, CanonBody []byte}`
- ✓ `content.SymbolHash(normVer int, id SymbolIdentity) Hash` — textual identity / dedup key
- ✓ `content.SignatureHash(normVer int, id SymbolIdentity) Hash` — API shape (body-independent)
- ✓ `content.BehaviorHash(symbolHash Hash, normVer int, callees []Hash) Hash` — Merkle over call graph
- ✓ `content.FileHash / EmbedHash / EdgeHash / ManifestHash`
- ✓ `content.AST interface { Type() string; Text() string; Children() []AST; IsNamed() bool }`
- ✓ `content.Canon(node AST, normVer int) []byte` — identity bytes: drops comments, sorts imports, structure-preserving
- ✓ `content.DocText(node AST) []byte` — comment/doc text for doc_hash (§5.5)
- ⟳ `content.BlobStore` (M1 S1.2)

## storage / processing / query / interfaces
- ⟳ populated as sessions M1→M5 land.

## processing  (✓ S1.5-pure)
- ✓ `processing.Node{ID string; SymbolHash content.Hash}`
- ✓ `processing.Edge{From, To string; Confidence float64}`
- ✓ `processing.Result{BehaviorHash map[string]content.Hash; BehaviorConf map[string]float64}`
- ✓ `processing.ComputeBehavior(nodes []Node, edges []Edge, normVer int) Result` — Tarjan SCC + reverse-topo Merkle + behavior_conf=min
- (content) ✓ `content.GroupHash(symbolHashes []Hash) Hash` — SCC unit identity

## query  (✓ compat + diff, pure)
- ✓ `query.Verdict` = absent | shape_changed | behavior_changed | compatible
- ✓ `query.SymbolRef{Present bool; SignatureHash, BehaviorHash content.Hash; BehaviorConf float64}`
- ✓ `query.Compat(origin, target SymbolRef) CompatResult` — tiered verdict, confidence=min of evidence
- ✓ `query.CompatAcross(origin SymbolRef, targets []Target) []CompatVerdict` — fleet-wide (§8.3)
- ✓ `query.DiffRefs(a, b map[string]SymbolRef) []SymbolChange` — added/removed/shape/behavior/unchanged, sorted
- ✓ `query.RootCause(target string, from, to RefSnapshot) RootCauseResult` — drill-down to the mutation-site frontier (ext §8A); types SymbolFacts/Callee/RefSnapshot/Origin/OriginKind
- ✓ `query.BuildTrigramIndex(files []File) *TrigramIndex` + `(*TrigramIndex).Grep(pattern string, opt GrepOptions) (GrepResult, error)` — trigram-prefiltered literal/regex search with enclosing-symbol fusion (ext §10A); types File/SymbolSpan/Match/GrepResult/GrepOptions

## content — content-addressing set logic  (✓ M2-pure)
- ✓ `content.Manifest{Ref, Commit string; Blobs []Hash}` + `.Hash()`
- ✓ `content.DiffManifests(a, b Manifest) ManifestDiff` — diff as a set operation (§4.1)
- ✓ `content.UniqueBlobs([]Manifest) []Hash` / `content.Dedup([]Manifest) DedupStats` — storage ∝ unique content
- ✓ `content.UnreferencedBlobs(live []Manifest, stored []Hash) []Hash` — GC mark phase (§11.4)

## query — Store seam (✓)
- ✓ `query.Store` interface { Repos; Refs(repo); SymbolAt(repo,symbol,ref); SymbolsAt(repo,ref); Snapshot(repo,ref); Files(repo,ref); Manifest(repo,ref) } — read surface for the query layer

## storage  (✓ in-memory; ⟳ sqlite adapter on-machine)
- ✓ `storage.Mem` implements `query.Store`; `storage.SymbolRecord`; Put/PutFile/PutManifest population API
- ⟳ `storage/sqlite` — SQLite adapter implementing `query.Store` (compiled on-machine)

## query — coordinators (✓)
- ✓ `query.CompatSymbol(s Store, origin RepoRef, symbol string, targets []RepoRef) (CompatReport, error)`
- ✓ `query.DiffRefsBy(s Store, repo, from, to string) DiffReport`
- ✓ `query.RootCauseBy(s Store, repo, target, from, to string) RootCauseResult`
- ✓ `query.GrepRepo(s Store, repo, ref, pattern string, opt GrepOptions) (GrepResult, error)`
- ✓ `query.SearchName(s Store, repo, ref, substr string) []SearchHit`
- ✓ types `RepoRef`, `Meta`, `CompatReport`, `DiffReport`, `SearchHit`

## interfaces — output (✓)
- ✓ `interfaces.CompatJSON / DiffJSON / RootCauseJSON / GrepJSON / SearchJSON` — JSON envelope (§10.3) with lowercase keys + `_meta`, decoupled from internal types
- ✓ `cmd/reponite demo` — in-memory end-to-end run of compat/rootcause/grep

## storage/sqlite  (⟳ on-machine, `-tags sqlite`)
- ⟳ `sqlite.Open(path string) (*Store, error)` — implements query.Store over modernc.org/sqlite (WAL); write API Put/PutFile/PutManifest/AddRef; behind `//go:build sqlite`. Default build excludes it (doc.go only). Verify: `make sqlite`.

## processing/parser  (⟳ on-machine, `-tags treesitter`, CGO)
- ⟳ `processing.ParseGo(src []byte) (content.AST, error)` — tree-sitter (smacker/go-tree-sitter) → content.AST adapter; `tsNode` exposes Type/Text/Children(all)/IsNamed per the contract doc. Behind `//go:build treesitter`. Verify in CI (treesitter job) or `make treesitter`.

## Verification (no local Go on the dev machine)
- GitHub Actions `.github/workflows/go.yml` runs 3 jobs on every push: `core` (default, pure packages), `sqlite` (`-tags sqlite`), `treesitter` (`-tags treesitter`, CGO). Adapter correctness is confirmed by CI, not locally.

## processing — extractor (✓ pure)
- ✓ `processing.ExtractGo(root content.AST, normVer int) []Symbol` — top-level symbols (function/method/type) with body-independent Signature (via child filtering), CanonBody, associated Doc, and heuristic name-based Callees (tree-sitter tier). Pure over content.AST; tested in-sandbox + a treesitter-tagged real-tree test.
- ✓ `processing.Symbol{Name, Kind, Signature string; CanonBody, Doc []byte; Callees []string}`

## processing — indexer core (✓ pure)
- ✓ `processing.Indexer` interface { Put(repo,ref,name, storage.SymbolRecord) error; PutFile(repo,ref, query.File) error } — satisfied by storage.Mem and storage/sqlite
- ✓ `processing.IndexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile) error` — hash symbols, resolve callees to tree-sitter-tier edges (0.6), ComputeBehavior over whole-ref graph, write records + files
- ✓ `processing.ParsedFile{Path, Content string; Symbols []Symbol; Spans []query.SymbolSpan}`
- storage.Mem write methods (Put/PutFile/PutManifest) now return error (share the Indexer/SQLite interface)

## finish line — indexer + CLI (⟳ on-machine / CI)
- ⟳ `processing.IndexDir(w Indexer, repo, ref, dir string, normVer int) error` (`-tags treesitter`) — walk *.go → parse → ExtractGo → line spans → IndexFiles.
- ⟳ `cmd/reponite` index/compat/diff/grep/search wired to sqlite.Open + coordinators + JSON (`-tags "sqlite treesitter"`, run_full.go); default build uses run_stub.go → notImplemented.
- ⟳ `internal/e2e` both-tags integration test: index 2 refs of a temp repo, assert Charge behavior_changed. CI job `e2e`.
- CI jobs: core / sqlite / treesitter / e2e. Build the CLI with `make cli`.

## MCP server (dogfooding) — ✓ pure dispatch, ⟳ SDK glue on-machine
- ✓ `query.Context(s Store, repo, ref, symbol string) ContextResult` — direct callers+callees
- ✓ `interfaces.ToolServer{Store, Repo}` + `.Call(tool string, args map[string]string) (string, error)` — pure dispatch to search/grep/compat/context/diff/rootcause/refs → JSON (tested vs Mem)
- ✓ `interfaces.ContextJSON / RefsJSON`
- ⟳ `interfaces.ServeStdio(*ToolServer) error` (`//go:build mcp`, github.com/mark3labs/mcp-go) — stdio MCP glue
- ⟳ `reponite mcp` command (`//go:build sqlite && mcp`): open sqlite store → ToolServer → ServeStdio. cmd sqlite helpers in store_sqlite.go (`//go:build sqlite`). CI job `mcp` (-tags "sqlite mcp").

## processing — multi-language engine (✓ pure)
- ✓ `processing.LangRules` + `processing.RulesForExt(ext) (LangRules, bool)` — per-language node-type rules; registry keyed by file extension
- ✓ rule tables: `GoRules`, `PythonRules`, `JavaScriptRules`, `TypeScriptRules`, `JavaRules`
- ✓ `processing.Extract(root content.AST, r LangRules, normVer int) []Symbol` — generic engine (recurses into classes for nested methods); `ExtractGo` now wraps `Extract(…, GoRules, …)`
- ⟳ grammar binding per language (parser layer, tagged) + IndexDir multi-extension + per-language CI parse tests; C/C++ (declarator names, NameByDesc) and HTML (structural tier) to follow
