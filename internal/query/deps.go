// deps.go reads the external dependencies a repo DECLARES, from its stored
// manifests: go.mod, package.json, requirements*.txt, pyproject.toml,
// Pipfile, setup.cfg, ROS package.xml, CMakeLists.txt find_package, Cargo.toml
// and pom.xml. Declared, not resolved: lock files are not read, so this is
// what the project asks for, not the transitive closure. Lexical and pure
// (ADR-018); a manifest it cannot read contributes nothing rather than a guess.
package query

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// Dependency is one declared external dependency, aggregated over every
// manifest in the repo that declares it.
type Dependency struct {
	Name      string
	Ecosystem string // go | npm | pypi | ros | cmake | cargo | maven
	Version   string // the declared constraint when every manifest agrees; "" otherwise
	Dev       bool   // declared only as a dev/test/build-tool dependency
	Manifests int    // how many manifests declare it
	Source    string // the first manifest declaring it (path)
}

// declDep is one declaration inside one manifest.
type declDep struct {
	name, eco, version string
	dev                bool
}

var (
	reGoRequire    = regexp.MustCompile(`^\s*(?:require\s+)?([\w.~-]+\.[\w./~-]+)\s+(v[\w.+-]+)(\s*//\s*indirect)?`)
	reReqLine      = regexp.MustCompile(`^\s*([A-Za-z0-9][A-Za-z0-9._-]*)\s*(\[[^\]]*\])?\s*([<>=!~^][^;#]*)?`)
	reTomlSection  = regexp.MustCompile(`^\s*\[\[?([^\]]+)\]\]?\s*$`)
	reTomlKey      = regexp.MustCompile(`^\s*["']?([A-Za-z0-9_.-]+)["']?\s*=\s*(.*)$`)
	reTomlStr      = regexp.MustCompile(`["']([^"']+)["']`)
	reRosDep       = regexp.MustCompile(`<(depend|build_depend|exec_depend|run_depend|test_depend|build_export_depend|buildtool_depend|doc_depend)(?:\s[^>]*)?>\s*([\w-]+)\s*</`)
	rePkgXMLName   = regexp.MustCompile(`<name>\s*([\w-]+)\s*</name>`)
	reFindPackage  = regexp.MustCompile(`(?is)find_package\s*\(\s*(\w+)([^)]*)\)`)
	rePomDep       = regexp.MustCompile(`(?s)<dependency>\s*(.*?)</dependency>`)
	rePomField     = regexp.MustCompile(`<(groupId|artifactId|version|scope)>\s*([^<]+?)\s*</`)
	reCfgInstall   = regexp.MustCompile(`(?m)^install_requires\s*=\s*(.*)$`)
	cmakeKeywords  = map[string]bool{"REQUIRED": true, "COMPONENTS": true, "QUIET": true, "CONFIG": true, "MODULE": true, "OPTIONAL_COMPONENTS": true, "EXACT": true, "NO_MODULE": true}
	reCMakeVersion = regexp.MustCompile(`^\d+(\.\d+)*$`)
)

// manifestDeps parses one manifest's declared dependencies.
func manifestDeps(path, content string) []declDep {
	base := strings.ToLower(filepath.Base(path))
	switch {
	case base == "go.mod":
		return goModDeps(content)
	case base == "package.json":
		return packageJSONDeps(content)
	case strings.HasSuffix(base, ".txt") && base != "cmakelists.txt":
		return requirementsDeps(content, strings.Contains(base, "dev") || strings.Contains(base, "test"))
	case base == "pyproject.toml":
		return pyprojectDeps(content)
	case base == "pipfile":
		return tomlTableDeps(content, map[string]string{"packages": "", "dev-packages": "dev"}, "pypi")
	case base == "setup.cfg":
		return setupCfgDeps(content)
	case base == "cargo.toml":
		return tomlTableDeps(content, map[string]string{"dependencies": "", "dev-dependencies": "dev", "build-dependencies": "dev"}, "cargo")
	case base == "package.xml":
		return packageXMLDeps(content)
	case base == "cmakelists.txt":
		return cmakeDeps(content)
	case base == "pom.xml":
		return pomDeps(content)
	}
	return nil
}

