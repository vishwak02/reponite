// models_py.go recognizes Python data models: Django models, DRF
// serializers, SQLAlchemy (declarative, 2.0 Mapped, Flask-SQLAlchemy) and
// SQLModel tables, Pydantic models and dataclasses (schemas). Classes are read
// with a small indentation-aware parser; a class's framework comes from its
// base chain, resolved through in-repo base classes first, so a project base
// named BaseModel that derives from models.Model is a Django model, not
// Pydantic.
package query

import (
	"path"
	"regexp"
	"strings"
)

type pyStmt struct {
	text string
	line int
}

type pyCls struct {
	name       string
	bases      []string          // as written, generics and kwargs removed
	kwargs     map[string]string // class keyword arguments (table=True)
	decorators []string
	line, end  int
	body       []pyStmt
	meta       map[string]string // the nested Meta / Config / model_config settings
	file       *File
	imports    string // the file's import lines, for framework evidence
}

var (
	pyClassHead = regexp.MustCompile(`^(\s*)class\s+(\w+)\s*([(:])`)
	pyAssignRe  = regexp.MustCompile(`^(\w+)\s*(?::\s*(.+?))?\s*=\s*(.+)$`)
	pyAnnRe     = regexp.MustCompile(`^(\w+)\s*:\s*(.+)$`)
	pyCallRe    = regexp.MustCompile(`^([\w.]+)\s*\(`)
)

func indentOf(s string) int {
	n := 0
	for _, c := range s {
		switch c {
		case ' ':
			n++
		case '\t':
			n += 8
		default:
			return n
		}
	}
	return n
}

// parenDelta is the bracket balance of a line outside strings.
func parenDelta(s string) int {
	d := 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if c == '\\' {
				i++
			} else if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'':
			q = c
		case '(', '[', '{':
			d++
		case ')', ']', '}':
			d--
		}
	}
	return d
}

// parsePyClasses reads every class of a Python file: header, decorators,
// top-level body statements (continuation lines joined) and the nested
// Meta/Config block.
func parsePyClasses(f *File) []*pyCls {
	src := stripPyComments(f.Content)
	lines := strings.Split(src, "\n")
	var imports []string
	for _, ln := range lines {
		if t := strings.TrimSpace(ln); strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "from ") {
			imports = append(imports, t)
		}
	}
	imp := strings.Join(imports, "\n")
	var out []*pyCls
	for i := 0; i < len(lines); i++ {
		m := pyClassHead.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		ind := len(m[1])
		c := &pyCls{name: m[2], line: i + 1, kwargs: map[string]string{}, meta: map[string]string{}, file: f, imports: imp}
		head := lines[i]
		j := i
		for parenDelta(head) > 0 && j+1 < len(lines) && j < i+20 {
			j++
			head += " " + strings.TrimSpace(lines[j])
		}
		if m[3] == "(" {
			open := strings.Index(head, "(")
			for _, a := range splitArgs(callArgs(head, open+1)) {
				a = strings.TrimSpace(a)
				if a == "" {
					continue
				}
				if k, v, ok := strings.Cut(a, "="); ok && pyIdent(strings.TrimSpace(k)) {
					c.kwargs[strings.TrimSpace(k)] = strings.TrimSpace(v)
					continue
				}
				if k := strings.Index(a, "["); k > 0 {
					a = a[:k]
				}
				c.bases = append(c.bases, a)
			}
		}
		for k := i - 1; k >= 0; k-- {
			t := strings.TrimSpace(lines[k])
			if strings.HasPrefix(t, "@") && indentOf(lines[k]) == ind {
				c.decorators = append(c.decorators, t)
				continue
			}
			if t == "" || strings.HasPrefix(t, ")") || indentOf(lines[k]) > ind {
				continue // a multi-line decorator's argument lines
			}
			break
		}
		// Body: every following line indented deeper (blank lines included).
		bodyInd := -1
		end := j
		for k := j + 1; k < len(lines); k++ {
			if strings.TrimSpace(lines[k]) == "" {
				continue
			}
			if indentOf(lines[k]) <= ind {
				break
			}
			end = k
			if bodyInd < 0 {
				bodyInd = indentOf(lines[k])
			}
		}
		c.end = end + 1
		inMeta := ""
		metaInd := 0
		for k := j + 1; k <= end; k++ {
			t := strings.TrimSpace(lines[k])
			if t == "" {
				continue
			}
			li := indentOf(lines[k])
			if inMeta != "" && li > metaInd {
				stmt, n := joinStmt(lines, k)
				if a := pyAssignRe.FindStringSubmatch(stmt); a != nil {
					c.meta[a[1]] = strings.TrimSpace(a[3])
				}
				k = n
				continue
			}
			inMeta = ""
			if li != bodyInd {
				continue
			}
			if mm := regexp.MustCompile(`^class\s+(Meta|Config)\b`).FindStringSubmatch(t); mm != nil {
				inMeta, metaInd = mm[1], li
				continue
			}
			stmt, n := joinStmt(lines, k)
			c.body = append(c.body, pyStmt{text: stmt, line: k + 1})
			// pydantic v2: model_config = ConfigDict(from_attributes=True, …)
			if a := pyAssignRe.FindStringSubmatch(stmt); a != nil && a[1] == "model_config" {
				if call := pyCallRe.FindStringIndex(a[3]); call != nil {
					for _, kv := range splitArgs(callArgs(a[3], call[1])) {
						if k2, v2, ok := strings.Cut(kv, "="); ok {
							c.meta[strings.TrimSpace(k2)] = strings.TrimSpace(v2)
						}
					}
				}
			}
			k = n
		}
		out = append(out, c)
	}
	return out
}

