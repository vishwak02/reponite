// An edge's provenance as a badge: its tier (compiler-proven, typed, guessed,
// ambiguous) drawn with the line style the graphs use for it, and the
// confidence. The method and what it means are in the tooltip.
import { methodNote, tierOf, TIER_HINT, TIER_LABEL, type Tier } from "@/lib/provenance";
import { cn } from "@/lib/utils";

const TONE: Record<Tier, string> = {
  proven: "bg-ok/15 text-ok",
  typed: "bg-primary/15 text-primary",
  guessed: "bg-warn/15 text-warn",
  ambiguous: "bg-bad/15 text-bad",
};

// solid, dashed, dotted, faint: the same strokes as the graph edges
export const TIER_DASH: Record<Tier, string | undefined> = { proven: undefined, typed: "3.5 2", guessed: "1.2 2.2", ambiguous: "1 3.5" };

export function TierStroke({ tier, className }: { tier: Tier; className?: string }) {
  return (
    <svg viewBox="0 0 14 6" className={cn("h-1.5 w-3.5 shrink-0", className)} aria-hidden>
      <line x1="0.5" y1="3" x2="13.5" y2="3" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeDasharray={TIER_DASH[tier]} opacity={tier === "ambiguous" ? 0.7 : 1} />
    </svg>
  );
}

export function ProvenanceBadge({ method, confidence, compact, className }: { method?: string; confidence?: number; compact?: boolean; className?: string }) {
  const tier = tierOf(method, confidence);
  const pct = confidence != null ? `${Math.round(confidence * 100)}%` : "";
  const title = [TIER_LABEL[tier] + ": " + TIER_HINT[tier], methodNote(method), pct && `confidence ${pct}`].filter(Boolean).join(" · ");
  return (
    <span title={title} data-tier={tier} className={cn("inline-flex shrink-0 items-center gap-1 rounded-md px-1.5 py-0.5 text-[11px] font-medium leading-none", TONE[tier], className)}>
      <TierStroke tier={tier} />
      {!compact && TIER_LABEL[tier]}
      {pct && <span className="tabular-nums opacity-75">{pct}</span>}
    </span>
  );
}
