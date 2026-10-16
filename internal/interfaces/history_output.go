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
