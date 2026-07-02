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
