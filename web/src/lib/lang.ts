// Which CodeMirror language mode highlights a file, by its extension. Each
// mode is its own lazily loaded chunk, so a page showing Go code never
// downloads the C++ or Python grammars.

export type Lang = "cpp" | "python" | "javascript" | "typescript" | "go" | "java" | "rust" | "yaml" | "json" | "xml";

const BY_EXT: Record<string, Lang> = {};
const add = (lang: Lang, exts: string[]) => exts.forEach((e) => (BY_EXT[e] = lang));
add("cpp", ["c", "cc", "cpp", "cxx", "h", "hh", "hpp", "hxx", "ino", "cu"]);
add("python", ["py", "pyx", "pyi"]);
add("javascript", ["js", "jsx", "mjs", "cjs"]);
add("typescript", ["ts", "tsx"]);
add("go", ["go"]);
add("java", ["java", "kt"]);
add("rust", ["rs"]);
add("yaml", ["yaml", "yml"]);
add("json", ["json"]);
add("xml", ["xml", "launch", "urdf", "xacro", "sdf", "html"]);

export function langFor(path: string): Lang | null {
  const base = path.split("/").pop() ?? "";
  const dot = base.lastIndexOf(".");
  if (dot < 0) return null;
  return BY_EXT[base.slice(dot + 1).toLowerCase()] ?? null;
}
