// history_output.go renders why / errors / tests: JSON by default (stable
// lowercase keys + _meta, like every other tool) and a terse text form
// (--format text) for a human or an LLM reading it inline.
package interfaces

import (
	"fmt"
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

type whyCommitDTO struct {
	Commit  string   `json:"commit"`
	Date    string   `json:"date"`
	Author  string   `json:"author"`
	Subject string   `json:"subject"`
	Body    string   `json:"body,omitempty"`
	PRs     []string `json:"prs,omitempty"`
	Tickets []string `json:"tickets,omitempty"`
	Kind    string   `json:"kind"`
	Path    string   `json:"path,omitempty"`
	Added   int      `json:"added"`
	Removed int      `json:"removed"`
	FirstIn string   `json:"first_in,omitempty"`
	InRefs  int      `json:"in_refs"`
}

type whyCompatDTO struct {
	Ref     string `json:"ref"`
	Date    string `json:"date,omitempty"`
	Present bool   `json:"present"`
	Verdict string `json:"verdict,omitempty"`
}

type whyDTO struct {
	Symbol    string         `json:"symbol"`
	Repo      string         `json:"repo"`
	Ref       string         `json:"ref"`
	Path      string         `json:"path,omitempty"`
	Lines     string         `json:"lines,omitempty"`
	Commits   []whyCommitDTO `json:"commits"`
	Total     int            `json:"total"`
	Compat    []whyCompatDTO `json:"compat_history"`
	Truncated bool           `json:"truncated"`
	Next      string         `json:"next,omitempty"`
	Note      string         `json:"note,omitempty"`
	Meta      metaDTO        `json:"_meta"`
}

func lines(a, b int) string {
	if a <= 0 {
		return ""
	}
	return fmt.Sprintf("%d-%d", a, b)
}

// WhyJSON renders a symbol's change history.
func WhyJSON(r query.WhyResult) (string, error) {
	dto := whyDTO{Symbol: r.Symbol, Repo: r.Repo, Ref: r.Ref, Path: r.Path, Lines: lines(r.StartLine, r.EndLine),
		Commits: []whyCommitDTO{}, Total: r.Total, Compat: []whyCompatDTO{}, Truncated: r.Truncated, Next: r.Next,
		Note: r.Note, Meta: metaOf(r.Meta)}
	for _, c := range r.Commits {
		dto.Commits = append(dto.Commits, whyCommitDTO{Commit: c.Commit, Date: c.Date, Author: c.Author, Subject: c.Subject,
			Body: c.Body, PRs: c.PRs, Tickets: c.Tickets, Kind: string(c.Kind), Path: c.Path, Added: c.Added,
			Removed: c.Removed, FirstIn: c.FirstIn, InRefs: c.InRefs})
	}
	for _, c := range r.Compat {
		dto.Compat = append(dto.Compat, whyCompatDTO{Ref: c.Ref, Date: c.Date, Present: c.Present, Verdict: c.Verdict})
	}
	return marshal(dto)
}

// WhyText is the terse rendering of WhyJSON.
func WhyText(r query.WhyResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "why %s  (%s@%s", r.Symbol, r.Repo, r.Ref)
	if r.Path != "" {
		fmt.Fprintf(&b, " %s:%s", r.Path, lines(r.StartLine, r.EndLine))
	}
	b.WriteString(")\n")
	for _, c := range r.Commits {
		link := ""
		for _, p := range c.PRs {
			link += " #" + p
		}
		for _, t := range c.Tickets {
			link += " " + t
		}
		first := ""
		if c.FirstIn != "" {
			first = " first_in=" + c.FirstIn
		}
		fmt.Fprintf(&b, "%s %s %-10s %s%s  [%s +%d/-%d%s]\n", c.Date, shortCommit(c.Commit), c.Kind, c.Subject, link, c.Author, c.Added, c.Removed, first)
		if c.Body != "" {
			fmt.Fprintf(&b, "    %s\n", c.Body)
		}
	}
	if len(r.Compat) > 0 {
		var parts []string
		for _, c := range r.Compat {
			v := c.Verdict
			if v == "" {
				v = map[bool]string{true: "present", false: "absent"}[c.Present]
			}
			parts = append(parts, c.Ref+":"+v)
		}
		fmt.Fprintf(&b, "compat: %s\n", strings.Join(parts, " → "))
	}
	if r.Truncated {
		fmt.Fprintf(&b, "truncated: %s\n", r.Next)
	}
	writeTail(&b, r.Note, r.Meta)
	return b.String()
}

