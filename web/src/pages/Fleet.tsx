// The fleet map: repos as nodes, cross-repo links (declared dependencies,
// imports, ROS topics/services, HTTP) as edges.
import { lazy, Suspense, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowLeft, ArrowRight, ExternalLink, X } from "lucide-react";
import { useScope } from "@/lib/scope";
import { cn, fmt, hslRgba, tint, tintRgba } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Empty, Skeleton } from "@/components/ui/skeleton";
import type { SEdge, SNode } from "@/components/graph/SigmaGraph";
import { useFleetMap } from "./Overview";

const SigmaGraph = lazy(() => import("@/components/graph/SigmaGraph"));

const KIND_HSL: Record<string, [number, number, number]> = {
  "declared-dep": [220, 10, 60],
  import: [248, 90, 64],
  "ros-topic": [170, 70, 40],
  "ros-service": [32, 92, 50],
  http: [340, 80, 55],
};
const kindHsl = (k: string) => KIND_HSL[k] ?? [268, 70, 58];
const kindColor = (k: string, a = 1) => { const [h, s, l] = kindHsl(k); return `hsl(${h} ${s}% ${l}% / ${a})`; };
const kindRgba = (k: string, a: number) => { const [h, s, l] = kindHsl(k); return hslRgba(h, s, l, a); };

