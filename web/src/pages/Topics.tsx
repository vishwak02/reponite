// ROS communication: every topic, service and action, who produces it and who
// consumes it — as a searchable list, a per-topic flow, and a fleet map.
import { lazy, Suspense, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Link } from "react-router-dom";
import { List, Network, Radio, Search } from "lucide-react";
import { api, type Endpoint, type TopicGroup, type TopicsResult } from "@/lib/api";
import { FLEET_REF } from "@/lib/scope";
import { cn, hslRgba, tint, tintRgba } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Badge, Confidence } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows, Skeleton } from "@/components/ui/skeleton";
import type { FEdge, FNode } from "@/components/graph/FlowGraph";
import type { SEdge, SNode } from "@/components/graph/SigmaGraph";

const FlowGraph = lazy(() => import("@/components/graph/FlowGraph"));
const SigmaGraph = lazy(() => import("@/components/graph/SigmaGraph"));

const key = (g: TopicGroup) => g.family + ":" + g.name;
const repos = (es?: Endpoint[]) => new Set((es ?? []).map((e) => e.repo));

export default function Topics() {
  const t = useQuery({ queryKey: ["topics", FLEET_REF], queryFn: () => api<TopicsResult>("topics", { ref: FLEET_REF }), staleTime: 5 * 60_000 });
  const [q, setQ] = useState("");
  const [fam, setFam] = useState("all");
  const [status, setStatus] = useState<"all" | "linked" | "cross" | "open">("all");
  const [sel, setSel] = useState<string | null>(null);
  const [view, setView] = useState<"list" | "map">("list");
  const groups = useMemo(() => {
    const ql = q.toLowerCase();
    return (t.data?.groups ?? []).filter((g) => {
      if (fam !== "all" && g.family !== fam) return false;
      if (status === "linked" && !g.connected) return false;
      if (status === "open" && g.connected) return false;
      if (status === "cross") {
        const all = new Set([...repos(g.producers), ...repos(g.consumers)]);
        if (!g.connected || all.size < 2) return false;
      }
      return !ql || g.name.toLowerCase().includes(ql) || [...(g.producers ?? []), ...(g.consumers ?? [])].some((e) => e.repo.toLowerCase().includes(ql) || (e.msg_type ?? "").toLowerCase().includes(ql));
    });
  }, [t.data, q, fam, status]);
  // nothing picked yet: show the first match, so the detail pane is never empty
  const selected = groups.find((g) => key(g) === sel) ?? (sel ? t.data?.groups?.find((g) => key(g) === sel) : undefined) ?? groups[0];
  const fams = useMemo(() => [...new Set((t.data?.groups ?? []).map((g) => g.family))], [t.data]);

  return (
    <Page
      wide
      title="ROS communication"
      sub={t.data ? `${t.data.groups?.length ?? 0} names · ${t.data.endpoints ?? 0} endpoints · ${t.data.launch_files ?? 0} launch files parsed · ${t.data.remapped ?? 0} remapped` : "Topics, services and actions across the fleet"}
      actions={
        <div className="flex rounded-lg bg-muted p-1">
          {(["list", "map"] as const).map((v) => (
            <button key={v} onClick={() => setView(v)} className={cn("flex items-center gap-1.5 rounded-md px-3 py-1 text-xs font-medium", view === v ? "bg-card shadow-sm" : "text-muted-foreground")}>
              {v === "list" ? <List className="size-3.5" /> : <Network className="size-3.5" />}
              {v === "list" ? "Browse" : "Map"}
            </button>
          ))}
        </div>
      }
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <div className="relative w-72">
          <Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-8 pl-8" placeholder="Name, repo or message type…" value={q} onChange={(e) => setQ(e.target.value)} />
        </div>
        <Seg value={fam} set={setFam} options={["all", ...fams]} />
        <Seg value={status} set={(v) => setStatus(v as typeof status)} options={["all", "linked", "cross", "open"]} labels={{ linked: "producer ↔ consumer", cross: "cross-repo", open: "one side only" }} />
        <span className="ml-auto text-xs text-muted-foreground">{groups.length} shown</span>
      </div>
      {t.isLoading ? (
        <Skeleton className="h-[70vh]" />
      ) : t.error ? (
        <Empty error title="Topics failed">{String(t.error)}</Empty>
      ) : view === "map" ? (
        <TopicMap groups={groups} onPick={(k) => { setSel(k); setView("list"); }} />
      ) : (
        <div className="grid gap-4 lg:grid-cols-[380px_1fr]">
          <TopicList groups={groups} sel={selected ? key(selected) : null} setSel={setSel} />
          {selected ? <TopicDetail g={selected} /> : <Empty title="Pick a name on the left">Its producers and consumers, and where each one is in the code.</Empty>}
        </div>
      )}
    </Page>
  );
}

