# reponite — Architecture

How reponite is built: what happens when a repository is indexed, how the index is
stored, and how a question is answered. Decisions and their rationale are in
[the ADRs](adr/); the agent-facing feature designs are in
[agent-features.md](agent-features.md). Diagrams are Mermaid and render on GitHub.

## 1. The system at a glance

reponite reads source code once — from git's object store or a working tree — and
keeps what it learned in one SQLite index per repository. Every question is then
answered from those indexes, across any number of repositories and versions,
without checking anything out.

```mermaid
flowchart LR
    subgraph sources["Sources (read-only)"]
        G["git object store<br/>tags · commits · submodule pins"]
        W["working tree"]
        S["SCIP index<br/>(optional)"]
    end

    subgraph index["Indexing — write path"]
        WK["walkers<br/>git tree · directory"]
        CL["classifier<br/>one decision per file"]
        PP["parse pool<br/>tree-sitter on every core"]
        RS["resolver<br/>call edges + confidence"]
        BH["behavior hash<br/>Merkle over the call graph"]
    end

    subgraph store["Storage"]
        DB[("SQLite per repo<br/>.reponite/index.db or<br/>$REPONITE_STORE_DIR/&lt;repo&gt;/")]
        FL["fleet registry<br/>fleet.json"]
        FC["fact cache<br/>.facts/ (derived answers)"]
    end

    subgraph query["Query engine — read path"]
        V["store views<br/>fleet · pins · no-tests"]
        A["analyzers<br/>compat · usages · trace · topics · routes · links · …"]
    end

    subgraph out["Interfaces"]
        CLI["CLI<br/>reponite &lt;command&gt;"]
        MCP["MCP server<br/>32 tools for AI agents"]
        WEB["web dashboard<br/>reponite serve"]
        VSC["VS Code extension<br/>editors/vscode → CLI"]
    end

    G --> WK
    W --> WK
    S --> RS
    WK --> CL --> PP --> RS --> BH --> DB
    CL -->|"content, data, skipped"| DB
    DB --> V
    FL --> V
    V --> A
    A <--> FC
    A --> CLI & MCP & WEB
    CLI --> VSC
```

## 2. Design principles

- **Never lie.** Every call edge carries how it was resolved and a confidence; every
  answer carries a `_meta` block; a verdict inherits the lowest confidence of its
  evidence. A name that cannot be resolved is listed as unresolved, not guessed.
  An empty answer can be checked against what was read (`coverage`).
