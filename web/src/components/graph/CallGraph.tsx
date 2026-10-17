// The call-graph workspace: a symbol's callers (left) and callees (right),
// laid out by ELK and drawn with React Flow. Double-click a node to expand its
// own neighborhood in place; the side panel opens or refocuses it.
import { memo, useCallback, useEffect, useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
  Background,
  BackgroundVariant,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
  Panel,
  Position,
  ReactFlow,
  ReactFlowProvider,
  useNodesInitialized,
  useReactFlow,
  type Edge,
  type Node,
  type NodeProps,
} from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { Crosshair, ExternalLink, FlaskConical, Loader2, Plus } from "lucide-react";
import { api, type GraphEdge, type GraphNode, type GraphResult } from "@/lib/api";
import { cn, shortName } from "@/lib/utils";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Badge, Confidence } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Empty } from "@/components/ui/skeleton";
import { fontsReady, layered, textWidth } from "./layout";

type CallData = GraphNode & { focus: boolean; expanded: boolean; dim: boolean; hl: boolean } & Record<string, unknown>;

const CallNode = memo(({ data, selected }: NodeProps<Node<CallData>>) => {
  const side = data.focus ? "focus" : data.hop < 0 ? "caller" : "callee";
  return (
    <div
      className={cn(
        "group relative rounded-lg border bg-card px-3 py-1.5 shadow-sm transition-[opacity,box-shadow,border-color]",
        data.focus && "border-transparent bg-gradient-to-br from-primary to-shape text-primary-foreground shadow-lg shadow-primary/25",
        side === "caller" && "border-l-[3px] border-l-shape",
        side === "callee" && "border-l-[3px] border-l-primary",
        data.pkg === "(external)" && "border-dashed",
        selected && !data.focus && "ring-2 ring-ring",
        data.hl && !data.focus && "border-primary/60 shadow-md",
        data.dim && "opacity-30",
      )}
      style={{ width: "100%", height: "100%" }}
    >
      <Handle type="target" position={Position.Left} className="!size-1.5 !border-0 !bg-muted-foreground/50" />
      <Handle type="source" position={Position.Right} className="!size-1.5 !border-0 !bg-muted-foreground/50" />
      <div className={cn("truncate text-[10px] leading-tight", data.focus ? "text-primary-foreground/75" : "text-muted-foreground")}>{data.pkg}</div>
      <div className="flex items-center gap-1 truncate text-[13px] font-semibold leading-snug">
        {data.test && <FlaskConical className="size-3 shrink-0 text-warn" />}
        {data.label}
      </div>
      {(data.more ?? 0) + (data.external ?? 0) > 0 && (
        <div className="absolute -right-2 -top-2 flex gap-0.5">
          {!!data.more && <span className="rounded-full bg-foreground px-1.5 text-[10px] font-semibold text-background" title={`${data.more} more neighbors not drawn — double-click to expand`}>+{data.more}</span>}
          {!!data.external && <span className="rounded-full border bg-card px-1.5 text-[10px] text-muted-foreground" title={`${data.external} library calls not drawn`}>{data.external} ext</span>}
        </div>
      )}
    </div>
  );
});
CallNode.displayName = "CallNode";
const nodeTypes = { call: CallNode };

const H = 46;
const width = (n: GraphNode) => Math.min(440, Math.max(120, Math.max(textWidth(n.label), textWidth(n.pkg, "10px Inter")) + 40));

interface Opts { depth: number; tests: boolean; external: boolean; limit: number }