func shortCommit(s string) string {
	if len(s) > 10 {
		return s[:10]
	}
	return s
}

func writeTail(b *strings.Builder, note string, m query.Meta) {
	if note != "" {
		fmt.Fprintf(b, "note: %s\n", note)
	}
	for _, w := range m.Warnings {
		fmt.Fprintf(b, "warning: %s\n", w)
	}
}

type errSiteDTO struct {
	Repo      string   `json:"repo"`
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	In        string   `json:"in,omitempty"`
	Handle    string   `json:"handle,omitempty"`
	Kind      string   `json:"kind"`
	Type      string   `json:"type,omitempty"`
	Status    int      `json:"status,omitempty"`
	Message   string   `json:"message,omitempty"`
	Text      string   `json:"text,omitempty"`
	Match     string   `json:"match,omitempty"`
	Fixed     int      `json:"fixed,omitempty"`
	Test      bool     `json:"test,omitempty"`
	Callers   []string `json:"callers,omitempty"`
	MoreCalls int      `json:"more_callers,omitempty"`
}

type errHandlerDTO struct {
	Repo   string   `json:"repo"`
	Path   string   `json:"path"`
	Line   int      `json:"line"`
	In     string   `json:"in,omitempty"`
	Kind   string   `json:"kind"`
	Types  []string `json:"types,omitempty"`
	Match  string   `json:"match"`
	OnPath bool     `json:"on_path,omitempty"`
	Text   string   `json:"text"`
	Test   bool     `json:"test,omitempty"`
}

type errTypeDTO struct {
	Repo     string   `json:"repo"`
	Type     string   `json:"type"`
	Bases    []string `json:"bases,omitempty"`
	Defined  string   `json:"defined,omitempty"`
	Status   int      `json:"status,omitempty"`
	Default  string   `json:"default_message,omitempty"`
	Produced int      `json:"produced"`
	Handled  int      `json:"handled"`
}

// errDataDTO is a line of a JSON/YAML catalogue that defines the error.
type errDataDTO struct {
	Repo     string   `json:"repo"`
	Path     string   `json:"path"`
	Line     int      `json:"line"`
	Key      string   `json:"key,omitempty"`
	Text     string   `json:"text"`
	Context  []string `json:"context,omitempty"`
	LoadedBy []string `json:"loaded_by,omitempty"`
}

type errorsDTO struct {
	Query     string          `json:"query,omitempty"`
	Mode      string          `json:"mode"`
	Sites     []errSiteDTO    `json:"producers"`
	Handlers  []errHandlerDTO `json:"handlers,omitempty"`
	Types     []errTypeDTO    `json:"types,omitempty"`
	Data      []errDataDTO    `json:"data,omitempty"`
	Total     int             `json:"total"`
	Truncated bool            `json:"truncated"`
	Next      string          `json:"next,omitempty"`
	Note      string          `json:"note,omitempty"`
	Meta      metaDTO         `json:"_meta"`
}