func pyIdent(s string) bool { return s != "" && regexp.MustCompile(`^[A-Za-z_]\w*$`).MatchString(s) }

// joinStmt joins a statement starting at lines[k] with its continuation
// lines (open brackets or a trailing backslash); returns the last line used.
func joinStmt(lines []string, k int) (string, int) {
	s := strings.TrimSpace(lines[k])
	d := parenDelta(s)
	n := k
	for (d > 0 || strings.HasSuffix(s, "\\")) && n+1 < len(lines) && n < k+40 {
		n++
		t := strings.TrimSpace(lines[n])
		s = strings.TrimSuffix(s, "\\") + " " + t
		d += parenDelta(t)
	}
	return s, n
}

// pyCall splits `callee(args…)` into the callee, positional and keyword
// arguments; ok=false when expr is not a call.
func pyCall(expr string) (callee string, pos []string, kw map[string]string, ok bool) {
	expr = strings.TrimSpace(expr)
	m := pyCallRe.FindStringSubmatchIndex(expr)
	if m == nil {
		return "", nil, nil, false
	}
	callee = expr[m[2]:m[3]]
	kw = map[string]string{}
	for _, a := range splitArgs(callArgs(expr, m[1])) {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if k, v, ok := strings.Cut(a, "="); ok && pyIdent(strings.TrimSpace(k)) && !strings.HasPrefix(v, "=") {
			kw[strings.TrimSpace(k)] = strings.TrimSpace(v)
			continue
		}
		pos = append(pos, a)
	}
	return callee, pos, kw, true
}

func isTrue(v string) bool { return strings.TrimSpace(v) == "True" || strings.TrimSpace(v) == "true" }

// Python framework categories.
const (
	catDjango     = "django"
	catSerializer = "drf"
	catSQLA       = "sqlalchemy"
	catSQLModel   = "sqlmodel"
	catPydantic   = "pydantic"
	catSettings   = "settings" // pydantic BaseSettings: configuration, not a model
	catDataclass  = "dataclass"
)

