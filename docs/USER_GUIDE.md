# reponite user guide

This guide covers everything reponite does and how to use it. To install it, see
[INSTALL.md](INSTALL.md).

## Contents

- [Concepts](#concepts)
- [Indexing](#indexing)
- [Asking questions](#asking-questions)
- [Data models and configuration](#data-models-and-configuration)
- [Working with many repos](#working-with-many-repos)
- [AI agents (MCP)](#ai-agents-mcp)
- [Web dashboard](#web-dashboard)
- [Configuration](#configuration)
- [Command reference](#command-reference)
- [Languages and file types](#languages-and-file-types)
- [How it works](#how-it-works)
- [FAQ](#faq)

---

## Concepts

A **ref** is one point in a repository's history — a branch, a tag, a commit, or the working
tree (`HEAD`). reponite indexes as many refs of as many repositories as you like, and every
question can be asked at any indexed ref.

Everything in reponite is built on **three hashes**, computed for every symbol at every
indexed ref. Understanding these three lines is understanding the whole tool.

| Hash | The question it answers | How it's built |
|---|---|---|
| `symbol_hash` | **Did the code text change?** | Hash of the normalized source of just this symbol |
| `signature_hash` | **Did the API shape change?** | Hash of the signature only — body excluded |
| `behavior_hash` | **Did behavior change?** | Merkle hash over the resolved call graph: `H(symbol_hash + all callee behavior_hashes)` |

That third one is the interesting one. Because a symbol's `behavior_hash` folds in the
hashes of everything it calls, **a change anywhere below propagates upward automatically**.

```
  validateCard   ← you edited this. symbol_hash changes.
        ▲
     Charge      ← untouched, but its behavior_hash changes, because a callee's did.
        ▲
   CheckoutFlow  ← also untouched, also behavior_changed. And so on, transitively.
```

Two consequences fall straight out:

**1. You can detect silent regressions.** Same signature, different behavior — the change
that passes code review because the diff looks local.

**2. Root-cause becomes cheap.** Given a behavior change, ask of each symbol:

- Did its **own** `symbol_hash` change? → it's a **mutation site**. The origin.
- Only its `behavior_hash` changed? → it was **carried along**. Keep walking down.

No re-analysis, no heuristics — just comparing hashes you already stored.

### The four verdicts

```sh
reponite compat <symbol>
```

| Verdict | Meaning | What to do |
|---|---|---|
| `compatible` | Same shape *and* same behavior | Safe to ship |
| `shape_changed` | The signature changed | **API break** — callers won't compile |
| `behavior_changed` | Same signature, different call graph underneath | **Silent regression risk** — review carefully |
| `absent` | The symbol doesn't exist at that ref | New, removed, or renamed |

### Storage stays small

Content is addressed by hash, so **identical code across refs is stored once**. Indexing
20 tags of a repo costs roughly what one tag costs, plus whatever genuinely differs.

### Confidence and provenance

Every answer carries how it was derived and how certain it is. Each call-graph edge is scored
by how it was resolved: `1.0` type-checker proven · `0.95` SCIP symbol-resolved across repos ·
`0.9` uniquely name-resolved · `0.75` import-resolved across repos · `0.6` external or opaque ·
`0.5` ambiguous. A verdict takes the **minimum** over its evidence, so a single weak edge caps
the whole answer. What cannot be resolved is counted and reported, never guessed. Every JSON
answer ends with a `_meta` block: the ref read, freshness, warnings, and coverage.

---

## Indexing

```sh
reponite index .                       # the working tree, as ref HEAD
reponite index . --git v1.0.0          # a tag, read from the object store (no checkout)
reponite index . --git main,v1.0.0     # several revisions at once
reponite index . --tags 'v2.*'         # every tag matching a glob
reponite index . --git v1.0.0 --submodules   # plus each submodule at the commit it pins
```

- `--git` reads a commit's tree straight from git, so your working directory is never touched.
- `--submodules` indexes every submodule at the exact commit the revision pins, as its own
  repo labeled `<parent>@<ref>`.
- Indexing a commit that is already indexed under the current ruleset is skipped; `--force`
  re-indexes it. When a new reponite version changes what is extracted (its *ruleset*), the
  next index of a ref picks the change up, and answers warn when the refs they compare were
  indexed under different rulesets.
- The index lives at `<repo>/.reponite/index.db`, or at `$REPONITE_STORE_DIR/<repo>/index.db`
  when that is set.
- Vendored code is excluded by default; see [Ignoring vendored code](#ignoring-vendored-code).
- Files that cannot be hand-written source are skipped and listed at the end of the index
  output: files larger than 4 MB (`REPONITE_MAX_SOURCE_BYTES` changes the limit), binary
  content, and minified or bundled web assets.
- Parsing runs on every core. A Linux-kernel-sized tree (about 67,000 files) indexes in a few
  minutes and needs about 6 GB of memory at its peak; ordinary repositories need far less.

`reponite watch [dir]` re-indexes `HEAD` whenever source files change.

---

## Asking questions

Every command below prints JSON, so it pipes cleanly into `jq` or a script.

### Orientation — `map`, `explain`, `guide`

The first calls on a codebase you have never seen. Both read any indexed ref, take
`--budget <tokens>` (default ~3000) and `--format text`, and end with `truncated` plus
`next` drill-in commands instead of cutting silently.

```sh
reponite map --repo shop 2.1.0 --format text   # one repo in depth
reponite map                                   # the fleet: every repo + cross-repo links
reponite explain orders.Load                   # a symbol
reponite explain web/src/App.tsx               # a file  (or path:line, or a directory)
reponite explain "GET /api/orders"             # a route: handler + every client calling it
reponite guide                                 # which command answers which question
```

- **`map`** reports the README summary line; languages with file and symbol counts;
  **entry points** — `main` functions, CLI commands and subcommands (argparse, click,
  typer, fire, cobra, urfave/cli, clap, commander/yargs, console scripts, npm `bin`), HTTP
  servers with their route count per framework, web UI roots (React, Vue, Angular) and
  page routes (React Router, Vue Router, Angular, Next.js), ROS nodes, launch files and
  pluginlib/component plugins, CMake/Cargo build targets, Dockerfile `CMD`/`ENTRYPOINT`,
  docker-compose services, CI jobs (GitHub Actions, GitLab CI, Jenkins), package.json
  scripts and Make targets; **modules** with the one-line purpose their own docs state
  (package manifest description, README, Go package doc, Python `__init__` docstring, Rust
  `//!`, Java `package-info`, Django app name, `@file`/`@module`) — never an invented one;
  **hubs**, the symbols with the most resolved call edges; and the **declared external
  dependencies** from go.mod, package.json, requirements*.txt, pyproject.toml, Pipfile,
  setup.cfg, package.xml, CMakeLists.txt, Cargo.toml and pom.xml (the repo's own packages
  removed). Without `--repo` it lists every indexed repo and the links the index already
  knows between them: HTTP client → route, ROS topic, import-resolved reference, declared
  dependency.
- **`explain`** resolves whatever handle you hold. A **symbol**: kind, signature,
  decorators, doc comment, location, body (trimmed to the budget), direct callers and
  callees with `resolution_method` + `confidence`, the HTTP route / ROS topic / UI event
  (`onClick={…}`, `@click`, `(click)`) that reaches it, the tests referencing it, and the
  `brief` command for editing it. A **file**: its symbols, imports, role, entry points,
  routes and topics, and which files call into it and which it calls. A **directory**: its
  purpose, files and most connected symbols. A **route**: the server handler and every
  client call that reaches it. A name defined in several repos returns the candidates.
- **`guide`** prints the decision table an AI agent should follow; MCP serves the same
  table as the `reponite_guide` tool, prompt and resource.

READMEs, Dockerfiles, Makefiles, manifests and CI workflows are stored at index time; a ref
indexed by a reponite too old to store them maps without them, and `map` says so.

### Search — four rungs, cheapest first

Pick the cheapest rung that answers your question.

```sh
# 1. Exact / regex. Trigram-prefiltered, and each hit is fused with its enclosing symbol.
reponite grep validateCard
reponite grep "TODO|FIXME"                 # real regex, alternation included
reponite grep "TODO|FIXME" --limit -1      # everything (default caps at 50)
reponite grep "TODO|FIXME" --offset 50     # page through a big result

# 2. Symbol names.
reponite search Charge

# 3. "Where is the thing that does X" — KEYWORD ranking (term-idf over identifiers and
#    bodies), not semantic, unless an embeddings endpoint is configured (neural build).
#    Per-repo term indexes are cached, so a warm fleet answers in well under a second.
reponite semsearch "where we charge a card"

# 4. Compatibility (the sections above).
reponite compat Charge
```

Every grep hit tells you which **function** it landed in, not just the line — so a match is
one hop from the call graph rather than a dead end.

Counts are precise, and they mean exactly one thing each: `total` is every matching line
(ground truth, independent of paging), `matches` is the window you asked for, `truncated`
means more exist *after* this window, and `scanned` is how many candidate **files** were
examined. No approximations hiding behind a number.

### Usages — every call site, and whether it's real

```sh
reponite usages validateCard
```

```jsonc
{
  "symbol": "validateCard",
  "total": 12,
  "usages": [
    { "path": "billing/charge.go", "line": 44, "in": "Charge",
      "text": "return validateCard(card)", "confirmed": true },
    { "path": "docs/notes.md",     "line": 7,  "in": "",
      "text": "// validateCard() is called from Charge", "confirmed": false }
  ]
}
```

`confirmed: true` means the enclosing function is a **resolved caller in the call graph**.
`false` means it's a lexical match — a comment, a string, or a dynamic call that static
analysis can't prove. Both are shown, clearly separated. Nothing is dropped and nothing is
overstated.

### Verify edit — what breaks, before you save

Pass a file's *proposed* content and find out what it breaks — no saving, no compiling.

```sh
reponite verify-edit internal/query/coordinator.go
```

```jsonc
{
  "path": "internal/query/coordinator.go",
  "safe": false,
  "changed": ["reposFor"],
  "impacts": [{
    "symbol": "reposFor",
    "kind": "signature_changed",
    "breaks": [
      { "path": "internal/query/grep.go",     "line": 153, "in": "GrepRepo",    "confirmed": true },
      { "path": "internal/query/semantic.go", "line": 173, "in": "SemanticSearch", "confirmed": true }
    ]
  }]
}
```

`safe: false` means at least one **confirmed** caller breaks. This is the tightest feedback
loop reponite offers, and it's why an AI agent can edit a shared symbol without guessing.

### Cross-repo impact — who else depends on this

```sh
reponite ximpact GetUser
```

```jsonc
{
  "target": "GetUser",
  "modules": ["github.com/acme/api"],
  "callers": [
    { "repo": "web",     "caller": "svc.Handle",  "resolution_method": "scip-resolved",
      "confidence": 0.95, "expected_signature": "stale" },
    { "repo": "worker",  "caller": "job.Sync",    "resolution_method": "import-resolved",
      "confidence": 0.75, "expected_signature": "current" }
  ],
  "stale_callers": 1,
  "contract_changed": true
}
```

Two things to notice.

**Callers are matched in tiers, and each says which tier it came from.** Highest first:

| Tier | `resolution_method` | Confidence | How the match was made |
|---|---|:---:|---|
| 0 | `scip-resolved` | 0.95 | A globally unique [SCIP](#scip-symbol-precise-cross-repo-edges) symbol identity — no name guessing |
| 1 | `import-resolved` | 0.75 | The caller's import resolves to this module, and the name matches |
| 2 | `unresolved-external` | 0.6 | Name match only — the honest fallback |

**`expected_signature` is per caller.** `stale` means that caller was last indexed against
an *older* shape of the symbol — it still expects the old contract. `stale_callers: 1` is
the "1 of 2 callers hasn't caught up" number you want before a deploy. A caller reponite
couldn't determine reads `""`: unknown, never guessed.

### Root cause — from symptom to the actual mutation

```sh
reponite rootcause Charge v1.0.0 HEAD
```

Or skip the guessing entirely and paste a stack trace:

```sh
reponite rootcause-trace crash.log --from v1.0.0 --to HEAD
```

reponite maps each frame to a symbol, walks only the **failing path**, and returns the
mutation sites on it — with the commit and PR that introduced each one, where git history
can supply it. Go, Python, JavaScript, and Java traces are understood.

### Investigate — “how does X work?” in one answer

```sh
reponite investigate "how does the picking workflow decide which item to grab"
```

Returns a single cited markdown dossier: the most relevant symbols across **all** your
repos, each with a body preview and its callers and callees, filled to a token budget.
It replaces the search → read → search → read loop, which matters most when the thing
asking is an AI agent paying for every round trip.

### Editing brief — everything needed to change one symbol

```sh
reponite brief Charge --budget 3000
```

One bundle: the full body, its callees (with previews), its callers, the tests that cover
it, the reason it last changed, and its compatibility across refs. Sections are filled in
priority order until the token budget runs out, and anything dropped is **listed by name**
so you know what you're missing rather than silently getting less.

### ROS topic graph — the edges no call graph contains

In a robotics system, a publisher and a subscriber live in **different processes**, joined
only by a topic-name string that the middleware resolves at runtime. There is no
source-level edge between them, so no call graph can ever contain one.

```sh
reponite topics                # the whole communication map
reponite topics /cmd_vel       # just this one: who publishes, who reacts
```

```jsonc
{
  "family": "topic", "name": "scan", "connected": true, "confidence": 0.9,
  "producers": [{ "repo": "driver", "path": "src/lidar.cpp", "line": 42,
                  "in": "Lidar::init", "msg_type": "sensor_msgs::LaserScan",
                  "msg_type_source": "template" }],
  "consumers": [{ "repo": "planner", "path": "nav.py", "line": 88,
                  "in": "Nav.__init__", "msg_type": "LaserScan",
                  "msg_type_source": "positional-arg" }]
}
```

Supports **roscpp, rospy, rclcpp, and rclpy** — publishers, subscribers, services, and
actions. Message types are recovered even where ROS 1 hides them: `subscribe()` carries no
template, so the type comes from the callback's parameter, and `msg_type_source` tells you
which method was used. Confidence rises to 0.9 when both ends agree on the type.

**Launch-file remapping is resolved.** A node whose source subscribes to `scan` may actually
receive `raw_scan_front`, because a launch file rewrote it:

```xml
<node pkg="rr_navigation" type="scan_filter" name="filter">
  <remap from="scan" to="raw_scan_front"/>
</node>
```

reponite indexes `.launch`/`.test` XML and pairs producers with consumers on the **runtime**
name, so remapped endpoints link to the right partner instead of dangling. Each one keeps its
source name, reports its `effective` name, and cites the launch file that did it
(`name_resolution: launch-remapped`). `$(arg …)` expands against declared defaults.

Still honest about what it cannot know, in the output itself: a remap target that keeps an
unexpanded substitution (`$(find …)`, `$(env …)`, an `$(arg …)` with no default) is **reported,
not applied** — grouping endpoints under the literal `$(arg scan_topic)` would be worse than not
remapping. `<include>` directives are not expanded, a name remapped inconsistently within one
package reads `launch-ambiguous` rather than being guessed, and names built by expressions no
trace below explains are *counted as unresolved*.

**Names read from parameters are resolved.** Production nodes rarely hard-code a topic: they
read it into a variable or a config-struct member and pass that to `advertise`/`subscribe`.
reponite traces the variable to the parameter read into it (`get_ros_param`, `param()`,
`getParam`, rclcpp `declare_parameter`/`get_parameter`, rospy `get_param` — in the same file, or
the same package, where a params struct's loader lives), then the parameter to its value: a
launch `<param>`/`<rosparam>` for a node of that package (YAML files are indexed for this, and
`$(find pkg)`/`$(arg …)` are followed), then any launch file in the repo, then the default
written in the call, then a package YAML no launch file is seen loading. Test launch files never
supply a production value.

```jsonc
{ "path": "planner/src/planner.cpp", "raw": "params.status_topic", "param": "status_topic",
  "name_resolution": "param:status_topic=/robot_status (planner/config/topics.yaml:3 via planner/launch/planner.launch:4)",
  "name_confidence": 0.7 }
```

Every such name carries `name_confidence` below a literal's, and a group's `confidence` never
exceeds its best-supported producer/consumer name. Sources that disagree are listed under each
candidate, labeled `ambiguous`; a variable or parameter with no resolvable value is still
reported under `unresolved_endpoints` (`param:status_topic (unresolved)`), and a focused
`topics /status` shows the ones named like the topic.

**Names passed in by a caller are resolved.** Library code often names neither its topic nor
its namespace: a publisher class advertises its `topic_name` argument on a node handle it was
given, the handle was built by a caller as `ros::NodeHandle private_nh("~/" + name)`, and
`name` comes from a class the launched node constructs. reponite follows the arguments back
through the call sites — string literals, `+` concatenations, function and constructor
parameters, `NodeHandle` declarations — and places a private `~name` under every launched node
of the package the chain starts in:

```jsonc
{ "repo": "navigation", "path": "costmap_2d/src/costmap_2d_publisher.cpp", "line": 52,
  "raw": "topic_name", "name": "move_base_flex/global_costmap/costmap",
  "name_resolution": "args: costmap_2d/src/costmap_2d_publisher.cpp:52 Costmap2DPublisher(topic_name) ← costmap_2d/src/costmap_2d_ros.cpp:159; namespace ~/global_costmap ← … ← mbf_costmap_nav/src/mbf_costmap_nav/costmap_navigation_server.cpp:164; node move_base_flex (pkg mbf_costmap_nav)",
  "name_confidence": 0.55 }
```

A function called from more than 24 places is too generic to say which call reached the
endpoint and is not followed; a name whose chain ends in anything other than a literal stays
under `unresolved_endpoints`.

**Bindings that only a launch file shows.** A launched node whose source is not indexed (a
third-party package) or that builds its topic name by an expression is still wired by its
`<remap>` lines. Each such remap is listed under `declared` (role `launch_declared`): the
node, the name its source uses, and the runtime name. Its direction is unknown, so it is never
counted as a producer or a consumer, and a leading namespace substitution such as `$(arg ns)`
is read as the root namespace and labeled. A remap that already explains an indexed endpoint
is not repeated.

**rviz configurations.** `.rviz` files are read as endpoints too: each display is a consumer
of the topic it shows (`Topic`, `Marker Topic`, `Update Topic`, or an rviz2 `Value:`), and
each tool under `Tools:` is a producer (`/initialpose`, a goal topic). The display's name and
class are cited with the line.

### Frontend — pages, component trees, and the button that calls the API

```sh
reponite ui                                   # the app's pages: client route -> component (+ handle)
reponite ui /orders/:id --format text         # that page's component tree, budget-bounded
reponite ui CartPage --budget 6000            # by component name, handle (dir.Name) or file path
reponite ui CartItem --uses                   # where a component is rendered, passed or routed to
```

**Pages** come from react-router (`<Route path element|component|render>` trees with nested
paths, `createBrowserRouter`/`useRoutes` objects), vue-router and any `{ path, component }`
route table (lazy `import()` and module-federation `exposes` names resolved), Next.js `pages/`
and `app/`, SvelteKit `src/routes` and Nuxt `pages/`. Route paths built from constants are
evaluated; what cannot be is shown as `{EXPR}`, never guessed.

**A tree** expands breadth-first until `--budget` is spent (`truncated` + a `next` command
for the first subtree left out). Per component: props (TS interface/type, PropTypes,
`defineProps`, Svelte `export let`/`$props`), state (`useState`/`useReducer`/`useContext`,
redux selectors and `connect`, zustand/pinia/vuex stores, Vue refs, Svelte runes), effects and
their deps, and every event binding (`onClick`, `@click`, `on:click`) with the API calls its
handler reaches — through local helpers, imports, store actions and the call graph — linked
with `routes` to the server endpoint:

```text
OrderForm  shop:src/components/OrderForm.tsx:6-19 [src/components.OrderForm]
  props: onDone: () => void
  state: qty (useState 1)
  input change → setQty :14
  button "Save" click → handleSave :15
    → handleSave → POST /api/orders → OrdersView (shop:api/shop/urls.py:3)
  button "Cancel" click → onDone (prop: parent decides) :16
```

`.vue` and `.svelte` files are indexed (script blocks parsed in place, so lines are the
file's own), and a JSX element or template tag is a call edge — `context CartPage` lists the
components it renders, `usages CartItem` where it is rendered. `query.UIEdges` returns the
same render / handler / API edges as a flat list for other queries.

### Trace — "what happens when …", end to end

`trace` walks the call graph from an entry DOWN (what it sets off) or UP (how execution gets
there) as one compact tree, across repos and across the boundaries a call graph cannot see:
a client HTTP call into the route handler that serves it (Django/DRF class views dispatched by
HTTP method, with the framework hooks and serializer methods they override; FastAPI, Flask,
Express, Go), a ROS publish into the subscribers' callbacks, a base/interface method into its
implementations in any repo (C++, Python, TypeScript/JavaScript, Java class heads), and an
import into another indexed repo's module. Each node lists the **side effects** its body
performs — HTTP calls out (with the route they reach), DB/ORM reads and writes (Django ORM,
SQLAlchemy, raw SQL, GORM, Prisma, TypeORM/repository calls), publish/emit, file writes,
subprocesses, log lines, raised errors — and each edge its `resolution_method` + confidence.

```sh
reponite trace createOrder                      # a symbol (any spelling, Class::method too)
reponite trace "POST /api/orders" --format text # an HTTP route: starts at the server handler
reponite trace src/pages/Cart.tsx:42            # the function enclosing a call site
reponite trace /cmd_vel                         # a ROS topic: starts at the subscribers' callbacks
reponite trace "manage.py import_orders"        # a CLI command: its registered handler
reponite trace OrderView.post --direction up    # who reaches this, up to the UI calling it
```

```text
- src/api.createOrder  @web:src/api/orders.ts:1
    ! http L1 POST /api/orders/ → shop:* api/orders/ → views.OrderView
  - [route] POST /api/orders/ → views.OrderView  ⟵http route-match 0.60  @shop:shop/urls.py:4  (django)
    - shop.OrderView.post  ⟵handler http-method-dispatch 0.75  @shop/views.py:5
        ! raise L7 ValidationError "empty order"
        ! db/write L8 order = Order.objects.create(total=request.data["total"])
        ! log L9 logger.info "created order %s"
      - shop.notify  ⟵call name-resolved 0.90  @shop/events.py:1
          ! exec L2 subprocess.run(["notify-send", str(order.id)])
```

Breadth-first under `--depth` (default 5) and `--budget` tokens (default 2000, measured on the
text rendering), so a cut drops the deepest detail first. Repeated subtrees are expanded once
(`stop: repeat`, `same: <handle>`), cycles are marked, an ambiguous call is resolved by the
caller's import or receiver when they decide it and otherwise listed with its candidates, and
standard/library calls are named under `ext`, never walked (`--skip prefix,…` adds prefixes).
The result says where the walk stopped and why (`stops`, `truncated`, `next`). A framework's
own behavior (an inherited DRF `create()`) is inferred and labeled so; an unrecognized
framework is reported as such rather than guessed.

### CI gate — fail the build on an API break

```sh
reponite ci-check --base origin/main --head HEAD
```

Exits non-zero if any **exported** symbol was removed or changed shape. "Exported" is
decided per language (Go's capitalization, the leading-underscore convention elsewhere),
not by one hardcoded rule. Behavior changes are reported but don't fail the build — they
aren't contract breaks.

### Why is this code like this? — `why`

```sh
reponite why Cart.add_item v2.3.0 --budget 1500 --format text
```

The commits that changed the symbol's lines or signature — `git log -L` on its line range at
each indexed ref's commit, read straight from the object store (no checkout) — newest first,
each with date, author, subject, a body excerpt (trailers dropped), the PR/issue numbers and
ticket ids in the message, a `kind` (`introduced`, `moved`, `signature` = the declaration line
changed, `body`), and `first_in`: the earliest indexed ref (release) that contains it. When an
older indexed ref has the symbol before its apparent introduction, the symbol moved, and the
history continues from that ref's own file and range. The symbol's compat history across the
indexed refs (in commit order) comes with it.

```text
why shop.Cart.add_item  (shop@v2.3.0 shop/cart.py:40-47)
2026-06-17 3f2a91c0de body       feat: bulk add (#882)  [ada +3/-1 first_in=v2.3.0]
2024-07-02 a81d03b7e2 introduced feat: cart (#520)  [bo +4/-0 first_in=v2.0.0]
compat: v2.0.0:present → v2.3.0:behavior_changed
```

A ref indexed from a working tree has no commit, so it gets the compat history only (index it
with `--git`). Budgeted: past `--budget`, `truncated: true` and `next` say how to get the rest.

### Where does this error come from, and who handles it? — `errors`

```sh
reponite errors 'Order 991 not found'      # the message a user saw, placeholders filled
reponite errors OrderNotFound              # a type
reponite errors 404 --refs api=v3,web=v5   # an HTTP status, across backend and frontend
reponite errors                            # the error catalogue per repo
```

Producers: `raise`/`throw`, error values (`errors.New`, `fmt.Errorf`, sentinels and every
`return` of them, `new XError(…)`, `Err(…)`), panics, error-level logs (`logger.error`,
`console.error`, `ROS_ERROR`), and HTTP error responses (`Response(status=4xx)`,
`JsonResponse`, `abort(404)`, `res.status(4xx).json`, `http.Error`, gin/echo,
`ResponseStatusException`) — with the message template, the HTTP status (literal, named
constant, a class's `status_code`, or the framework's own exception types), the enclosing
function as a handle, and one hop of callers. A message is matched like `logsite` (printf,
`{}`/f-strings, `${}`, concatenation); a DRF-style `default_detail` links to every bare raise
of its class. Handlers: `except`/`catch` naming the type or one of its base classes (class
hierarchy read from the code), JS `catch` narrowed by `instanceof`, `errors.Is/As`, `recover`,
DRF `EXCEPTION_HANDLER`, FastAPI/Flask exception handlers, Django `process_exception`,
Express error middleware, Nest `@Catch`, Spring `@ExceptionHandler`, React error boundaries,
and frontend status checks / response interceptors for HTTP errors. `on_path` marks a handler
in the producing function or a direct caller. Python, JS/TS, Go, C/C++, Java, Rust.

**Errors defined as data.** Many systems keep their error names and messages in a catalogue
(a JSON or YAML table mapping a code to a name and a text) and raise only the code. When the
queried name or text appears in such a file, `errors` returns those lines under `data`, with
the neighbouring keys of the entry (the code, the other languages) and `loaded_by`: the code
files that name the catalogue — the hop back into the code that raises it.

### Which tests cover this, and how do I run just those? — `tests`

```sh
reponite tests add_item --depth 3 --format text
```

```text
tests shop.add_item  (shop@HEAD)
d1 pytest    shop/tests/test_cart.py:10 TestCart.test_add [call_graph]
d1 pytest    shop/tests/test_api.py:14 test_orders [http]
d2 pytest    shop/tests/test_cart.py:5 test_checkout [call_graph] via shop.checkout
run (pytest, 3): pytest shop/tests/test_cart.py::TestCart::test_add shop/tests/test_api.py::test_orders shop/tests/test_cart.py::test_checkout
```

Tests reach a symbol through the call graph (≤ `--depth` hops), through an HTTP request the
test sends to the route serving it (`client.get("/api/orders")` → the view; see `routes`), or
by naming it in a test file (JS/TS `it()`/`test()` blocks, gtest `TEST(Suite, Name)`, mocks).
The framework is detected per file — pytest (incl. `conftest.py`), unittest, Django, go test,
jest, vitest, mocha, playwright, gtest, catch2, rostest (from the `.test` launch file naming
the script), cargo, junit — and each framework gets one command selecting exactly those tests.
gtest/catch2 binaries are not resolved (CMake is not indexed): the command names the source.

---

## Data models and configuration

`models` answers "what does this backend store, and who touches it?" without reading the
models package by hand. With no argument it is an ER summary — every model with its table,
field count and relations, tables first, then by how connected each one is. With a model (class
or table name, `repo:Name` to pick a repo) it lists the fields (type, null/default/unique/index/pk,
inherited ones marked), relations **both ways**, the serializers that expose it, the API routes
whose handler touches it (from `routes`), and the code that **writes** and **reads** it: ORM call
sites (manager/session/repository/client calls and their chained operation — `filter().update()`
is a write), related managers and instance saves at lower confidence, and raw SQL naming its
table anywhere in the fleet. Every site carries `file:line` and a handle for `brief`/`context`.

| Family | Recognized |
|---|---|
| Python | Django models (abstract bases, mixins, multi-table parents, `Meta.db_table`), DRF serializers, SQLAlchemy (declarative, 2.0 `Mapped`, Flask-SQLAlchemy), SQLModel, Pydantic and dataclasses (as schemas) |
| TypeScript/JavaScript | TypeORM, MikroORM, sequelize-typescript entities; Prisma client calls |
| Schema files | Prisma `.prisma`, SQL DDL (`CREATE`/`ALTER`/`DROP TABLE`, indexes) replayed across migration files in order, protobuf messages (wire schemas) |
| Go | GORM structs (tags, embedded `gorm.Model`, `TableName()`) |

`config` is the configuration surface: every environment variable, CLI flag, settings key,
feature flag and ROS parameter the code reads — with the default it falls back to — joined to
where the repo sets it: `.env` files, docker-compose `environment`, Kubernetes/Helm `env`,
Dockerfile `ENV`/`ARG`, CI workflow `env`, launch `<env>`/`<param>`/`<rosparam>`, settings
modules, `.properties`, shell `export`, code that writes the environment, and `--flags` passed in
scripts. Keys **read but never set** and **set but never read** are listed first — the usual answer
to "my config change did not take effect". Reads are recognized in Python (`os.getenv`/`os.environ`,
django-environ, decouple, pydantic `BaseSettings`, Django `settings.X`, argparse/click/typer),
JS/TS (`process.env`, `import.meta.env`, commander), Go (`os.Getenv`, `env`/`envconfig` tags,
viper, `flag`/cobra), C/C++ (`getenv`), Rust (`std::env`, clap), Java (`System.getenv`, `@Value`)
and shell (`${VAR:-default}`).

```bash
reponite models --format text                 # ER summary, fleet-wide
reponite models Order 2.4.0 --budget 6000     # one model at a tag
reponite config --kind env --no-tests         # env vars only, test code hidden
reponite config DATABASE_URL --format text    # every read and set of one key
```

Both take `[ref]`, `--ref`/`--refs`/`REPONITE_REFS`, `--repo`, `--no-tests`, `--budget N` (a cut
answer says `truncated` and how to get the rest) and `--format text`. Detection is by framework
idiom, so it is medium confidence and says what it recognizes; a model reached only through a
variable of unknown type, or a value set outside the indexed repos (a secret store, a deploy
tool), is not seen. Schema and configuration files (`.sql`, `.proto`, `.prisma`, `.env*`,
`Dockerfile`, `.properties`, CI workflow YAML) are indexed from ruleset 6; refs indexed earlier
are flagged in `_meta.warnings`.

---

## Working with many repos

Most interesting questions cross repository boundaries. reponite keeps a **fleet registry**
so you don't re-type your directory list on every command.

```sh
reponite index ~/src/api       # indexing registers the repo automatically
reponite index ~/src/web

reponite serve                 # mounts the whole fleet, from any directory
reponite mcp                   # so does the agent mount
reponite ximpact GetUser       # and so do cross-repo queries

reponite fleet list            # what's registered (stale entries flagged)
reponite fleet add <dir>       # register an already-indexed repo
reponite fleet remove <dir|repo>
reponite fleet path            # where the registry file lives
```

The registry is a small JSON file holding **metadata only** — repo, directory, module path,
last-index time. Your code and index stay in each repo's own `.reponite/index.db`.

Naming directories explicitly always beats the registry, and a registered repo whose index
has been deleted or moved is **reported as stale**, never silently skipped. Any command can
take `--local` to ignore the fleet and answer for the current repo alone.

### SCIP: symbol-precise cross-repo edges

Cross-repo links normally match on `(module path, name)` — precise about the module, but
still a name match. If you generate a **SCIP index** (`scip-go`, `scip-typescript`,
`scip-python`, …) and leave `index.scip` at a repo root, reponite reads it and every symbol
gains a globally unique **moniker**:

```
scip-go gomod github.com/acme/api v1.2.0 `pkg/user`/GetUser().
```

Two repos indexed independently produce the *same* string for the same symbol, so callers
are matched **symbol to symbol** — no name guessing at all — and are labeled
`scip-resolved` at 0.95 confidence.

If you have no SCIP index, nothing changes: the existing tiers answer exactly as before.
A corrupt index is reported and skipped rather than silently trusted. And because monikers
match by exact string, a version mismatch can only ever *lose* a match (falling back one
tier), never invent one.

---

## AI agents (MCP)

An AI agent can read, understand and safely modify code through reponite's tools without
opening source files one by one.

```sh
reponite setup .    # register with Claude Desktop, Claude Code, Cursor, Windsurf…
```

Then the agent has these tools (each description says when to use it; the `reponite_guide`
prompt/resource gives the order):

| Tool | What the agent gets |
|---|---|
| `reponite_map` | First call: entry points, modules, hubs, dependencies; fleet links |
| `reponite_explain` | Understand one symbol / file / directory / route |
| `reponite_guide` | Which tool answers which question |
| `reponite_investigate` | One cited dossier answering “how does X work?” |
| `reponite_search` | Symbols by name, fleet-wide |
| `reponite_semsearch` | Symbols by keyword ranking (semantic with an embeddings endpoint) |
| `reponite_grep` | Regex, with each hit's enclosing symbol |
| `reponite_usages` | Every call site, graph-confirmed |
| `reponite_context` | Direct callers and callees |
| `reponite_brief` | Token-budgeted bundle for editing a symbol |
| `reponite_blast_radius` | Everything that could break, pre-edit |
| `reponite_verify_edit` | What a proposed edit breaks, pre-save |
| `reponite_compat` | Compatibility verdicts across refs |
| `reponite_diff` | Symbol-level delta between two refs |
| `reponite_rootcause` | Walk a behavior change to its origin |
| `reponite_rootcause_trace` | Same, seeded from a stack trace |
| `reponite_ximpact` | Fleet callers + per-caller contract skew |
| `reponite_topics` | ROS publisher ↔ subscriber graph, launch-declared bindings and rviz displays |
| `reponite_logsite` | Which code printed a log line |
| `reponite_impls` | Subclasses and plugins of a base class, and their overrides |
| `reponite_routes` | HTTP client calls linked to the server routes that serve them |
| `reponite_ui` | Frontend pages and component trees, down to the API calls |
| `reponite_trace` | “What happens when …”: call tree across HTTP/topic/impl boundaries, with side effects |
| `reponite_models` | Data models: fields, relations, serializers, routes, readers and writers |
| `reponite_config` | Config keys read (with defaults) vs set; read-never-set / set-never-read |
| `reponite_repos` | What's indexed and where |
| `reponite_refs` | A repo's indexed refs |
| `reponite_why` | The commits (and PRs) that made a symbol what it is |
| `reponite_errors` | Where an error message/type/status comes from, and who catches it |
| `reponite_tests` | Tests exercising a symbol + the command to run just those |

Every response is **token-budgeted** and carries a `_meta` envelope with confidence,
freshness, and provenance — so the agent knows how far to trust each answer instead of
treating everything as equally certain.

A miss returns **“did you mean…?”** rather than an empty result, so an agent that
misremembers a name recovers in one step instead of concluding the symbol doesn't exist.

`reponite setup` writes the MCP server entry for a client:

```sh
reponite setup .                         # Claude Desktop (default)
reponite setup . --client claude-code    # Claude Code: the repo's .mcp.json
reponite setup . --client cursor         # ~/.cursor/mcp.json
reponite setup . --client windsurf       # ~/.codeium/windsurf/mcp_config.json
reponite setup . --config <file>         # any client using the mcpServers format
reponite setup . --print                 # print the entry instead of writing it
```

The entry runs `reponite mcp <repo>`. With no directory, `reponite mcp` mounts every repo in
the [fleet registry](#working-with-many-repos).

---

## Web dashboard

```sh
reponite serve        # every registered repo → http://127.0.0.1:8899
reponite serve .      # just this one
```

Seven views: **Overview** (what's indexed, and the database behind it) · **Explore**
(search → editing brief) · **Diff** · **Impact** (fleet callers and contract skew) ·
**Topics** (ROS graph) · **Usages** · **Verify** (paste an edit, see what breaks).

Every view is deep-linkable, so a URL is shareable with a teammate. Light and dark themes.
No external assets — the dashboard works offline.

---

## Configuration

### Ignoring vendored code

Vendored trees flood search results with third-party noise, so exclusion happens **at index
time** and every surface benefits at once. Three layers, all gitignore syntax:

1. **Always on:** `vendor/`, `third_party/`, `node_modules/`, `.git/`, `testdata/`, and every dot-directory.
2. **`.reponiteignore`** at the repo root:
   ```gitignore
   # a monorepo bundling upstream ROS
   external/
   *.generated.go
   !important.generated.go
   ```
3. **`--exclude`** on the command line (repeatable, comma-separable):
   ```sh
   reponite index . --exclude "external/**" --exclude "*.pb.go"
   ```

Supported: `#` comments · `!` negation (last match wins; an excluded *directory* can't be
re-included from below, as in git) · trailing `/` for directories only · leading `/` to
anchor at the root · `*` `?` `[...]` within a path segment · `**` across segments.

`index --git <rev>` reads `.reponiteignore` **from the indexed tree**, so a historical ref
is filtered by the rules that existed then.

### Environment variables

| Variable | Purpose | Default |
|---|---|---|
| `REPONITE_FLEET` | Fleet registry path | `$XDG_CONFIG_HOME/reponite/fleet.json`, else `~/.config/reponite/fleet.json` |
| `REPONITE_STORE_DIR` | Keep every index outside the repos, at `$REPONITE_STORE_DIR/<repo>/index.db` (with `--git`, the repo itself is then only read) | unset — `<repo>/.reponite/index.db` |
| `REPONITE_REFS` | Pin repos to refs, `repo=ref,…` — answers read those refs instead of `HEAD` | unset |
| `REPONITE_NO_TESTS` | `1` drops test code from answers (same as `--no-tests`) | unset |
| `REPONITE_MAX_SOURCE_BYTES` | Largest source file parsed; larger ones are skipped and listed | `4194304` (4 MB) |
| `REPONITE_CPUPROFILE` | Write a CPU profile of one command to this file (`go tool pprof`) | unset |
| `REPONITE_MEMPROFILE` | Rewrite a heap profile to this file every few seconds while a command runs | unset |
| `REPONITE_EMBED_ENDPOINT` | Optional embeddings endpoint for neural semantic search | unset — the built-in ranker is used |
| `REPONITE_EMBED_MODEL` | Model name for that endpoint | unset |
| `REPONITE_EMBED_API_KEY` | Bearer token, if the endpoint needs one | unset |

### Optional: neural semantic search

Semantic search works out of the box with **no model and no network** — an identifier-aware
ranker that splits `validateCardNumber` into `validate card number` and weights rare terms
higher. That's the default, and it's often enough.

For better recall on paraphrased queries, point it at any OpenAI-compatible embeddings
endpoint (Ollama, OpenAI, LiteLLM, vLLM):

```sh
export REPONITE_EMBED_ENDPOINT=http://localhost:11434/v1/embeddings
export REPONITE_EMBED_MODEL=nomic-embed-text
reponite semsearch "where we charge a card"
```

Results always name the ranker that produced them (`term-idf` or `neural:<model>`).
**If the endpoint is down, the search falls back to the built-in ranker and says so** in
the result — you never silently get a worse ranking that looks like a normal one.
Embeddings are cached by content hash, so a long-running server embeds each symbol once.

---

## Command reference

```
reponite <command> [arguments] [flags]
```

### Indexing

| Command | Description |
|---|---|
| `index [dir] [ref]` | Index a directory. `--git <rev>[,<rev>…]` indexes commits' trees without checking them out; `--tags <glob>` every matching tag; `--submodules` each submodule at its pinned commit; `--force` re-indexes a commit already indexed; `--exclude <glob>` skips paths. |
| `refs` | List a repo's indexed refs |
| `repos` | Every indexed repo with its module and per-ref statistics |
| `fleet list\|add\|remove\|path` | Manage the cross-run repo registry |
| `watch [dir]` | Re-index HEAD automatically when sources change |

### Understanding code

| Command | Description |
|---|---|
| `map [ref] [--repo R]` | Orientation: entry points, modules, hubs, dependencies (no `--repo`: the fleet + cross-repo links) |
| `explain <symbol\|path[:line]\|dir\|"METHOD /route"> [ref]` | Understand one thing: doc, body, callers/callees, routes/topics/UI events, tests |
| `guide` | The decision table: which command answers which question |
| `investigate <question…>` | One cited dossier answering a plain-language question |
| `search <substring>` | Find symbols by name |
| `semsearch <query>` | Rank symbols by keyword similarity (semantic only with an embeddings endpoint) |
| `grep <pattern> [ref]` | Regex/literal search, fused with the enclosing symbol |
| `context <symbol> [ref]` | Direct callers and callees |
| `brief <symbol> [ref]` | Token-budgeted bundle for editing a symbol |
| `usages <symbol>` | Every call site, with call-graph confirmation |
| `topics [name]` | ROS publisher ↔ subscriber / service / action graph, with launch-declared bindings and rviz displays |
| `logsite "<log line>"` | Which code printed a log line (format strings matched with placeholders filled) |
| `impls <Base[::method]>` | Subclasses and plugins of a base class across repos, and their overrides |
| `routes [path]` | HTTP edges: client calls linked to the server routes that serve them |
| `why <symbol> [ref]` | Commits that changed it (git log -L per indexed ref), PR/issue, first ref shipping each, compat history |
| `errors ["<message>"\|Type\|status] [ref]` | Producers of an error and the handlers that catch it, and catalogue entries that define it as data; no argument = error catalogue |
| `tests <symbol> [ref]` | Tests exercising it (call graph, HTTP, references), framework, and the command to run just those |
| `ui [route\|component] [ref]` | Frontend pages, component trees (props/state/effects/handlers → API → server route); `--uses`, `--budget`, `--format text` |
| `trace <entry> [ref]` | Call tree from a symbol, `"VERB /route"`, `path:line`, CLI command or topic, with side effects. `--direction up`, `--depth`, `--budget`, `--format text` |
| `models [model] [ref]` | Data layer: an ER summary, or one model's fields, relations, serializers, routes, readers and writers |
| `config [key] [ref]` | Configuration surface: env/flags/settings/feature flags/ROS params read vs set |

### Changing code safely

| Command | Description |
|---|---|
| `compat <symbol> [ref]` | Compatibility verdicts across every indexed ref |
| `diff <from> <to>` | Symbol-level delta. `--changed-only`, `--package`, `--confidence-min` |
| `rootcause <symbol> <from> <to>` | Walk a behavior change to its mutation site |
| `rootcause-trace <file\|-> --from --to` | Same, seeded from a stack trace |
| `ximpact <symbol>` | Cross-repo callers with per-caller contract skew |
| `blast-radius <symbol> [ref]` | Callers + fleet callers + tests + contract state |
| `verify-edit <path>` | What breaks if this file's current content is saved |
| `ci-check --base <ref> --head <ref>` | Non-zero exit on an exported API break |

### Serving

| Command | Description |
|---|---|
| `serve [dir…]` | Web dashboard + JSON API. `--addr` to change the bind address |
| `mcp [dir…]` | MCP server over stdio, for an AI agent |
| `setup [dir]` | Register reponite in your agent's config file |

Every command accepts `--help`. Fleet-wide commands accept `--local`.

- **`--no-tests`** (or `REPONITE_NO_TESTS=1`) on `usages`, `context`, `impls`, `topics`, `routes`, `ui`,
  `compat`, `rootcause`, `grep`, `search` and `semsearch` drops everything classified as test
  code — the same rule the indexer stamps (`test/` dirs, `*_test.*`, `test_*.py`, `mock_*` …) — so
  a test publisher or mock never answers a production question. Off by default; `brief` and
  `blast-radius`, whose answers include covering tests, ignore the env.
- **Symbols** may be given as reponite ids (`dir.Class.method`), partly qualified
  (`Class.method`), or in C++ spelling (`Class::method`, `ns::Class::method`). A symbol not in
  the current repo is looked up across the fleet; when several repos define it the candidates
  are printed (with `--repo R` to choose), and a miss says whether the ref was not indexed or
  the symbol is in no searched repo, with near matches and their repos.
- **`REPONITE_REFS=repo=ref,…`** pins repos to the refs a deployed system runs (standing in
  for the default `HEAD`; an explicit ref is read as asked). Answers report the ref actually
  read — the pin's label in `_meta.ref`, and per repo in `_meta.refs` for fleet-wide answers.

Commands available in every build: `version`, `help`, `guide`, `demo` (an in-memory run that
needs no index), `setup`.

---

## Languages and file types

Go · Python · JavaScript · TypeScript (JSX/TSX) · **Vue** and **Svelte** single-file components · Java · C · C++ · Rust · **Shell** · **Robot Framework** — plus **ROS
interface files** (`.msg`, `.srv`, `.action`), where the field list *is* the contract, so
adding a field is correctly reported as `shape_changed`, and **ROS launch files**
(`.launch`, `.test`), which supply the runtime topic remapping the source cannot show.

**Shell** covers `.sh`, `.bash`, `.zsh`, `.ksh` and — importantly — **extension-less scripts
identified by their shebang**, because a CLI's entry point (`installer/rdt`, `bin/deploy`) is
usually the most valuable file in the tree and rarely has an extension. `#!/usr/bin/env
python3` and `#!/usr/bin/node` are recognized too. Shell functions have no declared
parameter list, so a shell edit is reported as `behavior_changed` and never
`shape_changed` — the honest reading for a language with no signature.

Calls resolve within a language family only (C with C++, JavaScript with TypeScript, Vue and
Svelte), so a Python `logger.info()` is never linked to a shell function named `info`. The one
crossing is Robot Framework, whose keywords call Python library functions by name.

**Robot Framework** (`.robot`, `.resource`): every test case and keyword is a symbol and every
keyword a body calls is an edge. Robot names are case- and space-insensitive, so `Open Door`
resolves to a keyword of that name and to a Python library function `open_door` alike, and a
keyword with embedded arguments (`Open ${door} Door`) is linked from `Open Front Door`. `tests`
lists the test cases that reach a symbol, with a `robot --test '<name>' <suite>` command to run
just those.

**Content files.** These are stored and searchable (`grep`, `search`, `explain` on a path)
without a call graph of their own, and several commands read them. Their structure is indexed
as `section` symbols — Markdown and reStructuredText headings, YAML and JSON keys (to three
levels) and named list entries (`name`, `id`, `key`, `title`), TOML/INI tables, Dockerfile
stages and Makefile targets — so `search` finds `README:Installation` or
`values:services:gateway` and a grep hit names the section it is in:

| Files | Read by |
|---|---|
| YAML (`.yaml`, `.yml`, up to 256 KB) | `topics` (parameter values), `config` |
| JSON (up to 256 KB; lockfiles excluded) | `errors` (catalogues), `grep` |
| rviz configurations (`.rviz`) | `topics` |
| SQL DDL, `.proto`, `.prisma` | `models` (one span per table / message / model) |
| `.env*`, `.properties`, Dockerfiles, CI workflows, docker-compose, Makefiles | `config`, `map` |
| READMEs, package manifests (`go.mod`, `package.json`, `package.xml`, `Cargo.toml`, …) | `map`, `explain` |
| Markdown, reStructuredText, AsciiDoc, TOML, INI/`.cfg`/`.conf`, templates (`.j2`, `.tpl`, `.tmpl`), Salt (`.sls`), Jsonnet, XML/xacro/URDF/SDF, HTML | `grep`, `search` (sections) |
| gettext catalogues (`.po`, `.pot`) | `errors` (translated messages) |

Other file types are not read.

---

## How it works

```
    CLI          MCP server        Web dashboard      VS Code extension
      └───────────────┴─────────┬────────┴───────────────────┘
                                │
   ┌────────────────────────────▼──────────────────────────────┐
   │  PURE CORE — Go standard library only, no CGO              │
   │                                                            │
   │  canon()            normalize source → stable identity     │
   │  three hashes       symbol · signature · behavior          │
   │  behavior Merkle    propagate change up the call graph     │
   │  Compat Oracle      the four verdicts + confidence         │
   │  root cause · diff · grep · brief · ximpact · topics       │
   └────────────────────────────┬──────────────────────────────┘
                                │  thin, build-tagged adapters
        ┌───────────────┬───────┴───────┬───────────────┐
   tree-sitter        SQLite         go-git        embeddings
     (parsing)        (storage)      (refs)         (optional)
```

The **entire correctness-critical core is pure Go with zero dependencies**. Parsing,
storage, git access, and embeddings live in thin adapters behind interfaces, each enabled
by a build tag. That means `go build ./... && go test ./...` runs anywhere, instantly, with
nothing to install — and the logic that decides whether your API broke has no third-party
code in it at all. ([ADR-018](adr/ADR-018-pure-core-thin-adapters.md)); the design in full is in
[architecture.md](architecture.md).

| Build tag | Adds |
|---|---|
| *(none)* | The pure core — builds and tests anywhere |
| `sqlite` | Persistent index storage |
| `treesitter` | Real parsers, git ref indexing, Go type-checked edges |
| `mcp` | The MCP server |
| `neural` | Optional embeddings client for semantic search |

`make cli` builds with all of them; [INSTALL.md](INSTALL.md) covers building one set.

---

## FAQ

### Do I need to check out old branches to compare against them?

No. `reponite index . --git v1.0.0` reads the commit's tree straight from the git object
store. Your working directory is never touched, so you can index twenty tags without a
single checkout.

### How big does the index get?

Roughly proportional to your unique source content, not to the number of refs. Content is
addressed by hash, so a file that didn't change between two tags is stored once. Vendored
directories are excluded by default, which usually matters more than anything else.

### Does it send my code anywhere?

No. Everything is local. The only outbound network path in the entire tool is the
*optional* embeddings endpoint for neural semantic search, which is off unless you set
`REPONITE_EMBED_ENDPOINT` — and it's behind its own build tag precisely so that "no network"
is enforced by the compiler rather than by convention.

### What does `confidence: 0.6` actually mean?

Each call-graph edge is scored by how it was resolved: `1.0` type-checker proven ·
`0.95` SCIP symbol-resolved across repos · `0.9` uniquely name-resolved · `0.75`
import-resolved across repos · `0.6` external/opaque · `0.5` ambiguous. A verdict takes the
**minimum** over its evidence, so a single weak edge caps the whole answer. `compat` also
reports `direct_confidence` (this symbol's own edges), which is often much higher than the
transitive floor.

### My language isn't supported. How hard is it to add?

Usually one table entry plus a grammar binding. The extractor is language-agnostic and
driven by a `LangRules` table — see [Adding a language](../CLAUDE.md#adding-a-language).

### Why does grep return a `scanned` count that isn't the number of matches?

`scanned` is candidate **files** examined after the trigram prefilter; `total` is matching
**lines**. They're deliberately separate numbers because conflating them would misrepresent
how much was actually searched.

### Is it safe to run against a monorepo with vendored dependencies?

Yes — `vendor/`, `third_party/`, and `node_modules/` are excluded by default. For anything
else (a bundled upstream tree, say), add a `.reponiteignore`. See
[Ignoring vendored code](#ignoring-vendored-code).

