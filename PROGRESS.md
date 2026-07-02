# PROGRESS — reponite build checkpoint

**Read this first, every session.** Project memory: cursor, invariants,
interface index pointer, session log. Resume from here — do not re-read the repo.

## Cursor
- Milestone: **M1 — structural core (in progress)**. Pure moat kernel done.
- Last completed: **CLI + JSON output** — interfaces/output.go envelopes + working `reponite demo` (compat/rootcause/grep end-to-end from the binary). 89 tests; demo smoke-verified.
- **Next: M1 adapters (compiled on-machine).** SQLite `Store` (schema/migrate/nodes/edges/paths) + tree-sitter parser + resolver — written in-sandbox, compiled on a real machine / CI (module proxy blocked here). Then delta wiring (S1.6) and grep (S1.7). The pure moat path is complete and in-sandbox-verified end to end: canon + hashes + behavior pass + compat/diff verdicts (50 tests). Remaining work is the external-dependency adapters that feed these with real data.

## Session protocol (fixed)
1. Read PROGRESS.md + only the files the session touches + only the spec sections it needs (by number).
2. Do the scoped work (≤ ~4 files + tests; leave the tree compiling and tests green).
3. `source /tmp/goenv.sh && go build ./... && go test ./...` → green is the exit condition.
4. `git` commit (separate git dir, see below) tagged with the session ID.
5. Update this file (cursor, interface index in ADR-000, any new invariant/ADR).

## Build environment  (⚠ the sandbox VM is recycled between sessions)
Source files persist in the outputs folder; the Go toolchain and git dir in
`/tmp` do NOT. **Re-stage at the start of every build session:**
```sh
if [ ! -x /tmp/goroot/usr/lib/go-1.18/bin/go ]; then
  cd /tmp && rm -rf goget && mkdir goget && cd goget
  apt-get download golang-1.18-go golang-1.18-src
  mkdir -p /tmp/goroot; for d in *.deb; do dpkg -x "$d" /tmp/goroot; done
  GR=/tmp/goroot/usr/lib/go-1.18
  [ -d "$GR/src/fmt" ] || ln -s /tmp/goroot/usr/share/go-1.18/src "$GR/src"
fi
cat > /tmp/goenv.sh <<'SH'
export GOROOT=/tmp/goroot/usr/lib/go-1.18
export PATH=$GOROOT/bin:$PATH
export GOCACHE=/tmp/gocache GOPATH=/tmp/gopath GOPROXY=off GO111MODULE=on
SH
source /tmp/goenv.sh
```
- Module proxy is **blocked** → external deps can't be fetched here.
- **Compiles in-sandbox:** stdlib-only pure core (canon, hashes, behavior graph, compat/diff/grep logic — ADR-018).
- **Does NOT:** tree-sitter, modernc.org/sqlite, go-git, usearch adapters → written here, compiled on a real machine / CI.

## Git
- Separate git dir on local FS (outputs mount blocks git lock-file cleanup):
  `git --git-dir=/tmp/reponite.git --work-tree=$BASE <cmd>`; `printf '.git/\nbin/\n' > /tmp/reponite.git/info/exclude`.
- `/tmp/reponite.git` is ephemeral — re-init each session; **GitHub is canonical history**.

## Remote
- Push target: **github.com/vishwak02/reponite** (public, created). ⚠ The GitHub connector is READ-ONLY here: both create_repository and push_files return 403 'Resource not accessible by integration'. Likely the GitHub App is scoped to selected repos (reponite not in its allow-list) and/or lacks Contents:write. Unblock by granting the app access to reponite + Contents:write, OR push manually from the local machine.

## Invariants (must never break)
1. `norm_ver` baked into every hash; hashes across versions never silently collide.
2. `canon()` conservative — when in doubt, KEEP the difference. May under-normalize, must NEVER merge distinct code.
3. Storage dedups on `symbol_hash`; only the Oracle consults `behavior_hash`.
4. A ref is real only when its manifest is written **last**; interrupted index → orphan blobs, never a half-valid ref.
5. Every edge carries `resolution_method` + `confidence`; `behavior_conf = min` over the subgraph; the Oracle never claims more certainty than it computed.
6. Correctness-critical logic stays pure/stdlib behind interfaces; external deps live in thin adapters (ADR-018).

## Interface index
See `docs/adr/ADR-000-interface-index.md`. Adapter contract: `docs/adapters/treesitter-ast-contract.md`.

## Session log
| Session | What landed | Build/test |
|---|---|---|
| S0.1 | scaffold: go.mod, cmd/reponite (router + version), internal/* doc pkgs + version_test, Makefile, CI, ADR-000, ADR-018 | green |
| S0.2 | content/hash.go: 7 hashes over length-prefixed, domain-separated encoding; 13 tests incl. moat, field-boundary, edge resolution_method | green |
| S0.3 | content/canon.go: `content.AST` + Go canonicalization (drop comments→doc_hash, sort imports, structure-preserving) + DocText; 10 canon tests | green |
| S0.4 | broadened canon corpus (methods/receivers, generics, struct tags, composite-literal order, slices, keywords, multi-comment invariance, file_hash invariance); tree-sitter adapter contract doc; **M0 closed** → content pkg 32 tests | green |
| S1.5-pure | processing/behavior.go: pure call-graph behavior-hash — Tarjan SCC condensation, reverse-topological Merkle, behavior_conf=min, cross-repo callee as leaf; content.GroupHash; processing 7 tests, content +2 | green |
| S3-pure | query/compat.go + query/diff.go: Oracle verdicts (absent/shape/behavior/compatible, confidence=min) + ref diff (added/removed/shape/behavior/unchanged, sorted); 9 tests | green |
| S3-rootcause | query/rootcause.go: walk behavior-changed target to mutation-site frontier (text/signature/edge-set = origin vs propagation), confidence=min along path, external-callee note; 7 tests | green |
| S1.7-pure | query/grep.go: trigram index + literal/regex search (trigram prefilter, no-atom full-scan fallback), enclosing-symbol fusion, limit truncation; 8 tests | green |
| M2-pure | content/manifest.go: Manifest + DiffManifests (set-op), Dedup/UniqueBlobs (storage ∝ unique content), UnreferencedBlobs (GC mark); 7 tests | green |
| Store seam | query/store.go (Store interface) + storage/mem.go (pure in-memory impl, SymbolRecord, Put*); end-to-end tests feed store -> Compat/Diff/RootCause; 6 tests | green |
| coordinators | query/coordinator.go: CompatSymbol/DiffRefsBy/RootCauseBy/GrepRepo/SearchName over Store + Meta envelope; end-to-end query_test vs storage.Mem; 6 tests | green |
| CLI+output | interfaces/output.go (CompatJSON/DiffJSON/RootCauseJSON/GrepJSON/SearchJSON) + cmd/reponite demo; 3 tests + demo smoke | green |