export default function Fleet() {
  const fm = useFleetMap();
  const { setRepo } = useScope();
  const nav = useNavigate();
  const kinds = useMemo(() => {
    const m = new Map<string, number>();
    for (const l of fm.data?.links ?? []) m.set(l.kind, (m.get(l.kind) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [fm.data]);
  // package.xml dependencies outnumber every other kind; start with them hidden
  const [off, setOff] = useState<Set<string>>(new Set(["declared-dep"]));
  const [isolated, setIsolated] = useState(false);
  const [sel, setSel] = useState<string | null>(null);
  const links = useMemo(() => (fm.data?.links ?? []).filter((l) => !off.has(l.kind)), [fm.data, off]);

  const { nodes, edges } = useMemo(() => {
    const deg = new Map<string, number>();
    for (const l of links) {
      deg.set(l.from, (deg.get(l.from) ?? 0) + 1);
      deg.set(l.to, (deg.get(l.to) ?? 0) + 1);
    }
    const nodes: SNode[] = (fm.data?.repos ?? [])
      .filter((r) => isolated || deg.has(r.repo))
      .map((r) => ({ id: r.repo, label: r.repo, size: 3 + Math.sqrt(r.symbols ?? 0) / 9, color: tintRgba(r.repo) }));
    const edges: SEdge[] = links.map((l) => ({ from: l.from, to: l.to, size: 0.6 + Math.log2(1 + l.count) * 0.5, color: kindRgba(l.kind, l.kind === "declared-dep" ? 0.3 : 0.5), label: `${l.kind} ×${l.count}` }));
    return { nodes, edges };
  }, [fm.data, links, isolated]);

  const repo = fm.data?.repos?.find((r) => r.repo === sel);
  const out = links.filter((l) => l.from === sel).sort((a, b) => b.count - a.count);
  const inc = links.filter((l) => l.to === sel).sort((a, b) => b.count - a.count);

  return (
    <Page wide title="Fleet map" sub="Each repo is a node sized by its code; an edge means one repo depends on, imports, or talks to another.">
      <div className="mb-3 flex flex-wrap items-center gap-1.5">
        {kinds.map(([k, n]) => (
          <button
            key={k}
            onClick={() => setOff((s) => { const x = new Set(s); x.has(k) ? x.delete(k) : x.add(k); return x; })}
            className={cn("flex items-center gap-1.5 rounded-full border px-2.5 py-1 text-xs font-medium transition-opacity", off.has(k) && "opacity-40")}
          >
            <span className="size-2 rounded-full" style={{ background: kindColor(k) }} />
            {k}
            <span className="tabular-nums text-muted-foreground">{n}</span>
          </button>
        ))}
        <label className="ml-2 flex items-center gap-1.5 text-xs text-muted-foreground">
          <input type="checkbox" checked={isolated} onChange={(e) => setIsolated(e.target.checked)} />
          show unlinked repos
        </label>
        <select value={sel ?? ""} onChange={(e) => setSel(e.target.value || null)} className="ml-auto h-8 rounded-md border bg-card px-2 text-xs">
          <option value="">Find a repo…</option>
          {nodes.map((n) => n.id).sort().map((r) => <option key={r} value={r}>{r}</option>)}
        </select>
      </div>
      <div className="relative overflow-hidden rounded-xl border bg-card dotgrid">
        {fm.isLoading ? (
          <Skeleton className="h-[calc(100vh-220px)]" />
        ) : fm.error ? (
          <Empty error title="Fleet map failed">{String(fm.error)}</Empty>
        ) : (
          <Suspense fallback={<Skeleton className="h-[calc(100vh-220px)]" />}>
            <SigmaGraph nodes={nodes} edges={edges} selected={sel} onSelect={setSel} height="calc(100vh - 220px)" />
          </Suspense>
        )}
        <div className="pointer-events-none absolute bottom-3 left-3 rounded-md border bg-card/90 px-2.5 py-1 text-[11px] text-muted-foreground backdrop-blur">
          {nodes.length} repos · {edges.length} links · scroll to zoom, drag to pan
        </div>
        {repo && (
          <Card className="absolute bottom-3 right-3 top-3 flex w-[340px] flex-col overflow-hidden shadow-xl animate-fade-in">
            <div className="border-b p-3">
              <div className="flex items-start gap-2">
                <span className="mt-1.5 size-2.5 shrink-0 rounded-full" style={{ background: tint(repo.repo) }} />
                <div className="min-w-0 flex-1">
                  <div className="font-semibold">{repo.repo}</div>
                  <div className="font-mono text-[11px] text-muted-foreground">{repo.ref}</div>
                </div>
                <button onClick={() => setSel(null)} className="text-muted-foreground hover:text-foreground"><X className="size-4" /></button>
              </div>
              {repo.summary && <p className="mt-2 text-xs text-muted-foreground">{repo.summary}</p>}
              <div className="mt-2 flex gap-3 text-xs"><span><b className="tabular-nums">{fmt(repo.symbols)}</b> symbols</span><span><b className="tabular-nums">{fmt(repo.files)}</b> files</span></div>
              <Button size="xs" className="mt-3" onClick={() => { setRepo(repo.repo); nav("/symbol"); }}><ExternalLink />Open repo</Button>
            </div>
            <div className="flex-1 space-y-3 overflow-y-auto p-3 text-xs">
              <LinkList title="Depends on / talks to" icon={<ArrowRight className="size-3.5" />} items={out.map((l) => ({ other: l.to, ...l }))} pick={setSel} />
              <LinkList title="Used by" icon={<ArrowLeft className="size-3.5" />} items={inc.map((l) => ({ other: l.from, ...l }))} pick={setSel} />
            </div>
          </Card>
        )}
      </div>
    </Page>
  );
}

function LinkList({ title, icon, items, pick }: { title: string; icon: React.ReactNode; items: { other: string; kind: string; count: number; examples?: string[] }[]; pick: (r: string) => void }) {
  return (
    <div>
      <div className="mb-1 flex items-center gap-1 font-semibold text-muted-foreground">{icon}{title} <span className="font-normal">({items.length})</span></div>
      {!items.length && <div className="px-2 text-muted-foreground">none</div>}
      {items.map((l) => (
        <button key={l.other + l.kind} onClick={() => pick(l.other)} className="block w-full rounded-md px-2 py-1.5 text-left hover:bg-muted">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">{l.other}</span>
            <span className="ml-auto shrink-0 rounded px-1.5 text-[10px]" style={{ background: kindColor(l.kind, 0.15), color: kindColor(l.kind) }}>{l.kind} ×{l.count}</span>
          </div>
          {l.examples?.length ? <div className="truncate font-mono text-[10px] text-muted-foreground">{l.examples.slice(0, 3).join(", ")}</div> : null}
        </button>
      ))}
    </div>
  );
}
