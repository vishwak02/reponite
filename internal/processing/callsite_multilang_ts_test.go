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
		if strings.HasSuffix(path, ".json") {
			parsed = append(parsed, ParsedFile{Path: path, Content: src, Lang: "json"})
			continue
		}
		ext := ".ts"
		switch {
		case strings.HasSuffix(path, ".py"):
			ext = ".py"
		case strings.HasSuffix(path, ".tsx"):
			ext = ".tsx"
		case strings.HasSuffix(path, ".rs"):
			ext = ".rs"
		case strings.HasSuffix(path, ".java"):
			ext = ".java"
		}
		rules, _ := RulesForExt(ext)
		root, spans, err := parseFileRules([]byte(src), ext, rules)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, ParsedFile{Path: path, Content: src, Lang: rules.Name,
			Symbols: Extract(root, rules, 1), Spans: spans, Imports: Imports(root, rules), Types: typeFactsFor(root, rules)})
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

// Rust: Type::f() is that type's function — twelve types with a new() are
// twelve targets, not one; Self::, self., trait impls and paths into other
// crates each resolve by their own rule.
func TestRustCallSiteResolution(t *testing.T) {
	files := map[string]string{
		"src/walk.rs": `pub struct Walker { n: usize }
impl Walker {
    pub fn new() -> Self { Self::with(0) }
    fn with(n: usize) -> Self { Walker { n } }
    pub fn run(&self) { self.step(); helper(); crate::filter::matches(); std::mem::drop(1); }
    fn step(&self) { let v: Vec<u8> = Vec::new(); v.len(); }
}
impl std::fmt::Display for Walker {
    fn fmt(&self) { self.step(); }
}
fn helper() {}
pub fn scan() { Walker::new().run(); }
struct State;
impl State { fn scan(&self) { scan(); } }
`,
		"src/main.rs": `mod walk;
mod filter;
fn main() { walk::scan(); filter::size::parse(); scan_all(); }
fn scan_all() {}
`,
		"src/filter/size.rs": `pub fn parse() {}
`,
		"src/filter.rs": `pub struct Filter;
impl<T> Filter { pub fn new() -> Self { Filter } }
pub fn matches() -> bool { let w = crate::walk::Walker::new(); let f = Filter::new(); true }
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["src.walk.Walker.run"]
	if run == nil {
		t.Fatalf("Walker.run not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, run, "src.walk.Walker.step", MethodResolved)
	wantMethod(t, run, "src.walk.helper", MethodResolved)
	wantMethod(t, run, "src.filter.matches", MethodResolved)
	wantMethod(t, run, "std::drop", MethodExternal)
	wantMethod(t, edges["src.walk.Walker.new"], "src.walk.Walker.with", MethodResolved)
	wantMethod(t, edges["src.walk.Walker.fmt"], "src.walk.Walker.step", MethodResolved) // impl Trait for Walker
	m := edges["src.filter.matches"]
	wantMethod(t, m, "src.walk.Walker.new", MethodResolved)
	wantMethod(t, m, "src.filter.Filter.new", MethodResolved) // impl<T> Filter
	mn := edges["src.main"]
	if mn == nil {
		t.Fatalf("main not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, mn, "src.walk.scan", MethodResolved) // the module's function, not State.scan
	wantMethod(t, mn, "src/filter.size.parse", MethodResolved)
	wantMethod(t, edges["src.walk.State.scan"], "src.walk.scan", MethodResolved) // a bare call is never a method
	step := edges["src.walk.Walker.step"]
	wantMethod(t, step, "Vec::new", MethodExternal)
	for name := range step {
		if strings.HasSuffix(name, ".new") {
			t.Errorf("Vec::new() pinned on a crate type: %s", name)
		}
	}
}

// Rust method calls typed by their receiver: self's fields, parameters,
// let bindings, constructors' return types; a library value's method is the
// library's even when the crate defines a method of that name.
func TestRustReceiverTyping(t *testing.T) {
	files := map[string]string{
		"src/batch.rs": `pub struct Batch { items: Vec<u8> }
impl Batch {
    pub fn new() -> Self { Batch { items: vec![] } }
    pub fn lock(&self) -> bool { true }
}
pub struct Sender { batch: Batch, limit: usize }
impl Sender {
    pub fn build() -> Result<Sender> { Ok(Sender { batch: Batch::new(), limit: 1 }) }
    pub fn threads(&self) -> usize { self.limit }
    fn send(&mut self, other: &Batch) {
        self.batch.lock();
        other.lock();
        let b = Batch::new();
        b.lock();
        let s = Sender::build()?;
        s.threads();
        let mut builder = WalkBuilder::new();
        builder.threads(4);
        builder.hidden(true).lock();
        let v: Vec<u8> = Vec::new();
        v.lock();
        Sender::build().unwrap().threads();
    }
}
`,
	}
	edges := indexAndEdges(t, files)
	send := edges["src.batch.Sender.send"]
	if send == nil {
		t.Fatalf("Sender.send not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, send, "src.batch.Batch.lock", MethodTyped)
	wantMethod(t, send, "src.batch.Sender.threads", MethodTyped)
	wantMethod(t, send, "WalkBuilder::threads", MethodExternal)
	wantMethod(t, send, "Vec::lock", MethodExternal)
	wantMethod(t, send, "extern::lock", MethodExternal) // on what a library method returned
}

// Java: a call's receiver typed from fields, parameters and locals; an
// interface call reaches the declaration and the implementations; a library
// object's method is never the repo's method of the same name.
func TestJavaReceiverTyping(t *testing.T) {
	files := map[string]string{
		"app/repo/ArticleRepository.java": `package app.repo;
public interface ArticleRepository {
  Optional<Article> findBySlug(String slug);
  void save(Article a);
}`,
		"app/infra/MyBatisArticleRepository.java": `package app.infra;
public class MyBatisArticleRepository implements ArticleRepository {
  public Optional<Article> findBySlug(String slug) { return null; }
  public void save(Article a) {}
}`,
		"app/query/ArticleQueryService.java": `package app.query;
public class ArticleQueryService {
  public Optional<ArticleData> findBySlug(String slug, User user) { return null; }
  public String format(String s) { return s; }
  private void fill(String id) { format(id); }
  private void fill(String id, User u) { findBySlug(id, u); }
}`,
		"app/api/ArticleApi.java": `package app.api;
public class ArticleApi {
  private ArticleQueryService articleQueryService;
  private ArticleRepository articleRepository;
  public Object article(String slug, User user) {
    articleQueryService.findBySlug(slug, user);
    articleRepository.findBySlug(slug);
    String s = slug;
    s.format("x");
    ArticleQueryService q = new ArticleQueryService();
    q.format(s);
    return null;
  }
}`,
	}
	edges := indexAndEdges(t, files)
	a := edges["app/api.ArticleApi.article"]
	if a == nil {
		t.Fatalf("ArticleApi.article not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, a, "app/query.ArticleQueryService.findBySlug", MethodTyped)
	wantMethod(t, a, "app/repo.ArticleRepository.findBySlug", MethodTyped)
	wantMethod(t, a, "app/infra.MyBatisArticleRepository.findBySlug", MethodOverride)
	wantMethod(t, a, "app/query.ArticleQueryService.format", MethodTyped)
	wantMethod(t, a, "String::format", MethodExternal)
	// overloads share one id, which calls what each of them calls
	fill := edges["app/query.ArticleQueryService.fill"]
	wantMethod(t, fill, "app/query.ArticleQueryService.format", MethodResolved)
	wantMethod(t, fill, "app/query.ArticleQueryService.findBySlug", MethodResolved)
}

// tsconfig paths and baseUrl make an aliased import the project's own code.
func TestTSConfigPathAliases(t *testing.T) {
	files := map[string]string{
		"web/tsconfig.json": `{
  // comments and trailing commas, as tsconfig allows
  "compilerOptions": {
    "baseUrl": "./src",
    "paths": {
      "@app/*": ["./*"],
      "@app/types": ["types/index.ts"], /* exact */
    },
  },
}`,
		"web/src/components/empty/index.tsx": `export const EmptyPlaceholder = () => null;`,
		"web/src/types/index.ts": `export function isAgent(x: unknown) { return !!x; }`,
		"web/src/utils/fmt.ts": `export function fmt(s: string) { return s; }`,
		"web/src/components/popConfirm/index.tsx": `const PopconfirmCustom = () => null;
export default PopconfirmCustom;`,
		"web/src/dash/Header/Header.tsx": `const GrafanaHeader = () => null;
export default GrafanaHeader;`,
		"web/src/dash/Header/index.ts": `import Header from './Header';
export default Header;`,
		"web/src/lib/index.ts": `export { fmt as format } from '../utils/fmt';
export * from './more';`,
		"web/src/lib/more.ts": `export function helper() { return 1; }`,
		"web/src/changelog.ts": `class Changelog { undo() { return 1; } }
const changelog = new Changelog();
export default changelog;`,
		"web/src/app/page.tsx": `import { EmptyPlaceholder } from '@app/components/empty';
import { isAgent } from '@app/types';
import { fmt } from 'utils/fmt';
import PopConfirm from '@app/components/popConfirm';
import changelog from '@app/changelog';
export function Page() { isAgent(1); fmt('x'); return <EmptyPlaceholder />; }
import DashboardHeader from '@app/dash/Header';
import { format, helper } from '@app/lib';
export function Confirm() { return <PopConfirm />; }
export function Dash() { format('x'); helper(); return <DashboardHeader />; }
export function undo() { changelog.undo(); }`,
	}
	edges := indexAndEdges(t, files)
	p := edges["web/src/app.page.Page"]
	if p == nil {
		t.Fatalf("Page not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, p, "web/src/components/empty.EmptyPlaceholder", MethodResolved)
	wantMethod(t, p, "web/src/types.isAgent", MethodResolved)
	wantMethod(t, p, "web/src/utils.fmt", MethodResolved) // baseUrl-relative
	// a default import is the module's default export, under any local name
	wantMethod(t, edges["web/src/app.page.Confirm"], "web/src/components/popConfirm.PopconfirmCustom", MethodResolved)
	// barrels and default re-exports lead to the defining module
	d := edges["web/src/app.page.Dash"]
	wantMethod(t, d, "web/src/dash/Header.Header.GrafanaHeader", MethodResolved)
	wantMethod(t, d, "web/src/utils.fmt", MethodResolved)
	wantMethod(t, d, "web/src/lib.more.helper", MethodResolved)
	// changelog.undo() is the default export's method, never the caller undo
	u := edges["web/src/app.page.undo"]
	if _, self := u["web/src/app.page.undo"]; self {
		t.Error("changelog.undo() resolved to the caller itself")
	}
	wantMethod(t, u, "web/src.changelog.Changelog.undo", MethodTyped) // the default export is a Changelog
}

func TestJSONCToJSON(t *testing.T) {
	got := jsoncToJSON("{\"a\": \"http://x\", // c\n \"b\": [1, 2,], /* d */ }")
	if got != "{\"a\": \"http://x\", \n \"b\": [1, 2]  }" {
		t.Errorf("jsoncToJSON = %q", got)
	}
}

// Python method calls typed by annotations, constructors, factory returns
// and fields set on self.
func TestPythonReceiverTyping(t *testing.T) {
	files := map[string]string{
		"app/notify/notifier.py": `class Notifier:
    def alert(self, ids):
        pass
class Other:
    def alert(self, ids):
        pass
`,
		"app/notify/repo.py": `from .notifier import Notifier
class Repo:
    def save(self):
        pass
class Service:
    def __init__(self):
        self.repo = Repo()
    def run(self, n: Notifier):
        n.alert([1])
        self.repo.save()
`,
		"app/tests/test_notify.py": `from app.notify.notifier import Notifier
def make() -> Notifier:
    return Notifier()
def make2():
    return Notifier()
def test_alert():
    a = make()
    a.alert([7])
def test_alert2():
    b = make2()
    b.alert([7])
def test_direct():
    c = Notifier()
    c.alert([7])
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["app/notify.repo.Service.run"]
	if run == nil {
		t.Fatalf("Service.run not indexed; have %v", symKeys(edges))
	}
	wantMethod(t, run, "app/notify.notifier.Notifier.alert", MethodTyped)
	wantMethod(t, run, "app/notify.repo.Repo.save", MethodTyped)
	for _, fn := range []string{"test_alert", "test_alert2", "test_direct"} {
		e := edges["app/tests.test_notify."+fn]
		wantMethod(t, e, "app/notify.notifier.Notifier.alert", MethodTyped)
		if _, bad := e["app/notify.notifier.Other.alert"]; bad {
			t.Errorf("%s: alert pinned on Other", fn)
		}
	}
}
