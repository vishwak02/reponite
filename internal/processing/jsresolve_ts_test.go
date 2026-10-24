//go:build treesitter

package processing

import (
	"testing"
)

// export { useThing as default } from './useThing': the index passes on
// useThing itself, whether imported by name through a barrel or by default.
func TestTSDefaultReexportedByName(t *testing.T) {
	files := map[string]string{
		"src/hooks/useThing/useThing.ts": `export const useThing = () => 1;`,
		"src/hooks/useThing/index.ts":    `export { useThing as default } from './useThing';`,
		"src/hooks/index.ts": `import useThing from './useThing';
export { useThing };`,
		"src/other.ts": `export const useThing = () => 2;`,
		"src/app.ts": `import { useThing } from './hooks';
import useIt from './hooks/useThing';
export function a() { useThing(); }
export function b() { useIt(); }`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["src.app.a"], "src/hooks/useThing.useThing", MethodResolved)
	wantMethod(t, edges["src.app.b"], "src/hooks/useThing.useThing", MethodResolved)
}

// A JSX tag is an intrinsic element only when it starts with a lowercase
// letter or holds a dash: <_Row> renders the component _Row.
func TestTSXUnderscoreTagRenders(t *testing.T) {
	files := map[string]string{
		"src/list.tsx": `const _Row = () => <div />;
export const List = () => (
  <section>
    <_Row />
    <my-widget />
  </section>
);`,
	}
	edges := indexAndEdges(t, files)
	l := edges["src.list.List"]
	wantMethod(t, l, "src.list._Row", MethodResolved)
	for name := range l {
		if name == "section" || name == "my-widget" || name == "widget" {
			t.Errorf("intrinsic element taken as a call: %v", l)
		}
	}
}

// var helpers = { … }; export default helpers: a default import of the
// object calls its methods, functions of the defining module.
func TestJSDefaultImportedObjectLiteral(t *testing.T) {
	files := map[string]string{
		"src/utils/geo.js": `var geo = {
  normalize(v) { return v; },
  flatten(v) { return v; },
};
export default geo;`,
		"src/other.js": `export class Vec { normalize() { return 1; } }`,
		"src/line.js": `import geo from './utils/geo';
export function draw(p) {
  return geo.flatten(geo.normalize(p));
}`,
	}
	edges := indexAndEdges(t, files)
	l := edges["src.line.draw"]
	wantMethod(t, l, "src/utils.geo.normalize", MethodResolved)
	wantMethod(t, l, "src/utils.geo.flatten", MethodResolved)
	if _, bad := l["src.other.Vec.normalize"]; bad {
		t.Errorf("pinned on Vec: %v", l)
	}
}

// export default withIntl<Props>(Panel): the wrapped component renders.
func TestTSXWrappedDefaultWithTypeArguments(t *testing.T) {
	files := map[string]string{
		"src/panel/index.tsx": `import { withIntl } from 'intl-lib';
const Panel: React.FC<Props> = () => <div />;
export default withIntl<Props>(Panel);`,
		"src/page.tsx": `import Panel from './panel';
export const Page = () => <Panel />;`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["src.page.Page"], "src/panel.Panel", MethodResolved)
}

// `//` in JSX text is text, not a comment: the function around it is
// still parsed whole.
func TestTSXSlashesInJSXText(t *testing.T) {
	files := map[string]string{
		"src/bar.tsx": `import { Status } from './status';
export default function Bar() {
  if (x) {
    return (
      <div>
        <h1>
          A<span className="r">//</span>B
        </h1>
      </div>
    );
  }
  return <Status />;
}`,
		"src/status.tsx": `export function Status() { return null; }`,
	}
	edges := indexAndEdges(t, files)
	b, ok := edges["src.bar.Bar"]
	if !ok {
		t.Fatalf("Bar not indexed: %v", symKeys(edges))
	}
	wantMethod(t, b, "src.status.Status", MethodResolved)
}

