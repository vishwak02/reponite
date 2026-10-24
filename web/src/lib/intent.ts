// The Ask bar's intent router: which analysis a typed question or symbol is
// asking for. Plain rules, no model — the first rule that matches wins, and
// anything no rule claims goes to investigate (ranked evidence for a plain
// question). The Ask page runs the chosen analysis; the bar shows the choice
// as the user types, so the routing is never a surprise.

export type IntentKind =
  | "symbol" // a bare symbol: open its hub
  | "impact" // what breaks if X changes: blast radius
  | "callers" // who calls X
  | "tests" // which tests reach X
  | "impls" // what implements X
  | "history" // why X is the way it is: commits and versions
  | "explain" // what X is / does (a symbol, file, directory or route)
  | "route" // what runs on METHOD /path
  | "logline" // a pasted log line: where it is printed
  | "config" // where a configuration key comes from
  | "errors" // where an error message or type comes from
  | "investigate"; // anything else: a cited dossier

export interface Intent {
  kind: IntentKind;
  // what the analysis is asked about: a symbol, key, path, line or the
  // question itself
  arg: string;
  // the HTTP method of a route question ("" when any)
  method?: string;
}

export const INTENT_LABEL: Record<IntentKind, string> = {
  symbol: "Symbol",
  impact: "Blast radius",
  callers: "Callers",
  tests: "Tests",
  impls: "Implementations",
  history: "History",
  explain: "Explain",
  route: "Route",
  logline: "Log line",
  config: "Config key",
  errors: "Error",
  investigate: "Investigate",
};

const METHODS = "GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS";
// one name: an identifier, a qualified id (pkg/sub.Type.method, ns::f), or a
// path with an optional :line
const NAME = String.raw`[A-Za-z_~$][\w$~]*(?:(?:\.|::|/|->|#)[A-Za-z_~$][\w$~]*)*(?:\(\))?`;
const TARGET = String.raw`[\`'"]?([^\s\`'"?]+)[\`'"]?`;

// a log line: a level marker, a timestamp, a glog/ROS prefix
const LOG_LINE = [
  /^\s*\[\s*(?:INFO|WARN|WARNING|ERROR|DEBUG|FATAL|TRACE|CRITICAL)\s*\]/i,
  /^\s*(?:INFO|WARN|WARNING|ERROR|DEBUG|FATAL|TRACE|CRITICAL)\s*[:|\]-]/,
  /^\s*[IWEF]\d{4}\s+\d{2}:\d{2}:\d{2}/, // glog
  /\b\d{4}-\d{2}-\d{2}[ T]\d{2}:\d{2}(?::\d{2})?/, // ISO timestamp
  /^\s*\[\s*\d{9,}\.\d+\s*\]/, // [1690000000.123] (ROS)
  /^\s*\d{2}:\d{2}:\d{2}[.,]\d+\s/, // 12:01:02.345 message
];

// a file path: a known source, config or document extension, with an
// optional :line (pkg/sub.Type.method is a symbol, not a file)
const FILE_EXT = /\.(?:c|cc|cpp|cxx|h|hh|hpp|hxx|cu|ino|py|pyi|pyx|js|jsx|mjs|cjs|ts|tsx|vue|svelte|go|java|kt|rs|rb|php|cs|swift|scala|sh|bash|yaml|yml|json|toml|ini|cfg|conf|xml|launch|urdf|xacro|sdf|html|css|scss|md|rst|txt|proto|msg|srv|action|sql|gradle|cmake|mk|dockerfile|env)(?::\d+)?$/i;

