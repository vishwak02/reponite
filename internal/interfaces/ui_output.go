// ui_output.go renders `ui` results: compact JSON (every field omitted when
// empty, each component carrying its drill-in handle) and a terse text form
// that reads as "button Save → handleSave → POST /api/orders → OrdersView".
package interfaces

import (
	"fmt"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

type uiRefDTO struct {
	Repo       string `json:"repo"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Handle     string `json:"handle"`
	Resolution string `json:"resolution,omitempty"`
}

type uiPageDTO struct {
	Route     string    `json:"route"`
	Component string    `json:"component,omitempty"`
	Redirect  string    `json:"redirect,omitempty"`
	Framework string    `json:"framework"`
	Repo      string    `json:"repo"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	Def       *uiRefDTO `json:"def,omitempty"`
}

type uiPropDTO struct {
	Name     string `json:"name"`
	Type     string `json:"type,omitempty"`
	Optional bool   `json:"optional,omitempty"`
	Default  string `json:"default,omitempty"`
}

type uiStateDTO struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	From string `json:"from,omitempty"`
	Line int    `json:"line"`
}

type uiRouteDTO struct {
	Repo      string `json:"repo"`
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Method    string `json:"method,omitempty"`
	Pattern   string `json:"pattern"`
	Handler   string `json:"handler,omitempty"`
	Framework string `json:"framework,omitempty"`
	Via       string `json:"via_gateway,omitempty"`
}

type uiAPIDTO struct {
	Method string       `json:"method"`
	URL    string       `json:"url"`
	Repo   string       `json:"repo"`
	Path   string       `json:"path"`
	Line   int          `json:"line"`
	Via    []string     `json:"via,omitempty"`
	Routes []uiRouteDTO `json:"routes,omitempty"`
}

type uiEffectDTO struct {
	Kind    string     `json:"kind"`
	Trigger string     `json:"trigger"`
	Deps    []string   `json:"deps,omitempty"`
	Line    int        `json:"line"`
	API     []uiAPIDTO `json:"api,omitempty"`
}

type uiHandlerDTO struct {
	Event   string     `json:"event"`
	Element string     `json:"element"`
	Label   string     `json:"label,omitempty"`
	Handler string     `json:"handler"`
	Prop    bool       `json:"from_prop,omitempty"`
	Line    int        `json:"line"`
	API     []uiAPIDTO `json:"api,omitempty"`
}

type uiRenderDTO struct {
	Name     string    `json:"name"`
	Line     int       `json:"line"`
	Def      *uiRefDTO `json:"def,omitempty"`
	Expanded bool      `json:"expanded,omitempty"`
	Seen     bool      `json:"seen_above,omitempty"`
}

type uiComponentDTO struct {
	Name       string            `json:"name"`
	Kind       string            `json:"kind"`
	Framework  string            `json:"framework"`
	Repo       string            `json:"repo"`
	Path       string            `json:"path"`
	Line       int               `json:"line"`
	EndLine    int               `json:"end_line"`
	Handle     string            `json:"handle"`
	Resolution string            `json:"resolution,omitempty"`
	Props      []uiPropDTO       `json:"props,omitempty"`
	State      []uiStateDTO      `json:"state,omitempty"`
	Effects    []uiEffectDTO     `json:"effects,omitempty"`
	Handlers   []uiHandlerDTO    `json:"handlers,omitempty"`
	API        []uiAPIDTO        `json:"api,omitempty"`
	Renders    []uiRenderDTO     `json:"renders,omitempty"`
	Lib        []string          `json:"renders_external,omitempty"`
	Children   []*uiComponentDTO `json:"children,omitempty"`
}

type uiUseDTO struct {
	Repo       string `json:"repo"`
	Path       string `json:"path"`
	Line       int    `json:"line"`
	In         string `json:"in,omitempty"`
	Kind       string `json:"kind"`
	Text       string `json:"text"`
	Resolution string `json:"resolution,omitempty"`
}

type uiDTO struct {
	Target     string          `json:"target,omitempty"`
	Mode       string          `json:"mode"`
	Page       *uiPageDTO      `json:"page,omitempty"`
	Pages      []uiPageDTO     `json:"pages,omitempty"`
	Root       *uiComponentDTO `json:"tree,omitempty"`
	Components int             `json:"components,omitempty"`
	Uses       []uiUseDTO      `json:"uses,omitempty"`
	Candidates []uiRefDTO      `json:"other_definitions,omitempty"`
	Truncated  bool            `json:"truncated"`
	Next       string          `json:"next,omitempty"`
	Note       string          `json:"note,omitempty"`
	Meta       metaDTO         `json:"_meta"`
}