// External bases by framework (last segment of the base as written, or the
// full dotted spelling).
var pyExternalBase = map[string]string{
	"models.Model": catDjango, "django.db.models.Model": catDjango, "AbstractUser": catDjango, "AbstractBaseUser": catDjango,
	"PermissionsMixin": catDjango, "MPTTModel": catDjango, "PolymorphicModel": catDjango, "TimeStampedModel": catDjango,
	"serializers.ModelSerializer": catSerializer, "serializers.HyperlinkedModelSerializer": catSerializer,
	"serializers.Serializer": catSerializer, "ModelSerializer": catSerializer, "HyperlinkedModelSerializer": catSerializer,
	"GeoFeatureModelSerializer": catSerializer, "serializers.ListSerializer": catSerializer,
	"DeclarativeBase": catSQLA, "DeclarativeBaseNoMeta": catSQLA, "db.Model": catSQLA,
	"SQLModel": catSQLModel, "sqlmodel.SQLModel": catSQLModel,
	"pydantic.BaseModel": catPydantic, "BaseSettings": catSettings, "pydantic_settings.BaseSettings": catSettings,
	"pydantic.BaseSettings": catSettings,
}

type pyRepo struct {
	classes map[string][]*pyCls
	cat     map[*pyCls]string
	sqlBase map[string]bool // names bound to declarative_base()
}

func (r *pyRepo) lookup(c *pyCls, name string) *pyCls {
	if _, ext := pyExternalBase[name]; ext && strings.Contains(name, ".") {
		return nil // models.Model is the framework's, whatever the repo names "Model"
	}
	name = lastDot(name)
	cs := r.classes[name]
	for _, x := range cs {
		if x.file == c.file && x != c {
			return x
		}
	}
	for _, x := range cs {
		if x != c {
			return x
		}
	}
	return nil
}

// category resolves a class's framework through its base chain.
func (r *pyRepo) category(c *pyCls, depth int) string {
	if v, ok := r.cat[c]; ok {
		return v
	}
	r.cat[c] = "" // cycle guard
	cat := ""
	for _, d := range c.decorators {
		if strings.Contains(d, "dataclass") {
			cat = catDataclass
		}
	}
	for _, b := range c.bases {
		if cat != "" || depth > 12 {
			break
		}
		if x := r.lookup(c, b); x != nil {
			cat = r.category(x, depth+1)
			if cat != "" {
				continue
			}
		}
		if v, ok := pyExternalBase[b]; ok {
			cat = v
		} else if v, ok := pyExternalBase[lastDot(b)]; ok && strings.Contains(b, ".") == false {
			cat = v
		} else if r.sqlBase[b] {
			cat = catSQLA
		} else if lastDot(b) == "BaseModel" && strings.Contains(c.imports, "pydantic") {
			cat = catPydantic
		} else if strings.HasSuffix(b, "Serializer") && strings.Contains(b, "serializers.") {
			cat = catSerializer
		}
	}
	if cat == "" {
		cat = r.evidence(c)
	}
	r.cat[c] = cat
	return cat
}

// evidence classifies a class with no recognizable base by what its body
// declares (a mixin of Django fields, a SQLAlchemy __tablename__).
func (r *pyRepo) evidence(c *pyCls) string {
	for _, s := range c.body {
		a := pyAssignRe.FindStringSubmatch(s.text)
		if a == nil {
			continue
		}
		if a[1] == "__tablename__" {
			return catSQLA
		}
		callee, _, _, ok := pyCall(a[3])
		if !ok {
			continue
		}
		if strings.HasPrefix(callee, "models.") && djangoFieldName(lastDot(callee)) && strings.Contains(c.imports, "django") {
			return catDjango
		}
	}
	return ""
}

func djangoFieldName(n string) bool {
	return strings.HasSuffix(n, "Field") || n == "ForeignKey" || n == "GenericRelation" || n == "TaggableManager"
}

var pyDeclBase = regexp.MustCompile(`(?m)^(\w+)\s*=\s*(?:[\w.]*\.)?(?:declarative_base|as_declarative)\(`)

