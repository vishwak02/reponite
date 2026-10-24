// Read-only source with highlighting; loaded lazily (CodeMirror is the
// largest dependency), and each language mode only when a file needs it.
import { useEffect, useState } from "react";
import CodeMirror, { EditorView } from "@uiw/react-codemirror";
import { Decoration, lineNumbers, type DecorationSet } from "@codemirror/view";
import { RangeSetBuilder, StateField, type Extension } from "@codemirror/state";
import { langFor, type Lang } from "@/lib/lang";
import { useScope } from "@/lib/scope";

const modes: Record<Lang, () => Promise<Extension>> = {
  cpp: () => import("@codemirror/lang-cpp").then((m) => m.cpp()),
  python: () => import("@codemirror/lang-python").then((m) => m.python()),
  javascript: () => import("@codemirror/lang-javascript").then((m) => m.javascript({ jsx: true })),
  typescript: () => import("@codemirror/lang-javascript").then((m) => m.javascript({ jsx: true, typescript: true })),
  go: () => import("@codemirror/lang-go").then((m) => m.go()),
  java: () => import("@codemirror/lang-java").then((m) => m.java()),
  rust: () => import("@codemirror/lang-rust").then((m) => m.rust()),
  yaml: () => import("@codemirror/lang-yaml").then((m) => m.yaml()),
  json: () => import("@codemirror/lang-json").then((m) => m.json()),
  xml: () => import("@codemirror/lang-xml").then((m) => m.xml()),
};

// the file's language mode, once its chunk has loaded (plain text until then)
function useMode(path: string): Extension[] {
  const lang = langFor(path);
  const [mode, setMode] = useState<{ lang: Lang; ext: Extension } | null>(null);
  useEffect(() => {
    if (!lang || mode?.lang === lang) return;
    let live = true;
    modes[lang]().then(
      (ext) => live && setMode({ lang, ext }),
      () => {}, // highlighting is optional: a failed chunk leaves plain text
    );
    return () => {
      live = false;
    };
  }, [lang]); // eslint-disable-line react-hooks/exhaustive-deps
  return mode && mode.lang === lang ? [mode.ext] : [];
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
  const mode = useMode(path);
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
      extensions={[...mode, lineNumbers({ formatNumber: (n) => String(n + firstLine - 1) }), EditorView.lineWrapping, ...(mark ? [markLines(mark[0], mark[1])] : [])]}
    />
  );
}
