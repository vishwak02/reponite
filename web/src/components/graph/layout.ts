// Layered (Sugiyama) layout via ELK for directed graphs: calls, links,
// topic flows. Node boxes are sized from their measured labels.
// ELK is imported here, so it ships in the lazily loaded graph chunks only.
import ELK, { type ElkNode } from "elkjs/lib/elk.bundled.js";

const elk = new ELK();
let ctx: CanvasRenderingContext2D | null = null;

export function textWidth(s: string, font = "600 13px 'Inter Variable', Inter, sans-serif"): number {
  ctx ??= document.createElement("canvas").getContext("2d");
  if (!ctx) return s.length * 7.5;
  ctx.font = font;
  return ctx.measureText(s).width;
}

export interface Box { id: string; width: number; height: number }
export interface Placed { x: number; y: number }

export async function layered(nodes: Box[], edges: { from: string; to: string }[], direction: "RIGHT" | "DOWN" = "RIGHT"): Promise<Map<string, Placed>> {
  const ids = new Set(nodes.map((n) => n.id));
  const graph: ElkNode = {
    id: "root",
    layoutOptions: {
      "elk.algorithm": "layered",
      "elk.direction": direction,
      "elk.layered.spacing.nodeNodeBetweenLayers": "90",
      "elk.spacing.nodeNode": "22",
      "elk.layered.nodePlacement.strategy": "NETWORK_SIMPLEX",
      "elk.layered.crossingMinimization.strategy": "LAYER_SWEEP",
      "elk.layered.considerModelOrder.strategy": "NODES_AND_EDGES",
      "elk.layered.cycleBreaking.strategy": "DEPTH_FIRST",
      "elk.separateConnectedComponents": "true",
      "elk.spacing.componentComponent": "60",
    },
    children: nodes.map((n) => ({ id: n.id, width: n.width, height: n.height })),
    edges: edges.filter((e) => ids.has(e.from) && ids.has(e.to) && e.from !== e.to).map((e, i) => ({ id: "e" + i, sources: [e.from], targets: [e.to] })),
  };
  const out = await elk.layout(graph);
  return new Map((out.children ?? []).map((c) => [c.id, { x: c.x ?? 0, y: c.y ?? 0 }]));
}

// Labels are measured in the web font, so wait for it, but never for long:
// a font that fails to load must not hold the diagram back.
export const fontsReady = () => Promise.race([document.fonts?.ready, new Promise((r) => setTimeout(r, 400))]);