// pyModels runs the Python detectors over a repo.
func pyModels(repo string, files []File) ([]DataModel, []DataSerializer) {
	r := &pyRepo{classes: map[string][]*pyCls{}, cat: map[*pyCls]string{}, sqlBase: map[string]bool{}}
	var all []*pyCls
	for i := range files {
		f := &files[i]
		if path.Ext(f.Path) != ".py" || strings.Contains(f.Path, "/migrations/") || !strings.Contains(f.Content, "class ") {
			continue
		}
		for _, m := range pyDeclBase.FindAllStringSubmatch(f.Content, -1) {
			r.sqlBase[m[1]] = true
		}
		for _, c := range parsePyClasses(f) {
			r.classes[c.name] = append(r.classes[c.name], c)
			all = append(all, c)
		}
	}
	var ms []DataModel
	var ss []DataSerializer
	byClass := map[*pyCls]*DataModel{}
	for _, c := range all {
		cat := r.category(c, 0)
		switch cat {
		case catDjango:
			ms = append(ms, djangoModel(repo, c, r))
		case catSQLA, catSQLModel:
			if m, ok := sqlaModel(repo, c, cat); ok {
				ms = append(ms, m)
			}
		case catPydantic, catDataclass:
			if m, ok := schemaModel(repo, c, cat); ok {
				ms = append(ms, m)
			}
		case catSerializer:
			if s, m, ok := drfSerializer(repo, c, r); ok {
				ss = append(ss, s)
			} else if ok := len(m.Fields) > 0; ok {
				ms = append(ms, m)
			}
		}
	}
	for i := range ms {
		for _, c := range r.classes[ms[i].Name] {
			if c.file.Path == ms[i].Path && c.line == ms[i].Line {
				byClass[c] = &ms[i]
			}
		}
	}
	inheritPy(ms, byClass, r)
	return ms, ss
}

// inheritPy copies fields from in-repo bases of the same framework (an
// abstract Django model, a mixin, a base schema) and, for Django multi-table
// inheritance, records the parent link and adds the implicit primary key.
func inheritPy(ms []DataModel, byClass map[*pyCls]*DataModel, r *pyRepo) {
	for c, m := range byClass {
		seen := map[string]bool{}
		for _, f := range m.Fields {
			seen[f.Name] = true
		}
		var walk func(x *pyCls, depth int)
		walk = func(x *pyCls, depth int) {
			if depth > 8 {
				return
			}
			for _, b := range x.bases {
				bc := r.lookup(x, b)
				if bc == nil {
					continue
				}
				bm := byClass[bc]
				if bm == nil || bm.Framework != m.Framework {
					walk(bc, depth+1)
					continue
				}
				if m.Framework == "django" && bm.Kind == KindTable {
					if bm.proxy || m.proxy {
						if m.proxy {
							m.Table = bm.Table
						}
					} else {
						m.Relations = append(m.Relations, DataRelation{Field: snakeCase(bm.Name) + "_ptr", Kind: "parent", Target: bm.Name, Raw: bm.Name, Line: m.Line})
					}
					continue
				}
				for _, f := range bm.Fields {
					if !seen[f.Name] && !f.Implicit {
						seen[f.Name] = true
						if f.Inherited == "" {
							f.Inherited = bm.Name
						}
						m.Fields = append(m.Fields, f)
					}
				}
				for _, rel := range bm.Relations {
					if rel.Kind != "parent" {
						m.Relations = append(m.Relations, rel)
					}
				}
				m.managers = append(m.managers, bm.managers...)
				walk(bc, depth+1)
			}
		}
		walk(c, 0)
		if m.Framework == "django" && m.Kind == KindTable && !m.proxy {
			hasPK := false
			for _, f := range m.Fields {
				hasPK = hasPK || f.PK
			}
			for _, rel := range m.Relations {
				hasPK = hasPK || rel.Kind == "parent"
			}
			if !hasPK {
				m.Fields = append([]DataField{{Name: "id", Type: "AutoField", PK: true, Unique: true, Line: m.Line, Implicit: true}}, m.Fields...)
			}
		}
	}
}

func unquoteLit(v string) string {
	if s, ok := pyStringLit(v); ok {
		return s
	}
	return strings.TrimSpace(v)
}

