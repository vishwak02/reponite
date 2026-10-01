// logsite.go answers "which code printed this?" for a line copied out of a
// real log. A log line is a format string with its placeholders filled in
// ("Creating 5 new tasks for agent amr07"), so plain grep of the whole line
// finds nothing and grep of a guessed fragment finds too much. LogSites reads
// every string literal on every code line at the ref, turns its placeholders
// into wildcards (printf %d/%s/%.2f/%v, fmt/spdlog/Python {} and {name},
// Python %(name)s, JS ${…}), joins the literals of one statement (ROS_*_STREAM
// "a" << x << "b", "a" + x + "b") and keeps the sites whose pattern matches
// the line, ranked by how much fixed text matched. Pure over Store.Files, like
// grep and the ROS comms graph (ADR-018).
package query

import (
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// LogSite is one code location whose format string produces the log line.
type LogSite struct {
	Repo, Path string
	Line       int
	In         string // enclosing symbol
	Call       string // the function/macro the literal is passed to (ROS_WARN, logger.info, …)
	Format     string // the literal(s) as written
	Text       string // the source line, trimmed
	Fixed      int    // characters of fixed (non-placeholder) text that matched
	Logging    bool   // Call looks like a logging/raising call
	// InPackage: the line names its ROS logger (`[ros.<package>.<name>]`) and
	// this site lives in that package — the emitter is compiled there.
	InPackage bool
}

// LogSitesResult is the ranked emitters of a log line.
type LogSitesResult struct {
	Line  string
	Sites []LogSite
	Note  string
	Meta  Meta
}

var (
	printfSpec = regexp.MustCompile(`%[-+ #0']*(\*|\d+)?(\.(\*|\d+))?(hh|h|ll|l|L|q|j|z|t|I64|I32)?[diouxXeEfFgGaAcspnvqTw]`)
	pyNamed    = regexp.MustCompile(`%\([A-Za-z_]\w*\)[-+ #0]*\d*(\.\d+)?[diouxXeEfFgGcrsa]`)
	braceSpec  = regexp.MustCompile(`\{[^{}]*\}`)
	jsTmpl     = regexp.MustCompile(`\$\{[^{}]*\}`)
	rosLogger  = regexp.MustCompile(`\[ros\.([A-Za-z_]\w*)(?:\.[\w.]+)?\]`)
	callName   = regexp.MustCompile(`([A-Za-z_][\w]*(?:(?:::|\.|->)[A-Za-z_][\w]*)*)\s*$`)
	logCall    = regexp.MustCompile(`(?i)(log|ros_|rclcpp_|print|warn|error|info|debug|fatal|critical|trace|console|throw|raise|exception|panic|abort|assert|notice)`)
	codeExts   = map[string]string{".c": "c", ".h": "c", ".cc": "c", ".cpp": "c", ".cxx": "c", ".hpp": "c", ".hh": "c", ".hxx": "c",
		".go": "go", ".rs": "c", ".java": "c", ".py": "py", ".js": "js", ".jsx": "js", ".ts": "js", ".tsx": "js", ".mjs": "js", ".sh": "sh"}
)

// LogSites finds the code that prints logLine across repo (FleetRepo "*" =
// the fleet) at ref, best match first; limit 0 = 10.
func LogSites(s Store, repo, ref, logLine string, limit int) LogSitesResult {
	if limit <= 0 {
		limit = 10
	}
	line := strings.TrimSpace(logLine)
	res := LogSitesResult{Line: line, Meta: Meta{Repo: repo, Ref: ref}}
	if len(line) < 4 {
		res.Note = "log line too short to match"
		return res
	}
	// roscpp names a line's logger after the package the emitting source is
	// compiled in (ros.<package>[.<name>]), which outranks any text match.
	pkg := ""
	if m := rosLogger.FindStringSubmatch(line); m != nil {
		pkg = m[1]
	}
	var all []LogSite
	for _, rp := range reposFor(s, repo) {
		for _, f := range s.Files(rp, ref) {
			lang := codeExts[strings.ToLower(filepath.Ext(f.Path))]
			if lang == "" {
				continue
			}
			sites := scanLogSites(rp, f, lang, line)
			if pkg != "" && inPackage(f.Path, pkg) {
				for i := range sites {
					sites[i].InPackage = true
				}
			}
			all = append(all, sites...)
		}
	}
	// A line assembled from pieces (a stream operator<< printing a struct)
	// matches both the log call and the helpers' literals; the log call is the
	// answer, so a logging call's fixed text counts double.
	score := func(x LogSite) int {
		if x.Logging {
			return 2 * x.Fixed
		}
		return x.Fixed
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.InPackage != b.InPackage {
			return a.InPackage
		}
		if score(a) != score(b) {
			return score(a) > score(b)
		}
		if a.Fixed != b.Fixed {
			return a.Fixed > b.Fixed
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Line < b.Line
	})
	total := len(all)
	if len(all) > limit {
		all = all[:limit]
	}
	res.Sites = all
	switch {
	case total == 0:
		res.Note = "no string literal at this ref produces this line — it may come from another repo or version, a third-party library, or be built dynamically"
	default:
		res.Note = "ranked by the line's ROS logger package, then fixed text matched (a logging call's counts double); a site whose fixed text is much shorter than the line matched only a fragment — the rest may be printed by a helper (operator<<, toString) listed below it"
		if total > limit {
			res.Note += " — more sites matched than shown"
		}
	}
	return res
}

func scanLogSites(repo string, f File, lang, logLine string) []LogSite {
	var out []LogSite
	lines := strings.Split(f.Content, "\n")
	for i := 0; i < len(lines); i++ {
		code := lines[i]
		lits, cols := stringLiterals(code, lang)
		// A statement continued on the next line (stream or concatenation over
		// two lines) contributes its literals too.
		if len(lits) > 0 && i+1 < len(lines) && continuesStatement(code, lang) {
			nl, _ := stringLiterals(lines[i+1], lang)
			lits = append(lits, nl...)
		}
		if len(lits) == 0 {
			continue
		}
		best, bestFixed, bestFmt := false, 0, ""
		// Every literal alone, and all of them in order: a named logger
		// (ROS_INFO_NAMED("x", "fmt")) or a prefix literal must not block a match.
		cands := make([][]string, 0, len(lits)+1)
		if len(lits) > 1 {
			cands = append(cands, lits)
		}
		for _, l := range lits {
			cands = append(cands, []string{l})
		}
		for _, c := range cands {
			re, fixed, longest := formatPattern(c, lang)
			if re == nil || fixed < 6 || !strings.Contains(logLine, longest) {
				continue
			}
			if re.MatchString(logLine) && fixed > bestFixed {
				best, bestFixed, bestFmt = true, fixed, strings.Join(c, " … ")
			}
		}
		if !best {
			continue
		}
		call := ""
		if len(cols) > 0 {
			call = enclosingCall(code[:cols[0]])
		}
		out = append(out, LogSite{
			Repo: repo, Path: f.Path, Line: i + 1, In: enclosing(f.Symbols, i+1), Call: call,
			Format: bestFmt, Text: strings.TrimSpace(code), Fixed: bestFixed, Logging: call != "" && logCall.MatchString(call),
		})
	}
	return out
}

// inPackage: a directory of path is named pkg (ROS package directory).
func inPackage(path, pkg string) bool {
	for _, seg := range strings.Split(filepath.ToSlash(filepath.Dir(path)), "/") {
		if seg == pkg {
			return true
		}
	}
	return false
}

func continuesStatement(code, lang string) bool {
	t := strings.TrimSpace(code)
	switch lang {
	case "py":
		return strings.HasSuffix(t, "\\") || strings.HasSuffix(t, "+") || strings.Count(t, "(") > strings.Count(t, ")")
	case "sh":
		return strings.HasSuffix(t, "\\")
	}
	return !strings.HasSuffix(t, ";") && !strings.HasSuffix(t, "{") && !strings.HasSuffix(t, "}") && t != ""
}

// formatPattern compiles literals (one statement's, in order) into a regex
// that finds them in a log line with any placeholder filled; it returns the
// fixed character count and the longest fixed chunk (a cheap prefilter).
func formatPattern(lits []string, lang string) (*regexp.Regexp, int, string) {
	var b strings.Builder
	fixed, longest := 0, ""
	for i, l := range lits {
		if i > 0 {
			b.WriteString(`.*?`)
		}
		chunks := splitPlaceholders(l, lang)
		for j, ch := range chunks {
			if j > 0 {
				b.WriteString(`.*?`)
			}
			ch = strings.TrimRight(ch, "\n")
			b.WriteString(regexp.QuoteMeta(ch))
			fixed += len(strings.TrimSpace(ch))
			if len(ch) > len(longest) {
				longest = ch
			}
		}
	}
	longest = strings.TrimSpace(longest)
	if longest == "" {
		return nil, 0, ""
	}
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, 0, ""
	}
	return re, fixed, longest
}