function Seg({ value, set, options, labels = {} }: { value: string; set: (v: string) => void; options: string[]; labels?: Record<string, string> }) {
  return (
    <div className="flex rounded-md bg-muted p-0.5">
      {options.map((o) => (
        <button key={o} onClick={() => set(o)} className={cn("rounded px-2.5 py-1 text-xs font-medium", value === o ? "bg-card shadow-sm" : "text-muted-foreground hover:text-foreground")}>
          {labels[o] ?? o}
        </button>
      ))}
    </div>
  );
}

function TopicList({ groups, sel, setSel }: { groups: TopicGroup[]; sel: string | null; setSel: (k: string) => void }) {
  const parent = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({ count: groups.length, getScrollElement: () => parent.current, estimateSize: () => 54, overscan: 10 });
  return (
    <Card className="overflow-hidden">
      <div ref={parent} className="h-[calc(100vh-250px)] overflow-y-auto">
        <div style={{ height: v.getTotalSize(), position: "relative" }}>
          {v.getVirtualItems().map((it) => {
            const g = groups[it.index];
            const k = key(g);
            const np = g.producers?.length ?? 0, nc = g.consumers?.length ?? 0;
            return (
              <button key={k} onClick={() => setSel(k)} className={cn("absolute inset-x-0 flex flex-col justify-center border-b px-3 text-left hover:bg-muted/50", sel === k && "bg-accent/60")} style={{ top: it.start, height: it.size }}>
                <div className="flex items-center gap-2">
                  <Radio className={cn("size-3.5 shrink-0", g.connected ? "text-ok" : "text-muted-foreground")} />
                  <span className="truncate font-mono text-[13px] font-medium">{g.name}</span>
                  <span className="ml-auto shrink-0 text-[10px] uppercase text-muted-foreground">{g.family}</span>
                </div>
                <div className="flex gap-3 whitespace-nowrap pl-5 text-[11px] text-muted-foreground">
                  <span className={cn(!np && "text-warn")}>{np} out</span>
                  <span className={cn(!nc && "text-warn")}>{nc} in</span>
                  <span className="truncate">{[...new Set([...repos(g.producers), ...repos(g.consumers)])].join(", ")}</span>
                </div>
              </button>
            );
          })}
        </div>
      </div>
    </Card>
  );
}

function epId(e: Endpoint) {
  return `${e.repo}\u0000${e.path}\u0000${e.in ?? ""}`;
}

function TopicDetail({ g }: { g: TopicGroup }) {
  const { nodes, edges } = useMemo(() => {
    const nodes: FNode[] = [{ id: "topic", label: g.name, sub: g.family, tone: "focus" }];
    const edges: FEdge[] = [];
    const seen = new Set<string>();
    const add = (e: Endpoint, side: "p" | "c" | "d") => {
      const id = side + epId(e);
      if (!seen.has(id)) {
        seen.add(id);
        nodes.push({ id, label: e.in || e.path.split("/").pop()!, sub: `${e.repo} · ${e.role}`, color: tint(e.repo), tone: side === "d" ? "muted" : undefined, title: `${e.path}:${e.line}\n${e.text ?? ""}` });
        if (side === "c") edges.push({ from: "topic", to: id, label: e.msg_type?.split("::").pop() });
        else edges.push({ from: id, to: "topic", dashed: side === "d" });
      }
    };
    g.producers?.forEach((e) => add(e, "p"));
    g.consumers?.forEach((e) => add(e, "c"));
    g.declared?.forEach((e) => add(e, "d"));
    return { nodes, edges };
  }, [g]);
  return (
    <div className="min-w-0 space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <h2 className="font-mono text-lg font-semibold">{g.name}</h2>
        <Badge variant="outline">{g.family}</Badge>
        {g.connected ? <Badge variant="ok">producer ↔ consumer</Badge> : <Badge variant="warn">{g.producers?.length ? "no consumer found" : "no producer found"}</Badge>}
        <Confidence value={g.confidence} />
      </div>
      <Suspense fallback={<Skeleton className="h-[360px]" />}>
        <FlowGraph nodes={nodes} edges={edges} height="360px" />
      </Suspense>
      <div className="grid gap-4 xl:grid-cols-2">
        <EndpointList title="Producers" items={g.producers} />
        <EndpointList title="Consumers" items={g.consumers} />
      </div>
      {!!g.declared?.length && <EndpointList title="Declared (launch / config)" items={g.declared} />}
    </div>
  );
}

