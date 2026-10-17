package interfaces

import (
	"fmt"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// The compact text renderings below carry the same facts as the JSON for the
// commands an agent calls most, grouped by repo and file and without repeated
// field names — a fraction of the tokens. Every location is repo:path:line.

func where(repo, path string, line int) string {
	if line > 0 {
		return fmt.Sprintf("%s:%s:%d", repo, path, line)
	}
	return repo + ":" + path
}

// TopicsText renders a communication graph: one block per name, producers and
// consumers with how each name was resolved when it was not written literally.
func TopicsText(r query.CommGraphResult) string {
	var b strings.Builder
	ep := func(side string, e query.CommEndpoint) {
		fmt.Fprintf(&b, "  %s %s  %s", side, where(e.Repo, e.Path, e.Line), e.Role)
		if e.MsgType != "" {
			fmt.Fprintf(&b, " <%s>", e.MsgType)
		}
		if e.Raw != "" && strings.TrimPrefix(e.Raw, "/") != e.Name {
			fmt.Fprintf(&b, " as %q", e.Raw)
		}
		if e.NameResolution != "" && e.NameResolution != "as-written" {
			fmt.Fprintf(&b, "  [%s]", clipText(e.NameResolution, 220))
		}
		b.WriteString("\n")
	}
	for _, g := range r.Groups {
		state := "unlinked"
		if g.Connected() {
			state = "connected"
		}
		fmt.Fprintf(&b, "%s /%s  (%s, confidence %.2f)\n", g.Family, g.Name, state, g.Confidence)
		for _, e := range g.Producers {
			ep("pub", e)
		}
		for _, e := range g.Consumers {
			ep("sub", e)
		}
		for _, e := range g.Declared {
			ep("launch", e)
		}
	}
	if len(r.Unbound) > 0 {
		fmt.Fprintf(&b, "unresolved names (%d):\n", len(r.Unbound))
		for i, e := range r.Unbound {
			if i == 20 {
				fmt.Fprintf(&b, "  … %d more\n", len(r.Unbound)-20)
				break
			}
			fmt.Fprintf(&b, "  %s %s %s  [%s]\n", where(e.Repo, e.Path, e.Line), e.Role, e.Raw, clipText(e.NameResolution, 160))
		}
	}
	if len(r.Groups) == 0 {
		b.WriteString("no matching topic, service or action\n")
	}
	b.WriteString("note: " + clipText(r.Note, 600) + "\n")
	return b.String()
}

// UsagesText renders call sites grouped by repo and file.
func UsagesText(r query.UsagesResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: %d call site(s)\n", r.Symbol, r.Total)
	last := ""
	for _, u := range r.Usages {
		if k := u.Repo + ":" + u.Path; k != last {
			fmt.Fprintf(&b, "%s\n", k)
			last = k
		}
		mark := " "
		if u.Confirmed {
			mark = "*"
		}
		fmt.Fprintf(&b, " %s%5d  %s  (in %s)\n", mark, u.Line, clipText(u.Text, 140), u.In)
	}
	b.WriteString("* = the enclosing function is a resolved caller\n")
	return b.String()
}

// RoutesText renders routes with the client calls that reach them.
func RoutesText(r query.RoutesResult) string {
	var b strings.Builder
	for _, l := range r.Links {
		rt := l.Route
		fmt.Fprintf(&b, "%s /%s → %s  %s [%s]\n", rt.Method, strings.TrimPrefix(rt.Pattern, "/"), rt.Handler,
			where(rt.Repo, rt.Path, rt.Line), rt.Framework)
		for _, c := range l.Clients {
			fmt.Fprintf(&b, "    ← %s %s  %s (in %s)\n", c.Method, c.URL, where(c.Repo, c.Path, c.Line), c.In)
		}
	}
	if len(r.Unmatched) > 0 {
		fmt.Fprintf(&b, "client calls no indexed route serves (%d):\n", len(r.Unmatched))
		for i, c := range r.Unmatched {
			if i == 20 {
				fmt.Fprintf(&b, "  … %d more\n", len(r.Unmatched)-20)
				break
			}
			fmt.Fprintf(&b, "  %s %s  %s\n", c.Method, c.URL, where(c.Repo, c.Path, c.Line))
		}
	}
	fmt.Fprintf(&b, "%d routes, %d client calls\n", r.Routes, r.Clients)
	return b.String()
}

// ImplsText renders implementations of a base (method).
func ImplsText(r query.ImplsResult) string {
	var b strings.Builder
	head := r.Base
	if r.Method != "" {
		head += "::" + r.Method
	}
	fmt.Fprintf(&b, "%s: %d implementation(s)\n", head, len(r.Impls))
	for _, im := range r.Impls {
		tags := []string{fmt.Sprintf("depth %d", im.Depth)}
		if im.Plugin {
			tags = append(tags, "plugin")
		}
		if im.Test {
			tags = append(tags, "test")
		}
		fmt.Fprintf(&b, "  %s  %s  (%s)", im.Class, where(im.Repo, im.Path, im.Line), strings.Join(tags, ", "))
		if im.Method != "" {
			fmt.Fprintf(&b, "  defines %s", im.Method)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// LinksText renders data references to and from a target.
func LinksText(r query.LinksResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", r.Target)
	for _, l := range r.RefersTo {
		to := l.To
		if l.ToLine > 0 {
			to = fmt.Sprintf("%s:%d", l.To, l.ToLine)
		}
		fmt.Fprintf(&b, "  → [%s] %s  (line %d: %s)\n", l.Kind, to, l.Line, clipText(l.Text, 120))
	}
	for _, l := range r.ReferredBy {
		fmt.Fprintf(&b, "  ← [%s] %s  %s\n", l.Kind, where(l.Repo, l.Path, l.Line), clipText(l.Text, 120))
	}
	if len(r.RefersTo)+len(r.ReferredBy) == 0 {
		b.WriteString("  no references written as data\n")
	}
	return b.String()
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