// djangoAppLabel is the app a models module belongs to: the directory of
// models.py, or the parent of a models/ package.
func djangoAppLabel(p string) string {
	d := path.Dir(p)
	if path.Base(d) == "models" {
		d = path.Dir(d)
	}
	return path.Base(d)
}

func djangoModel(repo string, c *pyCls, r *pyRepo) DataModel {
	m := DataModel{Name: c.name, Kind: KindTable, Framework: "django", Repo: repo, Path: c.file.Path, Line: c.line, EndLine: c.end,
		Bases: c.bases, Test: IsTestPath(c.file.Path)}
	if isTrue(c.meta["abstract"]) {
		m.Kind = KindAbstract
	}
	if isTrue(c.meta["proxy"]) {
		m.proxy = true
	}
	// A class reaching Django only by the fields it declares (a mixin with no
	// models.Model in its chain) cannot be a table of its own.
	if !djangoChain(c, r, 0) {
		m.Kind = KindAbstract
	}
	label := djangoAppLabel(c.file.Path)
	if v, ok := c.meta["app_label"]; ok {
		label = unquoteLit(v)
	}
	m.Table = strings.ToLower(label + "_" + c.name)
	if v, ok := c.meta["db_table"]; ok {
		m.Table = unquoteLit(v)
	}
	if m.Kind == KindAbstract {
		m.Table = ""
	}
	indexed := map[string]bool{}
	if v := c.meta["indexes"]; v != "" {
		for _, mm := range regexp.MustCompile(`fields\s*=\s*\[\s*["']-?(\w+)["']`).FindAllStringSubmatch(v, -1) {
			indexed[mm[1]] = true
		}
	}
	for _, s := range c.body {
		a := pyAssignRe.FindStringSubmatch(s.text)
		if a == nil || strings.HasPrefix(a[1], "_") {
			continue
		}
		callee, pos, kw, ok := pyCall(a[3])
		if !ok {
			continue
		}
		typ := lastDot(callee)
		if strings.HasSuffix(typ, "Manager") || strings.HasSuffix(callee, ".as_manager") || strings.HasSuffix(callee, "from_queryset") {
			if a[1] != "objects" {
				m.managers = append(m.managers, a[1])
			}
			continue
		}
		if !djangoFieldName(typ) && typ != "ForeignKey" {
			continue
		}
		f := DataField{Name: a[1], Type: typ, Line: s.line, Null: isTrue(kw["null"]), Unique: isTrue(kw["unique"]),
			Index: isTrue(kw["db_index"]) || indexed[a[1]], PK: isTrue(kw["primary_key"])}
		if v, ok := kw["default"]; ok {
			f.Default = v
		}
		if v, ok := kw["db_column"]; ok {
			f.Column = unquoteLit(v)
		}
		if f.PK {
			f.Unique = true
		}
		kind := map[string]string{"ForeignKey": "fk", "OneToOneField": "o2o", "ManyToManyField": "m2m", "ParentalKey": "fk",
			"TreeForeignKey": "fk", "ParentalManyToManyField": "m2m", "GenericRelation": "has_many"}[typ]
		if kind != "" {
			target := kw["to"]
			if len(pos) > 0 {
				target = pos[0]
			}
			raw := unquoteLit(target)
			rel := DataRelation{Field: a[1], Kind: kind, Raw: raw, Line: s.line}
			if rn, ok := kw["related_name"]; ok {
				rel.Reverse = unquoteLit(rn)
			}
			if kind == "fk" || kind == "o2o" {
				f.Index = true
				if f.Column == "" {
					f.Column = a[1] + "_id"
				}
				if kind == "o2o" {
					f.Unique = true
				}
			}
			f.Type = typ + "(" + raw + ")"
			m.Relations = append(m.Relations, rel)
			if kind == "m2m" || kind == "has_many" {
				continue // no column on this table
			}
		}
		m.Fields = append(m.Fields, f)
	}
	return m
}

