//go:build treesitter

// sfc_ts.go indexes Vue and Svelte single-file components. The <script>
// blocks are parsed with the TypeScript or JavaScript grammar over a copy of
// the file in which every byte outside them is blanked (newlines kept), so
// every symbol's line — and column — is its line in the .vue/.svelte file.
// The component itself, which no script declares, becomes one symbol named
// by the file (query.SFCComponentName) spanning the whole file: its callees
// are the components its template renders and the handlers its markup binds,
// plus the script's calls, and its signature is its props contract.
package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/query"
)

// sfcFile parses a single-file component; ok=false when nothing could be read.
func sfcFile(path, src string, normVer int) (ParsedFile, bool) {
	sfc := query.SplitSFC(path, src)
	rules, ext := JavaScriptRules, ".jsx"
	switch sfc.ScriptLang {
	case "ts":
		rules, ext = TypeScriptRules, ".ts"
	case "tsx":
		rules, ext = TypeScriptRules, ".tsx"
	}
	pf := ParsedFile{Path: path, Content: src, Lang: rules.Name, IsTest: IsTestPath(path)}
	var qcalls []QualifiedCall
	if sfc.HasScript {
		root, spans, err := parseFileRules([]byte(sfc.Script), ext, rules)
		if err == nil && root != nil {
			pf.Symbols = Extract(root, rules, normVer)
			pf.Spans = spans
			pf.Imports = Imports(root, rules)
			qcalls = qualifiedCallsWithRules(root, rules)
		}
	}
	facts := query.SFCComponentFacts(path, src)
	if facts.Name == "" {
		return pf, len(pf.Symbols) > 0
	}
	for _, s := range pf.Symbols {
		if s.Name == facts.Name && s.Recv == "" {
			return pf, true // the script already names the component
		}
	}
	for _, n := range facts.Renders {
		qc := QualifiedCall{Name: n}
		if head, tail, ok := strings.Cut(n, "."); ok {
			qc = QualifiedCall{Qualifier: head, Name: tail}
		}
		qcalls = append(qcalls, qc)
	}
	for _, h := range facts.Handlers {
		qcalls = append(qcalls, QualifiedCall{Name: h})
	}
	seenQ := map[QualifiedCall]bool{}
	seen := map[string]bool{}
	var callees []string
	var uq []QualifiedCall
	for _, qc := range qcalls {
		if qc.Name == "" || seenQ[qc] || rules.Builtins[qc.Name] {
			continue
		}
		seenQ[qc] = true
		uq = append(uq, qc)
		if !seen[qc.Name] {
			seen[qc.Name] = true
			callees = append(callees, qc.Name)
		}
	}
	lines := strings.Count(src, "\n") + 1
	pf.Symbols = append(pf.Symbols, Symbol{
		Name: facts.Name, Kind: "function",
		Signature: "component " + facts.Name + "(" + facts.Props + ")",
		CanonBody: []byte(strings.Join(strings.Fields(src), " ")),
		Callees:   callees, QualifiedCalls: uq,
	})
	pf.Spans = append(pf.Spans, query.SymbolSpan{Name: facts.Name, StartLine: 1, EndLine: lines})
	return pf, true
}
