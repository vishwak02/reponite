// models.go is the data layer of a codebase: which entities it persists or
// exchanges, their fields and relations, and the code that reads and writes
// them. ORM models (Django, SQLAlchemy/SQLModel, TypeORM/MikroORM/
// sequelize-typescript, Prisma, GORM), SQL DDL (CREATE/ALTER TABLE across
// migrations), DRF serializers, request/response schemas (Pydantic,
// dataclasses, plain DRF serializers) and wire schemas (protobuf messages) are
// recognized from source text; a model's readers and writers are its ORM
// call sites (query/save/update/delete/bulk), raw SQL naming its table
// anywhere in the fleet, and the API routes (routes.go) whose handler touches
// it or one of its serializers. Pure over Store.Files (ADR-018). Detection is
// by framework idiom, so it is medium confidence by nature, and the result
// says which frameworks it recognized instead of guessing at the rest.
package query

import (
	"fmt"
	"path"
	"sort"
	"strings"
)

// DefaultModelsBudget is the token budget when the caller passes <= 0.
const DefaultModelsBudget = 4000

// Model kinds.
const (
	KindTable    = "table"    // persisted (an ORM model or a CREATE TABLE)
	KindView     = "view"     // a SQL view
	KindAbstract = "abstract" // an ORM base/mixin: its fields land in subclasses' tables
	KindSchema   = "schema"   // a request/response shape (Pydantic, dataclass, plain serializer)
	KindWire     = "wire"     // a wire format (protobuf message)
)

// DataField is one field/column of a model.
type DataField struct {
	Name      string
	Type      string
	Column    string // the stored column name when it differs from Name
	Null      bool
	Unique    bool
	Index     bool
	PK        bool
	Default   string
	Line      int
	Inherited string // the base class the field comes from
	Implicit  bool   // added by the framework (Django's id), not written
}

// DataRelation is a relation from one model to another.
type DataRelation struct {
	Field   string
	Kind    string // fk, o2o, m2m, has_many, has_one, to_many, to_one, relationship, parent, ref, ref_many
	Target  string // the target model's name ("" when unresolved: see Raw)
	Raw     string // the target as written ("app.Customer", "users.id", settings.AUTH_USER_MODEL)
	Reverse string // the accessor on the other side (related_name, back_populates, inverse property)
	Line    int
	// Set on incoming relations: the model holding the field.
	From       string
	Repo, Path string
}

// DataModel is one entity.
type DataModel struct {
	Name      string
	Kind      string
	Framework string
	Table     string
	Package   string // protobuf package
	Repo      string
	Path      string
	Line      int
	EndLine   int
	Handle    string // the reponite symbol id, for brief/context ("" when not a symbol)
	Bases     []string
	Fields    []DataField
	Relations []DataRelation
	Sources   []string // further files that shape it (SQL migrations altering the table)
	Test      bool

	managers []string // extra Django manager attributes (objects is implicit)
	proxy    bool
}

// DataSerializer is a serializer that exposes a model (DRF ModelSerializer).
type DataSerializer struct {
	Name    string
	Model   string
	Fields  []string // declared Meta.fields ("__all__" kept as written), plus declared fields
	Exclude []string
	Repo    string
	Path    string
	Line    int
	Handle  string
}

// DataAccess is one code site that reads or writes a model.
type DataAccess struct {
	Repo, Path string
	Line       int
	In         string // enclosing symbol
	Handle     string // its reponite id, for brief/context
	Op         string // the ORM method / SQL verb (filter, create, save, SELECT, …)
	Via        string // how it was recognized (orm, related-manager, instance-name, raw-sql, repository, …)
	Conf       float64
	Text       string
}

// DataRoute is an API route whose handler touches the model.
type DataRoute struct {
	Repo, Path string
	Line       int
	Method     string
	Pattern    string
	Handler    string
	Framework  string
	Via        string // the model/serializer name the handler mentions
}

// ModelDetail is everything known about one model.
type ModelDetail struct {
	Model       DataModel
	Incoming    []DataRelation
	Serializers []DataSerializer
	Routes      []DataRoute
	Writers     []DataAccess
	Readers     []DataAccess
	Hooks       []DataAccess // signal/event handlers bound to the model
	Omitted     map[string]int
}

