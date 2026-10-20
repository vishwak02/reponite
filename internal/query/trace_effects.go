// trace_effects.go reads the side effects a function body performs, line by
// line, for reponite_trace: HTTP calls out (linked to the route they reach when
// routes.go pairs them), database access (Django ORM, SQLAlchemy, raw SQL,
// GORM, Prisma, TypeORM/JPA-style repositories), message publish/emit (ROS,
// event emitters, task queues, brokers), file writes, subprocesses, log lines
// (the logsite literal reader) and raised errors. Pattern-based over source
// text, so each effect is cited by line and quoted as written; a pattern this
// table does not know is simply not reported, never guessed. Pure.
package query

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

type effectRule struct {
	kind, op string
	re       *regexp.Regexp
	langs    string // codeExts languages it applies to ("" = all)
}

// effectRules: most specific first; the first rule matching a line wins for
// its kind, so a line is reported once per kind.
var effectRules = []effectRule{
	// --- database --------------------------------------------------------
	{"db", "write", regexp.MustCompile(`\b([A-Z]\w*\.objects(?:\.\w+\([^()]*\))*\.(?:create|bulk_create|bulk_update|update|update_or_create|get_or_create|delete))\s*\(`), "py"},
	{"db", "read", regexp.MustCompile(`\b([A-Z]\w*\.objects\.\w+)\s*\(`), "py"},
	{"db", "write", regexp.MustCompile(`\b((?:self\.)?(?:\w+\.)?(?:session|db\.session|db|Session\(\))\.(?:add|add_all|delete|merge|commit|flush|bulk_save_objects|bulk_insert_mappings))\s*\(`), "py"},
	{"db", "read", regexp.MustCompile(`\b((?:self\.)?(?:\w+\.)?(?:session|db\.session)\.(?:query|get|scalars|scalar))\s*\(`), "py"},
	{"db", "sql", regexp.MustCompile(`\b(\w*(?:cursor|conn|connection|session|db|engine)\w*\.execute(?:many)?)\s*\(`), "py"},
	{"db", "tx", regexp.MustCompile(`\b(transaction\.(?:atomic|on_commit))\b`), "py"},
	{"db", "write", regexp.MustCompile(`\b((?:self\.)?[a-z_]\w*\.(?:save|delete))\(\s*(?:\)|update_fields|using|force_insert)`), "py"},
	{"db", "write", regexp.MustCompile(`\b(serializer\.save)\s*\(`), "py"},
	{"db", "sql", regexp.MustCompile(`\b(\w*(?:db|DB|tx|Tx|conn|Conn|pool)\w*\.(?:Exec|Query|QueryRow|QueryContext|ExecContext|QueryRowContext))\s*\(`), "go"},
	{"db", "write", regexp.MustCompile(`\b(\w*(?:db|DB|tx|Tx|gorm)\w*(?:\.\w+\([^()]*\))*\.(?:Create|Save|Delete|Updates?|UpdateColumns?))\s*\(`), "go"},
	{"db", "read", regexp.MustCompile(`\b(\w*(?:db|DB|tx|Tx|gorm)\w*(?:\.\w+\([^()]*\))*\.(?:Find|First|Last|Take|Scan|Pluck|Count))\s*\(`), "go"},
	{"db", "write", regexp.MustCompile(`\b((?:this\.)?prisma\.\w+\.(?:create|createMany|update|updateMany|upsert|delete|deleteMany))\s*\(`), "js"},
	{"db", "read", regexp.MustCompile(`\b((?:this\.)?prisma\.\w+\.(?:findMany|findUnique|findUniqueOrThrow|findFirst|findFirstOrThrow|count|aggregate|groupBy))\s*\(`), "js"},
	{"db", "sql", regexp.MustCompile(`\b((?:this\.)?prisma\.\$(?:queryRaw|executeRaw|queryRawUnsafe|executeRawUnsafe|transaction))\b`), "js"},
	{"db", "write", regexp.MustCompile(`\b((?:this\.)?\w*(?:[rR]epository|[rR]epo|manager|Manager|em)\.(?:save|insert|update|delete|remove|softDelete|upsert|persist|merge|saveAll|deleteById))\s*\(`), "js c"},
	{"db", "read", regexp.MustCompile(`\b((?:this\.)?\w*(?:[rR]epository|[rR]epo|manager|Manager|em)\.(?:find|findOne|findOneBy|findBy|findAndCount|findById|findAll|count|createQueryBuilder|query|exist|existsById))\s*\(`), "js c"},
	{"db", "read", regexp.MustCompile(`\b(getRepository|getManager|getCustomRepository)\s*\(`), "js"},
	{"db", "sql", regexp.MustCompile(`(?i)["'` + "`" + `]\s*(SELECT\s+[\w*]|INSERT\s+INTO|UPDATE\s+\w+\s+SET|DELETE\s+FROM)`), ""},

	// --- publish / emit ----------------------------------------------------
	{"publish", "ros", regexp.MustCompile(`\b([\w.\->\[\]]*?\w(?:\.|->)publish)\s*\(`), "c py"},
	{"publish", "emit", regexp.MustCompile(`\b([\w.]*\.emit)\s*\(\s*['"` + "`" + `]?([\w:./-]*)`), "js py"},
	{"publish", "task", regexp.MustCompile(`\b([\w.]+\.(?:delay|apply_async|send_task))\s*\(`), "py"},
	{"publish", "broker", regexp.MustCompile(`\b([\w.]*(?:producer|Producer|channel|Channel|publisher|Publisher|redis|Redis|client|kafka|nats|pubsub|sns|sqs|bus)\w*\.(?:send|produce|publish|basic_publish|send_message|put_record|xadd|lpush|rpush|group_send|Publish|Produce|Send))\s*\(`), ""},
	{"publish", "broker", regexp.MustCompile(`\b([\w.]*[rR]edis\w*\.(?:xadd|publish|lpush|rpush))\b`), ""},
	{"publish", "signal", regexp.MustCompile(`\b(\w+\.send(?:_robust)?)\s*\(\s*sender\s*=`), "py"},

	// --- file writes ---------------------------------------------------------
	{"file", "write", regexp.MustCompile(`\b(open)\s*\([^)]*,\s*(?:mode\s*=\s*)?["'][wax]b?\+?["']`), "py"},
	{"file", "write", regexp.MustCompile(`\b([\w.]*\.(?:write_text|write_bytes|unlink|rmdir|mkdir|rename|touch))\s*\(`), "py"},
	{"file", "write", regexp.MustCompile(`\b((?:shutil\.(?:copy\w*|move|rmtree))|os\.(?:remove|unlink|rename|replace|makedirs|mkdir|rmdir)|json\.dump|pickle\.dump|yaml\.(?:safe_)?dump)\s*\(`), "py"},
	{"file", "write", regexp.MustCompile(`\b((?:os|ioutil)\.(?:WriteFile|Create|OpenFile|Remove|RemoveAll|Rename|Mkdir|MkdirAll))\s*\(`), "go"},
	{"file", "write", regexp.MustCompile(`\b((?:fs|fsp|fs\.promises)\.(?:writeFile|writeFileSync|appendFile|appendFileSync|createWriteStream|unlink|unlinkSync|rm|rmSync|mkdir|mkdirSync|rename|renameSync))\s*\(`), "js"},
	{"file", "write", regexp.MustCompile(`\b(std::ofstream|std::fstream|ofstream|fopen\s*\([^)]*"[wa]|Files\.(?:write|writeString|delete|move|copy)|FileWriter|FileOutputStream)\b`), "c"},

	// --- file reads: a stored file touched per call (a FieldFile's size opens
	// storage; a missing file raises there) ---------------------------------
	{"file", "read", regexp.MustCompile(`\b([\w.]*\.(?:file|image|document|attachment)\.(?:size|read|open|path|chunks))\b`), "py"},
	{"file", "read", regexp.MustCompile(`\b(os\.path\.(?:getsize|getmtime|exists|isfile)|os\.stat|[\w.]*\.(?:read_text|read_bytes|stat))\s*\(`), "py"},

	// --- subprocess ------------------------------------------------------------
	{"exec", "", regexp.MustCompile(`\b(subprocess\.(?:run|call|Popen|check_output|check_call)|os\.(?:system|popen|exec\w*|spawn\w*))\s*\(`), "py"},
	{"exec", "", regexp.MustCompile(`\b(exec\.Command(?:Context)?|syscall\.Exec)\s*\(`), "go"},
	{"exec", "", regexp.MustCompile(`\b((?:child_process\.)?(?:execSync|execFile|execFileSync|spawn|spawnSync|fork))\s*\(`), "js"},
	{"exec", "", regexp.MustCompile(`(?:^|[^\w.])((?:std::)?system|popen|execv\w*|execl\w*|Runtime\.getRuntime\(\)\.exec|ProcessBuilder)\s*\(`), "c"},

	// --- HTTP out (calls routes.go could not resolve a URL for) -------------------
	{"http", "", regexp.MustCompile(`\b((?:requests|httpx|session|aiohttp\.ClientSession\(\)|urllib\.request)\.(?:get|post|put|patch|delete|request|urlopen))\s*\(`), "py"},
	{"http", "", regexp.MustCompile(`\b(fetch|axios(?:\.\w+)?)\s*\(`), "js"},
	{"http", "", regexp.MustCompile(`\b(http\.(?:Get|Post|PostForm|Head|NewRequest(?:WithContext)?)|\w*[cC]lient\.Do)\s*\(`), "go"},

	// --- raised errors -------------------------------------------------------------
	{"raise", "", regexp.MustCompile(`^\s*raise\s+([\w.]+)`), "py"},
	{"raise", "", regexp.MustCompile(`\bthrow\s+(?:new\s+)?([\w.:]+)`), "js c"},
	{"raise", "assert", regexp.MustCompile(`^\s*(assert)\b`), "py"},
	{"raise", "panic", regexp.MustCompile(`\b(panic|log\.Fatal\w*)\s*\(`), "go"},
	{"raise", "error", regexp.MustCompile(`\breturn\b.*\b(errors\.New|fmt\.Errorf)\s*\(`), "go"},
}

