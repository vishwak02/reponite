// datafile.go routes the data-layer and configuration files into the index:
// schema files (.sql, .proto, .prisma) and the files that SET configuration
// (.env*, Dockerfile, Java .properties, CI workflow YAML under a dot-directory).
// None of them is tree-sitter parsed: they are stored for content search and
// read back at query time by `models` and `config` (the same query-time-scan
// model `topics` uses for launch files). A schema file gets one span per
// table/message/model so a grep hit reports which definition it landed in.
// Pure and stdlib-only (ADR-018).
package processing

import (
	"path"
	"regexp"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// maxDataFileBytes skips data files too large to be hand-written schema or
// configuration (a database dump, a generated descriptor set).
const maxDataFileBytes = 256 << 10

// dataLang names the stored language of a data file by its path, "" when the
// path is not one.
func dataLang(p string) string {
	base := path.Base(p)
	lower := strings.ToLower(base)
	switch strings.ToLower(path.Ext(base)) {
	case ".sql":
		return "sql"
	case ".proto":
		return "proto"
	case ".prisma":
		return "prisma"
	case ".properties":
		return "properties"
	case ".dockerfile":
		return "dockerfile"
	case ".env":
		return "dotenv"
	}
	switch {
	case lower == "dockerfile" || strings.HasPrefix(lower, "dockerfile.") || strings.HasPrefix(lower, "containerfile"):
		return "dockerfile"
	case lower == ".env" || strings.HasPrefix(lower, ".env.") || strings.HasPrefix(lower, ".env-") || strings.HasPrefix(lower, ".env_"):
		return "dotenv"
	}
	return ""
}

// IsDataFile reports whether path is a schema or configuration file stored
// for `models`/`config`.
func IsDataFile(p string) bool { return dataLang(p) != "" }

// IsDataDotPath reports whether a path under a dot-file or dot-directory is
// still indexed: .env files and CI workflow definitions are where a repo SETS
// its configuration, and the dot-path rule would otherwise hide all of them.
func IsDataDotPath(p string) bool {
	p = strings.TrimPrefix(p, "./")
	base := path.Base(p)
	if dataLang(p) == "dotenv" {
		for _, seg := range strings.Split(path.Dir(p), "/") {
			if seg != "." && strings.HasPrefix(seg, ".") {
				return false // inside a dot-directory (.git, .venv, …)
			}
		}
		return true // a .env file in a regular directory
	}
	ext := strings.ToLower(path.Ext(base))
	yaml := ext == ".yml" || ext == ".yaml"
	switch {
	case yaml && strings.HasPrefix(p, ".github/workflows/") && strings.Count(p, "/") == 2:
		return true
	case yaml && strings.HasPrefix(p, ".circleci/") && strings.Count(p, "/") == 1:
		return true
	case p == ".gitlab-ci.yml" || p == ".gitlab-ci.yaml":
		return true
	}
	return false
}

// IsDataDotDir reports whether a dot-directory must still be walked because
// it holds indexed data files (CI workflows).
func IsDataDotDir(rel string) bool {
	switch strings.TrimPrefix(rel, "./") {
	case ".github", ".github/workflows", ".circleci":
		return true
	}
	return false
}

// underDotDir reports whether a path sits below a dot-directory.
func underDotDir(p string) bool {
	segs := strings.Split(p, "/")
	for _, s := range segs[:len(segs)-1] {
		if strings.HasPrefix(s, ".") && s != "." {
			return true
		}
	}
	return false
}

var (
	sqlCreateSpan    = regexp.MustCompile(`(?i)\bcreate\s+(?:or\s+replace\s+)?(?:(?:global\s+|local\s+)?(?:temporary|temp|unlogged)\s+)?(table|view|materialized\s+view)\s+(?:if\s+not\s+exists\s+)?([\w."` + "`" + `\[\]]+)`)
	protoMessageSpan = regexp.MustCompile(`^\s*(message|enum|service)\s+(\w+)\s*\{`)
	prismaBlockSpan  = regexp.MustCompile(`^\s*(model|enum|view|type)\s+(\w+)\s*\{`)
)

// dataFile stores a schema/config file: content plus a span per definition.
// ok=false when the file is too large.
func dataFile(p, content string) (ParsedFile, bool) {
	if len(content) > maxDataFileBytes {
		return ParsedFile{}, false
	}
	lang := dataLang(p)
	pf := ParsedFile{Path: p, Content: content, Lang: lang, IsTest: IsTestPath(p)}
	switch lang {
	case "dockerfile":
		pf.Symbols, pf.Spans = sectionSymbols(p, content)
	case "sql":
		for _, m := range sqlCreateSpan.FindAllStringSubmatchIndex(content, -1) {
			name := strings.Trim(content[m[4]:m[5]], "\"`[]")
			if i := strings.LastIndex(name, "."); i >= 0 {
				name = strings.Trim(name[i+1:], "\"`[]")
			}
			start := 1 + strings.Count(content[:m[0]], "\n")
			end := start
			if j := strings.Index(content[m[1]:], ";"); j >= 0 {
				end = start + strings.Count(content[m[0]:m[1]+j], "\n")
			}
			pf.Spans = append(pf.Spans, query.SymbolSpan{Name: name, StartLine: start, EndLine: end})
		}
	case "proto", "prisma":
		re := protoMessageSpan
		if lang == "prisma" {
			re = prismaBlockSpan
		}
		lines := strings.Split(content, "\n")
		for i, ln := range lines {
			m := re.FindStringSubmatch(ln)
			if m == nil {
				continue
			}
			depth, end := 0, i
			for j := i; j < len(lines); j++ {
				depth += strings.Count(lines[j], "{") - strings.Count(lines[j], "}")
				if depth <= 0 {
					end = j
					break
				}
				end = j
			}
			pf.Spans = append(pf.Spans, query.SymbolSpan{Name: m[2], StartLine: i + 1, EndLine: end + 1})
		}
	}
	return pf, true
}