// interface Item beside function Item: the value takes the id and its span.
func TestTSInterfaceNamedLikeFunction(t *testing.T) {
	src := `interface Item {
  id: number;
}
export function List(items: Item[]) {
  return items.map((i) => Item(i));
}
function Item(i: Item) {
  return i;
}
type Other = { a: number };
`
	rules, _ := RulesForExt(".ts")
	root, spans, err := parseFileRules([]byte(src), ".ts", rules)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string][]string{}
	for _, s := range Extract(root, rules, 1) {
		kinds[s.Name] = append(kinds[s.Name], s.Kind)
	}
	if got := kinds["Item"]; len(got) != 1 || got[0] != "function" {
		t.Errorf("Item: %v, want one function", got)
	}
	if got := kinds["Other"]; len(got) != 1 || got[0] != "type" {
		t.Errorf("Other: %v, want its type", got)
	}
	var lines []int
	for _, sp := range spans {
		if sp.Name == "Item" {
			lines = append(lines, sp.StartLine)
		}
	}
	if len(lines) != 1 || lines[0] != 7 {
		t.Errorf("Item spans at %v, want [7]", lines)
	}
}

// import * as Shapes from './shapes'; export { Shapes }: <Shapes.Circle />
// through the barrel is the Circle that module passes on.
func TestTSXNamespaceThroughBarrel(t *testing.T) {
	files := map[string]string{
		"src/layers/shapes/Circle/Circle.tsx": `const CircleLayer = () => null;
export default CircleLayer;`,
		"src/layers/shapes/Circle/index.ts": `import Circle from './Circle';
export default Circle;`,
		"src/layers/shapes/Square.tsx": `export const Square = () => null;`,
		"src/layers/shapes/index.ts": `import Circle from './Circle';
import { Square as Box } from './Square';
export { Circle, Box };`,
		"src/layers/index.ts": `import * as Shapes from './shapes';
export * as More from './shapes';
export { Shapes };`,
		"src/map.tsx": `import { Shapes, More } from './layers';
export const Map = () => (
  <div>
    <Shapes.Circle />
    <More.Box />
  </div>
);`,
	}
	edges := indexAndEdges(t, files)
	m := edges["src.map.Map"]
	wantMethod(t, m, "src/layers/shapes/Circle.Circle.CircleLayer", MethodResolved)
	wantMethod(t, m, "src/layers/shapes.Square", MethodResolved)
}

// A receiver whose class the code states — a parameter's annotation, a
// constructed local, an annotated let, the caller's own instance under
// another name, an element of a typed field — calls that class's method,
// never the same-named method of another class.
func TestTSReceiverTypedLocals(t *testing.T) {
	files := map[string]string{
		"src/shop/Slot.ts": `export default class Slot {
  setState(s: number) { return s; }
  reset() { return 0; }
}`,
		"src/shop/Shelf.ts": `import Slot from './Slot';
export default class Shelf {
  private slots: Array<Slot> = [];
  private byKey: Record<string, Slot> = {};
  private first?: Slot;
  private second?: Slot;
  setState(s: number) {
    this.slots.forEach((slot) => slot.setState(s));
    return s;
  }
  clearAll() {
    for (const s of this.slots) {
      s.reset();
    }
    Object.values(this.byKey).forEach((x) => x.reset());
    [this.first, this.second].forEach((y) => y?.reset());
    this.byKey['a']?.reset();
  }
  pick(a?: Slot, b?: Shelf) {
    a?.setState(1);
    b?.setState(2);
    let fresh: Slot;
    fresh = this.slots[0];
    fresh.reset();
    const made = new Slot();
    made.setState(3);
    const scope = this;
    scope.clearAll();
    this.slots.push(made);
  }
}`,
	}
	edges := indexAndEdges(t, files)
	set := edges["src/shop.Shelf.setState"]
	wantMethod(t, set, "src/shop.Slot.setState", MethodTyped)
	clr := edges["src/shop.Shelf.clearAll"]
	wantMethod(t, clr, "src/shop.Slot.reset", MethodTyped)
	p := edges["src/shop.Shelf.pick"]
	wantMethod(t, p, "src/shop.Slot.setState", MethodTyped)
	wantMethod(t, p, "src/shop.Shelf.setState", MethodTyped)
	wantMethod(t, p, "src/shop.Slot.reset", MethodTyped)
	wantMethod(t, p, "src/shop.Shelf.clearAll", MethodTyped)
	wantMethod(t, p, "Array::push", MethodExternal)
}

