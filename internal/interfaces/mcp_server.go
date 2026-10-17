//go:build mcp

// mcp_server.go exposes reponite's read tools over the Model Context Protocol
// (stdio), so an agent (Cowork, Claude Code, …) calls reponite_search/grep/
// context/compat/diff/rootcause/refs instead of reading files. Thin glue over
// ToolServer (pure); behind the `mcp` build tag (github.com/mark3labs/mcp-go).
package interfaces

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ServeStdio registers reponite's tools and serves MCP over stdio until EOF.
func ServeStdio(ts *ToolServer) error {
	CompactOutput = true // agents consume this; single-line JSON saves tokens
	s := server.NewMCPServer("reponite", "0.1.0",
		server.WithInstructions("Code intelligence over indexed git refs. On an unfamiliar codebase call reponite_map first, then reponite_explain on a handle it returns; read the reponite_guide prompt/resource for which tool answers which question."),
		server.WithPromptCapabilities(false), server.WithResourceCapabilities(false, false))

	add := func(tool mcp.Tool) {
		s.AddTool(tool, func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			args := map[string]string{}
			if m, ok := any(req.Params.Arguments).(map[string]interface{}); ok {
				for k, v := range m {
					if sv, ok := v.(string); ok {
						args[k] = sv
					}
				}
			}
			out, err := ts.Call(req.Params.Name, args)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			return mcp.NewToolResultText(out), nil
		})
	}

	add(mcp.NewTool("reponite_search",
		mcp.WithDescription("When: you know (part of) a symbol's NAME and want its id. Structural symbol-name search at a ref."),
		mcp.WithString("query", mcp.Required(), mcp.Description("substring of the symbol name")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("ref to search (default HEAD)")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("tests", mcp.Description(`"true" to include Test*/Benchmark*/Example*/Fuzz* symbols (default excluded)`))))
	add(mcp.NewTool("reponite_grep",
		mcp.WithDescription("When: you need literal TEXT (strings, config keys, comments, log text) — not definitions or callers, which search/context answer exactly. Trigram-prefiltered regex/literal search (Go regexp syntax, alternation included); each hit fused with its enclosing symbol. Counts: `total` = every matching line (ground truth), `matches` = the [offset, offset+limit) window in stable (repo,path,line) order, `truncated` = more matches exist past this window (page with offset), `scanned` = candidate files examined."),
		mcp.WithString("pattern", mcp.Required(), mcp.Description("regular expression (Go syntax); plain strings work as-is")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("fixed", mcp.Description(`"true" to match the pattern as a literal string (default: regex)`)),
		mcp.WithString("limit", mcp.Description("max matches returned (default 50, -1 = all)")),
		mcp.WithString("offset", mcp.Description("matches to skip — page through a truncated result"))))
	add(mcp.NewTool("reponite_compat",
		mcp.WithDescription("When: asking whether a symbol is still compatible across refs before shipping or upgrading. Compatibility verdicts (absent/shape/behavior/compatible) for a symbol across the repo's other refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("ref", mcp.Description("origin ref (default HEAD)"))))
	add(mcp.NewTool("reponite_context",
		mcp.WithDescription("When: you hold a symbol and want its direct callers and callees. Direct callers and callees of a symbol at a ref."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("tests", mcp.Description(`"true" to include test callers/callees (default excluded)`))))
	add(mcp.NewTool("reponite_diff",
		mcp.WithDescription("When: asking what changed between two refs, symbol by symbol. Per-symbol delta between two refs (added/removed/shape/behavior/unchanged)."),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("changed_only", mcp.Description(`"true" to hide unchanged symbols`)),
		mcp.WithString("package", mcp.Description("keep only symbols whose package has this prefix")),
		mcp.WithString("confidence_min", mcp.Description("hide changes below this confidence (0..1)"))))
	add(mcp.NewTool("reponite_rootcause",
		mcp.WithDescription("When: a symbol's behavior changed between refs and you need WHICH callee caused it. Walk a behavior-changed symbol to its mutation-site frontier between two refs."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_rootcause_trace",
		mcp.WithDescription("When: you have a stack trace and two refs (works / fails). Paste a stack trace (Go/Python/JS/Java); maps frames to symbols and drills down the failing path to the mutation site between two refs."),
		mcp.WithString("stacktrace", mcp.Required(), mcp.Description("the raw stack trace / traceback")),
		mcp.WithString("from", mcp.Required()),
		mcp.WithString("to", mcp.Required()),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_brief",
		mcp.WithDescription("When: you are about to EDIT one symbol (use explain to understand it first). Everything needed to edit a symbol in one token-budgeted bundle: full body, callees+callers (preview+handle), covering tests, and the compat snapshot. Replaces 5-6 file reads."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("budget", mcp.Description("token budget (default 3000)"))))
	add(mcp.NewTool("reponite_semsearch",
		mcp.WithDescription("When: you can describe what code does but not its name. Symbol search for 'where is the thing that does X'. Ranks symbols by similarity to a query. By default this is KEYWORD ranking, not semantic: `ranker` is `term-idf` (identifier-aware term frequency x IDF; code using other words is not found — follow up with grep/search for synonyms), or `neural:<model>` when an embeddings endpoint is configured; if a configured endpoint fails, results fall back to term-idf and `note` records the failure."),
		mcp.WithString("query", mcp.Required()),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)")),
		mcp.WithString("limit", mcp.Description("max hits (default 10)"))))
	add(mcp.NewTool("reponite_ximpact",
		mcp.WithDescription("When: changing an exported symbol other repos may call. Cross-repo impact: who across every indexed repo calls this (external) symbol — the question before changing an exported API. Source-call-graph, name-based (RPC invisible)."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("restrict each repo to this ref (default: all indexed refs)"))))
	add(mcp.NewTool("reponite_investigate",
		mcp.WithDescription("When: you have a question about a feature but no symbol/file handle yet. Ask a natural-language question about the code (\"how does X work?\", \"where is the picking workflow?\") and get ONE dense, cited dossier: the most relevant symbols across the whole fleet, each with a body preview and its callers/callees, budget-filled. Start here to understand a feature — it replaces the semsearch→brief→context loop."),
		mcp.WithString("question", mcp.Required(), mcp.Description("what you want to understand, in plain language")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("budget", mcp.Description("token budget for the dossier (default ~4000)"))))
	add(mcp.NewTool("reponite_verify_edit",
		mcp.WithDescription("When: you have a proposed new file content and want to know what it breaks before saving. Pre-commit safety check: pass a file path + your PROPOSED new content, and get back what breaks BEFORE saving/compiling — every symbol whose signature you changed or removed, with the exact call sites (fleet-wide) that rely on it. `safe:true` means no confirmed caller breaks. Call this after editing a shared symbol."),
		mcp.WithString("path", mcp.Required(), mcp.Description("repo-relative path of the file you're editing")),
		mcp.WithString("content", mcp.Required(), mcp.Description("the full proposed new content of that file")),
		mcp.WithString("repo", mcp.Description("repo that owns the file (defaults to current)")),
		mcp.WithString("ref", mcp.Description("baseline ref (default HEAD)"))))
	add(mcp.NewTool("reponite_usages",
		mcp.WithDescription("When: you need every call site of a symbol with its exact line (e.g. before changing a signature). Every call site of a symbol across the fleet — the exact calling line, its file:line, and the enclosing function — with `confirmed` when that function is a resolved caller in the call graph (vs a lexical match in a comment/string or a dynamic call). Use before changing a signature to see how it's actually called."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_blast_radius",
		mcp.WithDescription("When: before editing a load-bearing symbol. Pre-edit macro: everything that could break if you change this symbol, in one call — in-repo callers, cross-repo (fleet) callers, covering tests, and whether the API contract already moved across refs. Call this BEFORE editing a load-bearing symbol."),
		mcp.WithString("symbol", mcp.Required()),
		mcp.WithString("repo", mcp.Description("repo that defines the symbol (defaults to current)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_logsite",
		mcp.WithDescription("When: you have a line from a real log and need the code that printed it. Which code printed this log line? Paste a line from a real log (placeholders filled in: ids, counts, names) and get the code locations whose format string produces it — file:line, enclosing function, the logging call (ROS_WARN, logger.info, console.error, throw …) and the format as written — ranked by how much fixed text matched. Understands printf %d/%s, fmt/spdlog/Python {} and {name}, %(name)s, JS ${…}, and stream/concatenated literals. Use it to go from a log to the branch of code that emitted it, then read that function with brief/context."),
		mcp.WithString("line", mcp.Required(), mcp.Description("the log line as it appears in the log")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("limit", mcp.Description("max sites (default 10)"))))
	add(mcp.NewTool("reponite_impls",
		mcp.WithDescription("When: code calls a base class/interface and you need what actually runs (subclasses, plugins). What actually runs when code calls an interface? Given a C++ base class (optionally with a method: Base::method), lists every class deriving from it fleet-wide — including plugins and subclasses in OTHER repos that the call graph cannot reach, since they are loaded at runtime — with file:line, derivation depth, whether it is registered with PLUGINLIB_EXPORT_CLASS, and each class's own definition of the method (empty when inherited). Read with per-repo refs to answer for the exact versions a system runs. Then open the override with brief/context."),
		mcp.WithString("base", mcp.Required(), mcp.Description("base class, or Base::method / Base.method")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("limit", mcp.Description("max classes (default 50)"))))
	add(mcp.NewTool("reponite_routes",
		mcp.WithDescription("When: following an HTTP call from a client to the server view, or a view to its callers. HTTP edges the call graph CAN'T see: which frontend/client call reaches which backend route, across repos. Reads server routes (Django path/re_path/url composed through include() prefixes and DRF routers, FastAPI/Flask decorators with router prefixes, Express, Go net/http, gin/echo/chi) and client calls (fetch, axios or any client object's get/post/…, requests, httpx), resolving URLs built from literals, template strings, f-strings, concatenation and named URL constants, then pairs them by path and method. With `path`: only that endpoint's routes and callers. Use to go from a UI action to the view that serves it, or from a view to every UI that calls it. Medium confidence (base URLs, proxies and gateway rewrites are not resolved; stated in the result)."),
		mcp.WithString("path", mcp.Description("focus on routes/calls whose path contains this (default: the whole map)")),
		mcp.WithString("gateway", mcp.Description("reverse-proxy gateway config file(s), Caddy JSON, comma-separated — requests are followed through each mount to the repo behind it")),
		mcp.WithString("upstream", mcp.Description("which repo serves a gateway upstream, host:port=repo,... (default: matched by name)")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("limit", mcp.Description("max routes (default 50)"))))
	add(mcp.NewTool("reponite_topics",
		mcp.WithDescription("When: following ROS data between processes (who publishes/subscribes a topic). ROS communication graph — the runtime edges the call graph CAN'T see. A publisher and a subscriber live in different processes and are joined only by a topic name at runtime, so no source call graph links them; this does, fleet-wide, by scanning roscpp/rospy/rclcpp/rclpy pub/sub/service/action idioms and pairing producers with consumers by name. With no `topic`: the whole comms map (connected edges first). With `topic`: who publishes and who subscribes to that one name. Use to answer \"who reacts when I publish X?\" or \"where does this subscriber's data come from?\". Names are resolved to their runtime form: launch-file remaps, names read from parameters (launch/YAML values), names a library takes from its caller (argument and NodeHandle-namespace tracing), and rviz displays/tools; each endpoint states how its name was resolved and with what confidence."),
		mcp.WithString("topic", mcp.Description("focus on one topic/service/action name (default: whole comms graph)")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_links",
		mcp.WithDescription("When: something refers to a file or symbol through data, not code — which playbook uses this role, which launch file includes this one, which plan uses this behaviour, which template uses this Helm value, which config names this class. References written as data in any file kind: paths (relative, repo root, ROS $(find pkg)/package://, Ansible roles, unique suffix or file name), ids (a large number or UUID defined as \"id\" elsewhere), Helm/Go-template include of a define, .Values keys to values.yaml, protobuf rpc to its implementation, OpenAPI path to the route serving it, and configuration values naming a code symbol. Returns refers_to and referred_by for the target."),
		mcp.WithString("target", mcp.Required(), mcp.Description("a repo-relative path (suffix ok) or a symbol name")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,...")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_coverage",
		mcp.WithDescription("When: an answer came back empty and you need to know whether the files it would come from were read at all. Per repo and file kind: files the index stored, and every tracked file it skipped with the reason (binary, LFS pointer, lockfile, large data table, size limit, minified, ignore rule, tool directory)."),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,...")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_ui",
		mcp.WithDescription("Frontend understanding in one call. With no `target`: the app's PAGES — every client route (react-router <Route>/createBrowserRouter/useRoutes, vue-router and other {path, component} route tables incl. lazy imports and module-federation exposes, Next.js pages/ and app/, SvelteKit, Nuxt) mapped to the component it renders, with a drill-in handle. With a page route (\"/orders/:id\") or a component (name, handle or file path): its COMPONENT TREE (budget-bounded, breadth-first) and per component its props (TS types, PropTypes, defineProps, Svelte export let/$props), state (useState/useReducer/useContext, redux/zustand/pinia/vuex stores, Vue refs, Svelte runes), effects with deps, event handlers (onClick/@click/on:click → handler) and the API calls each reaches through the call graph, linked to the server route that serves it — \"button Save → handleSave → POST /api/orders → OrdersView.create\". With uses=true: where the component is rendered, passed or routed to. Use it to orient in a React/Vue/Svelte codebase or to trace a UI action to the backend; follow handles with brief/context. Medium confidence (source idioms + name-resolved calls; stated in the result)."),
		mcp.WithString("target", mcp.Description("page route or component (name, dir.Name handle, or file path); empty = list the pages")),
		mcp.WithString("uses", mcp.Description(`"true" to list where target is rendered/routed instead of its tree`)),
		mcp.WithString("budget", mcp.Description("token budget (default 3000); a cut answer says truncated + next")),
		mcp.WithString("format", mcp.Description(`"text" for a terse readable rendering (default JSON)`)),
		mcp.WithString("gateway", mcp.Description("reverse-proxy gateway config file(s), Caddy JSON, comma-separated — API calls are followed through each mount to the repo behind it")),
		mcp.WithString("upstream", mcp.Description("which repo serves a gateway upstream, host:port=repo,...")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_trace",
		mcp.WithDescription("\"What happens when …?\" in ONE call: the call tree below an entry (or, with direction=up, how execution gets there), fleet-wide, crossing the boundaries a call graph can't see — a client HTTP call into the server route handler (Django/DRF class views dispatched by method, with the hooks they override; FastAPI/Flask/Express/Go), a ROS publish into the subscribers' callbacks, a base/interface method into its implementations in any repo, an import into another indexed repo. Every node carries its side effects read from its body (HTTP out with the route it hits, DB/ORM reads/writes, publish/emit, file writes, subprocesses, log lines, raised errors), every edge its resolution_method + confidence, and the walk says where and why it stopped (depth, budget, cycle, repeat, ambiguous). entry: a symbol (any spelling, Class::method), \"POST /api/orders\", path/file.ts:42, a CLI command name, or a ROS topic (force with route:/topic:/cli:/symbol:). Use it before reading files to understand a feature end to end, then open nodes with brief/context by their handle."),
		mcp.WithString("entry", mcp.Required(), mcp.Description("symbol, \"VERB /path\", path:line, CLI command, or topic")),
		mcp.WithString("direction", mcp.Description("down (default): what it sets off; up: who/what reaches it")),
		mcp.WithString("depth", mcp.Description("levels to walk (default 5)")),
		mcp.WithString("budget", mcp.Description("token budget of the text rendering (default 2000); a cut is reported with a next hint")),
		mcp.WithString("format", mcp.Description(`"text" for a terse indented tree (cheapest), default JSON`)),
		mcp.WithString("repo", mcp.Description("the repo defining the entry symbol (default: every repo that has it)")),
		mcp.WithString("skip", mcp.Description("extra library/handle prefixes to treat as noise, comma-separated")),
		mcp.WithString("gateway", mcp.Description("reverse-proxy gateway config file(s), Caddy JSON, comma-separated")),
		mcp.WithString("upstream", mcp.Description("which repo serves a gateway upstream, host:port=repo,...")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("tests", mcp.Description(`"true" to walk into test code (default excluded)`))))
	add(mcp.NewTool("reponite_models",
		mcp.WithDescription("The data layer: which entities the code persists or exchanges and who touches them. With no `model`: an ER summary — every model with its table, field count and relations (Django, DRF serializers, SQLAlchemy/SQLModel, Pydantic/dataclass schemas, TypeORM/MikroORM/sequelize-typescript, Prisma, GORM, SQL CREATE/ALTER TABLE across migrations, protobuf messages as wire schemas), tables first. With `model` (class or table name): its fields (type, null/default/unique/index/pk), relations both ways, serializers, the API routes whose handler touches it, and the code that WRITES and READS it (ORM query/save/update/delete/bulk call sites, related managers, raw SQL naming its table fleet-wide), each with file:line and a handle for brief/context. Use before changing a schema, to find who writes a table, or to learn a backend's domain fast."),
		mcp.WithString("model", mcp.Description("a model class or table name (default: the ER summary); repo:Name to pick a repo")),
		mcp.WithString("kind", mcp.Description("keep one kind: table, view, abstract, schema, wire")),
		mcp.WithString("budget", mcp.Description("token budget (default 4000); a cut result says truncated + next")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering (default JSON)`)),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_config",
		mcp.WithDescription("The configuration surface: every environment variable, CLI flag, settings key, feature flag and ROS parameter the code reads — where, and with what default — joined to where the repo SETS it (.env files, docker-compose environment, Kubernetes/Helm env, Dockerfile ENV/ARG, CI workflow env, launch <env>/<param>/<rosparam>, settings modules, .properties, shell exports, flags passed in scripts). Flags keys read but never set and set but never read. With `key`: every read and set of that key. Use for \"why didn't my config change take effect?\", \"what does this service need to run?\", or before renaming a variable."),
		mcp.WithString("key", mcp.Description("one key (env name case-insensitive, --flag or flag_name, settings key, ROS param); default: the whole surface")),
		mcp.WithString("kind", mcp.Description("keep one kind: env, flag, setting, feature, rosparam")),
		mcp.WithString("budget", mcp.Description("token budget (default 4000); a cut result says truncated + next")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering (default JSON)`)),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD"))))
	add(mcp.NewTool("reponite_refs",
		mcp.WithDescription("When: you need which refs (tags/branches) of a repo are indexed. List indexed refs for the repo."),
		mcp.WithString("repo", mcp.Description("target repo (defaults to current)"))))
	add(mcp.NewTool("reponite_repos",
		mcp.WithDescription("When: you need what is indexed (repos, refs, sizes). Fleet orientation: every indexed repo with its module path and per-ref stats (symbols, call edges, files). Call this first to learn what's mounted and where a feature might live — search/grep/semsearch then default to fleet-wide.")))

	add(mcp.NewTool("reponite_map",
		mcp.WithDescription("When: FIRST call on an unfamiliar repo or fleet. Orientation map at a ref: README summary, languages with file/symbol counts, entry points (mains, CLI commands, HTTP servers with route counts, UI roots and page routes, ROS nodes/launch files, Dockerfile CMD, compose services, CI jobs, package scripts, Make targets), modules with the purpose their own docs state, hub symbols (most resolved call edges), declared external dependencies. No repo = every indexed repo plus the cross-repo links already known (HTTP, ROS topics, imports, declared deps). Token-budgeted; every item is a drill-in handle for reponite_explain."),
		mcp.WithString("repo", mcp.Description("map one repo (default: the fleet, or the only repo)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("budget", mcp.Description("token budget (default 3000)")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering instead of JSON`)),
		mcp.WithString("no_tests", mcp.Description(`"true" to drop test code from the answer`))))
	add(mcp.NewTool("reponite_explain",
		mcp.WithDescription("When: you hold a handle (symbol, file, file:line, directory, or \"METHOD /route\") and want to UNDERSTAND it, not edit it. A symbol: kind, signature, doc comment, location, body (trimmed to budget), direct callers/callees with resolution confidence, the HTTP routes / ROS topics / UI events that reach it, tests referencing it. A file: its symbols, imports, role, inbound/outbound file links. A directory: purpose, files, most connected symbols. A route: the server handler plus every client call reaching it. Ambiguous names return candidates, never a guess."),
		mcp.WithString("target", mcp.Required(), mcp.Description(`symbol (bare, Class.method, Class::method), path, path:line, directory, or "METHOD /route"`)),
		mcp.WithString("repo", mcp.Description("resolve only in this repo (default: current repo first, then the fleet)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("budget", mcp.Description("token budget (default 3000)")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering instead of JSON`)),
		mcp.WithString("no_tests", mcp.Description(`"true" to drop test code from the answer`))))
	add(mcp.NewTool("reponite_guide",
		mcp.WithDescription("When: unsure which reponite tool answers your question. Returns the decision table: map → explain/investigate → context/usages/routes/topics → brief/blast_radius/compat/rootcause for changes → grep only for text.")))
	// The same decision table as an MCP prompt and resource, for clients
	// that surface those rather than tools.
	s.AddPrompt(mcp.NewPrompt("reponite_guide", mcp.WithPromptDescription("Which reponite tool answers which question, in the order to use them.")),
		func(ctx context.Context, req mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return mcp.NewGetPromptResult("reponite decision table",
				[]mcp.PromptMessage{mcp.NewPromptMessage(mcp.RoleUser, mcp.NewTextContent(GuideText()))}), nil
		})
	s.AddResource(mcp.NewResource("reponite://guide", "reponite_guide",
		mcp.WithResourceDescription("Which reponite tool answers which question, in the order to use them."), mcp.WithMIMEType("text/plain")),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{mcp.TextResourceContents{URI: "reponite://guide", MIMEType: "text/plain", Text: GuideText()}}, nil
		})

	add(mcp.NewTool("reponite_why",
		mcp.WithDescription("Why is this code the way it is? The commits that changed a symbol's lines or signature (git log -L on its range at each indexed ref's commit, following moves between files), newest first — date, author, subject, body excerpt, PR/issue numbers, kind (introduced/moved/signature/body), and the earliest indexed ref (release) that contains each change — plus the symbol's compat history across indexed refs. Use before changing code whose intent is unclear, or to find the PR that introduced a behavior. Read-only on the git object store."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("symbol, Class.method or Class::method")),
		mcp.WithString("repo", mcp.Description("repo defining the symbol (default: current, else the one fleet repo that has it)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("budget", mcp.Description("token budget (default 1500); truncated results say how to get the rest")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering (default JSON)`))))
	add(mcp.NewTool("reponite_errors",
		mcp.WithDescription("Where does this error come from, and who handles it? Pass the message a user saw (placeholders filled: ids, names), an exception/error type, or an HTTP status (404). Returns the producing sites ranked like logsite — raise/throw, errors.New/fmt.Errorf/sentinels, new XError(…), panics, ROS_ERROR/logger.error, HTTP error responses (DRF/Django/FastAPI/Flask Response(status=4xx), res.status(4xx).json, http.Error, ResponseStatusException) — each with its enclosing function (handle) and one hop of callers, plus the handlers that catch it: except/catch naming the type or a base class, errors.Is/As, recover, DRF EXCEPTION_HANDLER, FastAPI/Flask exception handlers, Express error middleware, Nest @Catch, Spring @ExceptionHandler, frontend status checks and response interceptors. With no query: an error catalogue per repo (types, statuses, messages)."),
		mcp.WithString("query", mcp.Description("message as seen, a type name, or a status code; empty = catalogue")),
		mcp.WithString("repo", mcp.Description("scope to one repo (default: fleet-wide)")),
		mcp.WithString("refs", mcp.Description("per-repo refs repo=ref,... — read the exact combination of versions a deployed system runs (overrides ref for those repos)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("no_tests", mcp.Description(`"true" to drop test code`)),
		mcp.WithString("budget", mcp.Description("token budget (default 2000)")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering (default JSON)`))))
	add(mcp.NewTool("reponite_tests",
		mcp.WithDescription("Which tests exercise this symbol, and how do I run just those? Finds tests that call it directly or through up to `depth` callers, tests that send an HTTP request to the route serving it, and test files naming it (JS/TS it()/test() blocks, mocks); detects each file's framework (pytest, unittest, Django, go test, jest, vitest, mocha, gtest, rostest, cargo, junit) and returns the exact command selecting only those tests. Use after editing a symbol to run the relevant tests instead of the whole suite."),
		mcp.WithString("symbol", mcp.Required(), mcp.Description("symbol, Class.method or Class::method")),
		mcp.WithString("repo", mcp.Description("repo defining the symbol (default: current, else the one fleet repo that has it)")),
		mcp.WithString("ref", mcp.Description("default HEAD")),
		mcp.WithString("depth", mcp.Description("max call hops between a test and the symbol (default 3)")),
		mcp.WithString("budget", mcp.Description("token budget (default 1500)")),
		mcp.WithString("format", mcp.Description(`"text" for a terse rendering (default JSON)`))))

	return server.ServeStdio(s)
}