- **Storage proportional to unique content.** Content-addressing (git's model): ten
  indexed tags cost little more than one.
- **Pure core, thin adapters.** Correctness-critical logic is standard-library Go and
  unit-tested anywhere; SQLite, tree-sitter and the MCP SDK live in build-tagged
  adapters behind interfaces ([ADR-018](adr/ADR-018-pure-core-thin-adapters.md)).
- **Nothing silently dropped.** Every tracked file is parsed, stored, or skipped with
  a recorded reason.
- **Compute once.** Expensive derived results are cached against the exact content
  they were derived from, so a repeated question is answered from disk.

## 3. Indexing — the write path

```mermaid
flowchart TB
    START(["reponite index &lt;dir&gt; [label] --git &lt;rev&gt; [--submodules] [--exclude …]"])
    START --> REUSE{"label already indexed<br/>at this commit, ruleset<br/>and --exclude set?"}
    REUSE -->|yes| SKIP(["skipped (or aliased to the<br/>same commit's other label)"])
    REUSE -->|no| WALK

    WALK["walk the tree<br/>git: commit tree via go-git, nothing checked out<br/>dir: filepath.Walk, tool/cache dirs not entered"]
    WALK --> CLS{"classifyFile<br/>(shared by both walkers)"}

    CLS -->|"ignore rule · tool dir · binary · LFS pointer<br/>lockfile · data table &gt; 32 KB · size limit · minified"| SK["SkippedFile + reason"]
    CLS -->|"data / project / manifest / ROS msg · launch · params<br/>Robot · Vue/Svelte · content (YAML, JSON, Markdown, XML, …)"| CF["ParsedFile with sections as symbols<br/>(keys, headings, elements, columns, targets)"]
    CLS -->|"Kotlin · Swift · C# · Ruby · PHP · Lua · Scala · Dart<br/>Groovy · Perl · R · Elixir · Julia · Zig"| HF["heuristic extractor<br/>definitions by keyword, calls by name("]
    CLS -->|"any other text"| TF["text, structure sniffed from content<br/>JSON · XML · CSV"]
    CLS -->|"Go · Python · JS/TS · Java · C · C++ · Rust · shell"| JOB["parse job"]

    JOB --> POOL["parse pool — one worker per core, walk order kept<br/>tree-sitter parse → extract symbols, calls, imports, C++ types<br/>→ symbol_hash computed, canonical body dropped<br/>→ tree freed (C memory) → content re-read only when stored"]

    POOL --> IDX
    CF --> IDX
    HF --> IDX
    TF --> IDX
    SK --> REC["ref_skipped"]

    subgraph IDX["indexFiles — one whole-ref pass"]
        direction TB
        R1["link Robot keywords with embedded arguments"]
        R2["resolve every call to a definition<br/>language family only (C↔C++, JS↔TS, JVM, Robot→Python)<br/>call shape: plain · self · member · scoped · typed<br/>imports · receiver types · virtual overrides · SCIP · go/types"]
        R3["behavior_hash: reverse-topological pass over<br/>strongly-connected components (Tarjan)"]
        R4["write symbols, edges, files, spans,<br/>external refs, SCIP monikers"]
        R1 --> R2 --> R3 --> R4
    end

    IDX --> META["ref row: commit, manifest, ruleset (IndexVer),<br/>--exclude filter, module identity"]
    META --> SUB{"--submodules?"}
    SUB -->|yes| NEST["each submodule at its pinned commit,<br/>and theirs, up to 5 levels deep<br/>labeled &lt;top&gt;@&lt;label&gt;"]
```

**Resolution confidence.** Each call edge records its method; the method decides the
confidence and is part of the edge's identity:

| Method | Confidence | Basis |
|---|:---:|---|
| `go-types` | 1.0 | proven by the Go type checker |
| `scip-resolved` | 0.95 | a SCIP moniker two indexers agree on |
| `name-resolved` | 0.9 | exactly one definition in scope |
| `receiver-typed` | 0.85 | a C++ member call whose receiver's declared type is known |
| `import-resolved` | 0.75 | the caller's import binding resolves to the target's module root |
| member call | 0.7 | `obj.f()` with the receiver's type unknown |
| `virtual-override`, `unresolved-external` | 0.6 | a base-class call reaching overrides; a name defined outside the repo |
| `ambiguous` | 0.5 | several definitions, no way to choose |

**Scale.** Parse trees live in C memory the Go collector cannot see, so each is freed
the moment its file is extracted; symbol hashes are computed at parse time and the
canonical body dropped; source text is re-read when it is stored instead of being held
for the whole ref; collection runs at 1.5× the live heap during indexing. A full Linux
kernel tree (67,000 indexed files) indexes in about three minutes at about 5 GB peak.

## 4. The three-hash identity model

Three different "is this the same?" questions, three hashes over a canonical form
(`canon()`: formatting and comments removed; identifiers, literals, operators and
structure kept — when unsure, keep the difference):

```mermaid
flowchart LR
    SRC["function source"] --> CANON["canon()<br/>versioned by NormVer"]
    SRC --> SIG["signature"]
    CANON --> SH["symbol_hash<br/>same text?"]
    SIG --> GH["signature_hash<br/>same API shape?"]
    SH --> BHASH["behavior_hash<br/>same behavior?"]
    CALLEES["callees' behavior_hash<br/>(sorted)"] --> BHASH
    SH -.->|"storage dedup key"| DB[("syms")]
    BHASH -.->|"a change deep in a callee<br/>changes every caller's hash"| COMPAT["compat verdict"]
    GH -.-> COMPAT
```

`compat` compares the three hashes of one symbol at two refs:
`compatible` (same shape, same behavior), `behavior_changed` (its body or something it
calls changed), `shape_changed` (its signature changed), `absent` / `added`.
`rootcause` then walks the call graph to the mutation frontier — the symbols whose own
text changed — versus those merely carried along by a callee.

## 5. Storage

One SQLite database per repository, holding every indexed ref of it. A ref owns no
content: it points at deduplicated rows. Symbols, edge sets, span sets and file blobs
(zlib-compressed) are stored once and shared by every ref that contains them.

```mermaid
erDiagram
    refs_v2 ||--o{ ref_syms : "has"
    refs_v2 ||--o{ ref_files_v2 : "has"
    refs_v2 ||--o{ ref_ext_refs : "has"
    refs_v2 ||--o{ ref_monikers : "has"
    refs_v2 ||--o{ ref_skipped : "records"
    refs_v2 ||--o{ ref_manifest : "lists"
    ref_syms }o--|| syms : "symbol"
    ref_syms }o--|| names : "qualified name"
    syms }o--|| edge_sets : "calls"
    edge_sets ||--o{ edges : "contains"
    edges }o--|| names : "callee"
    ref_files_v2 }o--|| blobs : "content"
    ref_files_v2 }o--|| span_sets : "symbol spans"
    span_sets ||--o{ spans : "contains"
    ref_ext_refs }o--|| ext_refs : "cross-repo reference"

    refs_v2 {
        int id PK
        text repo
        text ref
        text commit_hash
        text manifest_hash
        int index_ver
    }
    syms {
        int id PK
        blob h UK
        text symbol_hash
        text signature_hash
        text behavior_hash
        real behavior_conf
        real direct_conf
        text lang
        int is_test
        int edge_set
    }
    edges {
        int set_id
        int callee_id
        text method
        real confidence
    }
    blobs {
        int id PK
        text hash UK
        blob z
    }
    ext_refs {
        int id PK
        int from_id
        text target_module
        text target_name
        text method
        real confidence
        text target_signature_hash
    }
    ref_skipped {
        int ref_id
        int path_id
        text reason
    }
```

`store_meta` holds the on-disk format version and, per ref, the `--exclude` filter it
was indexed with; `repo_modules` the repository's module identity (go.mod module,
package.json name, …) used to resolve cross-repo imports. Each ref is stamped with the
ruleset it was indexed under (`IndexVer`, currently 12); answers that compare refs
indexed under different rulesets say so.