// ModelsResult is the ER summary (no model named) or one model's detail.
type ModelsResult struct {
	Query      string
	Models     []DataModel   // list mode, ranked
	Details    []ModelDetail // focus mode: every model matching the query
	Total      int           // models found (before budget/kind filtering)
	Frameworks map[string]int
	Suggest    []string // near names when the query matched nothing
	Truncated  bool
	Omitted    int
	Next       string
	Note       string
	Meta       Meta
}

// ModelsOptions narrows and bounds a models query.
type ModelsOptions struct {
	Budget int    // tokens; <= 0 = DefaultModelsBudget
	Kind   string // keep one kind (table/schema/wire/abstract/view); "" = all
}

// Models lists the data models across repo (FleetRepo = every repo) at ref,
// or, when name is set, details the model(s) of that name (or table).
func Models(s Store, repo, ref, name string, opt ModelsOptions) ModelsResult {
	if opt.Budget <= 0 {
		opt.Budget = DefaultModelsBudget
	}
	res := ModelsResult{Query: name, Frameworks: map[string]int{}, Meta: NewMeta(s, repo, ref)}
	files := map[string][]File{}
	var all []DataModel
	var sers []DataSerializer
	for _, rp := range reposFor(s, repo) {
		fs := s.Files(rp, ref)
		files[rp] = fs
		ms, ss := detectModels(rp, fs)
		all = append(all, ms...)
		sers = append(sers, ss...)
	}
	setHandles(s, ref, all, sers)
	for _, m := range all {
		res.Frameworks[m.Framework]++
	}
	res.Total = len(all)
	if name == "" {
		listModels(&res, all, sers, opt)
		return res
	}
	matches := matchModels(all, name)
	if len(matches) == 0 {
		res.Suggest = nearModels(all, name, 8)
		res.Note = fmt.Sprintf("no model or table named %q", name)
		if len(all) == 0 {
			res.Note += " — no models recognized at this ref (" + recognizedFrameworks + ")"
		}
		return res
	}
	budget := opt.Budget
	for i, m := range matches {
		share := budget / (len(matches) - i)
		d, spent := modelDetail(s, ref, m, all, sers, files, share)
		budget -= spent
		res.Details = append(res.Details, d)
		for _, n := range d.Omitted {
			res.Omitted += n
		}
	}
	if res.Omitted > 0 {
		res.Truncated = true
		res.Next = fmt.Sprintf("%d site(s) omitted for budget — rerun with --budget %d", res.Omitted, opt.Budget*2)
	}
	res.Note = "fields and relations are read from the framework's declarations; readers/writers are ORM call sites naming the model (direct manager/session/repository calls; an instance variable named after the model and related managers at lower confidence), raw SQL naming its table fleet-wide, and routes whose handler mentions the model or one of its serializers — medium confidence: a model reached only through a variable of unknown type is missed"
	return res
}

const recognizedFrameworks = "recognized: Django, DRF serializers, SQLAlchemy/SQLModel, Pydantic, dataclasses, TypeORM/MikroORM/sequelize-typescript, Prisma, GORM, SQL DDL (.sql), protobuf"

var modelKindRank = map[string]int{KindTable: 0, KindView: 1, KindAbstract: 2, KindSchema: 3, KindWire: 4}

