// Package version holds build-identity constants shared across reponite.
package version

// Version is the semantic version of the reponite binary. It is a var (not a
// const) so release/CLI builds can stamp the real tag via
//
//	-ldflags "-X github.com/vishwak02/reponite/internal/version.Version=<tag>"
//
// Unstamped builds report "0.0.0-dev".
var Version = "0.0.0-dev"

const (
	// NormVer is the canonicalization ruleset version baked into every hash
	// (architecture §5.3). Bumping it lets old/new hashes coexist; GC retires
	// orphaned old-version content once unreferenced. Starts at 1.
	NormVer = 1

	// IndexVer is the extraction + edge-resolution ruleset version, stamped on
	// every indexed ref. Behavior hashes fold in resolved edges, so two refs
	// indexed under different rulesets can differ with no code change at all:
	// compat/diff/rootcause warn when the refs they compare disagree. 0 = a ref
	// indexed before stamping. Bump whenever resolution or extraction changes.
	//   2: C/C++ call-site shapes (this->, obj., A::, std::) + out-of-class
	//      definitions qualified by their class.
	//   3: JS/TS/Python/Java call shapes + import bindings (an imported
	//      external name or a runtime global is never matched to repo code).
	//   4: C++ receiver typing (parameter/local/field declared types, base
	//      classes, virtual overrides), C++ .h headers parsed as C++, and
	//      functions returning a pointer/reference named after themselves.
	//   5: YAML parameter files (.yaml/.yml up to 256 KB) are stored, so
	//      `topics` can resolve a topic name read from a parameter through the
	//      <rosparam> file a launch file loads.
	//   6: JSX elements are component-render call edges (<CartItem/> calls
	//      CartItem); .vue/.svelte single-file components are indexed (script
	//      parsed in place, the component a symbol whose callees are the
	//      components its template renders and the handlers it binds); project
	//      files (READMEs, Dockerfiles, Makefiles, dependency manifests, CI
	//      workflows) and schema/configuration files (.sql/.proto/.prisma,
	//      .env*, .properties, .circleci, .gitlab-ci.yml) are stored as
	//      content-only files, for `map`, `explain`, `models` and `config`.
	//   7: minified/bundled assets (*.min.js, *-bundle.js, *.chunk.js, …) are
	//      excluded by default.
	//   8: JSON data files (up to 256 KB, lockfiles excepted) and rviz
	//      configs are stored as content-only files, so `errors` finds names
	//      defined in JSON catalogues and `topics` reads rviz displays/tools.
	//   9: content files get sections as symbols (Markdown/rST headings,
	//      YAML/JSON keys and named list entries, TOML/INI tables, Dockerfile
	//      stages, Makefile targets); Markdown, rST, TOML/INI, templates, Salt,
	//      Jsonnet and XML are stored; oversized, binary and minified source
	//      is skipped and reported; Robot Framework suites and keywords.
	//  10: Robot keywords with embedded arguments (`Open ${door} Door`)
	//      are linked from their calls; HTML and gettext (.po) files are
	//      stored as content.
	//  11: every tracked text file is stored: unknown extensions and
	//      extension-less files by content (JSON/XML/CSV recognized, sections
	//      as symbols; XML elements named by name/id, CSV header columns),
	//      dot-directories except tool/cache ones (.github/scripts, actions,
	//      linter configs); binaries, LFS pointers, lockfiles and large data
	//      tables are skipped with a reason. Both walkers share one classifier.
	//  12: languages without a bundled grammar (Kotlin, Swift, C#, Ruby, PHP,
	//      Lua, Scala, Dart, Groovy, Perl, R, Elixir, Julia, Zig) get heuristic
	//      symbols, spans and calls; Java, Kotlin, Scala and Groovy resolve
	//      calls into each other.
	IndexVer = 12

	// GoTarget documents the intended production Go toolchain. The build
	// sandbox uses 1.18 for stdlib-only verification; external-dependency
	// adapters are built with this or newer on a real machine (see ADR-018).
	GoTarget = "1.22"
)
