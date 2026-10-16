// trace_output.go renders reponite_trace results: the nested JSON tree (stable
// lowercase keys, handles reusable in brief/context/trace) and the terse text
// tree for --format text.
package interfaces

import "github.com/vishwak02/reponite/internal/query"

type traceEffectDTO struct {
	Kind   string `json:"kind"`
	Op     string `json:"op,omitempty"`
	Line   int    `json:"line"`
	Text   string `json:"text"`
	Target string `json:"target,omitempty"`
}

type traceNodeDTO struct {
	Kind        string           `json:"kind"`
	Repo        string           `json:"repo,omitempty"`
	Handle      string           `json:"handle,omitempty"`
	Label       string           `json:"label,omitempty"`
	Path        string           `json:"path,omitempty"`
	Line        int              `json:"line,omitempty"`
	Edge        string           `json:"edge,omitempty"`
	Method      string           `json:"resolution_method,omitempty"`
	Confidence  float64          `json:"confidence,omitempty"`
	Note        string           `json:"note,omitempty"`
	Effects     []traceEffectDTO `json:"effects,omitempty"`
	MoreEffects int              `json:"more_effects,omitempty"`
	External    []string         `json:"external,omitempty"`
	Stop        string           `json:"stop,omitempty"`
	More        int              `json:"more,omitempty"`
	Same        string           `json:"same,omitempty"`
	Alt         []string         `json:"candidates,omitempty"`
	Children    []traceNodeDTO   `json:"children,omitempty"`
}

type traceDTO struct {
	Entry     string         `json:"entry"`
	EntryKind string         `json:"entry_kind,omitempty"`
	Direction string         `json:"direction"`
	Depth     int            `json:"depth"`
	Budget    int            `json:"budget"`
	Nodes     int            `json:"nodes"`
	Tokens    int            `json:"tokens"`
	Truncated bool           `json:"truncated"`
	Stops     map[string]int `json:"stops,omitempty"`
	Next      string         `json:"next,omitempty"`
	Roots     []traceNodeDTO `json:"roots"`
	Note      string         `json:"note"`
	Meta      metaDTO        `json:"_meta"`
}

func traceNode(n *query.TraceNode) traceNodeDTO {
	d := traceNodeDTO{Kind: n.Kind, Repo: n.Repo, Handle: n.Symbol, Label: n.Label, Path: n.Path, Line: n.Line, Edge: n.Edge,
		Method: n.Method, Confidence: n.Confidence, Note: n.Note, MoreEffects: n.MoreEffect, External: n.External,
		Stop: n.Stop, More: n.More, Same: n.Same, Alt: n.Alt}
	for _, e := range n.Effects {
		d.Effects = append(d.Effects, traceEffectDTO{Kind: e.Kind, Op: e.Op, Line: e.Line, Text: e.Text, Target: e.Target})
	}
	for _, c := range n.Children {
		d.Children = append(d.Children, traceNode(c))
	}
	return d
}

// TraceJSON renders a trace as a nested JSON tree.
func TraceJSON(r query.TraceResult) (string, error) {
	dto := traceDTO{Entry: r.Entry, EntryKind: r.EntryKind, Direction: r.Direction, Depth: r.Depth, Budget: r.Budget,
		Nodes: r.Nodes, Tokens: r.Tokens, Truncated: r.Truncated, Next: r.Next, Note: r.Note, Meta: metaOf(r.Meta),
		Roots: make([]traceNodeDTO, 0, len(r.Roots))}
	if len(r.Stops) > 0 {
		dto.Stops = r.Stops
	}
	for _, n := range r.Roots {
		dto.Roots = append(dto.Roots, traceNode(n))
	}
	return marshal(dto)
}

// TraceOutput renders a trace as JSON, or as the indented text tree when
// format is "text".
func TraceOutput(r query.TraceResult, format string) (string, error) {
	if format == "text" {
		return query.TraceText(r), nil
	}
	return TraceJSON(r)
}
