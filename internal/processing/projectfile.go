// projectfile.go stores a repo's project files — READMEs, Dockerfiles,
// Makefiles, dependency manifests, CI definitions — as content-only files (no
// symbols, no spans), so `map` and `explain` can read entry points, module
// purposes and declared dependencies at any indexed ref. The classification is
// query.ProjectFileKind, shared with the query layer.
package processing

import "github.com/vishwak02/reponite/internal/query"

// IsProjectFile reports whether path is a project file the indexer stores.
func IsProjectFile(path string) bool { return query.ProjectFileKind(path) != "" }

// projectFile builds the stored record for a project file; ok=false when the
// file is too large to be worth storing.
func projectFile(path, content string) (ParsedFile, bool) {
	if len(content) > query.MaxProjectFileBytes {
		return ParsedFile{}, false
	}
	return ParsedFile{Path: path, Content: content, Lang: "project", IsTest: IsTestPath(path)}, true
}
