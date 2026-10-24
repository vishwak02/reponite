// How sure the index is of a call edge, from the resolution method and the
// confidence every edge carries (internal/processing/resolve.go):
//   proven    — a compiler bound the call (Go type checker, clang with the
//               build's flags)
//   typed     — resolved through a declared type, an import or a moniker,
//               not compiled
//   guessed   — matched by name (one definition of that name in scope, one
//               class defining that member), or an external leaf
//   ambiguous — several definitions match; the edge may be any of them

export type Tier = "proven" | "typed" | "guessed" | "ambiguous";

export const TIER_LABEL: Record<Tier, string> = {
  proven: "compiler-proven",
  typed: "typed",
  guessed: "guessed",
  ambiguous: "ambiguous",
};

export const TIER_HINT: Record<Tier, string> = {
  proven: "a compiler bound this call",
  typed: "resolved through a declared type, an import or a symbol moniker",
  guessed: "matched by name, not by type",
  ambiguous: "several definitions match this call",
};

export const TIERS: Tier[] = ["proven", "typed", "guessed", "ambiguous"];

const PROVEN = /^(?:go-types|clang[-_]|tsc(?:-|$)|scip-typed)/;
const TYPED = new Set(["receiver-typed", "receiver-class", "scip-resolved", "import-resolved", "xrepo-import", "tsconfig", "sequelize-typescript"]);
const GUESSED = new Set([
  "name-resolved",
  "member-name-resolved",
  "virtual-override",
  "xrepo-override",
  "xrepo-base-call",
  "unresolved-external",
  "instance-name",
  "names-symbol",
  "name-in-inline",
  "topic-name",
]);

// tierOf maps an edge's resolution method and confidence to its tier. A
// known method decides; a method this build does not know is placed by its
// confidence. A low confidence demotes any method (an edge the indexer
// itself doubts is never shown as proven).
export function tierOf(method?: string, confidence?: number): Tier {
  const m = (method ?? "").toLowerCase();
  const c = confidence ?? 0;
  if (m === "ambiguous" || (c > 0 && c < 0.55)) return "ambiguous";
  if (PROVEN.test(m)) return c >= 0.95 || confidence == null ? "proven" : "typed";
  if (TYPED.has(m)) return "typed";
  if (GUESSED.has(m)) return "guessed";
  return c >= 0.8 ? "typed" : "guessed";
}

// a short reading of the method for a tooltip ("unresolved-external" is a
// call out of the indexed code)
export function methodNote(method?: string): string {
  if (!method) return "";
  if (method === "unresolved-external") return "external: defined outside the indexed code";
  return method;
}
