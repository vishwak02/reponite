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
	s := edges["src/store.sagas.watchAgent"]
	if s == nil {
		t.Fatalf("watchAgent not indexed; have %v", symKeys(edges))
	}
	for name, c := range s {
		switch {
		case name == "src/utils.kv.KVHelper.put" || name == "scripts.check.error":
			t.Errorf("imported/global call pinned on repo code: %s %+v", name, c)
		}
	}
	wantMethod(t, s, "redux-saga/effects::put", MethodExternal)
	wantMethod(t, s, "console::error", MethodExternal)
	wantMethod(t, s, "src/api.fetchMap", MethodResolved) // '@/api' is the repo's own alias
	wantMethod(t, s, "map", MethodAmbiguous)             // builtin array member vs KVHelper.map
	wantMethod(t, s, "src/utils.kv.Helper.onlyHere", MethodMember)
	// this.get() inside a class method: its own class.
	wantMethod(t, edges["src/utils.kv.KVHelper.put"], "src/utils.kv.KVHelper.get", MethodResolved)
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
	s := edges["pkg.worker.Worker.run"]
	if s == nil {
		t.Fatalf("run not indexed; have %v", symKeys(edges))
	}
	if _, bad := s["pkg.util.post"]; bad {
		t.Error("requests.post pinned on the repo's post()")
	}
	wantMethod(t, s, "pkg.worker.Worker.step", MethodResolved)
	wantMethod(t, s, "requests::post", MethodExternal)
	wantMethod(t, s, "pkg.util.helper", MethodResolved) // absolute import of the repo's own package
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
	for _, want := range []string{"src/components.ui.Loading", "src/components.ui.Keyboard", "src/components.ui.Pure",
		"src/components.ui.short", "src/components.ui.Old.onClick"} {
		if _, ok := edges[want]; !ok {
			t.Errorf("missing symbol %s (have %v)", want, symKeys(edges))
		}
	}
	if _, ok := edges["src/components.ui.handler"]; ok {
		t.Error("a non-function value (new Topic(...)) must not become a function symbol")
	}
	wantMethod(t, edges["src/components.ui.Loading"], "src/components.ui.format", MethodResolved)
	wantMethod(t, edges["src/components.ui.short"], "src/components.ui.format", MethodResolved)
	wantMethod(t, edges["src/components.ui.Old.onClick"], "src/components.ui.Old.save", MethodResolved)
}

// self.f() / this.f() with f defined on a base class: the base found through
// the module's imports, in Python's MRO, not a guess among same-named methods.
func TestInheritedSelfCallResolution(t *testing.T) {
	files := map[string]string{
		"app/core/base.py": `class Scaffold:
    def add_rule(self):
        pass
    def setup(self):
        pass
class App(Scaffold):
    def add_rule(self):
        pass
`,
		"app/main.py": `from .core.base import App
from . import core
class Flask(App):
    def __init__(self):
        self.add_rule()
        self.setup()
        self.missing()
class Left(App):
    def setup(self):
        pass
class Right(App):
    pass
class Diamond(Right, Left):
    def go(self):
        self.setup()
    def setup(self):
        super().setup()
        self.helper.setup()
`,
		"app/other.py": `class App:
    def add_rule(self):
        pass
class Tool(Unknown):
    def go(self):
        self.add_rule()
`,
		"web/src/base.ts": `export abstract class Repo {
  save(x: number) { return x; }
}`,
		"web/src/users.ts": `import { Repo } from './base';
export class Users extends Repo {
  add(x: number) { return this.save(x); }
}
export class Admins extends Users {
  save(x: number) { return super.save(x) + 1; }
}`,
	}
	edges := indexAndEdges(t, files)
	init := edges["app.main.Flask.__init__"]
	if init == nil {
		t.Fatalf("Flask.__init__ not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, init, "app/core.base.App.add_rule", MethodResolved) // App's own, not Scaffold's or other.App's
	wantMethod(t, init, "app/core.base.Scaffold.setup", MethodResolved)
	for name := range init {
		if name == "app.other.App.add_rule" || name == "app/core.base.Scaffold.add_rule" {
			t.Errorf("self call resolved past the MRO: %s", name)
		}
	}
	// C3: Diamond(Right, Left) -> Right, Left, App: Left.setup runs, not Scaffold's
	dgo := edges["app.main.Diamond.go"]
	wantMethod(t, dgo, "app.main.Diamond.setup", MethodResolved)
	wantMethod(t, edges["app.main.Diamond.setup"], "app.main.Left.setup", MethodResolved)
	if _, self := edges["app.main.Diamond.setup"]["app.main.Diamond.setup"]; self {
		t.Error("super().setup() resolved to the caller itself")
	}
	// a base outside the repo comes first: its own add_rule may be the one
	if c, ok := edges["app.other.Tool.go"]["app.other.App.add_rule"]; ok && c == MethodResolved {
		t.Errorf("self call through an unknown base resolved: %s", c)
	}
	add := edges["web/src.users.Users.add"]
	if add == nil {
		t.Fatalf("Users.add not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, add, "web/src.base.Repo.save", MethodResolved)
	wantMethod(t, add, "web/src.users.Admins.save", MethodOverride)
	wantMethod(t, edges["web/src.users.Admins.save"], "web/src.base.Repo.save", MethodResolved)
}

func TestPythonTwoArgSuper(t *testing.T) {
	files := map[string]string{
		"core/renderers.py": `class Base:
    def render(self, data):
        return data
`,
		"auth/renderers.py": `from core.renderers import Base
class User(Base):
    def render(self, data):
        return super(User, self).render(data)
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["auth.renderers.User.render"], "core.renderers.Base.render", MethodResolved)
	if _, self := edges["auth.renderers.User.render"]["auth.renderers.User.render"]; self {
		t.Error("super(User, self).render() resolved to the caller itself")
	}
}