const strip = (s: string) => s.replace(/^[`'"]+|[`'"?.!,;:]+$/g, "").replace(/\(\)$/, "");

function routeOf(q: string): Intent | null {
  const m = new RegExp(String.raw`\b(${METHODS})\s+(/\S*)`, "i").exec(q);
  if (m) return { kind: "route", arg: strip(m[2]), method: m[1].toUpperCase() };
  const p = /\b(?:what|which|who)\s+(?:runs|handles|serves|answers|is behind|happens)\b.*?\s(\/[\w\-./{}:<>*]*)/i.exec(q);
  if (p) return { kind: "route", arg: strip(p[1]), method: "" };
  if (/^\/[\w\-./{}:<>*]+$/.test(q) && !/\.\w{1,5}$/.test(q)) return { kind: "route", arg: q, method: "" };
  return null;
}

// routeIntent reads what a question asks for.
export function routeIntent(input: string): Intent | null {
  const q = input.trim();
  if (!q) return null;

  if (LOG_LINE.some((r) => r.test(q))) return { kind: "logline", arg: q };

  const route = routeOf(q);
  if (route) return route;

  let m: RegExpExecArray | null;
  // an error message or type: "error: card declined", `where does "card declined" come from`
  if ((m = /^(?:error|exception|panic|fatal)\s*[:\-]\s*(.+)$/i.exec(q))) return { kind: "errors", arg: strip(m[1]) };
  if ((m = /\bwhere\s+(?:does|do|is|are)\s+(?:the\s+)?(?:error\s+)?["“']([^"”']*\s[^"”']*)["”']\s+(?:come\s+from|thrown|raised|returned|produced|logged)/i.exec(q)))
    return { kind: "errors", arg: m[1] };
  if ((m = /\b(?:who|what)\s+(?:throws|raises|returns|handles|catches)\s+(?:the\s+)?(?:error\s+)?["“'`]?([^"”'`?]+?)["”'`]?\s*\??$/i.exec(q)))
    return { kind: "errors", arg: strip(m[1]) };

  // a configuration key: "where does max_speed come from", "who sets X"
  if ((m = new RegExp(String.raw`\bwhere\s+(?:does|do|is|are)\s+(?:the\s+)?(?:config(?:uration)?\s+|param(?:eter)?\s+|setting\s+|key\s+|env(?:ironment)?\s+(?:var(?:iable)?\s+)?)?${TARGET}\s+(?:come\s+from|get\s+set|set|defined|configured|read)`, "i").exec(q)))
    return { kind: "config", arg: strip(m[1]) };
  if ((m = new RegExp(String.raw`^(?:config(?:uration)?|param(?:eter)?|setting|env)\s*[:\s]\s*${TARGET}$`, "i").exec(q))) return { kind: "config", arg: strip(m[1]) };
  if ((m = new RegExp(String.raw`\b(?:who|what)\s+(?:sets|reads|configures)\s+(?:the\s+)?(?:config(?:uration)?\s+|param(?:eter)?\s+|key\s+)?${TARGET}\s*$`, "i").exec(q)))
    return { kind: "config", arg: strip(m[1]) };

  // what breaks if X changes
  if (
    (m = new RegExp(String.raw`\b(?:what\s+(?:breaks|would\s+break|will\s+break|is\s+affected|depends\s+on\s+it)|who\s+is\s+affected)\b.*?\b(?:change|changing|edit|editing|modify|modifying|remove|removing|rename|renaming|delete|deleting|touch|touching)\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\b(?:blast\s+radius|impact)\s+(?:of\s+)?(?:changing\s+)?${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\bif\s+I\s+(?:change|edit|modify|remove|rename|delete)\s+${TARGET}`, "i").exec(q))
  )
    return { kind: "impact", arg: strip(m[1]) };

  // who calls X
  if (
    (m = new RegExp(String.raw`\bwho\s+(?:calls|uses|invokes|references)\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\b(?:callers|usages|uses)\s+of\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\bwhere\s+is\s+${TARGET}\s+(?:called|used|invoked)`, "i").exec(q))
  )
    return { kind: "callers", arg: strip(m[1]) };

  // which tests reach X
  if (
    (m = new RegExp(String.raw`\b(?:which|what)\s+tests?\s+(?:cover|covers|reach|reaches|exercise|exercises|run|hit|hits|call|calls)\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`^tests?\s+(?:for|of)\s+${TARGET}`, "i").exec(q))
  )
    return { kind: "tests", arg: strip(m[1]) };

  // what implements X
  if (
    (m = new RegExp(String.raw`\b(?:who|what|which\s+classes?)\s+(?:implements|derives\s+from|inherits\s+from|extends|overrides)\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\b(?:implementations?|subclasses|overrides|derived\s+classes)\s+of\s+${TARGET}`, "i").exec(q))
  )
    return { kind: "impls", arg: strip(m[1]) };

  // why X is the way it is
  if (
    (m = new RegExp(String.raw`\bwho\s+(?:changed|wrote|touched|last\s+changed)\s+${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\b(?:history|blame)\s+(?:of\s+)?${TARGET}`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\bwhy\s+(?:is|was|does)\s+${TARGET}\s+(?:like\s+this|written|this\s+way|changed|implemented)`, "i").exec(q)) ||
    (m = new RegExp(String.raw`\bwhen\s+did\s+${TARGET}\s+change`, "i").exec(q))
  )
    return { kind: "history", arg: strip(m[1]) };

  // what X is / does
  if (
    (m = new RegExp(String.raw`^(?:what\s+is|what's|what\s+does|explain|describe|show(?:\s+me)?)\s+(?:the\s+)?${TARGET}(?:\s+do)?\s*\??$`, "i").exec(q))
  )
    return { kind: "explain", arg: strip(m[1]) };

  // one name, or a path
  if (!/\s/.test(q)) {
    const t = strip(q);
    if (/[/\\]/.test(t) && (FILE_EXT.test(t) || t.endsWith("/"))) return { kind: "explain", arg: t };
    if (new RegExp(`^${NAME}$`).test(t)) return { kind: "symbol", arg: t };
  }
  return { kind: "investigate", arg: q };
}

// The examples the bar suggests (they exercise the rules above).
export const ASK_EXAMPLES = [
  "what breaks if I change Charge",
  "who calls validateCard",
  "what runs on POST /api/orders",
  "where does max_speed come from",
  "[ERROR] charging order a1 failed",
  "how is an order charged",
];

export interface NamedHit {
  repo: string;
  name: string;
  is_test?: boolean;
}

// how well a qualified id names what was typed: 0 exact, 1 its last segment
// (or Class.method pair), 2 a suffix, 3 anything else
export function nameScore(name: string, arg: string): number {
  if (name === arg) return 0;
  const a = arg.replace(/::|->|#/g, ".");
  const n = name.replace(/::|->|#/g, ".");
  if (n === a) return 0;
  const tail = n.slice(n.lastIndexOf("/") + 1);
  const parts = tail.split(".");
  if (parts[parts.length - 1] === a || parts.slice(-2).join(".") === a) return 1;
  if (n.endsWith("." + a) || n.endsWith("/" + a)) return 2;
  return 3;
}

// pickSymbol orders search hits for what was typed — best name match, then
// production code, then the preferred repo, then the shorter id — and says
// whether the best is the only one that good (an unambiguous answer).
export function pickSymbol<T extends NamedHit>(hits: T[], arg: string, prefer?: string): { ranked: T[]; unique: boolean } {
  const key = (h: T) => [nameScore(h.name, arg), h.is_test ? 1 : 0, h.repo === prefer ? 0 : 1, h.name.length] as const;
  const ranked = [...hits].sort((a, b) => {
    const x = key(a);
    const y = key(b);
    for (let i = 0; i < x.length; i++) if (x[i] !== y[i]) return x[i] - y[i];
    return a.repo < b.repo ? -1 : a.repo > b.repo ? 1 : a.name < b.name ? -1 : 1;
  });
  const best = ranked[0];
  if (!best) return { ranked, unique: false };
  const b = key(best);
  const tied = ranked.filter((h) => {
    const k = key(h);
    return k[0] === b[0] && k[1] === b[1];
  });
  return { ranked, unique: b[0] <= 2 && tied.length === 1 };
}
