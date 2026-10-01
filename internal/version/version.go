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
	IndexVer = 4

	// GoTarget documents the intended production Go toolchain. The build
	// sandbox uses 1.18 for stdlib-only verification; external-dependency
	// adapters are built with this or newer on a real machine (see ADR-018).
	GoTarget = "1.22"
)
