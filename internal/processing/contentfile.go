// contentfile.go stores text files that carry wiring or catalogues but are
// neither source nor launch/param/schema files: JSON data (error catalogues,
// message tables, fixture maps, i18n strings) and rviz display configs. They
// are content-only — no symbols — and read back at query time: `topics` reads
// rviz configs as subscribers/publishers, `errors` finds an error name or text
// defined in a JSON catalogue the code looks up at runtime, and grep/search
// see them like any other file. Pure and stdlib-only (ADR-018).
package processing

import (
	"path"
	"strings"
)

// maxContentFileBytes skips JSON too large to be hand-maintained data (a
// lockfile, a generated dump, a recorded fixture).
const maxContentFileBytes = 256 << 10

// contentLang names the stored language of a content-only file, "" when the
// path is not one.
func contentLang(p string) string {
	base := strings.ToLower(path.Base(p))
	switch {
	case strings.HasSuffix(base, ".rviz"):
		return "rviz"
	case strings.HasSuffix(base, ".json"):
		// Lockfiles and generated maps are machine output, never a catalogue.
		if base == "package-lock.json" || base == "composer.lock" || strings.HasSuffix(base, ".min.json") ||
			strings.HasSuffix(base, ".map.json") || base == "tsconfig.tsbuildinfo" {
			return ""
		}
		return "json"
	}
	return ""
}

// IsContentFile reports whether path is stored as a content-only file.
func IsContentFile(p string) bool { return contentLang(p) != "" }

// contentFile stores a content-only file; ok=false when it is too large.
func contentFile(p, content string) (ParsedFile, bool) {
	if len(content) > maxContentFileBytes {
		return ParsedFile{}, false
	}
	return ParsedFile{Path: p, Content: content, Lang: contentLang(p), IsTest: IsTestPath(p)}, true
}
