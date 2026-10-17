// File references written as data — a launch file naming a config, a Helm
// template including a define, a YAML value naming a class — drawn per file.
import { lazy, Suspense, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { FileText, Search } from "lucide-react";
import { api, type Link as L, type LinksResult } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { cn } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows, Skeleton } from "@/components/ui/skeleton";
import type { FEdge, FNode } from "@/components/graph/FlowGraph";

const FlowGraph = lazy(() => import("@/components/graph/FlowGraph"));
const base = (p: string) => p.split("/").pop() || p;
const dir = (p: string) => p.split("/").slice(0, -1).join("/");

export default function Links() {
  const { repo, ref } = useScope();
  const all = useQuery({ queryKey: ["links", repo, ref], queryFn: () => api<LinksResult>("links", { repo, ref }), enabled: !!repo });
  const [q, setQ] = useState("");
  const [kind, setKind] = useState("all");
  const [file, setFile] = useState<string | null>(null);
  const links = useMemo(() => (all.data?.refers_to ?? []).filter((l) => kind === "all" || l.kind === kind), [all.data, kind]);
  const kinds = useMemo(() => {
    const m = new Map<string, number>();
    for (const l of all.data?.refers_to ?? []) m.set(l.kind, (m.get(l.kind) ?? 0) + 1);
    return [...m.entries()].sort((a, b) => b[1] - a[1]);
  }, [all.data]);
  const files = useMemo(() => {
    const m = new Map<string, { out: number; in: number }>();
    const bump = (p: string, k: "out" | "in") => m.set(p, { ...(m.get(p) ?? { out: 0, in: 0 }), [k]: (m.get(p)?.[k] ?? 0) + 1 });
    for (const l of links) {
      if (l.path === l.to) continue; // a file's references to itself say nothing about structure
      bump(l.path, "out");
      bump(l.to, "in");
    }
    return [...m.entries()].filter(([p]) => !q || p.toLowerCase().includes(q.toLowerCase())).sort((a, b) => b[1].in + b[1].out - (a[1].in + a[1].out));
  }, [links, q]);

  return (
    <Page wide title="Links" sub={<>References written as data in <b>{repo}</b>: paths, ids, template includes, config values naming code.</>}>
      {all.isLoading ? <Rows n={10} /> : all.error ? <Empty error title="Links failed">{String(all.error)}</Empty> : !all.data?.refers_to?.length ? (
        <Empty title={`No data references found in ${repo}`}>{all.data?.note}</Empty>
      ) : (
        <div className="grid gap-4 lg:grid-cols-[360px_1fr]">
          <Card className="flex flex-col overflow-hidden">
            <div className="space-y-2 border-b p-3">
              <div className="relative"><Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input className="h-8 pl-8" placeholder="Filter files…" value={q} onChange={(e) => setQ(e.target.value)} /></div>
              <div className="flex flex-wrap gap-1">
                {[["all", all.data.refers_to.length] as const, ...kinds].map(([k, n]) => (
                  <button key={k} onClick={() => setKind(k)} className={cn("rounded-md border px-2 py-0.5 text-[11px] font-medium", kind === k ? "border-primary/40 bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted")}>
                    {k} <span className="tabular-nums opacity-70">{n}</span>
                  </button>
                ))}
              </div>
            </div>
            <div className="h-[calc(100vh-300px)] overflow-y-auto">
              {files.slice(0, 600).map(([p, c]) => (
                <button key={p} onClick={() => setFile(p)} className={cn("flex w-full items-center gap-2 border-b px-3 py-1.5 text-left hover:bg-muted/50", (file ?? files[0]?.[0]) === p && "bg-accent/60")}>
                  <FileText className="size-3.5 shrink-0 text-muted-foreground" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-[13px] font-medium">{base(p)}</div>
                    <div className="truncate font-mono text-[10px] text-muted-foreground">{dir(p)}</div>
                  </div>
                  <span className="shrink-0 text-[10px] tabular-nums text-muted-foreground">{c.in}↓ {c.out}↑</span>
                </button>
              ))}
            </div>
          </Card>
          {file ?? files[0]?.[0] ? <FileLinks file={(file ?? files[0][0])!} links={links} pick={setFile} /> : <Empty title="No file matches">What it refers to, and what refers to it.</Empty>}
        </div>
      )}
    </Page>
  );
}

function FileLinks({ file, links, pick }: { file: string; links: L[]; pick: (f: string) => void }) {
  const out = links.filter((l) => l.path === file && l.to !== file);
  const inc = links.filter((l) => l.to === file && l.path !== file);
  const self = links.filter((l) => l.path === file && l.to === file).length;
  const { nodes, edges } = useMemo(() => {
    const nodes = new Map<string, FNode>([[file, { id: file, label: base(file), sub: dir(file), tone: "focus" }]]);
    const edges: FEdge[] = [];
    const seen = new Set<string>();
    for (const l of inc) {
      if (!nodes.has(l.path)) nodes.set(l.path, { id: l.path, label: base(l.path), sub: dir(l.path), tone: "shape" });
      const k = l.path + ">" + file;
      if (!seen.has(k)) { seen.add(k); edges.push({ from: l.path, to: file, label: l.kind }); }
    }
    for (const l of out) {
      if (!nodes.has(l.to)) nodes.set(l.to, { id: l.to, label: base(l.to), sub: dir(l.to), tone: "primary" });
      const k = file + ">" + l.to;
      if (!seen.has(k)) { seen.add(k); edges.push({ from: file, to: l.to, label: l.kind }); }
    }
    return { nodes: [...nodes.values()], edges };
  }, [file, out, inc]);
  return (
    <div className="min-w-0 space-y-4">
      <div>
        <h2 className="text-lg font-semibold">{base(file)}</h2>
        <div className="flex items-center gap-2 font-mono text-xs text-muted-foreground">
          {file}
          {self > 0 && <Badge variant="outline" className="font-sans">{self} within the file</Badge>}
        </div>
      </div>
      <Suspense fallback={<Skeleton className="h-[380px]" />}>
        <FlowGraph nodes={nodes} edges={edges} height="380px" onSelect={(id) => id && id !== file && pick(id)} />
      </Suspense>
      <div className="grid gap-4 xl:grid-cols-2">
        <LinkTable title="Referred to by" items={inc} side="from" />
        <LinkTable title="Refers to" items={out} side="to" />
      </div>
    </div>
  );
}

function LinkTable({ title, items, side }: { title: string; items: L[]; side: "from" | "to" }) {
  return (
    <Card>
      <CardHeader title={title} right={<Badge variant="outline">{items.length}</Badge>} />
      <div className="max-h-[40vh] divide-y overflow-y-auto">
        {!items.length && <div className="px-4 py-5 text-center text-sm text-muted-foreground">none</div>}
        {items.map((l, i) => (
          <div key={i} className="px-4 py-2 text-xs">
            <div className="flex items-center gap-2">
              <span className="truncate font-mono">{side === "from" ? `${l.path}:${l.line}` : `${l.to}${l.to_line ? ":" + l.to_line : ""}`}</span>
              <Badge variant="outline" className="ml-auto shrink-0">{l.kind}</Badge>
            </div>
            <div className="mt-0.5 truncate font-mono text-[11px] text-muted-foreground">{l.text}</div>
          </div>
        ))}
      </div>
    </Card>
  );
}