// logLike: the call a string literal is passed to is a logging call.
var logLike = regexp.MustCompile(`(?i)(^|[.:>_])(log\w*|logger|logging|console\.\w+|ROS_\w+|RCLCPP_\w+|RCUTILS_\w+|print\w*|printf|puts|warn\w*|error\w*|info\w*|debug\w*|fatal\w*|critical|trace|notice|exception|Print\w*|Fprint\w*|spdlog::\w+|std::cerr|cerr|cout)$`)

// effectsIn reports the side effects in lines [lo, hi] of file fi of v.
func (t *tracer) effectsIn(v *repoView, fi, lo, hi int) []TraceEffect {
	lines := v.fileLines(fi)
	if lo <= 0 || lo > len(lines) {
		return nil
	}
	if hi > len(lines) {
		hi = len(lines)
	}
	path := v.files[fi].Path
	lang := codeExts[strings.ToLower(filepath.Ext(path))]
	if lang == "" {
		return nil
	}
	linked := t.clientsIn(v.repo, path, lo, hi)
	var pubs map[string]string // publisher variable -> topic, built on the first publish
	var out []TraceEffect
	for ln := lo; ln <= hi; ln++ {
		raw := lines[ln-1]
		code := strings.TrimSpace(raw)
		if code == "" || strings.HasPrefix(code, "//") || strings.HasPrefix(code, "#") || strings.HasPrefix(code, "*") || strings.HasPrefix(code, "/*") {
			continue
		}
		if lang == "py" && strings.HasPrefix(code, "@") && !strings.Contains(code, "transaction.atomic") {
			continue // a decorator (a route, a cache) is not a call made by the body
		}
		got := map[string]bool{}
		if cs, ok := linked[ln]; ok {
			for _, c := range cs {
				e := TraceEffect{Kind: "http", Op: c.call.Method, Line: ln, Text: clip(c.call.Method + " " + c.call.URL)}
				if len(c.routes) > 0 {
					r := c.routes[0]
					e.Target = r.Repo + ":" + routeLabel(r)
				} else {
					e.Target = "(no indexed route serves it)"
				}
				out = append(out, e)
			}
			got["http"] = true
		}
		for _, r := range effectRules {
			if got[r.kind] || r.langs != "" && !strings.Contains(" "+r.langs+" ", " "+lang+" ") {
				continue
			}
			m := r.re.FindStringSubmatch(raw)
			if m == nil {
				continue
			}
			got[r.kind] = true
			e := TraceEffect{Kind: r.kind, Op: r.op, Line: ln, Text: clip(code)}
			switch r.kind {
			case "raise":
				e.Text = m[1]
				if lits, _ := stringLiterals(raw, lang); len(lits) > 0 {
					e.Text += fmt.Sprintf(" %q", clipN(lits[0], 60))
				}
			case "publish":
				if r.op == "ros" {
					if pubs == nil {
						pubs = t.publishTopics(v.repo)
					}
					if topic := pubs[lastMember(strings.TrimSuffix(strings.TrimSuffix(m[1], ".publish"), "->publish"))]; topic != "" {
						e.Target = "topic " + topic
					}
				} else if r.op == "emit" && len(m) > 2 && m[2] != "" {
					e.Target = "event " + m[2]
				}
			}
			out = append(out, e)
		}
		if !got["raise"] {
			if lits, cols := stringLiterals(raw, lang); len(lits) > 0 {
				call := enclosingCall(raw[:cols[0]])
				if call != "" && logLike.MatchString(call) {
					out = append(out, TraceEffect{Kind: "log", Op: call, Line: ln, Text: fmt.Sprintf("%s %q", call, clipN(strings.Join(lits, " … "), 70))})
				}
			}
		}
	}
	return out
}

