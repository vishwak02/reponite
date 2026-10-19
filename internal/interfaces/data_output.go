// data_output.go renders `models` and `config` as the JSON envelope (stable
// lowercase keys, _meta) or, with format "text", a terse rendering an LLM
// or a human reads directly. Every site carries path:line and, where the
// enclosing symbol is indexed, a handle for brief/context.
package interfaces

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

type dataFieldDTO struct {
	Name      string `json:"name"`
	Type      string `json:"type,omitempty"`
	Column    string `json:"column,omitempty"`
	Null      bool   `json:"null,omitempty"`
	Unique    bool   `json:"unique,omitempty"`
	Index     bool   `json:"index,omitempty"`
	PK        bool   `json:"pk,omitempty"`
	Default   string `json:"default,omitempty"`
	Line      int    `json:"line,omitempty"`
	Inherited string `json:"inherited_from,omitempty"`
	Implicit  bool   `json:"implicit,omitempty"`
}

type dataRelDTO struct {
	Field   string `json:"field"`
	Kind    string `json:"kind"`
	Target  string `json:"target,omitempty"`
	Raw     string `json:"unresolved,omitempty"`
	Reverse string `json:"reverse,omitempty"`
	Line    int    `json:"line,omitempty"`
	From    string `json:"from,omitempty"`
	Path    string `json:"path,omitempty"`
}

type dataModelDTO struct {
	Name      string         `json:"name"`
	Kind      string         `json:"kind"`
	Framework string         `json:"framework"`
	Table     string         `json:"table,omitempty"`
	Package   string         `json:"package,omitempty"`
	Repo      string         `json:"repo"`
	Path      string         `json:"path"`
	Line      int            `json:"line"`
	Handle    string         `json:"handle,omitempty"`
	NFields   int            `json:"fields_count"`
	Fields    []dataFieldDTO `json:"fields,omitempty"`
	Relations []dataRelDTO   `json:"relations,omitempty"`
	Sources   []string       `json:"also_shaped_by,omitempty"`
	Test      bool           `json:"test,omitempty"`
}

type dataSerDTO struct {
	Name    string   `json:"name"`
	Repo    string   `json:"repo"`
	Path    string   `json:"path"`
	Line    int      `json:"line"`
	Handle  string   `json:"handle,omitempty"`
	Fields  []string `json:"fields,omitempty"`
	Exclude []string `json:"exclude,omitempty"`
}

type dataAccessDTO struct {
	Repo   string  `json:"repo"`
	Path   string  `json:"path"`
	Line   int     `json:"line"`
	In     string  `json:"in,omitempty"`
	Handle string  `json:"handle,omitempty"`
	Op     string  `json:"op"`
	Via    string  `json:"via"`
	Conf   float64 `json:"confidence"`
	Text   string  `json:"text"`
}

type dataRouteDTO struct {
	Method    string `json:"method"`
	Pattern   string `json:"pattern"`
	Handler   string `json:"handler,omitempty"`
	Framework string `json:"framework"`
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Via       string `json:"via"`
}

type modelDetailDTO struct {
	Model       dataModelDTO    `json:"model"`
	Incoming    []dataRelDTO    `json:"incoming,omitempty"`
	Serializers []dataSerDTO    `json:"serializers,omitempty"`
	Routes      []dataRouteDTO  `json:"routes,omitempty"`
	Writers     []dataAccessDTO `json:"writers"`
	Readers     []dataAccessDTO `json:"readers"`
	Hooks       []dataAccessDTO `json:"hooks,omitempty"`
	Omitted     map[string]int  `json:"omitted,omitempty"`
}

type modelsDTO struct {
	Query      string           `json:"query,omitempty"`
	Total      int              `json:"total"`
	Frameworks map[string]int   `json:"frameworks,omitempty"`
	Models     []dataModelDTO   `json:"models,omitempty"`
	Details    []modelDetailDTO `json:"details,omitempty"`
	Suggest    []string         `json:"did_you_mean,omitempty"`
	Truncated  bool             `json:"truncated"`
	Omitted    int              `json:"omitted,omitempty"`
	Next       string           `json:"next,omitempty"`
	Note       string           `json:"note,omitempty"`
	Meta       metaDTO          `json:"_meta"`
}

