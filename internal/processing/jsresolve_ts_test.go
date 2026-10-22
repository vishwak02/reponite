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
