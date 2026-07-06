// exported.go decides whether a symbol name is part of a language's *public*
// API — the notion ci-check needs to gate only on breaking changes to symbols
// other code can depend on. Visibility is expressed differently per language:
// Go encodes it in the name's case, Python/JS by a leading-underscore
// convention, while Java/Rust/C/C++ use keywords (public/pub/static) that the
// name alone doesn't reveal. Where a naming convention exists we honor it;
// otherwise we err toward "exported" (a PR gate should surface a possible break,
// not hide it), treating a leading underscore as the near-universal "private".
package query

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// IsExportedName reports whether base (a bare symbol name, no package/receiver
// qualifier) is public in the given language. An empty or unknown language is
// treated with the conservative default (exported unless underscore-prefixed).
func IsExportedName(lang, base string) bool {
	if base == "" {
		return false
	}
	switch lang {
	case "go":
		// Go: exported iff the first rune is an uppercase letter.
		r, _ := utf8.DecodeRuneInString(base)
		return unicode.IsUpper(r)
	case "python", "javascript", "typescript":
		// Convention: a leading underscore marks a private/internal name.
		return !strings.HasPrefix(base, "_")
	default:
		// java, rust, c, cpp, ros, unknown: visibility is a keyword not captured
		// in the name, so err toward exported (fail closed for a gate) except for
		// the leading-underscore private convention.
		return !strings.HasPrefix(base, "_")
	}
}
