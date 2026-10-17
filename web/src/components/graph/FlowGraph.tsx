// A small directed diagram (ELK + React Flow) for any node/edge list: a
// topic's producers and consumers, a file's references.
import { memo, useEffect, useMemo, useState } from "react";
import { Background, BackgroundVariant, Controls, Handle, MarkerType, Position, ReactFlow, ReactFlowProvider, useNodesInitialized, useReactFlow, type Edge, type Node, type NodeProps } from "@xyflow/react";
import "@xyflow/react/dist/style.css";
import { cn } from "@/lib/utils";
import { fontsReady, layered, textWidth } from "./layout";

export interface FNode { id: string; label: string; sub?: string; tone?: "primary" | "shape" | "ok" | "warn" | "muted" | "focus"; color?: string; title?: string }
export interface FEdge { from: string; to: string; label?: string; dashed?: boolean; width?: number }
type D = FNode & { dim: boolean } & Record<string, unknown>;

const tones: Record<string, string> = {
  primary: "border-l-[3px] border-l-primary",
  shape: "border-l-[3px] border-l-shape",
  ok: "border-l-[3px] border-l-ok",
  warn: "border-l-[3px] border-l-warn",
  muted: "border-dashed",
  focus: "border-transparent bg-gradient-to-br from-primary to-shape text-primary-foreground shadow-lg shadow-primary/25",
};

const FNodeView = memo(({ data, selected }: NodeProps<Node<D>>) => (
  <div title={data.title} className={cn("h-full w-full rounded-lg border bg-card px-3 py-1.5 shadow-sm transition-opacity", tones[data.tone ?? ""], selected && data.tone !== "focus" && "ring-2 ring-ring", data.dim && "opacity-30")} style={data.color ? { borderLeftColor: data.color, borderLeftWidth: 3 } : undefined}>
    <Handle type="target" position={Position.Left} className="!size-1.5 !border-0 !bg-muted-foreground/50" />
    <Handle type="source" position={Position.Right} className="!size-1.5 !border-0 !bg-muted-foreground/50" />
    {data.sub && <div className={cn("truncate text-[10px] leading-tight", data.tone === "focus" ? "text-primary-foreground/75" : "text-muted-foreground")}>{data.sub}</div>}
    <div className="truncate text-[13px] font-semibold leading-snug">{data.label}</div>
  </div>
));
FNodeView.displayName = "FNodeView";
const nodeTypes = { f: FNodeView };

function Inner({ nodes, edges, height, onSelect }: { nodes: FNode[]; edges: FEdge[]; height: string; onSelect?: (id: string | null) => void }) {
  const rf = useReactFlow();
  const [laid, setLaid] = useState<Node<D>[]>([]);
  const [hover, setHover] = useState<string | null>(null);
  useEffect(() => {
    let live = true;
    let boxes: { id: string; width: number; height: number }[] = [];
    fontsReady()
      .then(() => {
        boxes = nodes.map((n) => ({ id: n.id, width: Math.min(420, Math.max(110, Math.max(textWidth(n.label), textWidth(n.sub ?? "", "10px Inter")) + 36)), height: n.sub ? 46 : 34 }));
        return layered(boxes, edges);
      })
      .then((pos) => {
        if (!live) return;
        setLaid(nodes.map((n, i) => ({ id: n.id, type: "f", position: pos.get(n.id) ?? { x: 0, y: 0 }, width: boxes[i].width, height: boxes[i].height, data: { ...n, dim: false } })));
        setFit((f) => f + 1);
      });
    return () => {
      live = false;
    };
  }, [nodes, edges]); // eslint-disable-line react-hooks/exhaustive-deps
  const [fit, setFit] = useState(0);
  const measured = useNodesInitialized();
  useEffect(() => {
    if (fit && measured) rf.fitView({ padding: 0.15, maxZoom: 1.1, minZoom: 0.3 });
  }, [fit, measured, rf]);
  const near = useMemo(() => {
    if (!hover) return null;
    const s = new Set([hover]);
    for (const e of edges) if (e.from === hover || e.to === hover) s.add(e.from).add(e.to);
    return s;
  }, [hover, edges]);
  const vn = useMemo(() => laid.map((n) => ({ ...n, data: { ...n.data, dim: !!near && !near.has(n.id) } })), [laid, near]);
  const ve: Edge[] = useMemo(
    () =>
      edges.map((e, i) => {
        const on = !!hover && (e.from === hover || e.to === hover);
        return {
          id: "e" + i,
          source: e.from,
          target: e.to,
          label: e.label,
          labelStyle: { fontSize: 10, fill: "hsl(var(--muted-foreground))" },
          labelBgStyle: { fill: "hsl(var(--card))" },
          animated: on,
          markerEnd: { type: MarkerType.ArrowClosed, width: 14, height: 14, color: on ? "hsl(var(--primary))" : "hsl(var(--muted-foreground) / .5)" },
          style: { strokeWidth: e.width ?? 1.4, strokeDasharray: e.dashed ? "5 4" : undefined, stroke: on ? "hsl(var(--primary))" : undefined, opacity: near && !on ? 0.15 : 1 },
        };
      }),
    [edges, hover, near],
  );
  return (
    <div className="overflow-hidden rounded-xl border bg-card dotgrid" style={{ height }}>
      <ReactFlow
        nodes={vn}
        edges={ve}
        nodeTypes={nodeTypes}
        nodesConnectable={false}
        onNodeMouseEnter={(_, n) => setHover(n.id)}
        onNodeMouseLeave={() => setHover(null)}
        onNodeClick={(_, n) => onSelect?.(n.id)}
        onPaneClick={() => onSelect?.(null)}
        minZoom={0.1}
        proOptions={{ hideAttribution: true }}
      >
        <Background variant={BackgroundVariant.Dots} gap={20} size={1} color="hsl(var(--grid))" />
        <Controls showInteractive={false} position="bottom-left" />
      </ReactFlow>
    </div>
  );
}

export default function FlowGraph(p: { nodes: FNode[]; edges: FEdge[]; height?: string; onSelect?: (id: string | null) => void }) {
  return (
    <ReactFlowProvider>
      <Inner {...p} height={p.height ?? "420px"} />
    </ReactFlowProvider>
  );
}
