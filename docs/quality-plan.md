# Quality plan: correct answers for any codebase

reponite is meant for any developer on any codebase. Until now its answers have been
checked only on one kind of code (large C++/ROS fleets indexed at tags), and only
for "does the command run" and "how long does it take". This plan replaces that
with measured correctness on a public benchmark, fixes what the measurements
show, and keeps domain-specific analysis (ROS, deployment templates) as optional
packs on a general core.

## What "accurate" means here

Static analysis cannot know everything a running program does (reflection,
dependency injection, dynamic dispatch, runtime-built names). So the target is:

- **No silent wrong answers.** Every fact reponite states is either correct, or
  carries a confidence below the "resolved" level and says why.
- **High-confidence facts are 100% precise** on the benchmark: a call edge,
  route, symbol location or file marked resolved is always right.
- **Recall is measured and reported**, never implied: an answer says what it
  could not see (unindexed refs, dynamic calls, unsupported frameworks).

## Baseline findings (measured on public repositories)

Benchmark repositories (pinned commits): the RealWorld reference apps in Go/gin,
Node/Express, Django and Spring Boot (the same 19-endpoint API in four stacks),
the FastAPI full-stack template (Python + React/TypeScript), fd (Rust),
Flask (Python library), sealed-secrets (Go + Helm chart) and move_base_flex
(C++/ROS).

| Area | Finding | Effect |
|---|---|---|
| Scope | Inside one repo, `routes`, `map` and others answer for every repo ever indexed on the machine; a Flask test's client call is linked to the Django admin route | Unrelated projects pollute every answer |
| Routes | Spring MVC (`@RestController`, `@GetMapping`) unsupported in `routes` (0 found) though `map` counts 26 | Inconsistent; the most common Java stack missing |
| Routes | gin group prefixes lost (`/api/articles/:slug` shown as `/{}`), handler names empty | Wrong paths |
| Routes | Express: 8 of 19 endpoints found | Low recall |
| Routes | FastAPI: every route listed 5 times (115 for 23 endpoints) | Wrong counts |
| Routes | Django REST: HTTP methods unknown (`*`) | Incomplete |
| Routes | Test files' routes listed with production ones | Noise |
| Call graph | Java `repository.save()` resolved to a shell function `save` in `gradlew` | Cross-language false edges |
| Call graph | Rust `Type::new` for 12 types merged into one symbol `src.new` (31 callers) | Conflated symbols |
| Call graph | C++ out-of-class methods located by bare name; overloads share one id | Wrong bodies (fixed: location; flagged: overloads) |
| Index | Refs indexed by an older ruleset answered with no warning | Silent gaps (fixed) |
| Fleet | Repos indexed only at tags answered empty at `HEAD` | Silent empty answers (fixed) |
| Links | A plain value (`type: default`) linked to a same-named method | False link (fixed) |
| Config | A key in a YAML file cannot be followed through templates to the code reading the rendered value | Missing capability (in progress) |
| Process | No test compares an answer with ground truth; the battery checks only crashes and time | Every gap above was found by hand |

## Ground truth, per capability

| Capability | Ground truth | Automated |
|---|---|---|
| Files stored/skipped | `git ls-files` at the commit | yes |
| Grep | the same search over the files at the commit | yes |
| Symbol definitions and spans | Go `go/ast`; Python `ast`; TypeScript compiler API | yes (Go, Python, TS/JS) |
| Call edges | Go `go/types` + static call graph; TypeScript type checker; Python `ast` with import resolution (resolvable subset) | yes |
| Call edges (Java, Rust, C++) | hand-verified golden sample per repo | golden files |
| Usages | the call sites from the call-edge truth | yes |
| Diff between tags | changed function bodies from the AST spans of both commits | yes (Go, Python) |
| Routes | hand-written endpoint lists (RealWorld spec, FastAPI template) | golden files |
| Topics, launch, params (ROS) | hand-verified golden sample | golden files |
| Config flow | hand-verified chains | golden files |
| map / explain / trace / brief | golden questions: the facts an answer must contain and must not contain | golden files |
| CLI = MCP = HTTP | the same question through all three gives the same answer | yes |
| Cost | time and peak memory per command per repo, with budgets | yes |

