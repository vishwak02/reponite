package scip

import "sort"

// Span is a symbol's line range in a file (mirrors query.SymbolSpan, kept local
// so this package stays dependency-free within the core).
type Span struct {
	Name      string
	StartLine int
	EndLine   int
}

// FileMonikers is what one document contributes to the index: the moniker each
// locally DEFINED symbol owns, and the outward references made from inside each
// local symbol.
type FileMonikers struct {
	// Defs maps a local symbol name to its SCIP moniker (its globally unique
	// cross-repo identity).
	Defs map[string]string
	// Refs are references from a local symbol to a moniker defined elsewhere.
	Refs []Reference
}

// Reference is one call/use from a local symbol to an external moniker.
type Reference struct {
	From   string // enclosing local symbol name
	Symbol string // the referenced SCIP moniker
}

// LocalDefs indexes every definition moniker in the index by document path, so
// a reference can be classified as in-repo (drop) or cross-boundary (keep).
func (i Index) LocalDefs() map[string]bool {
	defs := map[string]bool{}
	for _, d := range i.Documents {
		for _, o := range d.Occurrences {
			if o.IsDef && o.Symbol != "" {
				defs[o.Symbol] = true
			}
		}
	}
	return defs
}

// Map attributes a document's occurrences to the symbols whose spans contain
// them: a definition occurrence names the enclosing symbol's moniker, and a
// non-definition occurrence of a moniker NOT defined in this index is an
// outward (cross-repo) reference from the enclosing symbol.
//
// Attribution uses the innermost containing span, matching how grep/usages fuse
// a line to its symbol. An occurrence inside no known span is dropped — file-
// level code has no symbol to attribute to, and inventing one would misreport.
func Map(doc Document, spans []Span, localDefs map[string]bool) FileMonikers {
	out := FileMonikers{Defs: map[string]string{}}
	for _, o := range doc.Occurrences {
		name := enclosing(spans, o.Line)
		if name == "" {
			continue
		}
		if o.IsDef {
			// A symbol can carry several definition occurrences (e.g. the name
			// and its receiver); the first stable one wins.
			if _, seen := out.Defs[name]; !seen {
				out.Defs[name] = o.Symbol
			}
			continue
		}
		if localDefs[o.Symbol] {
			continue // resolves inside this repo — the in-repo call graph owns it
		}
		out.Refs = append(out.Refs, Reference{From: name, Symbol: o.Symbol})
	}
	dedupeRefs(&out)
	return out
}

// enclosing returns the innermost span containing line, or "".
func enclosing(spans []Span, line int) string {
	best, bestSize := "", int(^uint(0)>>1)
	for _, s := range spans {
		if line >= s.StartLine && line <= s.EndLine {
			if size := s.EndLine - s.StartLine; size < bestSize {
				best, bestSize = s.Name, size
			}
		}
	}
	return best
}

// dedupeRefs collapses repeated (from, symbol) pairs — N calls to the same
// external symbol are one dependency — and sorts for deterministic storage.
func dedupeRefs(fm *FileMonikers) {
	seen := map[Reference]bool{}
	out := fm.Refs[:0]
	for _, r := range fm.Refs {
		if seen[r] {
			continue
		}
		seen[r] = true
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].From != out[j].From {
			return out[i].From < out[j].From
		}
		return out[i].Symbol < out[j].Symbol
	})
	fm.Refs = out
}
