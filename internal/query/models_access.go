// models_access.go finds the code that reads and writes one model: ORM call
// sites by framework (Django managers and related managers, SQLAlchemy
// session/select, TypeORM repositories, Prisma client delegates, GORM
// chains, Sequelize statics), raw SQL naming its table anywhere in the fleet,
// signal/event hooks, and the routes (routes.go) whose handler touches the
// model or one of its serializers. Each site carries its enclosing symbol's
// reponite id so an agent can drill in with brief/context.
package query

import (
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Confidence of an access site, by how it was recognized.
const (
	accessConfDirect   = 0.9 // the model's own manager/session/repository/delegate call
	accessConfRelated  = 0.6 // a Django related manager (customer.orders.filter)
	accessConfInstance = 0.5 // an instance variable named after the model (order.save())
	accessConfRawSQL   = 0.7 // raw SQL naming the table
)

var (
	writeOps = map[string]bool{"create": true, "bulk_create": true, "update": true, "bulk_update": true, "update_or_create": true,
		"get_or_create": true, "delete": true, "save": true, "add": true, "remove": true, "set": true, "clear": true,
		"acreate": true, "aupdate": true, "adelete": true, "abulk_create": true, "aget_or_create": true, "aupdate_or_create": true,
		"asave": true, "abulk_update": true, "insert": true, "upsert": true, "softDelete": true, "softRemove": true, "restore": true,
		"increment": true, "decrement": true, "createMany": true, "updateMany": true, "deleteMany": true, "destroy": true,
		"bulkCreate": true, "merge": true, "add_all": true, "Create": true, "Save": true, "Delete": true, "Update": true,
		"Updates": true, "UpdateColumn": true, "UpdateColumns": true, "FirstOrCreate": true, "CreateInBatches": true,
		"insert_many": true, "bulk_save_objects": true, "bulk_insert_mappings": true, "bulk_update_mappings": true,
		"findOrCreate": true, "persist": true, "persistAndFlush": true, "nativeInsert": true, "nativeUpdate": true, "nativeDelete": true}
	methodCall = regexp.MustCompile(`\.\s*(\w+)\s*\(`)
	importLine = regexp.MustCompile(`^\s*(?:from\s+\S+\s+import\b|import\s+[\w.{*]|export\s+\{|.*\bimport\b.*\bfrom\s+['"])`)
)

// stmtFrom is the logical statement starting at byte pos of content: up to
// the end of the line, extended while brackets are open (bounded).
func stmtFrom(content string, pos int) string {
	end, depth, lines := pos, 0, 0
	for end < len(content) {
		switch content[end] {
		case '(', '[', '{':
			depth++
		case ')', ']', '}':
			depth--
		case '\n':
			lines++
			if depth <= 0 || lines > 12 {
				return content[pos:end]
			}
		}
		end++
	}
	return content[pos:end]
}

// classifyChain picks the op of a call chain: the first write method if any
// (the chain mutates), else the first method (a read).
// skip is the entry method the pattern itself matched (session.query): a
// later method names the operation better.
func classifyChain(chain, skip string) (op string, write bool) {
	for _, m := range methodCall.FindAllStringSubmatch(chain, -1) {
		if writeOps[m[1]] {
			return m[1], true
		}
		if op == "" && m[1] != skip {
			op = m[1]
		}
	}
	return op, false
}

type accessPat struct {
	re   *regexp.Regexp
	via  string
	conf float64
	kind string // "chain": classify the chain after the match; "read"/"write"/"hook": fixed
	op   string
}

type accessScan struct {
	m      DataModel
	repo   string
	files  []File
	syms   map[string]SymbolRef
	writes []DataAccess
	reads  []DataAccess
	hooks  []DataAccess
	seen   map[string]bool
}

func clip(t string) string {
	t = strings.TrimSpace(t)
	if len(t) > 160 {
		t = t[:160] + "…"
	}
	return t
}

func (a *accessScan) add(f File, line int, op, via string, conf float64, write bool, text string) {
	key := f.Path + ":" + strconv.Itoa(line)
	if a.seen[key] {
		return
	}
	a.seen[key] = true
	in, handle := enclosingHandle(f, line, a.syms)
	x := DataAccess{Repo: a.repo, Path: f.Path, Line: line, In: in, Handle: handle, Op: op, Via: via, Conf: conf, Text: clip(text)}
	if write {
		a.writes = append(a.writes, x)
	} else {
		a.reads = append(a.reads, x)
	}
}

// enclosingHandle is the innermost symbol around line and its reponite id
// (dir.Class.method, else dir.name), checked against the indexed symbols.
func enclosingHandle(f File, line int, syms map[string]SymbolRef) (string, string) {
	var around []SymbolSpan
	for _, s := range f.Symbols {
		if line >= s.StartLine && line <= s.EndLine {
			around = append(around, s)
		}
	}
	if len(around) == 0 {
		return "", ""
	}
	sort.SliceStable(around, func(i, j int) bool {
		return around[i].EndLine-around[i].StartLine < around[j].EndLine-around[j].StartLine
	})
	in := around[0].Name
	dir := path.Dir(f.Path)
	q := func(n string) string {
		if dir == "." || dir == "" {
			return n
		}
		return dir + "." + n
	}
	var cands []string
	if len(around) > 1 {
		cands = append(cands, q(around[1].Name+"."+in))
	}
	cands = append(cands, q(in))
	for i, c := range cands {
		if _, ok := syms[c]; ok {
			if i == 0 && len(cands) == 2 {
				in = around[1].Name + "." + in
			}
			return in, c
		}
	}
	return in, ""
}

func (a *accessScan) patterns() ([]accessPat, map[string]bool) {
	m := a.m
	n := regexp.QuoteMeta(m.Name)
	var pats []accessPat
	add := func(expr, via string, conf float64, kind, op string) {
		pats = append(pats, accessPat{regexp.MustCompile(expr), via, conf, kind, op})
	}
	var langs map[string]bool
	switch m.Framework {
	case "django":
		langs = map[string]bool{"py": true}
		mgrs := append([]string{"objects", "_default_manager", "_base_manager"}, m.managers...)
		add(`\b`+n+`\.(?:`+strings.Join(quoteAll(mgrs), "|")+`)\b`, "orm", accessConfDirect, "chain", "")
		add(`\bget_(?:object|list)_or_404\(\s*`+n+`\b`, "orm", accessConfDirect, "read", "get_object_or_404")
		for _, r := range m.Relations {
			acc := r.Reverse
			if acc == "" && (r.Kind == "fk" || r.Kind == "m2m") {
				acc = strings.ToLower(m.Name) + "_set"
			}
			if acc == "" || strings.HasSuffix(acc, "+") || strings.Contains(acc, "%(") {
				continue
			}
			add(`\.`+regexp.QuoteMeta(acc)+`\.\s*(?:all|filter|exclude|get|count|exists|first|last|create|update|delete|add|remove|set|clear|values|values_list|order_by|select_related|prefetch_related|aggregate|annotate|iterator|bulk_create|get_or_create|update_or_create)\(`,
				"related-manager", accessConfRelated, "chain", "")
		}
		inst := regexp.QuoteMeta(snakeCase(m.Name))
		add(`(?:^|[^\w.]|self\.)(?:\w+_)?`+inst+`\.(save|delete|asave|adelete|refresh_from_db)\(`, "instance-name", accessConfInstance, "chain", "")
		add(`\b`+n+`\([^)]*\)\.save\(`, "orm", accessConfDirect, "write", "save")
		add(`\bsender\s*=\s*`+n+`\b`, "signal", accessConfDirect, "hook", "signal")
	case "sqlalchemy", "sqlmodel":
		langs = map[string]bool{"py": true}
		add(`\.query\(\s*`+n+`\b`, "session", accessConfDirect, "chain", "query")
		add(`(?:^|[^\w.])select\(\s*`+n+`\b`, "select", accessConfDirect, "read", "select")
		add(`\.get\(\s*`+n+`\s*,`, "session", accessConfDirect, "read", "get")
		add(`\b`+n+`\.query\b`, "flask-sqlalchemy", accessConfDirect, "chain", "query")
		add(`(?:^|[^\w.])(?:sa\.)?(update|insert|delete)\(\s*`+n+`\b`, "core", accessConfDirect, "write", "")
		add(`\.(add|merge|add_all)\(\s*\[?\s*`+n+`\(`, "session", accessConfDirect, "write", "")
		add(`\.(bulk_insert_mappings|bulk_update_mappings)\(\s*`+n+`\b`, "session", accessConfDirect, "write", "")
		add(`\blistens?_for\(\s*`+n+`\b`, "event", accessConfDirect, "hook", "event")
	case "typeorm", "mikro-orm", "sequelize-typescript":
		langs = map[string]bool{"js": true}
		add(`getRepository\(\s*`+n+`\s*\)`, "repository", accessConfDirect, "chain", "")
		add(`\.(find\w*|count\w*|save|insert|update|delete|remove|softDelete|upsert|exist\w*|increment|decrement|persist\w*|nativeInsert|nativeUpdate|nativeDelete)\(\s*`+n+`\s*,`, "entity-manager", accessConfDirect, "write?", "")
		add(`createQueryBuilder\(\s*`+n+`\b`, "query-builder", accessConfDirect, "chain", "createQueryBuilder")
		add(`\b`+n+`\.(findAll|findOne|findByPk|findAndCountAll|count|create|bulkCreate|update|destroy|upsert|increment|findOrCreate)\(`, "static", accessConfDirect, "chain", "")
		for _, v := range tsRepoVars(a.files, m.Name) {
			add(`\b(?:this\.)?`+regexp.QuoteMeta(v)+`\.(\w+)\(`, "repository", accessConfDirect, "chain", "")
		}
	case "prisma":
		langs = map[string]bool{"js": true}
		acc := strings.ToLower(m.Name[:1]) + m.Name[1:]
		add(`\.\s*`+regexp.QuoteMeta(acc)+`\s*\.\s*(findMany|findUnique|findUniqueOrThrow|findFirst|findFirstOrThrow|count|aggregate|groupBy|create|createMany|createManyAndReturn|update|updateMany|upsert|delete|deleteMany)\(`,
			"prisma-client", accessConfDirect, "chain", "")
	case "gorm":
		langs = map[string]bool{"go": true}
	}
	return pats, langs
}

func (a *accessScan) scan() {
	m := a.m
	pats, langs := a.patterns()
	var vars map[string]bool
	if m.Framework == "gorm" {
		vars = goVarsOfType(a.files, m.Name)
	}
	for _, f := range a.files {
		if !langs[codeExts[strings.ToLower(path.Ext(f.Path))]] {
			continue
		}
		if m.Framework == "gorm" {
			a.scanGorm(f, vars)
			continue
		}
		for _, p := range pats {
			for _, loc := range p.re.FindAllStringSubmatchIndex(f.Content, -1) {
				line := lineOf(f.Content, loc[0])
				ls := strings.LastIndex(f.Content[:loc[0]], "\n") + 1
				lineText := f.Content[ls:]
				if k := strings.IndexByte(lineText, '\n'); k >= 0 {
					lineText = lineText[:k]
				}
				if t := strings.TrimSpace(lineText); strings.HasPrefix(t, "#") || strings.HasPrefix(t, "//") || importLine.MatchString(lineText) {
					continue
				}
				if f.Path == m.Path && line >= m.Line && line <= m.EndLine {
					continue // the model's own declaration
				}
				switch p.kind {
				case "hook":
					in, h := enclosingHandle(f, line, a.syms)
					a.hooks = append(a.hooks, DataAccess{Repo: a.repo, Path: f.Path, Line: line, In: in, Handle: h, Op: p.op, Via: p.via, Conf: p.conf, Text: clip(lineText)})
				case "read", "write":
					op := p.op
					if op == "" && len(loc) > 3 && loc[2] >= 0 {
						op = f.Content[loc[2]:loc[3]]
					}
					a.add(f, line, op, p.via, p.conf, p.kind == "write", lineText)
				case "write?":
					op := f.Content[loc[2]:loc[3]]
					a.add(f, line, op, p.via, p.conf, writeOps[op], lineText)
				default:
					chain := stmtFrom(f.Content, loc[0])
					op, write := classifyChain(chain, p.op)
					if op == "" {
						op = p.op
					}
					if op == "" {
						op = "query"
					}
					a.add(f, line, op, p.via, p.conf, write, lineText)
				}
			}
		}
		if m.Framework == "sqlalchemy" || m.Framework == "sqlmodel" {
			a.scanInstanceAdds(f)
		}
	}
}

func quoteAll(xs []string) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = regexp.QuoteMeta(x)
	}
	return out
}