// lastMember is the last member of an access path: self.pub_ -> pub_.
func lastMember(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexAny(s, ".>"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSuffix(s, "-")
}

func clip(s string) string { return clipN(s, 100) }

func clipN(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// --- rendering ---------------------------------------------------------------

// nodeLine is one node rendered on one line (the text format and the budget
// unit): the handle or label, how it was reached, and where it lives — the
// repo only when it differs from the parent's.
func nodeLine(n *TraceNode, parentRepo string) string {
	var b strings.Builder
	switch n.Kind {
	case NodeSymbol:
		b.WriteString(n.Symbol)
	default:
		b.WriteString("[" + n.Kind + "] " + n.Label)
	}
	if n.Edge != "" {
		fmt.Fprintf(&b, "  ⟵%s", n.Edge)
		if n.Method != "" {
			b.WriteString(" " + n.Method)
		}
		fmt.Fprintf(&b, " %.2f", n.Confidence)
	}
	if n.Path != "" {
		b.WriteString("  @")
		if n.Repo != parentRepo {
			b.WriteString(n.Repo + ":")
		}
		fmt.Fprintf(&b, "%s:%d", n.Path, n.Line)
	} else if n.Repo != parentRepo && n.Repo != "" {
		b.WriteString("  @" + n.Repo)
	}
	if n.Note != "" {
		b.WriteString("  (" + n.Note + ")")
	}
	switch n.Stop {
	case "":
	case StopRepeat:
		b.WriteString("  ↺ repeat — expanded above at " + n.Same)
	case StopCycle:
		b.WriteString("  ⟲ cycle")
	case StopAmbiguous:
		b.WriteString("  ? ambiguous: " + strings.Join(n.Alt, " | "))
	default:
		fmt.Fprintf(&b, "  ✂ %s", n.Stop)
		if n.More > 0 {
			fmt.Fprintf(&b, " (+%d not walked)", n.More)
		}
	}
	if n.Stop == "" && n.More > 0 {
		fmt.Fprintf(&b, "  (+%d more children)", n.More)
	}
	return b.String()
}

func effectLine(e TraceEffect) string {
	s := e.Kind
	if e.Op != "" && e.Kind != "log" && e.Kind != "http" {
		s += "/" + e.Op
	}
	s += fmt.Sprintf(" L%d %s", e.Line, e.Text)
	if e.Target != "" {
		s += " → " + e.Target
	}
	return s
}

func effectTokens(n *TraceNode) int {
	t := 0
	for _, e := range n.Effects {
		t += estTokens(effectLine(e)) + 1
	}
	if len(n.External) > 0 {
		t += estTokens(strings.Join(n.External, ", ")) + 2
	}
	return t
}

// TraceText renders a trace as an indented tree an LLM reads cheaply: one
// line per node, its effects ("!") and external calls ("ext") under it.
func TraceText(r TraceResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "trace %q (%s, %s) depth %d, budget %d — %d nodes, ~%d tokens", r.Entry, r.EntryKind, r.Direction, r.Depth, r.Budget, r.Nodes, r.Tokens)
	if r.Truncated {
		b.WriteString(", TRUNCATED")
	}
	b.WriteString("\n")
	var walk func(n *TraceNode, depth int, parentRepo string)
	walk = func(n *TraceNode, depth int, parentRepo string) {
		ind := strings.Repeat("  ", depth)
		b.WriteString(ind + "- " + nodeLine(n, parentRepo) + "\n")
		for _, e := range n.Effects {
			b.WriteString(ind + "    ! " + effectLine(e) + "\n")
		}
		if n.MoreEffect > 0 {
			fmt.Fprintf(&b, "%s    ! +%d more effects\n", ind, n.MoreEffect)
		}
		if len(n.External) > 0 {
			b.WriteString(ind + "    ext " + strings.Join(n.External, ", ") + "\n")
		}
		for _, c := range n.Children {
			walk(c, depth+1, n.Repo)
		}
	}
	for _, root := range r.Roots {
		walk(root, 0, "")
	}
	if len(r.Roots) == 0 {
		b.WriteString("(no entry resolved)\n")
	}
	for _, w := range r.Meta.Warnings {
		b.WriteString("warning: " + w + "\n")
	}
	if r.Next != "" {
		b.WriteString("next: " + r.Next + "\n")
	}
	b.WriteString("note: " + r.Note + "\n")
	return b.String()
}