// djangoChain: the class reaches a real Django model base (models.Model or a
// known third-party model) through its bases.
func djangoChain(c *pyCls, r *pyRepo, depth int) bool {
	if depth > 12 {
		return false
	}
	for _, b := range c.bases {
		if x := r.lookup(c, b); x != nil {
			if djangoChain(x, r, depth+1) {
				return true
			}
			continue
		}
		if pyExternalBase[b] == catDjango || pyExternalBase[lastDot(b)] == catDjango && !strings.Contains(b, ".") {
			return true
		}
	}
	return false
}

// drfSerializer reads a DRF serializer: a ModelSerializer's Meta.model and
// fields (ok=true), or a plain Serializer's declared fields as a schema.
func drfSerializer(repo string, c *pyCls, r *pyRepo) (DataSerializer, DataModel, bool) {
	model := c.meta["model"]
	fieldsSpec, exclude := c.meta["fields"], c.meta["exclude"]
	// Meta inherited from an in-repo base serializer.
	for x, depth := c, 0; model == "" && depth < 8; depth++ {
		var next *pyCls
		for _, b := range x.bases {
			if bc := r.lookup(x, b); bc != nil {
				next = bc
				break
			}
		}
		if next == nil {
			break
		}
		model = next.meta["model"]
		if fieldsSpec == "" {
			fieldsSpec = next.meta["fields"]
		}
		x = next
	}
	var declared []DataField
	for _, s := range c.body {
		a := pyAssignRe.FindStringSubmatch(s.text)
		if a == nil || strings.HasPrefix(a[1], "_") {
			continue
		}
		callee, _, kw, ok := pyCall(a[3])
		if !ok {
			continue
		}
		t := lastDot(callee)
		if !strings.HasSuffix(t, "Field") && !strings.HasSuffix(t, "Serializer") && !strings.HasPrefix(callee, "serializers.") {
			continue
		}
		f := DataField{Name: a[1], Type: t, Line: s.line, Null: isTrue(kw["allow_null"])}
		if v, ok := kw["default"]; ok {
			f.Default = v
		}
		declared = append(declared, f)
	}
	if model != "" {
		s := DataSerializer{Name: c.name, Model: lastDot(strings.TrimSpace(model)), Repo: repo, Path: c.file.Path, Line: c.line}
		s.Fields = pyStringList(fieldsSpec)
		s.Exclude = pyStringList(exclude)
		for _, f := range declared {
			if !containsStr(s.Fields, f.Name) {
				s.Fields = append(s.Fields, f.Name)
			}
		}
		return s, DataModel{}, true
	}
	m := DataModel{Name: c.name, Kind: KindSchema, Framework: "drf", Repo: repo, Path: c.file.Path, Line: c.line, EndLine: c.end,
		Bases: c.bases, Fields: declared, Test: IsTestPath(c.file.Path)}
	return DataSerializer{}, m, false
}

// pyStringList reads ["a", "b"] / ("a", "b") / "__all__" / a single name.
func pyStringList(v string) []string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	if s, ok := pyStringLit(v); ok {
		return []string{s}
	}
	var out []string
	for _, m := range regexp.MustCompile(`["'](\w+)["']`).FindAllStringSubmatch(v, -1) {
		out = append(out, m[1])
	}
	if len(out) == 0 {
		out = []string{v} // a name bound elsewhere (fields = BASE_FIELDS + …)
	}
	return out
}

var (
	mappedRe   = regexp.MustCompile(`^Mapped\[\s*(.+)\s*\]$`)
	optionalRe = regexp.MustCompile(`^(?:Optional\[\s*(.+)\s*\]|(.+?)\s*\|\s*None|None\s*\|\s*(.+))$`)
	listAnnRe  = regexp.MustCompile(`^(?:List|list|Set|set|Sequence|Tuple|tuple|Iterable|FrozenSet|frozenset)\[\s*(.+?)\s*(?:,\s*\.\.\.)?\]$`)
	sqlaFKRe   = regexp.MustCompile(`ForeignKey\(\s*["']([\w.]+)["']`)
)

// unwrapAnn strips Optional/| None and reports nullability.
func unwrapAnn(a string) (string, bool) {
	a = strings.TrimSpace(a)
	if m := optionalRe.FindStringSubmatch(a); m != nil {
		for _, x := range m[1:] {
			if x != "" {
				return strings.TrimSpace(x), true
			}
		}
	}
	return a, false
}