func modelDTO(m query.DataModel, full bool) dataModelDTO {
	d := dataModelDTO{Name: m.Name, Kind: m.Kind, Framework: m.Framework, Table: m.Table, Package: m.Package, Repo: m.Repo,
		Path: m.Path, Line: m.Line, Handle: m.Handle, NFields: len(m.Fields), Sources: m.Sources, Test: m.Test}
	for _, r := range m.Relations {
		d.Relations = append(d.Relations, relDTO(r))
	}
	if full {
		for _, f := range m.Fields {
			d.Fields = append(d.Fields, dataFieldDTO{Name: f.Name, Type: f.Type, Column: f.Column, Null: f.Null, Unique: f.Unique,
				Index: f.Index, PK: f.PK, Default: f.Default, Line: f.Line, Inherited: f.Inherited, Implicit: f.Implicit})
		}
	}
	return d
}

func relDTO(r query.DataRelation) dataRelDTO {
	d := dataRelDTO{Field: r.Field, Kind: r.Kind, Target: r.Target, Reverse: r.Reverse, Line: r.Line, From: r.From, Path: r.Path}
	if r.Target == "" {
		d.Raw = r.Raw
	}
	return d
}

func accessDTOs(xs []query.DataAccess) []dataAccessDTO {
	out := make([]dataAccessDTO, 0, len(xs))
	for _, x := range xs {
		out = append(out, dataAccessDTO{Repo: x.Repo, Path: x.Path, Line: x.Line, In: x.In, Handle: x.Handle, Op: x.Op, Via: x.Via, Conf: x.Conf, Text: x.Text})
	}
	return out
}

func modelsToDTO(r query.ModelsResult) modelsDTO {
	dto := modelsDTO{Query: r.Query, Total: r.Total, Frameworks: r.Frameworks, Suggest: r.Suggest, Truncated: r.Truncated,
		Omitted: r.Omitted, Next: r.Next, Note: r.Note, Meta: metaOf(r.Meta)}
	for _, m := range r.Models {
		dto.Models = append(dto.Models, modelDTO(m, false))
	}
	for _, d := range r.Details {
		x := modelDetailDTO{Model: modelDTO(d.Model, true), Writers: accessDTOs(d.Writers), Readers: accessDTOs(d.Readers), Omitted: d.Omitted}
		if len(d.Hooks) > 0 {
			x.Hooks = accessDTOs(d.Hooks)
		}
		x.Model.NFields = len(d.Model.Fields) + d.Omitted["fields"]
		for _, r := range d.Incoming {
			x.Incoming = append(x.Incoming, relDTO(r))
		}
		for _, s := range d.Serializers {
			x.Serializers = append(x.Serializers, dataSerDTO{Name: s.Name, Repo: s.Repo, Path: s.Path, Line: s.Line, Handle: s.Handle, Fields: s.Fields, Exclude: s.Exclude})
		}
		for _, rt := range d.Routes {
			x.Routes = append(x.Routes, dataRouteDTO{Method: rt.Method, Pattern: rt.Pattern, Handler: rt.Handler, Framework: rt.Framework,
				Repo: rt.Repo, Path: rt.Path, Line: rt.Line, Via: rt.Via})
		}
		if len(x.Omitted) == 0 {
			x.Omitted = nil
		}
		dto.Details = append(dto.Details, x)
	}
	return dto
}

// ModelsJSON renders the data-model summary or one model's detail.
func ModelsJSON(r query.ModelsResult) (string, error) { return marshal(modelsToDTO(r)) }

