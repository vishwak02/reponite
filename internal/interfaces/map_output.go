// map_output.go renders `map` and `explain` results: JSON (snake_case keys,
// empty values dropped, `_meta` envelope) by default, or a terse text form for
// a human or an LLM reading it directly (--format text).
package interfaces

import (
	"fmt"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// MapJSON renders one repo's orientation map.
func MapJSON(r query.MapResult) (string, error) { return snakeJSON(r) }

// FleetMapJSON renders the fleet orientation map.
func FleetMapJSON(r query.FleetMapResult) (string, error) { return snakeJSON(r) }

// ExplainJSON renders an explain bundle.
func ExplainJSON(r query.ExplainResult) (string, error) { return snakeJSON(r) }

func loc(path string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%d", path, line)
	}
	return path
}

func writeMeta(b *strings.Builder, m query.Meta, truncated bool, next []string) {
	for _, w := range m.Warnings {
		fmt.Fprintf(b, "! %s\n", w)
	}
	if truncated {
		b.WriteString("(truncated)\n")
	}
	if len(next) > 0 {
		b.WriteString("next:\n")
		for _, n := range next {
			fmt.Fprintf(b, "  %s\n", n)
		}
	}
}

// MapText is the terse rendering of a repo map.
func MapText(r query.MapResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s @ %s", r.Repo, r.Ref)
	if r.Commit != "" {
		fmt.Fprintf(&b, " (%s)", shortSHA(r.Commit))
	}
	if r.Module != "" {
		fmt.Fprintf(&b, " module %s", r.Module)
	}
	b.WriteString("\n")
	if r.Summary != "" {
		fmt.Fprintf(&b, "%s  [%s]\n", r.Summary, r.SummarySource)
	}
	var langs []string
	for _, l := range r.Languages {
		langs = append(langs, fmt.Sprintf("%s %d files/%d syms", l.Lang, l.Files, l.Symbols))
	}
	fmt.Fprintf(&b, "%d files, %d symbols, %d call edges — %s\n", r.Files, r.Symbols, r.Edges, strings.Join(langs, ", "))
	if len(r.EntryPoints) > 0 {
		b.WriteString("\n## entry points\n")
		for _, e := range r.EntryPoints {
			b.WriteString(entryLine(e))
		}
	}
	if len(r.Modules) > 0 {
		b.WriteString("\n## modules (files/symbols/fan-in)\n")
		for _, m := range r.Modules {
			fmt.Fprintf(&b, "- %s  %d/%d/%d", m.Path, m.Files, m.Symbols, m.FanIn)
			if m.Purpose != "" {
				fmt.Fprintf(&b, " — %s [%s]", m.Purpose, m.Source)
			}
			b.WriteString("\n")
		}
	}
	if len(r.Hubs) > 0 {
		b.WriteString("\n## hubs (in/out)\n")
		for _, h := range r.Hubs {
			fmt.Fprintf(&b, "- %s  %d/%d  %s\n", h.Symbol, h.In, h.Out, loc(h.Path, h.Line))
		}
	}
	if len(r.Dependencies) > 0 {
		fmt.Fprintf(&b, "\n## dependencies (from %d manifests", len(r.Manifests))
		if r.InternalDeps > 0 {
			fmt.Fprintf(&b, "; %d internal omitted", r.InternalDeps)
		}
		b.WriteString(")\n")
		byEco := map[string][]string{}
		var ecos []string
		for _, d := range r.Dependencies {
			if _, ok := byEco[d.Ecosystem]; !ok {
				ecos = append(ecos, d.Ecosystem)
			}
			n := d.Name
			if d.Version != "" {
				n += "@" + d.Version
			}
			if d.Dev {
				n += "(dev)"
			}
			if d.Manifests > 1 {
				n += fmt.Sprintf("×%d", d.Manifests)
			}
			byEco[d.Ecosystem] = append(byEco[d.Ecosystem], n)
		}
		for _, e := range ecos {
			fmt.Fprintf(&b, "- %s: %s\n", e, strings.Join(byEco[e], ", "))
		}
	}
	b.WriteString("\n")
	writeMeta(&b, r.Meta, r.Truncated, r.Next)
	return b.String()
}

func entryLine(e query.EntryPoint) string {
	var b strings.Builder
	fmt.Fprintf(&b, "- [%s] %s", e.Kind, e.Name)
	if e.Framework != "" {
		fmt.Fprintf(&b, " (%s)", e.Framework)
	}
	if e.Path != e.Name {
		fmt.Fprintf(&b, "  %s", loc(e.Path, e.Line))
	} else if e.Line > 1 {
		fmt.Fprintf(&b, ":%d", e.Line)
	}
	if e.Detail != "" {
		fmt.Fprintf(&b, " — %s", e.Detail)
	}
	if len(e.Items) > 0 {
		fmt.Fprintf(&b, " — %s", strings.Join(e.Items, ", "))
		if e.Count > len(e.Items) {
			fmt.Fprintf(&b, " (+%d)", e.Count-len(e.Items))
		}
	}
	b.WriteString("\n")
	return b.String()
}