function EndpointList({ title, items }: { title: string; items?: Endpoint[] }) {
  return (
    <Card>
      <CardHeader title={title} right={<Badge variant="outline">{items?.length ?? 0}</Badge>} />
      <div className="divide-y">
        {!items?.length && <div className="px-4 py-5 text-center text-sm text-muted-foreground">none found</div>}
        {items?.map((e, i) => (
          <div key={i} className="px-4 py-2.5 text-xs">
            <div className="flex flex-wrap items-center gap-2">
              <span className="rounded px-1.5 py-0.5 text-[11px] font-medium" style={{ color: tint(e.repo, 42), background: tint(e.repo, 55).replace(")", " / .12)") }}>{e.repo}</span>
              {e.in ? <Link to={symbolHref(e.repo, FLEET_REF, e.in)} className="font-medium hover:text-primary hover:underline">{e.in}</Link> : null}
              <span className="text-muted-foreground">{e.role.replace("_", " ")}</span>
              {e.msg_type && <code className="ml-auto font-mono text-[11px] text-shape">{e.msg_type}</code>}
            </div>
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{e.path}:{e.line}{e.name_resolution && e.name_resolution !== "as-written" ? ` · ${e.name_resolution}` : ""}</div>
            {e.text && <pre className="mt-1 overflow-x-auto rounded-md bg-muted/60 px-2 py-1 font-mono text-[11px]">{e.text}</pre>}
          </div>
        ))}
      </div>
    </Card>
  );
}

function TopicMap({ groups, onPick }: { groups: TopicGroup[]; onPick: (k: string) => void }) {
  const { nodes, edges } = useMemo(() => {
    const nodes = new Map<string, SNode>();
    const edges: SEdge[] = [];
    const deg = new Map<string, number>();
    for (const g of groups) {
      const tid = "t:" + key(g);
      nodes.set(tid, { id: tid, label: g.name, size: 2.5, color: g.connected ? hslRgba(152, 55, 45) : hslRgba(220, 10, 60) });
      for (const e of g.producers ?? []) {
        edges.push({ from: "r:" + e.repo, to: tid, size: 0.7, color: hslRgba(248, 90, 64, 0.35) });
        deg.set(e.repo, (deg.get(e.repo) ?? 0) + 1);
      }
      for (const e of g.consumers ?? []) {
        edges.push({ from: tid, to: "r:" + e.repo, size: 0.7, color: hslRgba(268, 70, 58, 0.35) });
        deg.set(e.repo, (deg.get(e.repo) ?? 0) + 1);
      }
    }
    for (const [r, d] of deg) nodes.set("r:" + r, { id: "r:" + r, label: r, size: 5 + Math.sqrt(d) * 1.6, color: tintRgba(r) });
    return { nodes: [...nodes.values()], edges };
  }, [groups]);
  if (!groups.length) return <Empty title="No names match the filter" />;
  return (
    <div className="relative overflow-hidden rounded-xl border bg-card dotgrid">
      {groups.length > 700 && <div className="absolute left-3 top-3 z-10 rounded-md border bg-card/90 px-2.5 py-1 text-[11px] text-muted-foreground">Tip: filter to “cross-repo” for a readable map</div>}
      <Suspense fallback={<Rows />}>
        <SigmaGraph nodes={nodes} edges={edges} height="calc(100vh - 250px)" onSelect={(id) => id?.startsWith("t:") && onPick(id.slice(2))} />
      </Suspense>
    </div>
  );
}