// ModelsText is the terse rendering of the same result.
func ModelsText(r query.ModelsResult) string {
	var b strings.Builder
	fws := make([]string, 0, len(r.Frameworks))
	for k, v := range r.Frameworks {
		fws = append(fws, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(fws)
	fmt.Fprintf(&b, "models: %d found (%s) @ %s\n", r.Total, strings.Join(fws, " "), r.Meta.Ref)
	for _, w := range r.Meta.Warnings {
		fmt.Fprintf(&b, "! %s\n", w)
	}
	for _, m := range r.Models {
		fmt.Fprintf(&b, "%-8s %s", m.Kind, m.Name)
		if m.Table != "" {
			fmt.Fprintf(&b, " [%s]", m.Table)
		}
		fmt.Fprintf(&b, " %s %d fields  %s:%s:%d", m.Framework, len(m.Fields), m.Repo, m.Path, m.Line)
		if m.Handle != "" {
			fmt.Fprintf(&b, "  #%s", m.Handle)
		}
		b.WriteByte('\n')
		if rels := relText(m.Relations); rels != "" {
			fmt.Fprintf(&b, "         %s\n", rels)
		}
	}
	for _, d := range r.Details {
		m := d.Model
		fw := m.Framework
		if m.Table != "" {
			fw += " " + m.Table
		}
		fmt.Fprintf(&b, "\n%s %s (%s) %s:%s:%d", m.Kind, m.Name, fw, m.Repo, m.Path, m.Line)
		if m.Handle != "" {
			fmt.Fprintf(&b, "  #%s", m.Handle)
		}
		b.WriteString("\n fields:\n")
		for _, f := range m.Fields {
			var flags []string
			for _, x := range []struct {
				on bool
				s  string
			}{{f.PK, "pk"}, {f.Unique && !f.PK, "unique"}, {f.Index, "index"}, {f.Null, "null"}, {f.Implicit, "implicit"}} {
				if x.on {
					flags = append(flags, x.s)
				}
			}
			if f.Default != "" {
				flags = append(flags, "default="+f.Default)
			}
			if f.Column != "" {
				flags = append(flags, "column="+f.Column)
			}
			if f.Inherited != "" {
				flags = append(flags, "from "+f.Inherited)
			}
			fmt.Fprintf(&b, "  %-20s %s %s\n", f.Name, f.Type, strings.Join(flags, " "))
		}
		if rels := relText(m.Relations); rels != "" {
			fmt.Fprintf(&b, " relations: %s\n", rels)
		}
		for _, r := range d.Incoming {
			fmt.Fprintf(&b, " <- %s.%s (%s) %s:%d\n", r.From, r.Field, r.Kind, r.Path, r.Line)
		}
		for _, s := range d.Serializers {
			fmt.Fprintf(&b, " serializer %s %s:%d [%s]\n", s.Name, s.Path, s.Line, strings.Join(s.Fields, ","))
		}
		for _, rt := range d.Routes {
			fmt.Fprintf(&b, " route %s /%s -> %s (%s) %s:%d\n", rt.Method, strings.TrimPrefix(rt.Pattern, "/"), rt.Handler, rt.Via, rt.Path, rt.Line)
		}
		writeAccess(&b, "W", d.Writers)
		writeAccess(&b, "H", d.Hooks)
		writeAccess(&b, "R", d.Readers)
		if len(d.Omitted) > 0 {
			var om []string
			for k, v := range d.Omitted {
				om = append(om, fmt.Sprintf("%s=%d", k, v))
			}
			sort.Strings(om)
			fmt.Fprintf(&b, " omitted: %s\n", strings.Join(om, " "))
		}
	}
	if len(r.Suggest) > 0 {
		fmt.Fprintf(&b, "did you mean: %s\n", strings.Join(r.Suggest, ", "))
	}
	if r.Truncated {
		fmt.Fprintf(&b, "truncated: %s\n", r.Next)
	}
	if r.Note != "" {
		fmt.Fprintf(&b, "note: %s\n", r.Note)
	}
	return b.String()
}

func relText(rs []query.DataRelation) string {
	var out []string
	for _, r := range rs {
		t := r.Target
		if t == "" {
			t = "?" + r.Raw
		}
		out = append(out, fmt.Sprintf("%s -%s-> %s", r.Field, r.Kind, t))
	}
	return strings.Join(out, ", ")
}

func writeAccess(b *strings.Builder, tag string, xs []query.DataAccess) {
	for _, x := range xs {
		h := ""
		if x.Handle != "" {
			h = " #" + x.Handle
		}
		fmt.Fprintf(b, " %s %-10s %s:%s:%d %s%s (%s %.1f) | %s\n", tag, x.Op, x.Repo, x.Path, x.Line, x.In, h, x.Via, x.Conf, x.Text)
	}
}

// --- config ---------------------------------------------------------------

type cfgReadDTO struct {
	Repo    string `json:"repo"`
	Path    string `json:"path"`
	Line    int    `json:"line"`
	In      string `json:"in,omitempty"`
	Handle  string `json:"handle,omitempty"`
	Default string `json:"default,omitempty"`
	HasDef  bool   `json:"has_default,omitempty"`
	Via     string `json:"via"`
	Text    string `json:"text,omitempty"`
}

type cfgSetDTO struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Line   int    `json:"line,omitempty"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

type cfgKeyDTO struct {
	Kind     string       `json:"kind"`
	Key      string       `json:"key"`
	NReads   int          `json:"reads_count"`
	NSets    int          `json:"sets_count"`
	Defaults []string     `json:"defaults,omitempty"`
	Status   string       `json:"status,omitempty"`
	Note     string       `json:"note,omitempty"`
	Reads    []cfgReadDTO `json:"reads,omitempty"`
	Sets     []cfgSetDTO  `json:"sets,omitempty"`
}

type configDTO struct {
	Query        string         `json:"query,omitempty"`
	Total        int            `json:"total"`
	Counts       map[string]int `json:"kinds,omitempty"`
	ReadNeverSet []cfgKeyDTO    `json:"read_never_set,omitempty"`
	SetNeverRead []cfgKeyDTO    `json:"set_never_read,omitempty"`
	Keys         []cfgKeyDTO    `json:"keys"`
	Truncated    bool           `json:"truncated"`
	Omitted      int            `json:"omitted,omitempty"`
	Next         string         `json:"next,omitempty"`
	Note         string         `json:"note,omitempty"`
	Flow         *cfgFlowDTO    `json:"flow,omitempty"`
	Meta         metaDTO        `json:"_meta"`
}

// cfgFlowDTO is a key's path: defined → templated → read → used.
type cfgFlowDTO struct {
	Key       string        `json:"key"`
	NDefined  int           `json:"n_defined"`
	Defined   []cfgFlowSite `json:"defined"`
	Templates []cfgFlowSite `json:"templates"`
	Readers   []cfgFlowSite `json:"readers"`
	Uses      []cfgFlowSite `json:"uses"`
	Note      string        `json:"note,omitempty"`
}

type cfgFlowSite struct {
	Repo   string `json:"repo"`
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	In     string `json:"in,omitempty"`
	Via    string `json:"via,omitempty"`
	Kind   string `json:"kind,omitempty"`
	Writes string `json:"writes,omitempty"`
	Copies int    `json:"copies,omitempty"`
}

func cfgFlowToDTO(f *query.ConfigFlow) *cfgFlowDTO {
	if f == nil {
		return nil
	}
	conv := func(xs []query.ConfigFlowSite) []cfgFlowSite {
		out := make([]cfgFlowSite, 0, len(xs))
		for _, x := range xs {
			out = append(out, cfgFlowSite{Repo: x.Repo, Path: x.Path, Line: x.Line, Text: x.Text, In: x.In, Via: x.Via, Kind: x.Kind, Writes: x.Writes, Copies: x.Copies})
		}
		return out
	}
	return &cfgFlowDTO{Key: f.Key, NDefined: f.NDefined, Defined: conv(f.Defined), Templates: conv(f.Templates),
		Readers: conv(f.Readers), Uses: conv(f.Uses), Note: f.Note}
}

func cfgKeyToDTO(k query.ConfigKey) cfgKeyDTO {
	d := cfgKeyDTO{Kind: k.Kind, Key: k.Key, NReads: k.NReads, NSets: k.NSets, Defaults: k.Defaults, Status: k.Status, Note: k.Note}
	for _, r := range k.Reads {
		d.Reads = append(d.Reads, cfgReadDTO{Repo: r.Repo, Path: r.Path, Line: r.Line, In: r.In, Handle: r.Handle, Default: r.Default, HasDef: r.HasDefault, Via: r.Via, Text: r.Text})
	}
	for _, s := range k.Sets {
		d.Sets = append(d.Sets, cfgSetDTO{Repo: s.Repo, Path: s.Path, Line: s.Line, Value: s.Value, Source: s.Source})
	}
	return d
}

// ConfigJSON renders the configuration surface or one key's reads and sets.
func ConfigJSON(r query.ConfigResult) (string, error) {
	dto := configDTO{Query: r.Query, Total: r.Total, Counts: r.Counts, Keys: make([]cfgKeyDTO, 0, len(r.Keys)), Truncated: r.Truncated,
		Omitted: r.Omitted, Next: r.Next, Note: r.Note, Flow: cfgFlowToDTO(r.Flow), Meta: metaOf(r.Meta)}
	for _, k := range r.ReadNeverSet {
		dto.ReadNeverSet = append(dto.ReadNeverSet, cfgKeyToDTO(k))
	}
	for _, k := range r.SetNeverRead {
		dto.SetNeverRead = append(dto.SetNeverRead, cfgKeyToDTO(k))
	}
	for _, k := range r.Keys {
		dto.Keys = append(dto.Keys, cfgKeyToDTO(k))
	}
	return marshal(dto)
}

// ConfigText is the terse rendering of the same result.
func ConfigText(r query.ConfigResult) string {
	var b strings.Builder
	var ks []string
	for k, v := range r.Counts {
		ks = append(ks, fmt.Sprintf("%s=%d", k, v))
	}
	sort.Strings(ks)
	fmt.Fprintf(&b, "config: %d keys (%s) @ %s\n", r.Total, strings.Join(ks, " "), r.Meta.Ref)
	for _, w := range r.Meta.Warnings {
		fmt.Fprintf(&b, "! %s\n", w)
	}
	site := func(k query.ConfigKey) string {
		if len(k.Reads) > 0 {
			x := k.Reads[0]
			return fmt.Sprintf("read %s:%d", x.Path, x.Line)
		}
		if len(k.Sets) > 0 {
			x := k.Sets[0]
			return fmt.Sprintf("set %s:%d (%s)", x.Path, x.Line, x.Source)
		}
		return ""
	}
	if len(r.ReadNeverSet) > 0 {
		b.WriteString("read, never set:\n")
		for _, k := range r.ReadNeverSet {
			d := ""
			if len(k.Defaults) > 0 {
				d = " default=" + strings.Join(k.Defaults, "|")
			}
			fmt.Fprintf(&b, "  %s %s%s  %s\n", k.Kind, k.Key, d, site(k))
		}
	}
	if len(r.SetNeverRead) > 0 {
		b.WriteString("set, never read:\n")
		for _, k := range r.SetNeverRead {
			fmt.Fprintf(&b, "  %s %s  %s\n", k.Kind, k.Key, site(k))
		}
	}
	detail := r.Query != ""
	if !detail {
		b.WriteString("keys:\n")
	}
	for _, k := range r.Keys {
		d := ""
		if len(k.Defaults) > 0 {
			d = " default=" + strings.Join(k.Defaults, "|")
		}
		st := ""
		if k.Status != "" {
			st = " [" + k.Status + "]"
		}
		fmt.Fprintf(&b, "  %s %s r%d s%d%s%s\n", k.Kind, k.Key, k.NReads, k.NSets, d, st)
		for _, x := range k.Reads {
			h := ""
			if x.Handle != "" {
				h = " #" + x.Handle
			}
			def := ""
			if x.HasDefault {
				def = " default=" + x.Default
			}
			fmt.Fprintf(&b, "    R %s:%s:%d %s%s (%s)%s | %s\n", x.Repo, x.Path, x.Line, x.In, h, x.Via, def, x.Text)
		}
		for _, x := range k.Sets {
			fmt.Fprintf(&b, "    S %s:%s:%d %s = %s\n", x.Repo, x.Path, x.Line, x.Source, x.Value)
		}
	}
	if r.Truncated {
		fmt.Fprintf(&b, "truncated: %s\n", r.Next)
	}
	if r.Note != "" && detail && r.Flow == nil {
		fmt.Fprintf(&b, "note: %s\n", r.Note)
	}
	if f := r.Flow; f != nil {
		fmt.Fprintf(&b, "flow of %s:\n", f.Key)
		site := func(x query.ConfigFlowSite) string {
			s := fmt.Sprintf("%s:%s:%d", x.Repo, x.Path, x.Line)
			if x.In != "" {
				s += " in " + x.In
			}
			return s
		}
		fmt.Fprintf(&b, "  1. defined in %d file(s)\n", f.NDefined)
		for _, x := range f.Defined[:min(len(f.Defined), 4)] {
			fmt.Fprintf(&b, "     %s | %s\n", site(x), x.Text)
		}
		fmt.Fprintf(&b, "  2. templates reading it: %d\n", len(f.Templates))
		for _, x := range f.Templates {
			w, c := "", ""
			if x.Writes != "" {
				w = " → " + x.Writes
			}
			if x.Copies > 0 {
				c = fmt.Sprintf(" (+%d copies)", x.Copies)
			}
			fmt.Fprintf(&b, "     %s [%s]%s%s | %s\n", site(x), x.Kind, w, c, x.Text)
		}
		fmt.Fprintf(&b, "  3. read by: %d\n", len(f.Readers))
		for _, x := range f.Readers {
			fmt.Fprintf(&b, "     %s [%s via %q] | %s\n", site(x), x.Kind, x.Via, x.Text)
		}
		if len(f.Uses) > 0 {
			fmt.Fprintf(&b, "  4. then used: %d\n", len(f.Uses))
			for _, x := range f.Uses {
				fmt.Fprintf(&b, "     %s [%s] | %s\n", site(x), x.Via, x.Text)
			}
		}
		if f.Note != "" {
			fmt.Fprintf(&b, "  note: %s\n", f.Note)
		}
	}
	return b.String()
}