// annTarget is the model an annotation names ("Address", List["Address"]) and
// whether it is a collection.
func annTarget(a string) (string, bool) {
	a, _ = unwrapAnn(a)
	many := false
	if m := listAnnRe.FindStringSubmatch(a); m != nil {
		a, many = m[1], true
	}
	a, _ = unwrapAnn(a)
	return strings.Trim(a, `"' `), many
}

func sqlaModel(repo string, c *pyCls, cat string) (DataModel, bool) {
	fw := "sqlalchemy"
	if cat == catSQLModel {
		fw = "sqlmodel"
	}
	m := DataModel{Name: c.name, Kind: KindTable, Framework: fw, Repo: repo, Path: c.file.Path, Line: c.line, EndLine: c.end,
		Bases: c.bases, Test: IsTestPath(c.file.Path)}
	if cat == catSQLModel {
		if !isTrue(c.kwargs["table"]) {
			m.Kind = KindSchema
		}
		m.Table = strings.ToLower(c.name)
	} else {
		m.Table = snakeCase(c.name)
	}
	hasTable := false
	for _, s := range c.body {
		var name, ann, val string
		if a := pyAssignRe.FindStringSubmatch(s.text); a != nil {
			name, ann, val = a[1], a[2], a[3]
		} else if a := pyAnnRe.FindStringSubmatch(s.text); a != nil {
			name, ann = a[1], a[2]
		} else {
			continue
		}
		switch name {
		case "__tablename__":
			m.Table, hasTable = unquoteLit(val), true
			continue
		case "__abstract__":
			if isTrue(val) {
				m.Kind = KindAbstract
			}
			continue
		case "__table_args__", "model_config":
			continue
		}
		if strings.HasPrefix(name, "_") {
			continue
		}
		inner := ann
		if mm := mappedRe.FindStringSubmatch(strings.TrimSpace(ann)); mm != nil {
			inner = mm[1]
		}
		callee, pos, kw, isCall := pyCall(val)
		fn := lastDot(callee)
		if fn == "relationship" || fn == "Relationship" {
			target := ""
			if len(pos) > 0 {
				target = unquoteLit(pos[0])
			}
			t, many := annTarget(inner)
			if target == "" {
				target = t
			}
			kind := "relationship"
			switch {
			case kw["secondary"] != "" || kw["link_model"] != "":
				kind = "m2m"
			case kw["uselist"] == "False":
				kind = "to_one"
			case many:
				kind = "to_many"
			case t != "" && inner != "":
				kind = "to_one"
			}
			rel := DataRelation{Field: name, Kind: kind, Raw: target, Line: s.line, Reverse: unquoteLit(kw["back_populates"])}
			if rel.Reverse == "" {
				rel.Reverse = unquoteLit(kw["backref"])
			}
			m.Relations = append(m.Relations, rel)
			continue
		}
		isCol := isCall && (fn == "Column" || fn == "mapped_column" || fn == "Field")
		if !isCol && !(val == "" && strings.HasPrefix(strings.TrimSpace(ann), "Mapped[")) && !(cat == catSQLModel && ann != "") {
			continue
		}
		typ, nullable := unwrapAnn(inner)
		f := DataField{Name: name, Type: typ, Line: s.line}
		if isCol {
			for i, p := range pos {
				if lit, ok := pyStringLit(p); ok && i == 0 {
					f.Column = lit
					continue
				}
				if fk := sqlaFKRe.FindStringSubmatch(p); fk != nil {
					m.Relations = append(m.Relations, DataRelation{Field: name, Kind: "fk", Raw: fk[1], Line: s.line})
					f.Index = true
					continue
				}
				if f.Type == "" || fn == "Column" && i <= 1 {
					f.Type = strings.TrimPrefix(strings.TrimPrefix(p, "sa."), "db.")
				}
			}
			if v, ok := kw["foreign_key"]; ok {
				m.Relations = append(m.Relations, DataRelation{Field: name, Kind: "fk", Raw: unquoteLit(v), Line: s.line})
			}
			f.PK = isTrue(kw["primary_key"])
			f.Unique = isTrue(kw["unique"]) || f.PK
			f.Index = f.Index || isTrue(kw["index"])
			if v, ok := kw["default"]; ok {
				f.Default = v
			} else if v, ok := kw["server_default"]; ok {
				f.Default = v
			} else if fn == "Field" && len(pos) > 0 && pos[0] != "..." {
				f.Default = pos[0]
			}
			if v, ok := kw["nullable"]; ok {
				f.Null = isTrue(v)
			} else {
				f.Null = !f.PK && (nullable || fn == "Column" && ann == "")
			}
		} else {
			f.Null = nullable
			if val != "" {
				f.Default = val
			}
		}
		m.Fields = append(m.Fields, f)
	}
	if len(m.Fields) == 0 && len(m.Relations) == 0 && !hasTable {
		return m, false // a declarative base or an empty mixin
	}
	if cat == catSQLA && !hasTable && m.Kind == KindTable && !strings.Contains(strings.Join(c.bases, ","), "db.Model") {
		m.Kind = KindAbstract // a mixin: SQLAlchemy needs __tablename__ for a table
		m.Table = ""
	}
	return m, true
}

