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
	//  13: Go calls resolve by Go's scoping (F in the caller's package,
	//      pkg.F through the import, x.M by method name only) and a git
	//      revision is type-checked like a working tree; member calls only
	//      reach methods the caller's language can call; an unresolved call
	//      target that equals a symbol id is stored marked, never as that id.
	//  14: Python and JS/TS self.f() / this.f() resolve through the class's
	//      bases (followed through imports, Python's MRO) to the method that
	//      runs; TypeScript abstract classes are symbols. Rust calls resolve
	//      by shape (Type::f, module paths, self.f) and receiver type; Java
	//      receivers are typed from fields, parameters and locals, imports
	//      resolve to package directories, new Foo() is the class. JS/TS
	//      imports follow tsconfig/jsconfig paths and baseUrl, default
	//      imports and barrel re-exports to the defining module; f(x)(y)
	//      no longer calls the argument x.
	//  15: Git LFS pointers are skipped whatever their extension; obj.set() /
	//      qs.filter() are calls (only a bare builtin name is skipped);
	//      Python receivers typed as library or builtin classes call the
	//      library, pytest fixtures type a test's parameters, a repo root
	//      with its own __init__.py is still the import root, and a method
	//      calling its own name on another object delegates, not recurses.
	//      JS/TS named imports of instances and object literals, f().m()
	//      through return classes, export aliases and parameter defaults;
	//      Go init per file (init@file), git revisions type-checked with
	//      every tracked Go file, partly built packages resolved by scope;
	//      Python Cls.f() through the class's MRO.
	//  16: Python loop and comprehension variables typed by their
	//      container (list[X], a function returning one), pytest fixtures
	//      that receive or yield other fixtures, Cls.make().f() chains, and
	//      instances a function constructs are exactly their class; JS/TS
	//      `await import()` / require() bind a module; C++ Cls::f().m()
	//      receivers through f's return, a header overload calling the
	//      source's overload; an invoked lambda's body is not its callee;
	//      googletest cases are Suite.Case, friend functions are free, and a
	//      class defined again in another source file is Cls@file.
	//  17: C++ calls reach only what the file can name — its own file, its
	//      transitive includes (a header: what the files including it see),
	//      never another file's anonymous-namespace/static helper or source-
	//      file class, nor a method of an unrelated class for a bare f();
	//      ns::f() keeps to that namespace; argument counts rule out
	//      overloads; class-scope aliases, template-parameter bases through
	//      aliases, specializations and auto getters returning a field type
	//      receivers; overload parameter counts merge correctly. JS/TS: a
	//      bare f() is the module's own or a local/global (the function's
	//      own bindings, destructured props included), a name imported from
	//      a read module is never another module's, and a default export
	//      wrapped in a HOC (memo(X), connect(…)(X)) is X. Python: a called
	//      parameter is the argument; a fixture parameter called runs the
	//      fixture only when it returns a function it defines.
	IndexVer = 17

	// GoTarget documents the intended production Go toolchain. The build
	// sandbox uses 1.18 for stdlib-only verification; external-dependency
	// adapters are built with this or newer on a real machine (see ADR-018).
	GoTarget = "1.22"
)