// ErrorsJSON renders error producers/handlers, or the catalogue.
func ErrorsJSON(r query.ErrorsResult) (string, error) {
	dto := errorsDTO{Query: r.Query, Mode: r.Mode, Sites: []errSiteDTO{}, Total: r.Total, Truncated: r.Truncated,
		Next: r.Next, Note: r.Note, Meta: metaOf(r.Meta)}
	for _, x := range r.Sites {
		dto.Sites = append(dto.Sites, errSiteDTO{Repo: x.Repo, Path: x.Path, Line: x.Line, In: x.In, Handle: x.Handle,
			Kind: x.Kind, Type: x.Type, Status: x.Status, Message: x.Message, Text: x.Text, Match: x.Match, Fixed: x.Fixed,
			Test: x.Test, Callers: x.Callers, MoreCalls: x.MoreCalls})
	}
	for _, h := range r.Handlers {
		dto.Handlers = append(dto.Handlers, errHandlerDTO{Repo: h.Repo, Path: h.Path, Line: h.Line, In: h.In, Kind: h.Kind,
			Types: h.Types, Match: h.Match, OnPath: h.OnPath, Text: h.Text, Test: h.Test})
	}
	for _, d := range r.Data {
		dto.Data = append(dto.Data, errDataDTO{Repo: d.Repo, Path: d.Path, Line: d.Line, Key: d.Key, Text: d.Text,
			Context: d.Context, LoadedBy: d.LoadedBy})
	}
	for _, t := range r.Types {
		dto.Types = append(dto.Types, errTypeDTO{Repo: t.Repo, Type: t.Type, Bases: t.Bases, Defined: t.Defined,
			Status: t.Status, Default: t.Default, Produced: t.Produced, Handled: t.Handled})
	}
	return marshal(dto)
}

// ErrorsText is the terse rendering of ErrorsJSON.
func ErrorsText(r query.ErrorsResult) string {
	var b strings.Builder
	if r.Mode == "catalogue" {
		b.WriteString("error catalogue\n")
		repo := ""
		for _, t := range r.Types {
			if t.Repo != repo {
				repo = t.Repo
				fmt.Fprintf(&b, "[%s] types\n", repo)
			}
			extra := ""
			if t.Status > 0 {
				extra += fmt.Sprintf(" HTTP %d", t.Status)
			}
			if len(t.Bases) > 0 {
				extra += " < " + strings.Join(t.Bases, ",")
			}
			if t.Default != "" {
				extra += fmt.Sprintf(" %q", t.Default)
			}
			fmt.Fprintf(&b, "  %s%s  produced=%d handled=%d %s\n", t.Type, extra, t.Produced, t.Handled, t.Defined)
		}
		repo = ""
		for _, x := range r.Sites {
			if x.Repo != repo {
				repo = x.Repo
				fmt.Fprintf(&b, "[%s] messages\n", repo)
			}
			fmt.Fprintf(&b, "  %s:%d %s %s %q\n", x.Path, x.Line, x.Kind, errTypeLabel(x), x.Message)
		}
	} else {
		fmt.Fprintf(&b, "errors %q (%s)\n", r.Query, r.Mode)
		for _, x := range r.Sites {
			fmt.Fprintf(&b, "%s %s:%d in %s  %s %s  [%s]\n", x.Repo, x.Path, x.Line, orDash(x.Handle, x.In), x.Kind, errTypeLabel(x), x.Match)
			if x.Message != "" {
				fmt.Fprintf(&b, "    msg: %q\n", x.Message)
			}
			if len(x.Callers) > 0 {
				more := ""
				if x.MoreCalls > 0 {
					more = fmt.Sprintf(" +%d", x.MoreCalls)
				}
				fmt.Fprintf(&b, "    called by: %s%s\n", strings.Join(x.Callers, ", "), more)
			}
		}
		for _, d := range r.Data {
			fmt.Fprintf(&b, "defined as data: %s %s:%d  %s\n", d.Repo, d.Path, d.Line, d.Text)
			if len(d.LoadedBy) > 0 {
				fmt.Fprintf(&b, "    loaded by: %s\n", strings.Join(d.LoadedBy, ", "))
			}
		}
		for _, h := range r.Handlers {
			on := ""
			if h.OnPath {
				on = " on_path"
			}
			fmt.Fprintf(&b, "handled: %s %s:%d in %s  %s %v [%s%s]  %s\n", h.Repo, h.Path, h.Line, orDash(h.In, ""), h.Kind, h.Types, h.Match, on, h.Text)
		}
	}
	if r.Truncated {
		fmt.Fprintf(&b, "truncated: %s\n", r.Next)
	}
	writeTail(&b, r.Note, r.Meta)
	return b.String()
}