func shortSHA(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

// FleetMapText is the terse rendering of the fleet map.
func FleetMapText(r query.FleetMapResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# fleet: %d repos\n", len(r.Repos))
	for _, rs := range r.Repos {
		fmt.Fprintf(&b, "- %s @ %s  %d files/%d syms  %s", rs.Repo, rs.Ref, rs.Files, rs.Symbols, strings.Join(rs.Languages, " "))
		if rs.Routes > 0 {
			fmt.Fprintf(&b, "  routes:%d", rs.Routes)
		}
		var kinds []string
		for k, n := range rs.EntryPoints {
			kinds = append(kinds, fmt.Sprintf("%s:%d", k, n))
		}
		if len(kinds) > 0 {
			sort.Strings(kinds)
			fmt.Fprintf(&b, "  entry[%s]", strings.Join(kinds, " "))
		}
		if rs.Summary != "" {
			fmt.Fprintf(&b, "\n    %s", rs.Summary)
		}
		b.WriteString("\n")
	}
	if len(r.Links) > 0 {
		b.WriteString("\n## cross-repo links\n")
		for _, l := range r.Links {
			fmt.Fprintf(&b, "- %s → %s  %s ×%d  e.g. %s  (%s)\n", l.From, l.To, l.Kind, l.Count, strings.Join(l.Examples, ", "), l.Handle)
		}
	}
	b.WriteString("\n")
	writeMeta(&b, r.Meta, r.Truncated, r.Next)
	return b.String()
}

// ExplainText is the terse rendering of an explain bundle.
func ExplainText(r query.ExplainResult) string {
	var b strings.Builder
	switch r.Kind {
	case "symbol":
		symbolText(&b, r.Symbol)
	case "file":
		fileText(&b, r.File)
	case "directory":
		d := r.Dir
		fmt.Fprintf(&b, "# directory %s/%s @ %s\n", d.Repo, d.Path, r.Ref)
		if d.Purpose != "" {
			fmt.Fprintf(&b, "%s  [%s]\n", d.Purpose, d.Source)
		}
		b.WriteString("\n## files (symbols)\n")
		for _, f := range d.Files {
			fmt.Fprintf(&b, "- %s (%d)\n", f.Path, f.Symbols)
		}
		if d.MoreFiles > 0 {
			fmt.Fprintf(&b, "- … +%d files\n", d.MoreFiles)
		}
		if len(d.Hubs) > 0 {
			b.WriteString("\n## most connected (in/out)\n")
			for _, h := range d.Hubs {
				fmt.Fprintf(&b, "- %s %d/%d %s\n", h.Symbol, h.In, h.Out, loc(h.Path, h.Line))
			}
		}
	case "route":
		fmt.Fprintf(&b, "# route %s\n", r.Target)
		for _, er := range r.Routes {
			rt := er.Route
			fmt.Fprintf(&b, "\n## %s /%s [%s] %s/%s\n", rt.Method, strings.TrimPrefix(rt.Pattern, "/"), rt.Framework, rt.Repo, loc(rt.Path, rt.Line))
			if er.Handler != nil {
				fmt.Fprintf(&b, "handler %s (%s) %s\n", er.Handler.Symbol, er.Handler.Kind, loc(er.Handler.Path, er.Handler.StartLine))
				if er.Handler.Doc != "" {
					fmt.Fprintf(&b, "  %s\n", firstLine(er.Handler.Doc))
				}
			} else if rt.Handler != "" {
				fmt.Fprintf(&b, "handler %s (not resolved to an indexed symbol)\n", rt.Handler)
			}
			for _, c := range er.Clients {
				fmt.Fprintf(&b, "- client %s/%s in %s: %s\n", c.Repo, loc(c.Path, c.Line), c.In, c.Text)
			}
		}
		for _, c := range r.Unmatched {
			fmt.Fprintf(&b, "- unmatched client %s/%s: %s %s\n", c.Repo, loc(c.Path, c.Line), c.Method, c.URL)
		}
	case "ambiguous":
		fmt.Fprintf(&b, "# %s is ambiguous\n", r.Target)
		for _, c := range r.Candidates {
			fmt.Fprintf(&b, "- %s: %s\n", c.Repo, strings.Join(c.Symbols, ", "))
		}
	default:
		fmt.Fprintf(&b, "# %s: not found\n", r.Target)
	}
	if r.Note != "" {
		fmt.Fprintf(&b, "\nnote: %s\n", r.Note)
	}
	if len(r.Omitted) > 0 {
		fmt.Fprintf(&b, "omitted: %s\n", strings.Join(r.Omitted, "; "))
	}
	b.WriteString("\n")
	writeMeta(&b, r.Meta, r.Truncated, r.Next)
	return b.String()
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func symbolText(b *strings.Builder, s *query.ExplainSymbol) {
	fmt.Fprintf(b, "# %s %s  (%s/%s)\n", s.Kind, s.Symbol, s.Repo, loc(s.Path, s.StartLine))
	for _, d := range s.Decorators {
		fmt.Fprintf(b, "%s\n", d)
	}
	if s.Signature != "" {
		fmt.Fprintf(b, "%s\n", s.Signature)
	}
	if s.Doc != "" {
		fmt.Fprintf(b, "\n%s\n", s.Doc)
	}
	edges := func(title string, es []query.ExplainEdge, more int) {
		if len(es) == 0 && more == 0 {
			return
		}
		fmt.Fprintf(b, "\n## %s\n", title)
		for _, e := range es {
			where := loc(e.Path, e.Line)
			if e.Repo != "" {
				where = e.Repo + ":" + where
			}
			if e.External {
				where = "external"
			}
			fmt.Fprintf(b, "- %s  %s  [%s %.2f]\n", e.Symbol, where, e.ResolutionMethod, e.Confidence)
		}
		if more > 0 {
			fmt.Fprintf(b, "- … +%d\n", more)
		}
	}
	edges("callers", s.Callers, s.MoreCallers)
	edges("callees", s.Callees, s.MoreCallees)
	if len(s.ReachedBy) > 0 {
		b.WriteString("\n## reached by\n")
		for _, r := range s.ReachedBy {
			fmt.Fprintf(b, "- [%s] %s  %s\n", r.Kind, r.Detail, loc(r.Path, r.Line))
		}
	}
	if len(s.Tests) > 0 {
		b.WriteString("\n## tests\n")
		for _, t := range s.Tests {
			how := "mentions"
			if t.Calls {
				how = "calls"
			}
			fmt.Fprintf(b, "- %s %s %s\n", t.In, how, loc(t.Path, t.Line))
		}
	}
	if s.Body != "" {
		fmt.Fprintf(b, "\n```\n%s\n```\n", s.Body)
	}
}

func fileText(b *strings.Builder, f *query.ExplainFile) {
	fmt.Fprintf(b, "# file %s/%s  (%s, %d lines)\n", f.Repo, f.Path, f.Lang, f.Lines)
	if len(f.Role) > 0 {
		fmt.Fprintf(b, "role: %s\n", strings.Join(f.Role, "; "))
	}
	if f.Purpose != "" {
		fmt.Fprintf(b, "%s\n", f.Purpose)
	}
	if len(f.Imports) > 0 {
		fmt.Fprintf(b, "imports: %s\n", strings.Join(f.Imports, ", "))
	}
	if len(f.Symbols) > 0 {
		b.WriteString("\n## symbols (line, in/out)\n")
		for _, s := range f.Symbols {
			fmt.Fprintf(b, "- %d %s %s  %d/%d", s.Line, s.Kind, s.Symbol, s.In, s.Out)
			if s.Signature != "" {
				fmt.Fprintf(b, "  `%s`", s.Signature)
			}
			b.WriteString("\n")
		}
		if f.MoreSymbols > 0 {
			fmt.Fprintf(b, "- … +%d\n", f.MoreSymbols)
		}
	}
	for _, e := range f.EntryPoints {
		b.WriteString(entryLine(e))
	}
	for _, r := range f.Routes {
		fmt.Fprintf(b, "- route %s /%s → %s :%d\n", r.Method, strings.TrimPrefix(r.Pattern, "/"), r.Handler, r.Line)
	}
	for _, t := range f.Topics {
		fmt.Fprintf(b, "- %s %s :%d\n", t.Role, t.Name, t.Line)
	}
	links := func(title string, ls []query.FileLink) {
		if len(ls) == 0 {
			return
		}
		fmt.Fprintf(b, "\n## %s (calls)\n", title)
		for _, l := range ls {
			fmt.Fprintf(b, "- %s ×%d  e.g. %s\n", l.Path, l.Calls, strings.Join(l.Examples, ", "))
		}
	}
	links("called from", f.Inbound)
	links("calls into", f.Outbound)
}

// GuideText is the agent decision table (`reponite guide`, MCP reponite_guide).
func GuideText() string { return query.Guide }