func goModDeps(content string) []declDep {
	var out []declDep
	inReq := false
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "require ("):
			inReq = true
			continue
		case inReq && t == ")":
			inReq = false
			continue
		case !inReq && !strings.HasPrefix(t, "require "):
			continue
		}
		if m := reGoRequire.FindStringSubmatch(t); m != nil {
			out = append(out, declDep{name: m[1], eco: "go", version: m[2], dev: m[3] != ""})
		}
	}
	return out
}

func packageJSONDeps(content string) []declDep {
	var pj struct {
		Dependencies     map[string]string `json:"dependencies"`
		DevDependencies  map[string]string `json:"devDependencies"`
		PeerDependencies map[string]string `json:"peerDependencies"`
	}
	if json.Unmarshal([]byte(content), &pj) != nil {
		return nil
	}
	var out []declDep
	add := func(m map[string]string, dev bool) {
		for _, k := range sortedKeys(m) {
			out = append(out, declDep{name: k, eco: "npm", version: m[k], dev: dev})
		}
	}
	add(pj.Dependencies, false)
	add(pj.PeerDependencies, false)
	add(pj.DevDependencies, true)
	return out
}

func requirementsDeps(content string, dev bool) []declDep {
	var out []declDep
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if t == "" || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "-") || strings.Contains(t, "://") {
			continue
		}
		if m := reReqLine.FindStringSubmatch(t); m != nil {
			out = append(out, declDep{name: strings.ToLower(m[1]), eco: "pypi", version: strings.TrimSpace(m[3]), dev: dev})
		}
	}
	return out
}

func pyprojectDeps(content string) []declDep {
	var out []declDep
	section := ""
	inArray, arrayDev := false, false
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if inArray {
			for _, m := range reTomlStr.FindAllStringSubmatch(t, -1) {
				if r := reReqLine.FindStringSubmatch(m[1]); r != nil {
					out = append(out, declDep{name: strings.ToLower(r[1]), eco: "pypi", version: strings.TrimSpace(r[3]), dev: arrayDev})
				}
			}
			if strings.Contains(t, "]") {
				inArray = false
			}
			continue
		}
		if m := reTomlSection.FindStringSubmatch(t); m != nil {
			section = m[1]
			continue
		}
		km := reTomlKey.FindStringSubmatch(t)
		if km == nil {
			continue
		}
		key, val := km[1], strings.TrimSpace(km[2])
		switch {
		case section == "project" && key == "dependencies",
			strings.HasPrefix(section, "project.optional-dependencies"),
			section == "dependency-groups":
			arrayDev = section != "project"
			for _, m := range reTomlStr.FindAllStringSubmatch(val, -1) {
				if r := reReqLine.FindStringSubmatch(m[1]); r != nil {
					out = append(out, declDep{name: strings.ToLower(r[1]), eco: "pypi", version: strings.TrimSpace(r[3]), dev: arrayDev})
				}
			}
			if strings.HasPrefix(val, "[") && !strings.Contains(val, "]") {
				inArray = true
			}
		case section == "tool.poetry.dependencies" || section == "tool.poetry.dev-dependencies" ||
			strings.HasPrefix(section, "tool.poetry.group.") && strings.HasSuffix(section, ".dependencies"):
			if key == "python" {
				continue
			}
			v := ""
			if m := reTomlStr.FindStringSubmatch(val); m != nil && !strings.HasPrefix(val, "{") {
				v = m[1]
			} else if m := regexp.MustCompile(`version\s*=\s*["']([^"']+)`).FindStringSubmatch(val); m != nil {
				v = m[1]
			}
			out = append(out, declDep{name: strings.ToLower(key), eco: "pypi", version: v, dev: section != "tool.poetry.dependencies"})
		}
	}
	return out
}