function Inner({ repo, refName, symbol, height }: { repo: string; refName: string; symbol: string; height: string }) {
  const nav = useNavigate();
  const qc = useQueryClient();
  const rf = useReactFlow();
  const [opts, setOpts] = useState<Opts>({ depth: 2, tests: false, external: false, limit: 120 });
  const g = useQuery({
    queryKey: ["graph", repo, refName, symbol, opts],
    queryFn: () => api<GraphResult>("graph", { repo, ref: refName, symbol, ...opts }),
  });
  // nodes/edges added by expanding, on top of the fetched neighborhood
  const [extra, setExtra] = useState<{ nodes: GraphNode[]; edges: GraphEdge[]; expanded: Set<string> }>({ nodes: [], edges: [], expanded: new Set() });
  useEffect(() => setExtra({ nodes: [], edges: [], expanded: new Set() }), [repo, refName, symbol, opts]);
  const [expanding, setExpanding] = useState<string | null>(null);
  const [sel, setSel] = useState<string | null>(null);
  const [hover, setHover] = useState<string | null>(null);
  const [nodes, setNodes] = useState<Node<CallData>[]>([]);

  const merged = useMemo(() => {
    if (!g.data) return null;
    const byId = new Map<string, GraphNode>();
    for (const n of [...g.data.nodes, ...extra.nodes]) if (!byId.has(n.id)) byId.set(n.id, n);
    const ek = (e: GraphEdge) => e.from + "\u0000" + e.to;
    const edges = new Map<string, GraphEdge>();
    for (const e of [...g.data.edges, ...extra.edges]) if (byId.has(e.from) && byId.has(e.to)) edges.set(ek(e), e);
    return { nodes: [...byId.values()], edges: [...edges.values()] };
  }, [g.data, extra]);

  // layout whenever the node set changes
  useEffect(() => {
    if (!merged || !g.data) return;
    let live = true;
    let boxes: { id: string; width: number; height: number }[] = [];
    fontsReady()
      .then(() => {
        boxes = merged.nodes.map((n) => ({ id: n.id, width: width(n), height: H }));
        return layered(boxes, merged.edges);
      })
      .then((pos) => {
      if (!live) return;
      const w = new Map(boxes.map((b) => [b.id, b.width]));
      setNodes(
        merged.nodes.map((n) => ({
          id: n.id,
          type: "call",
          position: pos.get(n.id) ?? { x: 0, y: 0 },
          width: w.get(n.id),
          height: H,
          data: { ...n, focus: n.id === g.data!.focus, expanded: extra.expanded.has(n.id), dim: false, hl: false },
        })),
      );
      setFit((f) => f + 1);
    });
    return () => {
      live = false;
    };
  }, [merged]); // eslint-disable-line react-hooks/exhaustive-deps

  // fit once React Flow has measured the freshly laid-out nodes
  const [fit, setFit] = useState(0);
  const measured = useNodesInitialized();
  useEffect(() => {
    if (fit && measured) rf.fitView({ padding: 0.12, maxZoom: 1.1, minZoom: 0.35 });
  }, [fit, measured, rf]);

  const near = useMemo(() => {
    const f = hover ?? sel;
    if (!f || !merged) return null;
    const s = new Set([f]);
    for (const e of merged.edges) if (e.from === f || e.to === f) s.add(e.from).add(e.to);
    return s;
  }, [hover, sel, merged]);

  const viewNodes = useMemo(() => nodes.map((n) => ({ ...n, selected: n.id === sel, data: { ...n.data, dim: !!near && !near.has(n.id), hl: !!near && near.has(n.id) } })), [nodes, near, sel]);
  const viewEdges: Edge[] = useMemo(
    () =>
      (merged?.edges ?? []).map((e) => {
        const on = near && (e.from === (hover ?? sel) || e.to === (hover ?? sel));
        return {
          id: e.from + ">" + e.to,
          source: e.from,
          target: e.to,
          type: "default",
          animated: !!on,
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: on ? "hsl(var(--primary))" : "hsl(var(--muted-foreground) / .5)" },
          style: {
            strokeWidth: 0.8 + 1.6 * e.confidence,
            strokeDasharray: e.confidence < 0.6 ? "5 4" : undefined,
            stroke: on ? "hsl(var(--primary))" : undefined,
            opacity: near && !on ? 0.15 : 1,
          },

        };
      }),
    [merged, near, hover, sel],
  );

  const expand = useCallback(
    async (id: string) => {
      if (extra.expanded.has(id) || id === g.data?.focus) return;
      setExpanding(id);
      try {
        const r = await qc.fetchQuery({
          queryKey: ["graph", repo, refName, id, { ...opts, depth: 1, limit: 40 }],
          queryFn: () => api<GraphResult>("graph", { repo, ref: refName, symbol: id, ...opts, depth: 1, limit: 40 }),
        });
        setExtra((x) => ({ nodes: [...x.nodes, ...r.nodes], edges: [...x.edges, ...r.edges], expanded: new Set(x.expanded).add(id) }));
      } finally {
        setExpanding(null);
      }
    },
    [extra.expanded, g.data?.focus, opts, qc, repo, refName],
  );

  const selNode = merged?.nodes.find((n) => n.id === sel);
  const selEdges = merged?.edges.filter((e) => e.from === sel || e.to === sel) ?? [];

  if (g.error) return <Empty error title="Graph failed">{String(g.error)}</Empty>;
  if (g.data && !g.data.focus) return <Empty title="Nothing to draw">{g.data.note}</Empty>;

  return (
    <div className="relative overflow-hidden rounded-xl border bg-card dotgrid" style={{ height }}>
      <ReactFlow
        nodes={viewNodes}
        edges={viewEdges}
        nodeTypes={nodeTypes}
        onNodeClick={(_, n) => setSel(n.id)}
        onNodeDoubleClick={(_, n) => expand(n.id)}
        onNodeMouseEnter={(_, n) => setHover(n.id)}
        onNodeMouseLeave={() => setHover(null)}
        onPaneClick={() => setSel(null)}
        nodesConnectable={false}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="hsl(var(--grid))" />
        <Controls showInteractive={false} position="bottom-left" />
        <MiniMap pannable zoomable className="!rounded-lg !border !bg-card" nodeColor={(n) => ((n.data as unknown as CallData).focus ? "hsl(var(--primary))" : (n.data as unknown as CallData).hop < 0 ? "hsl(var(--shape) / .6)" : "hsl(var(--primary) / .45)")} maskColor="hsl(var(--background) / .6)" />
        <Panel position="top-left" className="!m-3">
          <div className="flex flex-wrap items-center gap-2 rounded-lg border bg-card/90 p-1.5 shadow-sm backdrop-blur">
            <div className="flex rounded-md bg-muted p-0.5">
              {[1, 2, 3].map((d) => (
                <button key={d} onClick={() => setOpts({ ...opts, depth: d })} className={cn("rounded px-2 py-0.5 text-xs font-medium", opts.depth === d ? "bg-card shadow-sm" : "text-muted-foreground")}>
                  {d} hop{d > 1 ? "s" : ""}
                </button>
              ))}
            </div>
            <Toggle on={opts.tests} set={(v) => setOpts({ ...opts, tests: v })}>tests</Toggle>
            <Toggle on={opts.external} set={(v) => setOpts({ ...opts, external: v })}>libraries</Toggle>
            <select value={opts.limit} onChange={(e) => setOpts({ ...opts, limit: +e.target.value })} className="h-6 rounded border bg-card px-1 text-xs">
              {[60, 120, 250, 500].map((l) => <option key={l} value={l}>≤ {l} nodes</option>)}
            </select>
            {(g.isFetching || expanding) && <Loader2 className="size-4 animate-spin text-muted-foreground" />}
          </div>
        </Panel>
        <Panel position="bottom-center" className="!mb-3 flex items-center gap-3 rounded-lg border bg-card/90 px-3 py-1.5 text-[11px] text-muted-foreground shadow-sm backdrop-blur">
          <span className="flex items-center gap-1"><span className="h-3 w-1 rounded bg-shape" />callers</span>
          <span className="flex items-center gap-1"><span className="h-3 w-1 rounded bg-primary" />callees</span>
          <span className="flex items-center gap-1"><span className="w-4 border-t border-dashed border-muted-foreground" />low confidence</span>
          <span>{merged?.nodes.length ?? 0} nodes · {merged?.edges.length ?? 0} calls{g.data?.truncated ? " · truncated" : ""}</span>
        </Panel>
        {g.data?.note && (
          <Panel position="top-center" className="!mt-16 max-w-xl rounded-md border border-warn/30 bg-warn/10 px-3 py-1 text-xs text-warn shadow-sm">{g.data.note}</Panel>
        )}
      </ReactFlow>
      {selNode && (
        <div className="absolute bottom-3 right-3 top-16 z-10 flex w-[320px] flex-col overflow-hidden rounded-xl border bg-card shadow-xl animate-fade-in">
          <div className="border-b p-3">
            <div className="text-[11px] text-muted-foreground">{selNode.pkg}</div>
            <div className="break-all font-semibold">{selNode.label}</div>
            <div className="mt-1 flex flex-wrap gap-1">
              <Badge variant={selNode.hop === 0 ? "default" : selNode.hop < 0 ? "shape" : "default"}>{selNode.hop === 0 ? "focus" : selNode.hop < 0 ? `caller · ${-selNode.hop} hop` : `callee · ${selNode.hop} hop`}</Badge>
              {selNode.test && <Badge variant="warn">test</Badge>}
              {selNode.pkg === "(external)" && <Badge variant="outline">outside the repo</Badge>}
            </div>
            <div className="mt-3 flex gap-1.5">
              <Button size="xs" onClick={() => nav(symbolHref(repo, refName, selNode.id))}><ExternalLink />Open</Button>
              <Button size="xs" variant="outline" onClick={() => nav(symbolHref(repo, refName, selNode.id, "graph"))}><Crosshair />Focus</Button>
              <Button size="xs" variant="outline" disabled={extra.expanded.has(selNode.id) || selNode.id === g.data?.focus} onClick={() => expand(selNode.id)}><Plus />Expand</Button>
            </div>
          </div>
          <div className="flex-1 overflow-y-auto p-2 text-xs">
            {selEdges.map((e) => {
              const other = e.from === sel ? e.to : e.from;
              return (
                <button key={e.from + e.to} onClick={() => { setSel(other); const n = rf.getNode(other); if (n) rf.setCenter(n.position.x + (n.width ?? 0) / 2, n.position.y + H / 2, { zoom: rf.getZoom(), duration: 300 }); }} className="flex w-full items-center gap-2 rounded-md px-2 py-1.5 text-left hover:bg-muted">
                  <span className={cn("w-10 shrink-0 text-[10px] font-medium uppercase", e.from === sel ? "text-primary" : "text-shape")}>{e.from === sel ? "calls" : "from"}</span>
                  <span className="truncate font-medium">{shortName(other)}</span>
                  <span className="ml-auto shrink-0"><Confidence value={e.confidence} /></span>
                </button>
              );
            })}
            <div className="break-all px-2 pt-3 font-mono text-[10px] text-muted-foreground">{selNode.id}</div>
          </div>
        </div>
      )}
      {!g.data && <div className="absolute inset-0 grid place-items-center"><Loader2 className="size-6 animate-spin text-muted-foreground" /></div>}
    </div>
  );
}

function Toggle({ on, set, children }: { on: boolean; set: (v: boolean) => void; children: React.ReactNode }) {
  return (
    <button onClick={() => set(!on)} className={cn("rounded-md border px-2 py-0.5 text-xs font-medium", on ? "border-primary/40 bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted")}>
      {children}
    </button>
  );
}

export default function CallGraph({ repo, refName, symbol, height = "72vh" }: { repo: string; refName: string; symbol: string; height?: string }) {
  return (
    <ReactFlowProvider>
      <Inner repo={repo} refName={refName} symbol={symbol} height={height} />
    </ReactFlowProvider>
  );
}

