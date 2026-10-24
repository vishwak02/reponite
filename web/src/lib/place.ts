// Incremental placement for a laid-out graph: when a node is expanded, its
// new neighbours are placed beside it — callers in a column to its left,
// callees to its right — and every node already on screen keeps its
// position. Pure, so it runs on the main thread in well under a frame.

export interface Rect { x: number; y: number; width: number; height: number }
export interface NewBox { id: string; width: number; height: number; side: -1 | 1 }

const overlaps = (a: Rect, b: Rect, gap: number) =>
  a.x < b.x + b.width + gap && b.x < a.x + a.width + gap && a.y < b.y + b.height + gap && b.y < a.y + a.height + gap;

export function placeAround(anchor: Rect, placed: Rect[], add: NewBox[], gapX = 90, gapY = 22): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>();
  const taken = [...placed];
  for (const side of [-1, 1] as const) {
    const col = add.filter((b) => b.side === side);
    if (!col.length) continue;
    const total = col.reduce((s, b) => s + b.height, 0) + gapY * (col.length - 1);
    let y = anchor.y + anchor.height / 2 - total / 2;
    for (const b of col) {
      // right edges align left of the anchor; left edges align right of it
      const x = side < 0 ? anchor.x - gapX - b.width : anchor.x + anchor.width + gapX;
      const r: Rect = { x, y, width: b.width, height: b.height };
      // slide down past whatever already occupies the spot
      for (let hit = taken.find((t) => overlaps(r, t, gapY / 2)); hit; hit = taken.find((t) => overlaps(r, t, gapY / 2))) r.y = hit.y + hit.height + gapY;
      taken.push(r);
      out.set(b.id, { x: r.x, y: r.y });
      y = r.y + b.height + gapY;
    }
  }
  return out;
}