func schemaModel(repo string, c *pyCls, cat string) (DataModel, bool) {
	m := DataModel{Name: c.name, Kind: KindSchema, Framework: cat, Repo: repo, Path: c.file.Path, Line: c.line, EndLine: c.end,
		Bases: c.bases, Test: IsTestPath(c.file.Path)}
	for _, s := range c.body {
		var name, ann, val string
		if a := pyAssignRe.FindStringSubmatch(s.text); a != nil && a[2] != "" {
			name, ann, val = a[1], a[2], a[3]
		} else if a := pyAnnRe.FindStringSubmatch(s.text); a != nil && !strings.Contains(a[2], "=") {
			name, ann = a[1], a[2]
		} else {
			continue
		}
		if strings.HasPrefix(name, "_") || name == "model_config" || strings.HasPrefix(strings.TrimSpace(ann), "ClassVar") {
			continue
		}
		typ, null := unwrapAnn(ann)
		f := DataField{Name: name, Type: typ, Null: null, Line: s.line}
		if val != "" {
			f.Default = val
			if callee, pos, kw, ok := pyCall(val); ok && (lastDot(callee) == "Field" || lastDot(callee) == "field") {
				f.Default = ""
				if v, ok := kw["default"]; ok {
					f.Default = v
				} else if v, ok := kw["default_factory"]; ok {
					f.Default = v + "()"
				} else if len(pos) > 0 && pos[0] != "..." {
					f.Default = pos[0]
				}
				if v, ok := kw["alias"]; ok {
					f.Column = unquoteLit(v)
				}
			}
			if strings.TrimSpace(val) == "None" {
				f.Null = true
			}
		}
		if t, many := annTarget(ann); t != "" && pyIdent(t) && t[0] >= 'A' && t[0] <= 'Z' && !pyBuiltinType[t] {
			kind := "ref"
			if many {
				kind = "ref_many"
			}
			m.Relations = append(m.Relations, DataRelation{Field: name, Kind: kind, Raw: t, Line: s.line})
		}
		m.Fields = append(m.Fields, f)
	}
	if len(m.Fields) == 0 {
		return m, false
	}
	return m, true
}

var pyBuiltinType = map[string]bool{"Any": true, "Dict": true, "List": true, "Optional": true, "Union": true, "Literal": true,
	"UUID": true, "Decimal": true, "Path": true, "Enum": true, "Json": true, "EmailStr": true, "HttpUrl": true, "AnyUrl": true,
	"SecretStr": true, "Callable": true, "Type": true, "Mapping": true, "PositiveInt": true, "IPvAnyAddress": true,
	"datetime": true, "date": true, "time": true, "timedelta": true, "Annotated": true, "Set": true, "Tuple": true}