func errTypeLabel(x query.ErrorSite) string {
	s := x.Type
	if x.Status > 0 {
		s = strings.TrimSpace(fmt.Sprintf("%s HTTP %d", s, x.Status))
	}
	return s
}

func orDash(a, b string) string {
	if a != "" {
		return a
	}
	if b != "" {
		return b
	}
	return "-"
}

type testHitDTO struct {
	Repo      string   `json:"repo"`
	Path      string   `json:"path"`
	Line      int      `json:"line"`
	Name      string   `json:"name"`
	Handle    string   `json:"handle,omitempty"`
	Framework string   `json:"framework"`
	Depth     int      `json:"depth"`
	Via       []string `json:"via,omitempty"`
	How       string   `json:"how"`
	Command   string   `json:"command"`
}

type testRunDTO struct {
	Repo      string `json:"repo"`
	Framework string `json:"framework"`
	Tests     int    `json:"tests"`
	Command   string `json:"command"`
}

type testsDTO struct {
	Symbol    string       `json:"symbol"`
	Repo      string       `json:"repo"`
	Ref       string       `json:"ref"`
	Tests     []testHitDTO `json:"tests"`
	Commands  []testRunDTO `json:"commands"`
	Total     int          `json:"total"`
	Truncated bool         `json:"truncated"`
	Next      string       `json:"next,omitempty"`
	Note      string       `json:"note,omitempty"`
	Meta      metaDTO      `json:"_meta"`
}

// TestsJSON renders the tests exercising a symbol and how to run them.
func TestsJSON(r query.TestsResult) (string, error) {
	dto := testsDTO{Symbol: r.Symbol, Repo: r.Repo, Ref: r.Ref, Tests: []testHitDTO{}, Commands: []testRunDTO{},
		Total: r.Total, Truncated: r.Truncated, Next: r.Next, Note: r.Note, Meta: metaOf(r.Meta)}
	for _, h := range r.Tests {
		dto.Tests = append(dto.Tests, testHitDTO{Repo: h.Repo, Path: h.Path, Line: h.Line, Name: h.Name, Handle: h.Handle,
			Framework: h.Framework, Depth: h.Depth, Via: h.Via, How: h.How, Command: h.Command})
	}
	for _, c := range r.Commands {
		dto.Commands = append(dto.Commands, testRunDTO{Repo: c.Repo, Framework: c.Framework, Tests: c.Tests, Command: c.Command})
	}
	return marshal(dto)
}

// TestsText is the terse rendering of TestsJSON.
func TestsText(r query.TestsResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "tests %s  (%s@%s)\n", r.Symbol, r.Repo, r.Ref)
	for _, h := range r.Tests {
		via := ""
		if len(h.Via) > 0 {
			via = " via " + strings.Join(h.Via, " → ")
		}
		fmt.Fprintf(&b, "d%d %-9s %s:%d %s [%s]%s\n", h.Depth, h.Framework, h.Path, h.Line, h.Name, h.How, via)
	}
	for _, c := range r.Commands {
		fmt.Fprintf(&b, "run (%s, %d): %s\n", c.Framework, c.Tests, c.Command)
	}
	if r.Truncated {
		fmt.Fprintf(&b, "truncated: %s\n", r.Next)
	}
	writeTail(&b, r.Note, r.Meta)
	return b.String()
}