// tomlTableDeps reads `name = ...` keys from the named TOML tables (Cargo.toml,
// Pipfile); kind "dev" marks the table as a dev dependency table.
func tomlTableDeps(content string, tables map[string]string, eco string) []declDep {
	var out []declDep
	section, inTable, dev := "", false, false
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if m := reTomlSection.FindStringSubmatch(t); m != nil {
			section = m[1]
			kind, ok := tables[section]
			if !ok {
				// target-specific tables: [target.'cfg(..)'.dependencies]
				for name, k := range tables {
					if strings.HasSuffix(section, "."+name) {
						kind, ok = k, true
					}
				}
			}
			inTable, dev = ok, kind == "dev"
			continue
		}
		if !inTable {
			continue
		}
		if km := reTomlKey.FindStringSubmatch(t); km != nil {
			v := ""
			if m := reTomlStr.FindStringSubmatch(km[2]); m != nil && !strings.HasPrefix(strings.TrimSpace(km[2]), "{") {
				v = m[1]
			} else if m := regexp.MustCompile(`version\s*=\s*["']([^"']+)`).FindStringSubmatch(km[2]); m != nil {
				v = m[1]
			}
			out = append(out, declDep{name: km[1], eco: eco, version: v, dev: dev})
		}
	}
	return out
}

func setupCfgDeps(content string) []declDep {
	var out []declDep
	lines := strings.Split(content, "\n")
	for i, ln := range lines {
		if !reCfgInstall.MatchString(ln) {
			continue
		}
		for k := i + 1; k < len(lines); k++ {
			if !strings.HasPrefix(lines[k], " ") && !strings.HasPrefix(lines[k], "\t") {
				break
			}
			if r := reReqLine.FindStringSubmatch(strings.TrimSpace(lines[k])); r != nil {
				out = append(out, declDep{name: strings.ToLower(r[1]), eco: "pypi", version: strings.TrimSpace(r[3])})
			}
		}
	}
	return out
}

func packageXMLDeps(content string) []declDep {
	var out []declDep
	for _, m := range reRosDep.FindAllStringSubmatch(content, -1) {
		dev := m[1] == "test_depend" || m[1] == "buildtool_depend" || m[1] == "doc_depend"
		out = append(out, declDep{name: m[2], eco: "ros", dev: dev})
	}
	return out
}

func cmakeDeps(content string) []declDep {
	var out []declDep
	for _, m := range reFindPackage.FindAllStringSubmatch(stripCMakeComments(content), -1) {
		name := m[1]
		var comps []string
		inComp := false
		for _, w := range strings.Fields(m[2]) {
			switch {
			case w == "COMPONENTS" || w == "OPTIONAL_COMPONENTS":
				inComp = true
			case cmakeKeywords[w] || reCMakeVersion.MatchString(w) || strings.HasPrefix(w, "$"):
			case inComp:
				comps = append(comps, w)
			}
		}
		if name == "catkin" || name == "ament_cmake" {
			// The ROS build system: its COMPONENTS are the package's ROS deps.
			for _, c := range comps {
				out = append(out, declDep{name: c, eco: "ros"})
			}
			continue
		}
		out = append(out, declDep{name: name, eco: "cmake"})
	}
	return out
}

func stripCMakeComments(s string) string {
	var b strings.Builder
	for _, ln := range strings.Split(s, "\n") {
		if i := strings.Index(ln, "#"); i >= 0 {
			ln = ln[:i]
		}
		b.WriteString(ln)
		b.WriteByte('\n')
	}
	return b.String()
}

func pomDeps(content string) []declDep {
	var out []declDep
	for _, m := range rePomDep.FindAllStringSubmatch(content, -1) {
		f := map[string]string{}
		for _, x := range rePomField.FindAllStringSubmatch(m[1], -1) {
			f[x[1]] = x[2]
		}
		if f["artifactId"] == "" {
			continue
		}
		v := f["version"]
		if strings.HasPrefix(v, "${") {
			v = ""
		}
		out = append(out, declDep{name: f["groupId"] + ":" + f["artifactId"], eco: "maven", version: v, dev: f["scope"] == "test"})
	}
	return out
}