The **fleet registry** (`fleet.json`) lists every indexed repository and where its
index lives; commands and the MCP server mount the whole fleet by default.

## 6. Answering a question — the read path

```mermaid
flowchart TB
    Q(["a command, an MCP tool call or a dashboard request"])
    Q --> VIEW

    subgraph VIEW["store view — composed per request"]
        direction TB
        P["Pinned<br/>REPONITE_REFS repo=ref,… : each repo read at its own version"]
        N["NoTests<br/>--no-tests : test files and symbols hidden"]
        M["MultiStore<br/>the fleet as one store"]
        SQ[("SQLite stores<br/>+ decoded-files cache per process, 512 MB bound<br/>+ path-filtered reads")]
        P --> N --> M --> SQ
    end

    VIEW --> AN

    subgraph AN["analyzers (internal/query, pure)"]
        direction LR
        A1["orientation<br/>map · explain · guide · coverage"]
        A2["search<br/>grep · search · semsearch · investigate"]
        A3["call graph<br/>usages · context · trace · impls · brief · logsite"]
        A4["across processes and files<br/>topics · routes · ui · links · errors · config · models"]
        A5["change<br/>compat · diff · rootcause · blast-radius · ximpact · verify-edit · ci-check"]
        A6["history and tests<br/>why · tests"]
    end

    AN <-->|"derived results keyed by the exact content in scope"| FC[("fact cache<br/>comm graph · route map · fleet map<br/>link graph · test-word index · term index")]
    AN --> R["result + _meta (refs read, confidence, warnings)<br/>JSON, or compact text"]
```