func uiRef(r *query.UIRef) *uiRefDTO {
	if r == nil {
		return nil
	}
	return &uiRefDTO{Repo: r.Repo, Path: r.Path, Line: r.Line, Handle: r.Handle, Resolution: r.Resolution}
}

func uiPage(p query.UIPage) uiPageDTO {
	return uiPageDTO{Route: p.Route, Component: p.Component, Redirect: p.Redirect, Framework: p.Framework, Repo: p.Repo, Path: p.Path, Line: p.Line, Def: uiRef(p.Def)}
}

func uiAPIs(as []query.UIAPICall) []uiAPIDTO {
	var out []uiAPIDTO
	for _, a := range as {
		d := uiAPIDTO{Method: a.Method, URL: a.URL, Repo: a.Repo, Path: a.Path, Line: a.Line, Via: a.Via}
		for _, r := range a.Routes {
			d.Routes = append(d.Routes, uiRouteDTO{Repo: r.Repo, Path: r.Path, Line: r.Line, Method: r.Method, Pattern: r.Pattern, Handler: r.Handler, Framework: r.Framework, Via: r.Via})
		}
		out = append(out, d)
	}
	return out
}

func uiComponent(c *query.UIComponent) *uiComponentDTO {
	if c == nil {
		return nil
	}
	d := &uiComponentDTO{Name: c.Name, Kind: c.Kind, Framework: c.Framework, Repo: c.Repo, Path: c.Path, Line: c.Line, EndLine: c.EndLine,
		Handle: c.Handle, Resolution: c.Resolution, API: uiAPIs(c.API), Lib: c.Lib}
	for _, p := range c.Props {
		d.Props = append(d.Props, uiPropDTO{Name: p.Name, Type: p.Type, Optional: p.Optional, Default: p.Default})
	}
	for _, s := range c.State {
		d.State = append(d.State, uiStateDTO{Name: s.Name, Kind: s.Kind, From: s.From, Line: s.Line})
	}
	for _, e := range c.Effects {
		d.Effects = append(d.Effects, uiEffectDTO{Kind: e.Kind, Trigger: e.Trigger, Deps: e.Deps, Line: e.Line, API: uiAPIs(e.API)})
	}
	for _, h := range c.Handlers {
		d.Handlers = append(d.Handlers, uiHandlerDTO{Event: h.Event, Element: h.Element, Label: h.Label, Handler: h.Handler, Prop: h.Prop, Line: h.Line, API: uiAPIs(h.API)})
	}
	for _, r := range c.Renders {
		d.Renders = append(d.Renders, uiRenderDTO{Name: r.Name, Line: r.Line, Def: uiRef(r.Def), Expanded: r.Expanded, Seen: r.Seen})
	}
	for _, ch := range c.Children {
		d.Children = append(d.Children, uiComponent(ch))
	}
	return d
}

// UIJSON renders a `ui` result.
func UIJSON(r query.UIResult) (string, error) {
	dto := uiDTO{Target: r.Target, Mode: r.Mode, Root: uiComponent(r.Root), Components: r.Components, Truncated: r.Truncated, Next: r.Next, Note: r.Note, Meta: metaOf(r.Meta)}
	if r.Page != nil {
		p := uiPage(*r.Page)
		dto.Page = &p
	}
	for _, p := range r.Pages {
		dto.Pages = append(dto.Pages, uiPage(p))
	}
	for _, u := range r.Uses {
		dto.Uses = append(dto.Uses, uiUseDTO{Repo: u.Repo, Path: u.Path, Line: u.Line, In: u.In, Kind: u.Kind, Text: u.Text, Resolution: u.Resolution})
	}
	for _, c := range r.Candidates {
		dto.Candidates = append(dto.Candidates, *uiRef(&c))
	}
	return marshal(dto)
}