// internalNames are the repo's own package names (package.xml <name>,
// package.json name, go.mod module, Cargo [package] name, pyproject name): a
// dependency on one of them is internal to the repo, not external.
func internalNames(files []File) map[string]bool {
	out := map[string]bool{}
	for _, f := range files {
		if ProjectFileKind(f.Path) != ProjManifest {
			continue
		}
		if n := manifestName(f.Path, f.Content); n != "" {
			out[n] = true
		}
	}
	return out
}

// manifestName is the package name a manifest declares, "" when it has none.
func manifestName(path, content string) string {
	switch strings.ToLower(filepath.Base(path)) {
	case "package.xml":
		if m := rePkgXMLName.FindStringSubmatch(content); m != nil {
			return m[1]
		}
	case "package.json":
		var pj struct {
			Name string `json:"name"`
		}
		if json.Unmarshal([]byte(content), &pj) == nil {
			return pj.Name
		}
	case "go.mod":
		for _, ln := range strings.Split(content, "\n") {
			if t := strings.TrimSpace(ln); strings.HasPrefix(t, "module ") {
				return strings.Trim(strings.TrimSpace(t[len("module "):]), `"`)
			}
		}
	case "cargo.toml", "pyproject.toml":
		return tomlValue(content, []string{"package", "project", "tool.poetry"}, "name")
	}
	return ""
}

// tomlValue reads key's string value in the first of sections that has it.
func tomlValue(content string, sections []string, key string) string {
	want := map[string]bool{}
	for _, s := range sections {
		want[s] = true
	}
	section := ""
	for _, ln := range strings.Split(content, "\n") {
		t := strings.TrimSpace(ln)
		if m := reTomlSection.FindStringSubmatch(t); m != nil {
			section = m[1]
			continue
		}
		if !want[section] {
			continue
		}
		if km := reTomlKey.FindStringSubmatch(t); km != nil && km[1] == key {
			if m := reTomlStr.FindStringSubmatch(km[2]); m != nil {
				return m[1]
			}
		}
	}
	return ""
}

// RepoDependencies aggregates every manifest's declared dependencies: one
// entry per (ecosystem, name), ranked by how many manifests declare it, with
// the repo's own packages removed. internal reports how many were dropped.
func RepoDependencies(files []File) (deps []Dependency, manifests []string, internal int) {
	own := internalNames(files)
	type key struct{ eco, name string }
	agg := map[key]*Dependency{}
	versions := map[key]map[string]bool{}
	nonDev := map[key]bool{}
	internalSeen := map[string]bool{}
	for _, f := range files {
		if ProjectFileKind(f.Path) != ProjManifest || IsTestPath(f.Path) {
			continue
		}
		ds := manifestDeps(f.Path, f.Content)
		if len(ds) == 0 {
			continue
		}
		manifests = append(manifests, f.Path)
		seen := map[key]bool{}
		for _, d := range ds {
			if own[d.name] {
				internalSeen[d.name] = true
				continue
			}
			k := key{d.eco, d.name}
			if seen[k] {
				continue
			}
			seen[k] = true
			a := agg[k]
			if a == nil {
				a = &Dependency{Name: d.name, Ecosystem: d.eco, Source: f.Path}
				agg[k] = a
				versions[k] = map[string]bool{}
			}
			a.Manifests++
			versions[k][d.version] = true
			if !d.dev {
				nonDev[k] = true
			}
		}
	}
	for k, a := range agg {
		a.Dev = !nonDev[k]
		if len(versions[k]) == 1 {
			for v := range versions[k] {
				a.Version = v
			}
		}
		deps = append(deps, *a)
	}
	sort.Slice(deps, func(i, j int) bool {
		a, b := deps[i], deps[j]
		if a.Dev != b.Dev {
			return !a.Dev
		}
		if a.Manifests != b.Manifests {
			return a.Manifests > b.Manifests
		}
		if a.Ecosystem != b.Ecosystem {
			return a.Ecosystem < b.Ecosystem
		}
		return a.Name < b.Name
	})
	sort.Strings(manifests)
	return deps, manifests, len(internalSeen)
}