// scanInstanceAdds: `x = Model(...)` then `session.add(x)` in the same symbol.
func (a *accessScan) scanInstanceAdds(f File) {
	re := regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*` + regexp.QuoteMeta(a.m.Name) + `\(`)
	for _, m := range re.FindAllStringSubmatchIndex(f.Content, -1) {
		v := f.Content[m[2]:m[3]]
		_, h := enclosingHandle(f, lineOf(f.Content, m[0]), a.syms)
		add := regexp.MustCompile(`\.(add|merge)\(\s*` + regexp.QuoteMeta(v) + `\s*\)`)
		k := add.FindStringIndex(f.Content[m[1]:])
		if k == nil {
			continue
		}
		pos := m[1] + k[0]
		l2 := lineOf(f.Content, pos)
		if _, h2 := enclosingHandle(f, l2, a.syms); h2 != h {
			continue
		}
		ls := strings.LastIndex(f.Content[:pos], "\n") + 1
		le := strings.IndexByte(f.Content[ls:], '\n')
		if le < 0 {
			le = len(f.Content) - ls
		}
		a.add(f, l2, "add", "session", accessConfDirect, true, f.Content[ls:ls+le])
	}
}

var gormMethods = regexp.MustCompile(`\.(Create|CreateInBatches|Save|Delete|Updates?|UpdateColumns?|First|Find|Take|Last|Scan|Model|Count|Pluck|FirstOrCreate|FirstOrInit|Preload|Joins|Where|Table)\(`)

// goVarsOfType: variables (and parameters) declared with the model's type.
func goVarsOfType(files []File, name string) map[string]bool {
	n := regexp.QuoteMeta(name)
	re := regexp.MustCompile(`\b(\w+)\s+(?:\*|\[\]\*?)?(?:\w+\.)?` + n + `\b|\b(\w+)\s*:=\s*(?:&|\[\]|make\(\[\])?(?:\w+\.)?` + n + `\b`)
	out := map[string]bool{}
	for _, f := range files {
		if path.Ext(f.Path) != ".go" || !strings.Contains(f.Content, name) {
			continue
		}
		for _, m := range re.FindAllStringSubmatch(f.Content, -1) {
			v := m[1]
			if v == "" {
				v = m[2]
			}
			switch v {
			case "", "type", "func", "return", "struct", "var", "range":
				continue
			}
			out[v] = true
		}
	}
	return out
}

func (a *accessScan) scanGorm(f File, vars map[string]bool) {
	if !strings.Contains(f.Content, a.m.Name) && len(vars) == 0 {
		return
	}
	n := regexp.QuoteMeta(a.m.Name)
	lit := regexp.MustCompile(`(?:&|\[\])?(?:\w+\.)?\b` + n + `\{|new\((?:\w+\.)?` + n + `\)`)
	var vre *regexp.Regexp
	if len(vars) > 0 {
		var vs []string
		for v := range vars {
			vs = append(vs, regexp.QuoteMeta(v))
		}
		sort.Strings(vs)
		vre = regexp.MustCompile(`\(\s*&?(?:` + strings.Join(vs, "|") + `)\s*[,)]`)
	}
	for i, ln := range strings.Split(f.Content, "\n") {
		if !gormMethods.MatchString(ln) || strings.HasPrefix(strings.TrimSpace(ln), "//") {
			continue
		}
		if !lit.MatchString(ln) && (vre == nil || !vre.MatchString(ln)) {
			continue
		}
		if f.Path == a.m.Path && i+1 >= a.m.Line && i+1 <= a.m.EndLine {
			continue
		}
		op, write := "", false
		for _, mm := range gormMethods.FindAllStringSubmatch(ln, -1) {
			if writeOps[mm[1]] {
				op, write = mm[1], true
				break
			}
			if op == "" || op == "Model" || op == "Where" || op == "Preload" || op == "Joins" || op == "Table" {
				op = mm[1]
			}
		}
		a.add(f, i+1, op, "gorm", accessConfDirect, write, ln)
	}
}

// tsRepoVars: variables holding a repository of the entity.
func tsRepoVars(files []File, name string) []string {
	n := regexp.QuoteMeta(name)
	res := []*regexp.Regexp{
		regexp.MustCompile(`(\w+)\s*(?::[^=;]+)?=\s*(?:await\s+)?[\w.]*getRepository\(\s*` + n + `\s*\)`),
		regexp.MustCompile(`@InjectRepository\(\s*` + n + `\s*\)\s*(?:(?:private|public|protected|readonly)\s+)*(\w+)`),
		regexp.MustCompile(`(\w+)\s*[?!]?\s*:\s*(?:Repository|EntityRepository|MongoRepository|TreeRepository)<\s*` + n + `\s*>`),
		regexp.MustCompile(`@InjectModel\(\s*` + n + `\s*\)\s*(?:(?:private|public|protected|readonly)\s+)*(\w+)`),
	}
	seen := map[string]bool{}
	var out []string
	for _, f := range files {
		if codeExts[strings.ToLower(path.Ext(f.Path))] != "js" || !strings.Contains(f.Content, name) {
			continue
		}
		for _, re := range res {
			for _, m := range re.FindAllStringSubmatch(f.Content, -1) {
				if !seen[m[1]] && m[1] != "const" && m[1] != "let" {
					seen[m[1]] = true
					out = append(out, m[1])
				}
			}
		}
	}
	sort.Strings(out)
	return out
}

var (
	sqlVerbLine   = regexp.MustCompile(`(?i)\bselect\b.*\bfrom\b|\binsert\s+into\b|\bdelete\s+from\b|\bupdate\s+\S+\s+set\b|\bjoin\b`)
	sqlDeleteVerb = regexp.MustCompile(`(?i)\bdelete\b`)
)

// rawSQL finds SQL naming the model's table in every repo's code and SQL files.
func rawSQL(files map[string][]File, m DataModel, syms func(string) map[string]SymbolRef) []DataAccess {
	table := m.Table
	if table == "" {
		return nil
	}
	bare := table[strings.LastIndex(table, ".")+1:]
	if len(bare) < 3 {
		return nil
	}
	re := regexp.MustCompile(`(?i)\b(DELETE\s+FROM|FROM|JOIN|INSERT\s+INTO|INTO|UPDATE|TRUNCATE(?:\s+TABLE)?|ALTER\s+TABLE|DROP\s+TABLE)\s+["'` + "`" + `\[]?(?:\w+["` + "`" + `\]]?\.)?["` + "`" + `\[]?` + regexp.QuoteMeta(bare) + `\b`)
	lbare := strings.ToLower(bare)
	var out []DataAccess
	repos := make([]string, 0, len(files))
	for r := range files {
		repos = append(repos, r)
	}
	sort.Strings(repos)
	for _, repo := range repos {
		for _, f := range files[repo] {
			ext := strings.ToLower(path.Ext(f.Path))
			isSQL := ext == ".sql"
			if codeExts[ext] == "" && !isSQL || !strings.Contains(strings.ToLower(f.Content), lbare) {
				continue
			}
			for i, ln := range strings.Split(f.Content, "\n") {
				mm := re.FindStringSubmatch(ln)
				if mm == nil || importLine.MatchString(ln) {
					continue
				}
				kw := mm[1]
				if !isSQL && kw != strings.ToUpper(kw) && !sqlVerbLine.MatchString(ln) {
					continue // "from orders import …", "failed to update orders"
				}
				if repo == m.Repo && f.Path == m.Path && i+1 >= m.Line && i+1 <= m.EndLine {
					continue
				}
				k := strings.ToUpper(strings.Fields(kw)[0])
				if isSQL && (k == "ALTER" || k == "DROP") {
					continue // schema history, folded into the model already
				}
				write := k != "FROM" && k != "JOIN"
				if k == "FROM" && sqlDeleteVerb.MatchString(ln) {
					write, k = true, "DELETE"
				}
				in, h := enclosingHandle(f, i+1, syms(repo))
				x := DataAccess{Repo: repo, Path: f.Path, Line: i + 1, In: in, Handle: h, Op: k, Via: "raw-sql", Conf: accessConfRawSQL, Text: clip(ln)}
				if write {
					x.Op = "w:" + k
				}
				out = append(out, x)
			}
		}
	}
	return out
}

// modelRoutes: the routes of the model's repo whose handler (or its
// decorator) mentions the model or one of its serializers.
func modelRoutes(repo string, files []File, names []string) []DataRoute {
	routes := djangoRoutes(repo, files)
	for _, f := range files {
		if codeExts[strings.ToLower(path.Ext(f.Path))] == "py" {
			routes = append(routes, pyDecoratorRoutes(repo, f, files)...)
		}
	}
	if len(routes) == 0 || len(names) == 0 {
		return nil
	}
	mention := regexp.MustCompile(`\b(` + strings.Join(quoteAll(names), "|") + `)\b`)
	type def struct {
		f    *File
		span SymbolSpan
	}
	byName := map[string][]def{}
	byPath := map[string]*File{}
	for i := range files {
		f := &files[i]
		byPath[f.Path] = f
		for _, s := range f.Symbols {
			byName[s.Name] = append(byName[s.Name], def{f, s})
		}
	}
	text := func(f *File, from, to int) string {
		lines := strings.Split(f.Content, "\n")
		if from < 1 {
			from = 1
		}
		if to > len(lines) {
			to = len(lines)
		}
		if from > to {
			return ""
		}
		return strings.Join(lines[from-1:to], "\n")
	}
	var out []DataRoute
	seen := map[string]bool{}
	for _, r := range routes {
		var body []string
		if r.Framework == "fastapi/flask" {
			f := byPath[r.Path]
			if f == nil {
				continue
			}
			end := r.Line + 1
			for _, s := range f.Symbols {
				if s.Name == r.Handler && s.StartLine >= r.Line {
					end = s.EndLine
					break
				}
			}
			body = append(body, text(f, r.Line, end))
		} else {
			for _, seg := range handlerNames(r.Handler) {
				for _, d := range byName[seg] {
					body = append(body, text(d.f, d.span.StartLine, d.span.EndLine))
				}
				if len(body) > 0 {
					break
				}
			}
		}
		for _, b := range body {
			if mm := mention.FindStringSubmatch(b); mm != nil {
				k := r.Method + " " + r.Pattern + " " + r.Handler
				if !seen[k] {
					seen[k] = true
					out = append(out, DataRoute{Repo: r.Repo, Path: r.Path, Line: r.Line, Method: r.Method, Pattern: r.Pattern,
						Handler: r.Handler, Framework: r.Framework, Via: mm[1]})
				}
				break
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Pattern < out[j].Pattern })
	return out
}

// handlerNames: the symbol names a route's handler expression names, most
// specific first ("views.OrderViewSet.as_view" → OrderViewSet; a DRF
// @action "OrderViewSet.history" → the class, whose body covers it).
func handlerNames(h string) []string {
	h = strings.TrimSpace(h)
	if i := strings.IndexAny(h, "({"); i >= 0 {
		h = h[:i]
	}
	segs := strings.Split(h, ".")
	var out []string
	for i := len(segs) - 1; i >= 0; i-- {
		s := strings.TrimSpace(segs[i])
		if s == "" || s == "as_view" || s == "urls" {
			continue
		}
		out = append(out, s)
	}
	if len(out) >= 2 && out[1][0] >= 'A' && out[1][0] <= 'Z' {
		out = append([]string{out[1]}, out...)
	}
	return out
}

// modelDetail assembles one model's dossier within budget tokens.
func modelDetail(s Store, ref string, m DataModel, all []DataModel, sers []DataSerializer, files map[string][]File, budget int) (ModelDetail, int) {
	d := ModelDetail{Model: m, Omitted: map[string]int{}}
	symCache := map[string]map[string]SymbolRef{}
	syms := func(repo string) map[string]SymbolRef {
		if x, ok := symCache[repo]; ok {
			return x
		}
		x := s.SymbolsAt(repo, ref)
		symCache[repo] = x
		return x
	}
	for _, o := range all {
		if o.Repo != m.Repo || o.Name == m.Name && o.Path == m.Path {
			continue
		}
		for _, r := range o.Relations {
			if r.Target == m.Name {
				r.From, r.Repo, r.Path = o.Name, o.Repo, o.Path
				d.Incoming = append(d.Incoming, r)
			}
		}
	}
	names := []string{m.Name}
	for _, x := range sers {
		if x.Repo == m.Repo && x.Model == m.Name {
			d.Serializers = append(d.Serializers, x)
			names = append(names, x.Name)
		}
	}
	if m.Kind != KindWire {
		d.Routes = modelRoutes(m.Repo, files[m.Repo], names)
	}
	a := &accessScan{m: m, repo: m.Repo, files: files[m.Repo], syms: syms(m.Repo), seen: map[string]bool{}}
	if m.Kind == KindTable || m.Kind == KindAbstract {
		a.scan()
	}
	for _, x := range rawSQL(files, m, syms) {
		if a.seen[x.Path+":"+strconv.Itoa(x.Line)] {
			continue
		}
		if strings.HasPrefix(x.Op, "w:") {
			x.Op = x.Op[2:]
			a.writes = append(a.writes, x)
		} else {
			a.reads = append(a.reads, x)
		}
	}
	rank := func(xs []DataAccess) {
		sort.SliceStable(xs, func(i, j int) bool {
			if xs[i].Conf != xs[j].Conf {
				return xs[i].Conf > xs[j].Conf
			}
			if ti, tj := IsTestPath(xs[i].Path), IsTestPath(xs[j].Path); ti != tj {
				return !ti
			}
			if xs[i].Repo != xs[j].Repo {
				return xs[i].Repo == m.Repo || xs[j].Repo != m.Repo && xs[i].Repo < xs[j].Repo
			}
			if xs[i].Path != xs[j].Path {
				return xs[i].Path < xs[j].Path
			}
			return xs[i].Line < xs[j].Line
		})
	}
	rank(a.writes)
	rank(a.reads)
	rank(a.hooks)

	// Budget: the model itself first, then its edges, then the sites.
	spent := estTokens(m.Name+m.Table+m.Path+m.Handle) + 20
	keep := 0
	for _, f := range m.Fields {
		c := estTokens(f.Name+f.Type+f.Default+f.Column+f.Inherited) + 8
		if spent+c > budget && keep > 0 {
			break
		}
		spent += c
		keep++
	}
	if keep < len(m.Fields) {
		d.Omitted["fields"] = len(m.Fields) - keep
		d.Model.Fields = m.Fields[:keep]
	}
	for _, r := range m.Relations {
		spent += estTokens(r.Field+r.Kind+r.Target+r.Raw+r.Reverse) + 6
	}
	take := func(section string, n int, cost func(int) int) int {
		k := 0
		for ; k < n; k++ {
			c := cost(k)
			if spent+c > budget {
				break
			}
			spent += c
		}
		if k < n {
			d.Omitted[section] = n - k
		}
		return k
	}
	d.Incoming = d.Incoming[:take("incoming", len(d.Incoming), func(i int) int {
		r := d.Incoming[i]
		return estTokens(r.From+r.Field+r.Kind+r.Path) + 8
	})]
	d.Serializers = d.Serializers[:take("serializers", len(d.Serializers), func(i int) int {
		x := d.Serializers[i]
		return estTokens(x.Name+x.Path+x.Handle+strings.Join(x.Fields, ",")) + 8
	})]
	d.Routes = d.Routes[:take("routes", len(d.Routes), func(i int) int {
		r := d.Routes[i]
		return estTokens(r.Method+r.Pattern+r.Handler+r.Path) + 10
	})]
	acost := func(xs []DataAccess) func(int) int {
		return func(i int) int { x := xs[i]; return estTokens(x.Path+x.In+x.Handle+x.Op+x.Text) + 10 }
	}
	// Writers first: who mutates a table is the rarer, more decisive question.
	d.Writers = a.writes[:take("writers", len(a.writes), acost(a.writes))]
	d.Hooks = a.hooks[:take("hooks", len(a.hooks), acost(a.hooks))]
	d.Readers = a.reads[:take("readers", len(a.reads), acost(a.reads))]
	return d, spent
}
