//go:build treesitter

package processing

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// Real misattributions from a React/redux-saga UI: `yield put(...)` (bound by
// an import from redux-saga/effects) resolved to the repo's KVHelper.put, and
// `console.error(...)` to a script's `error` function.
func TestTSCallSiteResolution(t *testing.T) {
	files := map[string]string{
		"src/store/sagas.ts": `import { put } from 'redux-saga/effects';
import { fetchMap } from '@/api';
export function* watchAgent() {
  yield put({ type: 'X' });
  console.error('bad');
  fetchMap();
  items.map((x) => x);
  helper.onlyHere(1);
}`,
		"src/utils/kv.ts": `export class KVHelper {
  put(k: string) { return this.get(k); }
  get(k: string) { return k; }
  map(k: string) { return k; }
}
export class Helper { onlyHere(n: number) { return n; } }`,
		"src/api/index.ts": `export function fetchMap() { return 1; }`,
		"scripts/check.ts": `export function error(msg: string) { return msg; }`,
	}
	edges := indexAndEdges(t, files)
	s := edges["src/store.watchAgent"]
	if s == nil {
		t.Fatalf("watchAgent not indexed; have %v", symKeys(edges))
	}
	for name, c := range s {
		switch {
		case name == "src/utils.KVHelper.put" || name == "scripts.error":
			t.Errorf("imported/global call pinned on repo code: %s %+v", name, c)
		}
	}
	wantMethod(t, s, "redux-saga/effects::put", MethodExternal)
	wantMethod(t, s, "console::error", MethodExternal)
	wantMethod(t, s, "src/api.fetchMap", MethodResolved) // '@/api' is the repo's own alias
	wantMethod(t, s, "map", MethodAmbiguous)             // builtin array member vs KVHelper.map
	wantMethod(t, s, "src/utils.Helper.onlyHere", MethodMember)
	// this.get() inside a class method: its own class.
	wantMethod(t, edges["src/utils.KVHelper.put"], "src/utils.KVHelper.get", MethodResolved)
}

func TestPythonCallSiteResolution(t *testing.T) {
	files := map[string]string{
		"pkg/worker.py": `import logging
from requests import post
from pkg.util import helper
class Worker:
    def run(self):
        self.step()
        post("http://x")
        logging.error("x")
        helper()
    def step(self):
        pass
`,
		"pkg/util.py": `def helper():
    pass
def post(u):
    pass
`,
	}
	edges := indexAndEdges(t, files)
	s := edges["pkg.Worker.run"]
	if s == nil {
		t.Fatalf("run not indexed; have %v", symKeys(edges))
	}
	if _, bad := s["pkg.post"]; bad {
		t.Error("requests.post pinned on the repo's post()")
	}
	wantMethod(t, s, "pkg.Worker.step", MethodResolved)
	wantMethod(t, s, "requests::post", MethodExternal)
	wantMethod(t, s, "pkg.helper", MethodResolved) // absolute import of the repo's own package
}

func indexAndEdges(t *testing.T, files map[string]string) map[string]map[string]string {
	t.Helper()
	var parsed []ParsedFile
	for path, src := range files {
		ext := ".ts"
		switch {
		case strings.HasSuffix(path, ".py"):
			ext = ".py"
		case strings.HasSuffix(path, ".tsx"):
			ext = ".tsx"
		}
		rules, _ := RulesForExt(ext)
		root, spans, err := parseFileRules([]byte(src), ext, rules)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, ParsedFile{Path: path, Content: src, Lang: rules.Name,
			Symbols: Extract(root, rules, 1), Spans: spans, Imports: Imports(root, rules)})
	}
	m := storage.NewMem()
	if err := IndexFiles(m, "r", "v1", 1, parsed); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]string{}
	for caller, cs := range m.Snapshot("r", "v1").Callees {
		out[caller] = map[string]string{}
		for _, c := range cs {
			out[caller][c.Name] = c.ResolutionMethod
		}
	}
	for q := range m.Snapshot("r", "v1").Symbols {
		if out[q] == nil {
			out[q] = map[string]string{}
		}
	}
	return out
}

func wantMethod(t *testing.T, edges map[string]string, name, method string) {
	t.Helper()
	got, ok := edges[name]
	if !ok {
		t.Errorf("missing edge %s (have %v)", name, edges)
		return
	}
	if got != method {
		t.Errorf("%s: %s, want %s", name, got, method)
	}
}

// React components are mostly `const X = () => ...`, forwardRef/memo-wrapped,
// or class-field arrows — none were symbols before VarFuncDecl.
func TestTSArrowComponentsAreSymbols(t *testing.T) {
	files := map[string]string{
		"src/components/ui.tsx": `import React, { memo } from 'react';
export const Loading: React.FC = () => {
  return <div>{format(1)}</div>;
};
const Keyboard = React.forwardRef<HTMLDivElement, Props>((props, ref) => {
  useThing();
  return <div ref={ref} />;
});
const Pure = memo(function Pure() { return null; });
const short = () => format(2);
const handler = new Topic({ name: '/x' });
class Old extends React.Component {
  onClick = () => { this.save(); };
  save() {}
}
export function format(n: number) { return String(n); }`,
	}
	edges := indexAndEdges(t, files)
	for _, want := range []string{"src/components.Loading", "src/components.Keyboard", "src/components.Pure",
		"src/components.short", "src/components.Old.onClick"} {
		if _, ok := edges[want]; !ok {
			t.Errorf("missing symbol %s (have %v)", want, symKeys(edges))
		}
	}
	if _, ok := edges["src/components.handler"]; ok {
		t.Error("a non-function value (new Topic(...)) must not become a function symbol")
	}
	wantMethod(t, edges["src/components.Loading"], "src/components.format", MethodResolved)
	wantMethod(t, edges["src/components.short"], "src/components.format", MethodResolved)
	wantMethod(t, edges["src/components.Old.onClick"], "src/components.Old.save", MethodResolved)
}
