// What the index holds: per repo, the files stored and the files skipped,
// with the reason for each skip.
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronRight, Search } from "lucide-react";
import { api, type CoverageRepo, type CoverageResult } from "@/lib/api";
import { FLEET_REF } from "@/lib/scope";
import { cn, fmt, tint } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { Badge } from "@/components/ui/badge";
import { Card, Stat } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows } from "@/components/ui/skeleton";

const REASON_HUE: Record<string, string> = { binary: "220 10% 55%", generated: "268 70% 58%", vendored: "32 92% 50%", too_large: "356 72% 55%", lfs: "190 70% 45%" };
const reasonColor = (r: string) => `hsl(${REASON_HUE[r] ?? `${(r.length * 47) % 360} 55% 55%`})`;

export default function Coverage() {
  const c = useQuery({ queryKey: ["coverage", FLEET_REF], queryFn: () => api<CoverageResult>("coverage", { ref: FLEET_REF }), staleTime: 5 * 60_000 });
  const [q, setQ] = useState("");
  const [open, setOpen] = useState<string | null>(null);
  const repos = useMemo(() => (c.data?.repos ?? []).filter((r) => !q || r.repo.toLowerCase().includes(q.toLowerCase())).sort((a, b) => b.stored + b.skipped - (a.stored + a.skipped)), [c.data, q]);
  const tot = useMemo(() => {
    const reasons = new Map<string, number>();
    let stored = 0, skipped = 0;
    for (const r of c.data?.repos ?? []) {
      stored += r.stored;
      skipped += r.skipped;
      for (const [k, v] of Object.entries(r.reasons ?? {})) reasons.set(k, (reasons.get(k) ?? 0) + v);
    }
    return { stored, skipped, reasons: [...reasons.entries()].sort((a, b) => b[1] - a[1]) };
  }, [c.data]);
  return (
    <Page title="Index coverage" sub="Which files each repo's index stored, and why the rest were left out.">
      {c.isLoading ? <Rows n={10} /> : c.error ? <Empty error title="Coverage failed">{String(c.error)}</Empty> : (
        <>
          <div className="grid grid-cols-2 items-start gap-3 lg:grid-cols-4">
            <Stat label="Files stored" value={fmt(tot.stored)} />
            <Stat label="Files skipped" value={fmt(tot.skipped)} />
            <Stat label="Stored share" value={`${((100 * tot.stored) / Math.max(1, tot.stored + tot.skipped)).toFixed(1)}%`} />
            <Card className="p-4">
              <div className="text-xs font-medium text-muted-foreground">Skip reasons</div>
              <div className="mt-2 flex flex-wrap gap-1.5">
                {tot.reasons.map(([k, v]) => <span key={k} className="flex items-center gap-1 text-xs"><span className="size-2 rounded-full" style={{ background: reasonColor(k) }} />{k} <b className="tabular-nums">{fmt(v)}</b></span>)}
              </div>
            </Card>
          </div>
          <div className="mb-3 mt-5 flex items-center gap-2">
            <div className="relative w-64"><Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input className="h-8 pl-8" placeholder="Filter repos…" value={q} onChange={(e) => setQ(e.target.value)} /></div>
            {c.data?.note && <span className="text-xs text-muted-foreground">{c.data.note}</span>}
          </div>
          <Card className="divide-y overflow-hidden">
            {repos.map((r) => <RepoRow key={r.repo} r={r} open={open === r.repo} toggle={() => setOpen(open === r.repo ? null : r.repo)} />)}
          </Card>
        </>
      )}
    </Page>
  );
}

function RepoRow({ r, open, toggle }: { r: CoverageRepo; open: boolean; toggle: () => void }) {
  const total = r.stored + r.skipped || 1;
  const reasons = Object.entries(r.reasons ?? {}).sort((a, b) => b[1] - a[1]);
  return (
    <div>
      <button onClick={toggle} className="flex w-full items-center gap-3 px-4 py-2.5 text-left hover:bg-muted/40">
        <ChevronRight className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <span className="size-2 shrink-0 rounded-full" style={{ background: tint(r.repo) }} />
        <span className="w-56 truncate text-sm font-medium">{r.repo}</span>
        <div className="flex h-2 flex-1 overflow-hidden rounded-full bg-muted">
          <div className="h-full bg-ok" style={{ width: `${(100 * r.stored) / total}%` }} title={`${r.stored} stored`} />
          {reasons.map(([k, v]) => <div key={k} className="h-full" style={{ width: `${(100 * v) / total}%`, background: reasonColor(k) }} title={`${k}: ${v}`} />)}
        </div>
        <span className="w-28 text-right text-xs tabular-nums text-muted-foreground">{fmt(r.stored)} / {fmt(r.stored + r.skipped)}</span>
        {!r.skips_recorded && <Badge variant="warn" title="Indexed before skips were recorded; re-index to see them">no skip log</Badge>}
      </button>
      {open && (
        <div className="grid gap-4 border-t bg-muted/20 px-11 py-3 text-xs lg:grid-cols-2 animate-fade-in">
          <div>
            <div className="mb-1.5 font-semibold text-muted-foreground">By file kind</div>
            <table className="w-full">
              <tbody>
                {r.kinds.slice(0, 24).map((k) => (
                  <tr key={k.kind} className="border-b border-border/50">
                    <td className="py-1 font-mono">{k.kind}</td>
                    <td className="py-1 text-right tabular-nums">{fmt(k.stored)}</td>
                    <td className="py-1 pl-3 text-muted-foreground">{Object.entries(k.skipped ?? {}).map(([a, b]) => `${b} ${a}`).join(", ")}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
          <div>
            <div className="mb-1.5 font-semibold text-muted-foreground">Examples of skipped files</div>
            {Object.entries(r.examples ?? {}).map(([k, xs]) => (
              <div key={k} className="mb-2">
                <div className="flex items-center gap-1.5 font-medium"><span className="size-2 rounded-full" style={{ background: reasonColor(k) }} />{k}</div>
                {xs.slice(0, 5).map((x) => <div key={x} className="truncate pl-3.5 font-mono text-[11px] text-muted-foreground">{x}</div>)}
              </div>
            ))}
            {!Object.keys(r.examples ?? {}).length && <div className="text-muted-foreground">Nothing skipped.</div>}
          </div>
        </div>
      )}
    </div>
  );
}
