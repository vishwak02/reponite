package processing

import (
	"encoding/json"
	"path"
	"sort"
	"strings"
)

// tsPaths is one tsconfig.json / jsconfig.json's module resolution: its
// baseUrl and its path aliases ("@app/*": ["./src/*"]), with directories
// relative to the repo root. A project's alias imports (`@app/store`) are
// its own code; without them every such import looked like a package.
type tsPaths struct {
	dir     string // the config's directory: it applies to files under it
	baseURL string // "" when unset
	aliases []tsAlias
}

type tsAlias struct {
	pattern string   // "@app/*" or an exact "@app/types"
	targets []string // "src/*", relative to the repo root
}

// parseTSConfig reads a tsconfig/jsconfig (JSON with comments and trailing
// commas) at p. ok is false when it has neither baseUrl nor paths.
func parseTSConfig(p, src string) (tsPaths, bool) {
	var cfg struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if json.Unmarshal([]byte(jsoncToJSON(src)), &cfg) != nil {
		return tsPaths{}, false
	}
	co := cfg.CompilerOptions
	if co.BaseURL == "" && len(co.Paths) == 0 {
		return tsPaths{}, false
	}
	dir := path.Dir(p)
	if dir == "." {
		dir = ""
	}
	tp := tsPaths{dir: dir}
	// paths are relative to baseUrl, or to the config itself when unset
	base := dir
	if co.BaseURL != "" {
		base = path.Join(dir, co.BaseURL)
		tp.baseURL = base
	}
	for pat, targets := range co.Paths {
		a := tsAlias{pattern: pat}
		for _, t := range targets {
			a.targets = append(a.targets, path.Join(base, t))
		}
		tp.aliases = append(tp.aliases, a)
	}
	// the longest prefix wins, as in TypeScript
	sort.Slice(tp.aliases, func(i, j int) bool {
		pi, pj := strings.Index(tp.aliases[i].pattern, "*"), strings.Index(tp.aliases[j].pattern, "*")
		if pi < 0 {
			pi = len(tp.aliases[i].pattern) + 1 // exact patterns first
		}
		if pj < 0 {
			pj = len(tp.aliases[j].pattern) + 1
		}
		if pi != pj {
			return pi > pj
		}
		return tp.aliases[i].pattern < tp.aliases[j].pattern
	})
	return tp, true
}

// candidates are the repo paths a module specifier can name under this
// config: each matching alias's targets, then baseUrl + specifier.
func (tp tsPaths) candidates(module string) []string {
	var out []string
	for _, a := range tp.aliases {
		star := strings.Index(a.pattern, "*")
		var rest string
		switch {
		case star < 0:
			if module != a.pattern {
				continue
			}
		default:
			pre, suf := a.pattern[:star], a.pattern[star+1:]
			if !strings.HasPrefix(module, pre) || !strings.HasSuffix(module, suf) || len(module) < len(pre)+len(suf) {
				continue
			}
			rest = module[len(pre) : len(module)-len(suf)]
		}
		for _, t := range a.targets {
			out = append(out, strings.Replace(t, "*", rest, 1))
		}
		break // TypeScript uses the best-matching pattern only
	}
	if tp.baseURL != "" {
		out = append(out, path.Join(tp.baseURL, module))
	}
	return out
}

// jsoncToJSON drops // and /* */ comments (outside strings), then trailing
// commas, so a tsconfig parses as JSON.
func jsoncToJSON(s string) string {
	var b strings.Builder
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(s) && s[i+1] == '/':
			for i < len(s) && s[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i += 2
			for i+1 < len(s) && !(s[i] == '*' && s[i+1] == '/') {
				i++
			}
			i++
		default:
			b.WriteByte(c)
		}
	}
	s = b.String()
	b.Reset()
	inStr = false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			if c == '\\' && i+1 < len(s) {
				b.WriteByte(c)
				i++
				c = s[i]
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
		case c == ',':
			j := i + 1
			for j < len(s) && strings.IndexByte(" \t\r\n", s[j]) >= 0 {
				j++
			}
			if j < len(s) && (s[j] == '}' || s[j] == ']') {
				continue // a trailing comma
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}
