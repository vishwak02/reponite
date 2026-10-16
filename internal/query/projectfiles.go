// projectfiles.go classifies the non-source files that describe a project
// rather than implement it: READMEs, build and container files, dependency
// manifests and CI definitions. The indexer stores them (content only, no
// symbols) and `map`/`explain` read them back for entry points, module
// purposes and external dependencies. The rule lives here, in the pure query
// layer, so the indexer and every query agree on what a project file is.
package query

import (
	"path/filepath"
	"strings"
)

// Project-file kinds returned by ProjectFileKind.
const (
	ProjReadme   = "readme"
	ProjDocker   = "dockerfile"
	ProjCompose  = "compose"
	ProjMake     = "makefile"
	ProjManifest = "manifest"
	ProjCI       = "ci"
)

// MaxProjectFileBytes caps a stored project file: a generated README or a
// vendored manifest larger than this carries no orientation value.
const MaxProjectFileBytes = 256 << 10

// manifestBases are dependency/package manifests by exact base name.
var manifestBases = map[string]bool{
	"go.mod": true, "package.json": true, "pyproject.toml": true, "setup.cfg": true,
	"pipfile": true, "cargo.toml": true, "package.xml": true, "cmakelists.txt": true,
	"pom.xml": true, "build.gradle": true, "build.gradle.kts": true,
}

// ProjectFileKind reports which kind of project file a repo-relative path is,
// or "" when it is not one. Lock files are deliberately excluded: they list
// the transitive closure, not what the project declares.
func ProjectFileKind(path string) string {
	path = filepath.ToSlash(path)
	base := strings.ToLower(filepath.Base(path))
	dir := filepath.ToSlash(filepath.Dir(path))
	switch {
	case base == "readme" || strings.HasPrefix(base, "readme.") && isDocExt(filepath.Ext(base)):
		return ProjReadme
	case base == "dockerfile" || base == "containerfile" || strings.HasPrefix(base, "dockerfile.") || strings.HasSuffix(base, ".dockerfile"):
		return ProjDocker
	case (strings.HasPrefix(base, "docker-compose") || strings.HasPrefix(base, "compose")) && isYAMLExt(filepath.Ext(base)):
		return ProjCompose
	case base == "makefile" || base == "gnumakefile" || strings.HasSuffix(base, ".mk"):
		return ProjMake
	case manifestBases[base]:
		return ProjManifest
	case strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"),
		strings.HasSuffix(dir, "requirements") && strings.HasSuffix(base, ".txt"):
		return ProjManifest
	case (dir == ".github/workflows" || strings.HasSuffix(dir, "/.github/workflows")) && isYAMLExt(filepath.Ext(base)):
		return ProjCI
	case base == ".gitlab-ci.yml" || base == "jenkinsfile" || base == ".travis.yml" || base == "azure-pipelines.yml" || base == "bitbucket-pipelines.yml":
		return ProjCI
	}
	return ""
}

func isDocExt(ext string) bool {
	switch ext {
	case ".md", ".markdown", ".rst", ".txt", ".adoc":
		return true
	}
	return false
}

func isYAMLExt(ext string) bool { return ext == ".yml" || ext == ".yaml" }

// IsProjectDir reports whether a dot-directory still holds project files the
// indexer should read (CI workflows); every other dot-directory stays skipped.
func IsProjectDir(rel string) bool {
	rel = filepath.ToSlash(rel)
	return rel == ".github" || rel == ".github/workflows" ||
		strings.HasSuffix(rel, "/.github") || strings.HasSuffix(rel, "/.github/workflows")
}
