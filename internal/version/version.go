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

	// GoTarget documents the intended production Go toolchain. The build
	// sandbox uses 1.18 for stdlib-only verification; external-dependency
	// adapters are built with this or newer on a real machine (see ADR-018).
	GoTarget = "1.22"
)
