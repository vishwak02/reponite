// module.go derives a repo's module/package path — its identity across the repo
// boundary (§8B.2) — from the manifest file a repo's ecosystem uses: go.mod
// (Go), package.json (npm), pyproject.toml (Python), or pom.xml (Maven). A
// symbol's cross-repo identity is (module_path, name), so this path is what a
// caller's import binding is matched against in ximpact (ADR-016). Pure and
// stdlib-only (ADR-018): the tagged index layer reads the bytes off disk / the
// git tree and hands them here.
package processing

import (
	"encoding/json"
	"encoding/xml"
	"path/filepath"
	"strings"
)

// manifestName is a manifest filename in ecosystem-priority order; the first
// one present (root-most copy) wins. A repo is almost always one ecosystem.
var manifestNames = []string{"go.mod", "package.json", "pyproject.toml", "pom.xml", "Cargo.toml"}

// DetectModulePath returns the repo's module path given its candidate manifest
// files keyed by repo-relative path. When several copies of a manifest exist
// (a monorepo with nested modules), the root-most (fewest path segments) wins,
// so the path names the repo as a whole. Returns ("", false) if none parse.
func DetectModulePath(files map[string][]byte) (string, bool) {
	for _, base := range manifestNames {
		if content, ok := rootMost(files, base); ok {
			if mod, ok := parseModule(base, content); ok {
				return mod, true
			}
		}
	}
	return "", false
}

// rootMost returns the content of the shallowest file whose base name is base.
func rootMost(files map[string][]byte, base string) ([]byte, bool) {
	var bestContent []byte
	bestDepth := -1
	for path, content := range files {
		if filepath.Base(path) != base {
			continue
		}
		depth := strings.Count(filepath.ToSlash(path), "/")
		if bestDepth == -1 || depth < bestDepth {
			bestContent, bestDepth = content, depth
		}
	}
	return bestContent, bestDepth != -1
}

func parseModule(base string, content []byte) (string, bool) {
	switch base {
	case "go.mod":
		return goModPath(content)
	case "package.json":
		return packageJSONName(content)
	case "pyproject.toml":
		return pyprojectName(content)
	case "pom.xml":
		return pomCoordinates(content)
	case "Cargo.toml":
		return cargoName(content)
	}
	return "", false
}

// cargoName reads the crate name from a Cargo.toml [package] table.
func cargoName(content []byte) (string, bool) {
	section := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section != "package" {
			continue
		}
		if strings.HasPrefix(line, "name") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "name"))
			if strings.HasPrefix(rest, "=") {
				name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(rest, "=")), "\"'")
				if name != "" {
					return name, true
				}
			}
		}
	}
	return "", false
}

// goModPath reads the `module <path>` directive (the first line that has it).
func goModPath(content []byte) (string, bool) {
	for _, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "module ") {
			mod := strings.TrimSpace(strings.TrimPrefix(line, "module "))
			mod = strings.Trim(mod, "\"") // module "path" is legal in go.mod
			if mod != "" {
				return mod, true
			}
		}
	}
	return "", false
}

// packageJSONName reads the "name" field of a package.json.
func packageJSONName(content []byte) (string, bool) {
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(content, &pkg); err != nil || pkg.Name == "" {
		return "", false
	}
	return pkg.Name, true
}

// pyprojectName reads the project name from pyproject.toml, accepting both the
// PEP 621 [project] table and Poetry's [tool.poetry]. No stdlib TOML parser
// exists, so this scans for the first `name = "..."` under either table.
func pyprojectName(content []byte) (string, bool) {
	section := ""
	for _, raw := range strings.Split(string(content), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section != "project" && section != "tool.poetry" {
			continue
		}
		if strings.HasPrefix(line, "name") {
			rest := strings.TrimSpace(strings.TrimPrefix(line, "name"))
			if strings.HasPrefix(rest, "=") {
				name := strings.Trim(strings.TrimSpace(strings.TrimPrefix(rest, "=")), "\"'")
				if name != "" {
					return name, true
				}
			}
		}
	}
	return "", false
}

// pomCoordinates reads a Maven pom.xml's groupId:artifactId (the project's, not
// the parent's). tree-sitter isn't involved; encoding/xml handles it.
func pomCoordinates(content []byte) (string, bool) {
	var project struct {
		GroupID    string `xml:"groupId"`
		ArtifactID string `xml:"artifactId"`
	}
	if err := xml.Unmarshal(content, &project); err != nil {
		return "", false
	}
	switch {
	case project.GroupID != "" && project.ArtifactID != "":
		return project.GroupID + ":" + project.ArtifactID, true
	case project.ArtifactID != "":
		return project.ArtifactID, true
	case project.GroupID != "":
		return project.GroupID, true
	}
	return "", false
}

// IsManifestFile reports whether path's base name is a module manifest
// DetectModulePath understands, so the index layer can collect it while walking.
func IsManifestFile(path string) bool {
	base := filepath.Base(path)
	for _, m := range manifestNames {
		if base == m {
			return true
		}
	}
	return false
}
