// errors_data.go finds error names and texts that are defined as DATA — an
// error catalogue in JSON or YAML that maps a code to its name and message —
// rather than written in a raise/throw. Many systems report errors this way
// (robot error tables, i18n message files, HTTP problem catalogues): the
// producing code holds only a numeric code or a key, so scanning source for the
// text a user saw finds nothing. Pure over Store.Files (ADR-018).
package query

import (
	"path"
	"sort"
	"strings"
)

// ErrorDataDef is one line of a data file that defines the queried error.
type ErrorDataDef struct {
	Repo, Path string
	Line       int
	Key        string   // the JSON/YAML key on that line, when there is one
	Text       string   // the line, trimmed
	Context    []string // neighbouring lines of the same entry (sibling keys such as code/id)
	LoadedBy   []string // repo:path:line of code that names this data file
}

const maxErrorDataDefs = 12

func isErrorDataPath(p string) bool {
	switch strings.ToLower(path.Ext(p)) {
	case ".json", ".yaml", ".yml":
		return true
	}
	return false
}

// errorDataDefs returns the data-file lines that contain q, each with the
// sibling lines of its entry and the code files that name its file.
func errorDataDefs(s Store, repo, ref, q string) []ErrorDataDef {
	q = strings.TrimSpace(q)
	if len(q) < 3 {
		return nil
	}
	var out []ErrorDataDef
	type src struct{ repo, path string }
	var code []struct {
		repo string
		f    File
	}
	hitFiles := map[src]bool{}
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			if !isErrorDataPath(f.Path) {
				if errLang(f.Path) != "" {
					code = append(code, struct {
						repo string
						f    File
					}{rp, f})
				}
				continue
			}
			if !strings.Contains(f.Content, q) {
				continue
			}
			lines := strings.Split(f.Content, "\n")
			for i, ln := range lines {
				if !strings.Contains(ln, q) {
					continue
				}
				d := ErrorDataDef{Repo: rp, Path: f.Path, Line: i + 1, Text: clipN(strings.TrimSpace(ln), 240)}
				if k := dataKey(ln); k != "" {
					d.Key = k
				}
				for j := i - 4; j <= i+4; j++ {
					if j < 0 || j >= len(lines) || j == i {
						continue
					}
					t := strings.TrimSpace(lines[j])
					if t == "" || t == "{" || t == "}" || t == "}," || t == "[" || t == "]" {
						continue
					}
					d.Context = append(d.Context, clipN(t, 160))
				}
				out = append(out, d)
				hitFiles[src{rp, f.Path}] = true
				if len(out) >= maxErrorDataDefs {
					break
				}
			}
			if len(out) >= maxErrorDataDefs {
				break
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	// Who loads each data file: code naming its base name.
	loaders := map[src][]string{}
	for sf := range hitFiles {
		base := path.Base(sf.path)
		for _, c := range code {
			if !strings.Contains(c.f.Content, base) {
				continue
			}
			for i, ln := range strings.Split(c.f.Content, "\n") {
				if strings.Contains(ln, base) {
					loaders[sf] = append(loaders[sf], c.repo+":"+c.f.Path+":"+itoa(i+1))
					break
				}
			}
			if len(loaders[sf]) >= 5 {
				break
			}
		}
		sort.Strings(loaders[sf])
	}
	for i := range out {
		out[i].LoadedBy = loaders[src{out[i].Repo, out[i].Path}]
	}
	return out
}

// dataKey returns the key of a `"key": value` (JSON) or `key: value` (YAML) line.
func dataKey(ln string) string {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(ln), "- "))
	i := strings.Index(t, ":")
	if i <= 0 {
		return ""
	}
	k := strings.Trim(strings.TrimSpace(t[:i]), `"'`)
	if strings.ContainsAny(k, " {}[]") {
		return ""
	}
	return k
}
