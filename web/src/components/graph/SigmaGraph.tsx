// A WebGL force-directed graph (Sigma + ForceAtlas2) for maps too large to
// draw as boxes: the fleet's repos, every topic. Hover lights a node's
// neighborhood; click selects.
import { useEffect, useRef } from "react";
import Graph from "graphology";
import forceAtlas2 from "graphology-layout-forceatlas2";
import Sigma from "sigma";
import { EdgeArrowProgram, EdgeRectangleProgram } from "sigma/rendering";
import { useScope } from "@/lib/scope";

export interface SNode { id: string; label: string; size: number; color: string; kind?: string }
export interface SEdge { from: string; to: string; size: number; color: string; label?: string }

const cssColor = (v: string) => `hsl(${getComputedStyle(document.documentElement).getPropertyValue(v).trim()})`;

export default function SigmaGraph({ nodes, edges, selected, onSelect, height, arrows = true }: { nodes: SNode[]; edges: SEdge[]; selected?: string | null; onSelect?: (id: string | null) => void; height: string; arrows?: boolean }) {
  const box = useRef<HTMLDivElement>(null);
  const sig = useRef<Sigma | null>(null);
  const state = useRef<{ hover: string | null; sel: string | null }>({ hover: null, sel: null });
  const { dark } = useScope();
  const onSel = useRef(onSelect);
  onSel.current = onSelect;

  useEffect(() => {
    if (!box.current) return;
    const g = new Graph({ multi: false, type: "directed" });
    const n = nodes.length || 1;
    nodes.forEach((v, i) => {
      const a = (2 * Math.PI * i) / n;
      g.addNode(v.id, { label: v.label, size: v.size, color: v.color, x: Math.cos(a) * 100 + Math.random(), y: Math.sin(a) * 100 + Math.random() });
    });
    for (const e of edges) if (g.hasNode(e.from) && g.hasNode(e.to) && e.from !== e.to && !g.hasEdge(e.from, e.to)) g.addEdge(e.from, e.to, { size: e.size, color: e.color, label: e.label });
    forceAtlas2.assign(g, { iterations: Math.min(600, 150 + 4000 / Math.sqrt(n)), settings: { ...forceAtlas2.inferSettings(g), barnesHutOptimize: n > 400, gravity: 1, scalingRatio: 8, slowDown: 2 } });

    const fg = cssColor("--foreground");
    const s = new Sigma(g, box.current, {
      defaultEdgeType: arrows ? "arrow" : "line",
      edgeProgramClasses: { arrow: EdgeArrowProgram, line: EdgeRectangleProgram },
      labelColor: { color: fg },
      labelFont: "Inter Variable, Inter, sans-serif",
      labelSize: 12,
      labelWeight: "500",
      labelRenderedSizeThreshold: 7,
      labelDensity: 0.6,
      zIndex: true,
      minCameraRatio: 0.05,
      maxCameraRatio: 4,
      defaultDrawNodeHover: (ctx, data, settings) => {
        const size = settings.labelSize;
        ctx.font = `600 ${size}px ${settings.labelFont}`;
        const w = ctx.measureText(data.label ?? "").width + 12;
        ctx.fillStyle = dark ? "#1b1e2a" : "#ffffff";
        ctx.shadowBlur = 8;
        ctx.shadowColor = "rgba(0,0,0,.25)";
        ctx.beginPath();
        ctx.roundRect(data.x + data.size + 4, data.y - size / 2 - 5, w, size + 10, 6);
        ctx.fill();
        ctx.shadowBlur = 0;
        ctx.fillStyle = fg;
        ctx.fillText(data.label ?? "", data.x + data.size + 10, data.y + size / 3);
      },
    });
    const focusOf = () => state.current.hover ?? state.current.sel;
    s.setSetting("nodeReducer", (node, d) => {
      const f = focusOf();
      if (!f) return d;
      if (node === f) return { ...d, highlighted: true, zIndex: 2 };
      if (g.areNeighbors(node, f)) return { ...d, zIndex: 1, forceLabel: true };
      return { ...d, color: dark ? "#2a2e3b" : "#e3e6ec", label: "", zIndex: 0 };
    });
    s.setSetting("edgeReducer", (edge, d) => {
      const f = focusOf();
      if (!f) return d;
      return g.hasExtremity(edge, f) ? { ...d, size: Math.max(1.5, d.size * 1.4), zIndex: 1 } : { ...d, hidden: true };
    });
    s.on("enterNode", ({ node }) => {
      state.current.hover = node;
      box.current!.style.cursor = "pointer";
      s.refresh({ skipIndexation: true });
    });
    s.on("leaveNode", () => {
      state.current.hover = null;
      box.current!.style.cursor = "";
      s.refresh({ skipIndexation: true });
    });
    s.on("clickNode", ({ node }) => onSel.current?.(node));
    s.on("clickStage", () => onSel.current?.(null));
    sig.current = s;
    return () => {
      s.kill();
      sig.current = null;
    };
  }, [nodes, edges, dark, arrows]);

  // selection from outside (side panel, search) moves the camera
  useEffect(() => {
    state.current.sel = selected ?? null;
    const s = sig.current;
    if (!s) return;
    s.refresh({ skipIndexation: true });
    if (selected && s.getGraph().hasNode(selected)) {
      const pos = s.getNodeDisplayData(selected);
      if (pos) s.getCamera().animate({ x: pos.x, y: pos.y, ratio: Math.min(s.getCamera().ratio, 0.6) }, { duration: 400 });
    }
  }, [selected]);

  return <div ref={box} className="w-full" style={{ height }} />;
}
