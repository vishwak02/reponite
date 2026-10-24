// Callers and callees, each with its provenance: compiler-proven, typed,
// guessed or ambiguous (lib/provenance.ts), filterable by tier. Explain gives
// the edges with their call-site lines; context (with cross-repo dispatch)
// adds the neighbours explain caps and the overrides and callers other repos
// plug in at runtime.
import { useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowDownRight, ArrowUpLeft } from "lucide-react";
import { api, isSuggestion, type ContextResult, type ExplainEdge, type Suggestion } from "@/lib/api";
import { tierOf, TIER_LABEL, TIERS, type Tier } from "@/lib/provenance";
import { cn } from "@/lib/utils";
import { TierStroke } from "@/components/ui/provenance";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Empty, Rows } from "@/components/ui/skeleton";
import { EdgeRow } from "./ExplainTab";
import { useSymbolExplain, type TabProps } from "./data";

// mergeEdges: explain's edges first (they carry lines), then context's
// neighbours explain did not list
export function mergeEdges(rich: ExplainEdge[] | undefined, more: { name: string; resolution_method?: string; confidence?: number; repo?: string; is_test?: boolean }[] | undefined): ExplainEdge[] {
  const out = [...(rich ?? [])];
  const seen = new Set(out.map((e) => e.symbol));
  for (const m of more ?? []) {
    if (seen.has(m.name)) continue;
    seen.add(m.name);
    out.push({ symbol: m.name, repo: m.repo, resolution_method: m.resolution_method, confidence: m.confidence, is_test: m.is_test, external: m.resolution_method === "unresolved-external" });
  }
  return out;
}

export default function CallsTab({ repo, refName, s }: TabProps) {
  const ex = useSymbolExplain(repo, refName, s);
  const ctx = useQuery({
    queryKey: ["context", repo, refName, s],
    queryFn: ({ signal }) => api<ContextResult | Suggestion>("v2/context", { repo, ref: refName, symbol: s, tests: true }, signal),
  });
  const [only, setOnly] = useState<Tier | "">("");
  const c = ctx.data && !isSuggestion(ctx.data) ? ctx.data : undefined;
  const sym = ex.data?.symbol;
  const callers = useMemo(
    () => mergeEdges(sym?.callers, c?.caller_edges?.map((e) => ({ name: e.name, repo: e.repo, is_test: e.is_test, resolution_method: e.via ? "xrepo-base-call" : undefined, confidence: e.via ? 0.6 : undefined }))),
    [sym, c],
  );
  const callees = useMemo(() => mergeEdges(sym?.callees, c?.callee_edges), [sym, c]);
  if (ex.isLoading) return <Rows />;
  if (ex.error) return <Empty error title="Could not load the calls">{String(ex.error)}</Empty>;
  if (!sym) return <Empty title="No symbol to show calls for">{ex.data?.note}</Empty>;
  const all = [...callers, ...callees];
  const count = (t: Tier) => all.filter((e) => tierOf(e.resolution_method, e.confidence) === t).length;
  const keep = (e: ExplainEdge) => !only || tierOf(e.resolution_method, e.confidence) === only;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-1.5" role="group" aria-label="Filter by provenance">
        <button onClick={() => setOnly("")} className={cn("rounded-full border px-2.5 py-1 text-xs font-medium", !only ? "border-primary/40 bg-accent text-accent-foreground" : "bg-card text-muted-foreground hover:text-foreground")}>
          All <span className="tabular-nums opacity-70">{all.length}</span>
        </button>
        {TIERS.map((t) => (
          <button
            key={t}
            data-filter={t}
            onClick={() => setOnly(only === t ? "" : t)}
            disabled={!count(t)}
            className={cn("inline-flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium disabled:opacity-40", only === t ? "border-primary/40 bg-accent text-accent-foreground" : "bg-card text-muted-foreground hover:text-foreground")}
          >
            <TierStroke tier={t} />
            {TIER_LABEL[t]} <span className="tabular-nums opacity-70">{count(t)}</span>
          </button>
        ))}
        {ctx.isLoading && <span className="text-xs text-muted-foreground">adding cross-repo dispatch…</span>}
      </div>
      <div className="grid gap-4 lg:grid-cols-2">
        <EdgeList title="Callers" icon={<ArrowUpLeft className="size-4 text-shape" />} edges={callers.filter(keep)} total={Math.max(callers.length, (sym.callers?.length ?? 0) + (sym.more_callers ?? 0))} repo={repo} refName={refName} empty="Nothing in the index calls it." />
        <EdgeList title="Callees" icon={<ArrowDownRight className="size-4 text-primary" />} edges={callees.filter(keep)} total={Math.max(callees.length, (sym.callees?.length ?? 0) + (sym.more_callees ?? 0))} repo={repo} refName={refName} empty="It calls nothing in the index." />
      </div>
      {c?._meta?.warnings?.map((w) => (
        <p key={w} className="text-xs text-muted-foreground">
          {w}
        </p>
      ))}
    </div>
  );
}

function EdgeList({ title, icon, edges, total, repo, refName, empty }: { title: string; icon: ReactNode; edges: ExplainEdge[]; total: number; repo: string; refName: string; empty: string }) {
  return (
    <Card className="overflow-hidden">
      <CardHeader title={<span className="flex items-center gap-1.5">{icon}{title}</span>} right={<Badge variant="outline">{total}</Badge>} />
      <div className="max-h-[62vh] divide-y overflow-y-auto">
        {!edges.length && <div className="px-4 py-6 text-center text-sm text-muted-foreground">{empty}</div>}
        {edges.map((e) => (
          <EdgeRow key={e.symbol + (e.repo ?? "") + (e.line ?? "")} e={e} repo={repo} refName={refName} />
        ))}
      </div>
    </Card>
  );
}