// UIText renders a `ui` result as terse text for a human or an LLM.
func UIText(r query.UIResult) string {
	var b strings.Builder
	switch r.Mode {
	case "pages":
		fmt.Fprintf(&b, "pages (%d)\n", len(r.Pages))
		for _, p := range r.Pages {
			target := p.Component
			if p.Redirect != "" {
				target = "→ redirect " + p.Redirect
			}
			def := "  (definition not found)"
			if p.Def != nil {
				def = "  " + p.Def.Repo + ":" + p.Def.Path + ":" + fmt.Sprint(p.Def.Line) + " [" + p.Def.Handle + "]"
			}
			if p.Redirect != "" {
				def = ""
			}
			fmt.Fprintf(&b, "%-28s %s  (%s, %s:%d)%s\n", p.Route, target, p.Framework, p.Path, p.Line, def)
		}
	case "uses":
		fmt.Fprintf(&b, "uses of %s (%d)\n", r.Target, len(r.Uses))
		for _, u := range r.Uses {
			fmt.Fprintf(&b, "%s:%s:%d  %s in %s [%s]  %s\n", u.Repo, u.Path, u.Line, u.Kind, u.In, u.Resolution, u.Text)
		}
	case "tree":
		if r.Page != nil {
			fmt.Fprintf(&b, "page %s → %s (%s:%d)\n", r.Page.Route, r.Page.Component, r.Page.Path, r.Page.Line)
		}
		textComponent(&b, r.Root, 0)
	default:
		fmt.Fprintf(&b, "%s: %s\n", r.Mode, r.Target)
	}
	for _, c := range r.Candidates {
		fmt.Fprintf(&b, "also defined: %s:%s:%d [%s]\n", c.Repo, c.Path, c.Line, c.Handle)
	}
	if r.Truncated {
		fmt.Fprintf(&b, "… truncated — next: %s\n", r.Next)
	}
	if r.Note != "" {
		fmt.Fprintf(&b, "note: %s\n", r.Note)
	}
	return b.String()
}

func apiText(a query.UIAPICall) string {
	s := a.Method + " " + a.URL
	if len(a.Via) > 0 {
		s = strings.Join(a.Via, " → ") + " → " + s
	}
	if len(a.Routes) == 0 {
		return s + " (no indexed route)"
	}
	var rs []string
	for _, r := range a.Routes {
		h := r.Handler
		if h == "" {
			h = r.Pattern
		}
		rs = append(rs, h+" ("+r.Repo+":"+r.Path+":"+fmt.Sprint(r.Line)+")")
	}
	return s + " → " + strings.Join(rs, " | ")
}

func textComponent(b *strings.Builder, c *query.UIComponent, depth int) {
	if c == nil {
		return
	}
	ind := strings.Repeat("  ", depth)
	fmt.Fprintf(b, "%s%s  %s:%s:%d-%d [%s]\n", ind, c.Name, c.Repo, c.Path, c.Line, c.EndLine, c.Handle)
	ind += "  "
	if len(c.Props) > 0 {
		var ps []string
		for _, p := range c.Props {
			s := p.Name
			if p.Optional {
				s += "?"
			}
			if p.Type != "" {
				s += ": " + p.Type
			}
			ps = append(ps, s)
		}
		fmt.Fprintf(b, "%sprops: %s\n", ind, strings.Join(ps, ", "))
	}
	if len(c.State) > 0 {
		var ss []string
		for _, s := range c.State {
			x := s.Name + " (" + s.Kind
			if s.From != "" {
				x += " " + s.From
			}
			ss = append(ss, x+")")
		}
		fmt.Fprintf(b, "%sstate: %s\n", ind, strings.Join(ss, ", "))
	}
	for _, e := range c.Effects {
		trig := e.Trigger
		if len(e.Deps) > 0 {
			trig = "[" + strings.Join(e.Deps, ", ") + "]"
		}
		fmt.Fprintf(b, "%seffect %s %s :%d\n", ind, e.Kind, trig, e.Line)
		for _, a := range e.API {
			fmt.Fprintf(b, "%s  → %s\n", ind, apiText(a))
		}
	}
	for _, h := range c.Handlers {
		el := h.Element
		if h.Label != "" {
			el += " \"" + h.Label + "\""
		}
		who := h.Handler
		if h.Prop {
			who += " (prop: parent decides)"
		}
		fmt.Fprintf(b, "%s%s %s → %s :%d\n", ind, el, h.Event, who, h.Line)
		for _, a := range h.API {
			fmt.Fprintf(b, "%s  → %s\n", ind, apiText(a))
		}
	}
	for _, a := range c.API {
		fmt.Fprintf(b, "%sapi: %s\n", ind, apiText(a))
	}
	if len(c.Renders) > 0 {
		var rs []string
		for _, r := range c.Renders {
			s := r.Name
			switch {
			case r.Seen:
				s += " (above)"
			case !r.Expanded:
				s += " (not expanded)"
			}
			rs = append(rs, s)
		}
		fmt.Fprintf(b, "%srenders: %s\n", ind, strings.Join(rs, ", "))
	}
	if len(c.Lib) > 0 {
		fmt.Fprintf(b, "%sexternal: %s\n", ind, strings.Join(c.Lib, ", "))
	}
	for _, ch := range c.Children {
		textComponent(b, ch, depth+1)
	}
}
