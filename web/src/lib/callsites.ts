// Where in a symbol's body each of its callees is called: the lines that
// name the callee followed by a call's "(" — a textual match, coloured by the
// edge's own provenance (the index records which callees a body calls, not
// on which line). A line calling several is marked with its least sure edge.
import { tierOf, TIERS, TIER_LABEL, type Tier } from "./provenance";

export interface CallSite {
  line: number; // absolute line in the file
  tier: Tier;
  title: string;
}

interface Edge {
  symbol: string;
  resolution_method?: string;
  confidence?: number;
}

const esc = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// the name a call site spells: the last segment of a qualified id
export function calledName(symbol: string): string {
  const t = symbol.replace(/\(\)$/, "");
  const parts = t.split(/::|->|\.|\/|#/);
  return parts[parts.length - 1] || t;
}

// callSites marks the lines of text (whose first line is firstLine) that
// call one of callees, within [from, to] when given.
export function callSites(text: string, firstLine: number, callees: Edge[], from = -Infinity, to = Infinity): CallSite[] {
  const pats = callees
    .map((e) => ({ e, name: calledName(e.symbol) }))
    .filter((x) => /^[A-Za-z_$~][\w$~]*$/.test(x.name))
    .map((x) => ({ ...x, re: new RegExp(`(?:^|[^\\w$])${esc(x.name)}\\s*(?:<[^<>()]*>)?\\s*\\(`) }));
  const out: CallSite[] = [];
  text.split("\n").forEach((src, i) => {
    const line = firstLine + i;
    if (line < from || line > to) return;
    const hits = pats.filter((p) => p.re.test(src));
    if (!hits.length) return;
    const tiers = hits.map((h) => tierOf(h.e.resolution_method, h.e.confidence));
    const tier = tiers.reduce((a, b) => (TIERS.indexOf(b) > TIERS.indexOf(a) ? b : a));
    const title = hits
      .map((h, k) => `${h.e.symbol} — ${TIER_LABEL[tiers[k]]}${h.e.resolution_method ? ` (${h.e.resolution_method}${h.e.confidence != null ? ` ${Math.round(h.e.confidence * 100)}%` : ""})` : ""}`)
      .join("\n");
    out.push({ line, tier, title });
  });
  return out;
}