**Caching.** A fleet-wide result (the ROS communication graph, the HTTP route map, the
fleet map, the reference graph, each repo's test-word index) is a pure function of the
indexed content, so it is cached on disk under a key made of every in-scope repo's
ref, commit, manifest hash and ruleset, the call's arguments, the binary's identity and
the test view. A re-index, a different pin or a rebuilt binary is a different key —
nothing stale is served. Repeated fleet `map` on 40 repositories: 32 s the first time,
0.1 s after.

## 7. Crossing boundaries

The call graph stops at a process, a file format or a repository. These analyzers
recover the edges it cannot see:

```mermaid
flowchart LR
    subgraph repoA["repo A"]
        UI["UI component<br/>onClick → handler"]
        CLIENT["HTTP client call"]
        PUB["ROS publisher<br/>advertise(name)"]
        CFG["YAML / launch / plan"]
    end
    subgraph repoB["repo B"]
        ROUTE["server route → handler"]
        SUB["ROS subscriber"]
        IMPL["implementation / plugin"]
        API["exported symbol"]
    end

    UI -->|"ui · trace"| CLIENT
    CLIENT -->|"routes: path matching, through gateway mounts"| ROUTE
    PUB -->|"topics: runtime name"| SUB
    CFG -->|"links: path · id · template · values · names a symbol"| IMPL
    repoA -->|"ximpact: import → module root, SCIP monikers"| API
    API -.->|"impls: subclasses and PLUGINLIB plugins"| IMPL
```

| Edge | How it is resolved |
|---|---|
| UI → API → server | `ui` finds pages, component trees and handlers; `routes` matches each client URL to a route by path segments, through reverse-proxy gateway mounts (Caddy JSON); `trace` chains them with the call graph and side effects |
| ROS publisher → subscriber | `topics` pairs endpoints on the **runtime** name: launch-file remaps, names read from parameters (launch `<param>`/`<rosparam>`, YAML), names a library takes from its caller (arguments and `NodeHandle` namespaces followed back through call sites), rviz displays and tools, launch-declared bindings |
| repo → repo | `ximpact` / `blast-radius`: tier 0 SCIP moniker (0.95), tier 1 import resolved to a module root (0.75), tier 2 bare name (0.6); each caller records the signature it expected |
| file → file, config → code | `links`: strings resolving to tracked files (relative, root, ROS package, Ansible role, unique suffix or name), ids defined in one structured file and used in another, template includes, Helm `.Values` keys, protobuf rpcs to implementations, OpenAPI paths to routes, configuration values naming a symbol |
| base → implementation | `impls`: class hierarchies and `PLUGINLIB_EXPORT_CLASS` across the fleet |
| error text → producer → handler | `errors`: raise/throw/error values/HTTP responses matched by message template or fragment, catalogues defined as data (JSON, YAML, gettext) |

## 8. Code layout

```mermaid
flowchart BT
    content["internal/content<br/>canon() · hashes · manifests"]
    version["internal/version<br/>NormVer · IndexVer"]
    roslaunch["internal/roslaunch<br/>launch XML · remaps · params"]
    scip["internal/scip<br/>SCIP reader"]
    query["internal/query<br/>Store interface · analyzers · fact cache"]
    storage["internal/storage<br/>Mem · MultiStore · Pinned · NoTests"]
    sqlite["internal/storage/sqlite<br/>SQLite adapter"]
    processing["internal/processing<br/>walkers · classifier · parsers · resolver · behavior hash"]
    semantic["internal/semantic<br/>embeddings client"]
    fleet["internal/fleet<br/>repo registry"]
    interfaces["internal/interfaces<br/>JSON/text output · MCP · web dashboard"]
    cmd["cmd/reponite<br/>CLI · caches · fleet commands"]

    query --> content
    query --> roslaunch
    query --> version
    storage --> query
    sqlite --> storage
    processing --> query
    processing --> content
    processing --> scip
    interfaces --> query
    cmd --> interfaces
    cmd --> processing
    cmd --> sqlite
    cmd --> fleet
    cmd --> semantic
```

| Package | What it holds | Build tag |
|---|---|---|
| `internal/content` | `canon()`, the three hashes, manifests and dedup | — |
| `internal/processing` | classifier, content sections, heuristic extractor, Robot, ROS files, resolver, behavior hash, module detection | — |
| `internal/processing` (`*_ts.go`, `index_git.go`) | tree-sitter parsing, `IndexDir`, `IndexGitRef`, parse pool | `treesitter` |
| `internal/query` | the `Store` interface and every analyzer, fact cache, coverage, links | — |
| `internal/roslaunch` | launch-file parsing, remap and parameter resolution | — |
| `internal/storage` | in-memory store and the fleet/pin/no-test views | — |
| `internal/storage/sqlite` | the SQLite store, decoded-files cache, skips and filters | `sqlite` |
| `internal/scip` | SCIP index reader (a protobuf subset) | — |
| `internal/fleet` | the persistent repo registry | — |
| `internal/semantic` | optional embeddings endpoint for semantic search | `neural` |
| `internal/interfaces` | JSON and compact-text renderings, MCP tools, the web server and its embedded app (`webui/dist`, built from `web/`) | `mcp` for the server |
| `cmd/reponite` | the CLI | `sqlite && treesitter` for index-backed commands |

## 9. Build tiers and verification

| Job | Build | Verifies |
|-----|-------|----------|
| `core` | default | the pure packages, and the `neural` adapter (standard library only) |
| `sqlite` | `-tags sqlite` | the SQLite store, its migrations and caches |
| `treesitter` | `-tags treesitter` | parsing, extraction, the classifier, both walkers |
| `mcp` | `-tags "sqlite mcp"` | the MCP server builds against the SDK |
| `e2e` | `-tags "sqlite treesitter"` | index real repositories across refs and assert answers |

`tools/battery.py` runs every command over a directory of real repositories and
grades each call (crash, error, slow, ok); two runs can be compared.

## 10. Known limits

Each is stated in the output rather than hidden:

- Everything is static. A name built at runtime from a value no file holds (a server
  response, an environment variable set elsewhere) cannot be followed; it is listed as
  unresolved.
- Heuristic languages find definitions by keyword and calls written as `name(`; a call
  without parentheses or a definition in an unusual form is missed.
- `<include>` directives in ROS launch files are not expanded; a remap whose target
  keeps an unexpanded substitution is reported, not applied.
- Cross-repo **behavior** propagation is out of scope: impact answers who calls, not
  whether it still behaves the same over there.
- SCIP's relationship graph (implementations, overrides) is not consumed; `impls`
  reads class hierarchies from source.
- Indexing memory grows with the number of symbols: about 5 GB at the peak for a full
  Linux kernel tree.