func listModels(res *ModelsResult, all []DataModel, sers []DataSerializer, opt ModelsOptions) {
	degree := map[string]int{}
	for _, m := range all {
		for _, r := range m.Relations {
			degree[m.Repo+"\x00"+m.Name]++
			if r.Target != "" {
				degree[m.Repo+"\x00"+r.Target]++
			}
		}
	}
	nser := map[string]int{}
	for _, x := range sers {
		nser[x.Repo+"\x00"+x.Model]++
	}
	var keep []DataModel
	for _, m := range all {
		if opt.Kind != "" && m.Kind != opt.Kind {
			continue
		}
		keep = append(keep, m)
	}
	sort.SliceStable(keep, func(i, j int) bool {
		a, b := keep[i], keep[j]
		if modelKindRank[a.Kind] != modelKindRank[b.Kind] {
			return modelKindRank[a.Kind] < modelKindRank[b.Kind]
		}
		da, db := degree[a.Repo+"\x00"+a.Name]+nser[a.Repo+"\x00"+a.Name], degree[b.Repo+"\x00"+b.Name]+nser[b.Repo+"\x00"+b.Name]
		if da != db {
			return da > db
		}
		if a.Repo != b.Repo {
			return a.Repo < b.Repo
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
	spent := 60
	for i, m := range keep {
		cost := modelListCost(m)
		if spent+cost > opt.Budget && i > 0 {
			res.Truncated = true
			res.Omitted = len(keep) - i
			res.Next = fmt.Sprintf("%d more model(s) — rerun with --budget %d, filter with --kind table|schema|wire, or name one: models <Name>", res.Omitted, opt.Budget*2)
			break
		}
		spent += cost
		res.Models = append(res.Models, m)
	}
	switch {
	case len(all) == 0:
		res.Note = "no data models recognized at this ref (" + recognizedFrameworks + ") — the repo may use another framework, or not be indexed"
	default:
		res.Note = "ranked: tables first, then by how connected each model is; name a model (models <Name>) for its fields, relations both ways, serializers, routes, readers and writers"
	}
}

func modelListCost(m DataModel) int {
	c := estTokens(m.Name+m.Table+m.Path+m.Framework+m.Handle) + 12
	for _, r := range m.Relations {
		c += estTokens(r.Field+r.Kind+r.Target) + 2
	}
	return c
}

// matchModels: exact name, then case-insensitive name, then table name,
// then a qualified spelling (app.Model, Outer.Inner, repo:Model).
func matchModels(all []DataModel, q string) []DataModel {
	repoQ := ""
	if r, n, ok := strings.Cut(q, ":"); ok {
		repoQ, q = r, n
	}
	pick := func(f func(DataModel) bool) []DataModel {
		var out []DataModel
		for _, m := range all {
			if (repoQ == "" || m.Repo == repoQ) && f(m) {
				out = append(out, m)
			}
		}
		return out
	}
	tries := []func(DataModel) bool{
		func(m DataModel) bool { return m.Name == q },
		func(m DataModel) bool { return strings.EqualFold(m.Name, q) },
		func(m DataModel) bool { return m.Table != "" && strings.EqualFold(m.Table, q) },
		func(m DataModel) bool {
			return m.Table != "" && strings.EqualFold(m.Table[strings.LastIndex(m.Table, ".")+1:], q)
		},
		func(m DataModel) bool {
			i := strings.LastIndex(q, ".")
			return i > 0 && strings.EqualFold(m.Name, q[i+1:]) && (strings.Contains(m.Path, strings.ReplaceAll(q[:i], ".", "/")) || strings.EqualFold(m.Package, q[:i]))
		},
	}
	for _, t := range tries {
		if out := pick(t); len(out) > 0 {
			sort.SliceStable(out, func(i, j int) bool {
				if modelKindRank[out[i].Kind] != modelKindRank[out[j].Kind] {
					return modelKindRank[out[i].Kind] < modelKindRank[out[j].Kind]
				}
				return out[i].Repo+out[i].Path < out[j].Repo+out[j].Path
			})
			return out
		}
	}
	return nil
}

func nearModels(all []DataModel, q string, n int) []string {
	lq := strings.ToLower(q)
	seen := map[string]bool{}
	var out []string
	for _, m := range all {
		if strings.Contains(strings.ToLower(m.Name), lq) || strings.Contains(strings.ToLower(m.Table), lq) {
			k := m.Name + " (" + m.Repo + ")"
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	if len(out) > n {
		out = out[:n]
	}
	return out
}

// setHandles stamps each model/serializer with its reponite symbol id when
// the class is an indexed symbol (dir.Name), so brief/context can drill in.
func setHandles(s Store, ref string, ms []DataModel, ss []DataSerializer) {
	syms := map[string]map[string]SymbolRef{}
	get := func(repo string) map[string]SymbolRef {
		if m, ok := syms[repo]; ok {
			return m
		}
		m := s.SymbolsAt(repo, ref)
		syms[repo] = m
		return m
	}
	id := func(repo, p, name string) string {
		q := name
		if d := path.Dir(p); d != "." && d != "" {
			q = d + "." + name
		}
		if _, ok := get(repo)[q]; ok {
			return q
		}
		return ""
	}
	for i := range ms {
		ms[i].Handle = id(ms[i].Repo, ms[i].Path, ms[i].Name)
	}
	for i := range ss {
		ss[i].Handle = id(ss[i].Repo, ss[i].Path, ss[i].Name)
	}
}

// detectModels runs every framework detector over one repo's files and
// resolves relation targets within the repo.
func detectModels(repo string, files []File) ([]DataModel, []DataSerializer) {
	var ms []DataModel
	py, sers := pyModels(repo, files)
	ms = append(ms, py...)
	ms = append(ms, tsModels(repo, files)...)
	ms = append(ms, prismaModels(repo, files)...)
	ms = append(ms, gormModels(repo, files)...)
	ms = append(ms, sqlModels(repo, files)...)
	ms = append(ms, protoModels(repo, files)...)
	resolveRelations(ms)
	return ms, sers
}

// resolveRelations points each relation at a model of this repo: by name,
// then (SQL and SQLAlchemy ForeignKey("users.id")) by table.
func resolveRelations(ms []DataModel) {
	byName := map[string]bool{}
	byTable := map[string]string{}
	for _, m := range ms {
		byName[m.Name] = true
		if m.Table != "" {
			t := strings.ToLower(m.Table)
			byTable[t] = m.Name
			byTable[t[strings.LastIndex(t, ".")+1:]] = m.Name
		}
	}
	for i := range ms {
		for j := range ms[i].Relations {
			r := &ms[i].Relations[j]
			if r.Target != "" {
				continue
			}
			raw := strings.Trim(r.Raw, `"'`+"`")
			cand := raw
			if k := strings.LastIndex(cand, "."); k >= 0 && ms[i].Framework != "protobuf" {
				cand = cand[k+1:]
			}
			switch {
			case raw == "self":
				r.Target = ms[i].Name
			case byName[cand]:
				r.Target = cand
			case byName[lastDot(raw)]:
				r.Target = lastDot(raw)
			default:
				// "users.id" / "public.users" / "users": a table reference.
				t := strings.ToLower(raw)
				if k := strings.LastIndex(t, "."); k >= 0 && !byTableHas(byTable, t) {
					t = t[:k]
				}
				if n, ok := byTable[t]; ok {
					r.Target = n
				} else if n, ok := byTable[t[strings.LastIndex(t, ".")+1:]]; ok {
					r.Target = n
				}
			}
		}
	}
}

func byTableHas(m map[string]string, k string) bool { _, ok := m[k]; return ok }

func lastDot(s string) string { return s[strings.LastIndex(s, ".")+1:] }

// snakeCase: OrderLine -> order_line, HTTPServer -> http_server.
func snakeCase(s string) string {
	var b strings.Builder
	rs := []rune(s)
	for i, r := range rs {
		upper := r >= 'A' && r <= 'Z'
		if upper && i > 0 {
			prevLower := rs[i-1] >= 'a' && rs[i-1] <= 'z' || rs[i-1] >= '0' && rs[i-1] <= '9'
			nextLower := i+1 < len(rs) && rs[i+1] >= 'a' && rs[i+1] <= 'z'
			if prevLower || nextLower && rs[i-1] >= 'A' && rs[i-1] <= 'Z' {
				b.WriteByte('_')
			}
		}
		if upper {
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// pluralize is GORM's default table naming (snake_case, English plural).
func pluralize(s string) string {
	switch {
	case strings.HasSuffix(s, "y") && len(s) > 1 && !strings.ContainsRune("aeiou", rune(s[len(s)-2])):
		return s[:len(s)-1] + "ies"
	case strings.HasSuffix(s, "s"), strings.HasSuffix(s, "x"), strings.HasSuffix(s, "ch"), strings.HasSuffix(s, "sh"):
		return s + "es"
	}
	return s + "s"
}
