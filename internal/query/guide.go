// guide.go is the agent's decision table: which reponite tool answers which
// kind of question, in the order an AI should reach for them on a codebase it
// has never seen. Served by `reponite guide`, the MCP prompt and resource
// "reponite_guide", and kept general — no stack is assumed.
package query

// Guide is the decision table, as terse text an LLM reads once.
const Guide = `reponite — which tool to call (start at the top; each answer carries handles for the next call)

1. ORIENT       reponite_map                 first call on an unfamiliar repo/fleet: what it is, languages, entry
                                             points, modules + stated purpose, hub symbols, dependencies, and
                                             (no repo) every repo with the cross-repo links already known
2. UNDERSTAND   reponite_explain <handle>    one thing in depth: a symbol, file, file:line, directory, or
                                             "METHOD /route" — what it is, doc, body, callers/callees, routes/
                                             topics/UI events reaching it, tests
                reponite_investigate "<q>"   a feature by question when you have no handle yet
                reponite_trace <entry>       "what happens when …": the call tree below a symbol, "VERB /route",
                                             CLI command or topic (direction=up: how it is reached), crossing
                                             UI → API → server, topics and implementations, with side effects
                reponite_ui [page|component] frontend: the pages, a page's component tree, props/state/handlers
                                             and the API call (and server route) behind each handler
                reponite_models [model]      data: tables/entities, fields, relations, and the code that reads or
                                             writes each one
                reponite_config [key]        configuration: env vars, flags, settings — where each is read (with
                                             default) and where it is set; read-never-set and set-never-read
                reponite_errors ["<msg>"]    where an error message, type or HTTP status is produced and who catches it
                reponite_why <symbol>        the commits behind a symbol (and their PRs), oldest change first shipped
                reponite_tests <symbol>      which tests exercise it, and the command to run just those
3. NAVIGATE     reponite_context             direct callers/callees with confidence
                reponite_usages              every call site with its line (before changing a signature)
                reponite_routes / topics     runtime edges the call graph cannot see: HTTP client ↔ route,
                                             ROS publisher ↔ subscriber
                reponite_impls               which subclasses/plugins actually run behind a base class
                reponite_logsite "<line>"    which code printed a log line
                reponite_search / semsearch  a symbol by name / by words
4. CHANGE       reponite_brief               the bundle for EDITING one symbol (body, callees, callers, tests)
                reponite_blast_radius        everything that could break, before editing
                reponite_verify_edit         what a proposed file edit breaks, before saving
                reponite_compat / diff       did it change across refs (shape vs behavior)?
                reponite_rootcause[_trace]   which callee caused a behavior change (or a stack trace's)
                reponite_ximpact             who in OTHER repos calls an exported symbol
5. TEXT ONLY    reponite_grep                literal/regex text (strings, config keys, comments) — not for
                                             finding definitions or callers; the tools above are exact

Every tool takes ref (a tag/branch/SHA that is indexed) and refs=repo=ref,... to read a deployed
combination of versions; reponite_repos / reponite_refs say what is indexed. Prefer the drill-in handle
a result gives you (symbol id, path:line) over re-searching.`