// A field's class: its annotation, its initializer, a constructor parameter
// property, or what the class's methods assign to it (plain JS); a
// module-level instance's fields; what an imported function constructs.
func TestTSFieldTyping(t *testing.T) {
	files := map[string]string{
		"src/store/accessor.ts": `export class Accessor<T> {
  get(): T | undefined { return undefined; }
}
export class SiteAccessor extends Accessor<string> {
  get = () => 'site';
}
export class Store {
  auth = new Accessor<string>();
  site = new SiteAccessor();
}
export const STORE = new Store();
export const token = () => STORE.site.get();`,
		"src/net/conn.ts": `class Conn {
  on(ev: string) { return ev; }
  close() { return 0; }
}
const open = (url: string) => new Conn();
export default open;`,
		"src/net/index.ts": `export class Index { get(k: string) { return k; } remove(k: string) { return k; } }`,
		"src/net/session.ts": `import { Index } from './index';
export class Session {
  private index: Index;
  constructor(private backup: Index) {
    this.index = backup;
  }
  modify(k: string) {
    this.index.get(k);
    this.backup.remove(k);
  }
}`,
		"src/net/client.js": `import open from './conn';
export class Client {
  constructor() {
    this.ws = null;
  }
  connect() {
    this.ws = open('x');
    this.ws.on('open');
    const self = this;
    setTimeout(function () {
      this.ws.close();
    });
    self.ws.close();
  }
}`,
		"src/ui/login.ts": `import { STORE } from '../store/accessor';
export function signIn() {
  return STORE.auth.get();
}`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["src/ui.login.signIn"], "src/store.accessor.Accessor.get", MethodTyped)
	wantMethod(t, edges["src/store.accessor.token"], "src/store.accessor.SiteAccessor.get", MethodTyped)
	m := edges["src/net.session.Session.modify"]
	wantMethod(t, m, "src/net.Index.get", MethodTyped)
	wantMethod(t, m, "src/net.Index.remove", MethodTyped)
	c := edges["src/net.client.Client.connect"]
	wantMethod(t, c, "src/net.conn.Conn.on", MethodTyped)
	wantMethod(t, c, "src/net.conn.Conn.close", MethodTyped)
}

// What the code does not state is not guessed: a variable given two
// classes, `this` inside a function that binds its own, a field a library
// base class may declare.
func TestTSReceiverTypingLimits(t *testing.T) {
	files := map[string]string{
		"src/a.ts": `import { Widget } from 'ui-lib';
export class Cat { speak() { return 1; } }
export class Dog { speak() { return 2; } }
export class Panel extends Widget {
  run(flag: boolean) {
    let pet = new Cat();
    if (flag) {
      pet = new Dog();
    }
    pet.speak();
    this.child.speak();
  }
}`,
	}
	edges := indexAndEdges(t, files)
	r := edges["src.a.Panel.run"]
	for _, bad := range []string{"src.a.Cat.speak", "src.a.Dog.speak"} {
		if m, ok := r[bad]; ok && m != MethodMember {
			t.Errorf("speak typed on %s (%s): %v", bad, m, r)
		}
	}
}

// const [a, store] = await Promise.all([import('./a'), import('./store')]):
// each name is the module loaded at its position.
func TestTSDynamicImportsInPromiseAll(t *testing.T) {
	files := map[string]string{
		"src/store/index.ts": `export const createStore = () => 1;`,
		"src/main.ts":        `export const createStore = () => 2; export const start = () => 3;`,
		"src/app.ts": `export async function boot() {
  const [mainModule, , storeModule] = await Promise.all([import('./main'), import('./i18n'), import('./store')]);
  mainModule.start();
  storeModule.createStore();
}`,
	}
	edges := indexAndEdges(t, files)
	b := edges["src.app.boot"]
	wantMethod(t, b, "src/store.createStore", MethodResolved)
	wantMethod(t, b, "src.main.start", MethodResolved)
	if _, bad := b["src.main.createStore"]; bad {
		t.Errorf("pinned on src.main: %v", b)
	}
}