## Phases

1. **Benchmark and scorecard** — `tools/bench`: a manifest of public repos at
   pinned commits, the ground-truth extractors above, a scorer that prints a
   per-command, per-repo table (precision, recall, consistency, time, memory),
   and golden files for what cannot be computed. Run before every push.
2. **Scope and first-run experience** — inside a repo, the default scope is that
   repo; the fleet is opt-in (`--fleet`) or a declared group. Test code ranks
   after production code (and is excluded from listings) unless asked for.
   `index .` → `map` → `explain` → `trace` → dashboard works on a fresh machine
   with no registry, pins or flags.
3. **Accuracy fixes, by root cause** — cross-language edge leakage (calls resolve
   only within the caller's language family unless an explicit bridge exists);
   associated functions and methods qualified by their type (Rust, Java, C#,
   Kotlin, Swift, TS classes); one route extractor shared by `routes`, `map` and
   `trace`, with Spring, gin/echo/chi groups, Express routers, DRF view methods
   and FastAPI routers; route de-duplication.
4. **Packs** — ROS (topics, params, launch, pluginlib, ALICA) and template-driven
   configuration (Jinja/Ansible, Helm, Kustomize) become packs with detection,
   on by default when a repo looks like it needs them, each with its own golden
   tests. The core carries no domain idioms.
5. **Private regression** — the same scorecard run locally on private
   repositories (never committed), so domain users do not regress.
6. **Gate and docs** — CI runs the public scorecard on every push and fails on a
   precision drop; the user guide states measured precision/recall per language
   and framework instead of feature lists.

## Status

Updated as phases land; the scorecard output is the source of truth.

| Phase | State |
|---|---|
| 1. Benchmark and scorecard | Ground truth for Go, Python and TypeScript call graphs; golden route lists for five apps; scorer with call-graph and route modes. Golden files for Java/Rust/C++ call edges, ROS and config flow not yet written. |
| 2. Scope | Inside a repo the default registry is that repo's workspace; fleet-wide is opt-in. Test-only routes hidden by default. |
| 3. Accuracy fixes | Call edges: type-checked Go, module-qualified Python/JS/TS ids, language-family filtering, colliding unresolved names stored as external/ambiguous. Routes: one engine shared by `routes`, `map`, `trace`, `explain` and `models`. Open: Rust associated functions, Python methods inherited through `self`, constructor calls of classes passed as values. |
| 4. Packs | Not started. |
| 5. Private regression | Route engine run on private repositories: all differences were fixes or intended test-route exclusions. Not yet automated. |
| 6. Gate and docs | Not started. |

### Scorecard

Call edges marked resolved (confidence ≥ 0.85), against the type checker / AST truth:

| Repo | Language | Definitions | Resolved precision | Recall (static) |
|---|---|---|---|---|
| golang-gin-realworld-example-app | Go | 100% (167) | 100% (404) | 100% (404) |
| sealed-secrets | Go | 99.7% (348/349) | 100% (505) | 100% (505) |
| flask | Python | 99.2% (901/908) | 100% (237) | 98.8% (255/258) |
| django-realworld-example-app | Python | 100% (108) | 100% (7) | 100% (7) |
| full-stack-fastapi-template | Python | 100% (162) | 100% (207) | 100% (207) |
| full-stack-fastapi-template | TypeScript | 100% (266) | 100% (476) | 100% (476) |
| node-express-realworld-example-app | TypeScript | 100% (32) | 100% (34) | 100% (34) |

Routes, against the golden endpoint lists:

| App | Endpoints | Path + method recall | Precision | Handler names |
|---|---|---|---|---|
| golang-gin-realworld-example-app | 20 | 100% | 100% | 100% |
| node-express-realworld-example-app | 20 | 100% | 100% | n/a |
| django-realworld-example-app | 20 | 100% | 100% | 100% |
| spring-boot-realworld-example-app | 19 | 100% | 100% | 100% |
| full-stack-fastapi-template | 23 | 100% | 100% | 100% |

Before the route engine: Spring 0 routes, gin group prefixes lost, Express 8 of 19
endpoints, FastAPI routes listed five times, DRF methods unknown.