// splitPlaceholders cuts a format literal at its placeholders.
func splitPlaceholders(l, lang string) []string {
	s := strings.ReplaceAll(l, "%%", "\x00")
	for _, re := range []*regexp.Regexp{pyNamed, printfSpec, jsTmpl, braceSpec} {
		if re == braceSpec && strings.Contains(s, "{{") {
			s = strings.NewReplacer("{{", "\x01", "}}", "\x02").Replace(s)
		}
		s = re.ReplaceAllString(s, "\x03")
	}
	parts := strings.Split(s, "\x03")
	r := strings.NewReplacer("\x00", "%", "\x01", "{", "\x02", "}")
	for i := range parts {
		parts[i] = r.Replace(parts[i])
	}
	return parts
}

// stringLiterals returns the contents of the string literals on one line (escapes
// resolved for \n \t \" \\), with the column each starts at. C-family single
// quotes are char literals and skipped; Python/JS/sh single quotes are strings.
func stringLiterals(line, lang string) ([]string, []int) {
	var out []string
	var cols []int
	singleOK := lang == "py" || lang == "js" || lang == "sh"
	for i := 0; i < len(line); i++ {
		c := line[i]
		if lang == "c" || lang == "js" || lang == "go" {
			if c == '/' && i+1 < len(line) && line[i+1] == '/' {
				break // line comment
			}
		}
		if (lang == "py" || lang == "sh") && c == '#' {
			break
		}
		if !(c == '"' || c == '`' && (lang == "js" || lang == "go") || c == '\'' && singleOK) {
			continue
		}
		q := c
		var b strings.Builder
		j := i + 1
		for ; j < len(line); j++ {
			if line[j] == '\\' && j+1 < len(line) && q != '`' {
				switch line[j+1] {
				case 'n', 't', 'r':
					b.WriteByte(' ')
				default:
					b.WriteByte(line[j+1])
				}
				j++
				continue
			}
			if line[j] == q {
				break
			}
			b.WriteByte(line[j])
		}
		if j >= len(line) && q != '`' {
			break // unterminated on this line
		}
		out = append(out, b.String())
		cols = append(cols, i)
		i = j
	}
	return out, cols
}

// enclosingCall is the function or macro whose argument list the text ends
// inside: the identifier before the innermost '(' still open at the end.
func enclosingCall(before string) string {
	depth := 0
	for i := len(before) - 1; i >= 0; i-- {
		switch before[i] {
		case ')':
			depth++
		case '(':
			if depth == 0 {
				if m := callName.FindStringSubmatch(before[:i]); m != nil {
					return m[1]
				}
				return ""
			}
			depth--
		}
	}
	return ""
}
