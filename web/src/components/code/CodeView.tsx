// Read-only source with highlighting; loaded lazily (CodeMirror is the
// largest dependency).
import CodeMirror, { EditorView } from "@uiw/react-codemirror";
import { cpp } from "@codemirror/lang-cpp";
import { python } from "@codemirror/lang-python";
import { javascript } from "@codemirror/lang-javascript";
import { go } from "@codemirror/lang-go";
import { java } from "@codemirror/lang-java";
import { rust } from "@codemirror/lang-rust";
import { yaml } from "@codemirror/lang-yaml";
import { json } from "@codemirror/lang-json";
import { xml } from "@codemirror/lang-xml";
import { Decoration, lineNumbers, type DecorationSet } from "@codemirror/view";
import { RangeSetBuilder, StateField, type Extension } from "@codemirror/state";
import { useScope } from "@/lib/scope";

function lang(path: string) {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  if (["c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx", "ino", "cu"].includes(ext)) return [cpp()];
  if (["py", "pyx", "pyi"].includes(ext)) return [python()];
  if (["js", "jsx", "mjs", "cjs"].includes(ext)) return [javascript({ jsx: true })];
  if (["ts", "tsx"].includes(ext)) return [javascript({ jsx: true, typescript: true })];
  if (ext === "go") return [go()];
  if (["java", "kt"].includes(ext)) return [java()];
  if (ext === "rs") return [rust()];
  if (["yaml", "yml"].includes(ext)) return [yaml()];
  if (ext === "json") return [json()];
  if (["xml", "launch", "urdf", "xacro", "sdf", "html"].includes(ext)) return [xml()];
  return [];
}

// marks lines [from, to] (1-based, in the shown text) and scrolls them into view
function markLines(from: number, to: number): Extension {
  const deco = Decoration.line({ class: "cm-marked" });
  const field = StateField.define<DecorationSet>({
    create(state) {
      const b = new RangeSetBuilder<Decoration>();
      for (let n = Math.max(1, from); n <= Math.min(to, state.doc.lines); n++) b.add(state.doc.line(n).from, state.doc.line(n).from, deco);
      return b.finish();
    },
    update: (d) => d,
    provide: (f) => EditorView.decorations.from(f),
  });
  return field;
}

export default function CodeView({ code, path, firstLine = 1, maxHeight = "70vh", mark, editable, onChange }: { code: string; path: string; firstLine?: number; maxHeight?: string; mark?: [number, number]; editable?: boolean; onChange?: (v: string) => void }) {
  const { dark } = useScope();
  return (
    <CodeMirror
      onCreateEditor={(view) => {
        if (mark && mark[0] <= view.state.doc.lines) {
          const pos = view.state.doc.line(mark[0]).from;
          requestAnimationFrame(() => view.dispatch({ effects: EditorView.scrollIntoView(pos, { y: "start", yMargin: 60 }) }));
        }
      }}
      onChange={onChange}
      value={code}
      theme={dark ? "dark" : "light"}
      editable={!!editable}
      readOnly={!editable}
      maxHeight={maxHeight}
      basicSetup={{ lineNumbers: false, foldGutter: true, highlightActiveLine: false, highlightActiveLineGutter: false }}
      extensions={[...lang(path), lineNumbers({ formatNumber: (n) => String(n + firstLine - 1) }), EditorView.lineWrapping, ...(mark ? [markLines(mark[0], mark[1])] : [])]}
    />
  );
}
