import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]) {
  return twMerge(clsx(inputs));
}

export const fmt = (n: number | undefined | null) => (n == null ? "–" : n.toLocaleString());

// A stable hue per string (repo, package), so a name keeps its color everywhere.
export function hue(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) >>> 0;
  return h % 360;
}
export const tint = (s: string, l = 55) => `hsl(${hue(s)} 62% ${l}%)`;

// The last segment of a qualified id, keeping a Class.method pair.
export function shortName(qid: string): string {
  const slash = qid.lastIndexOf("/");
  const tail = slash >= 0 ? qid.slice(slash + 1) : qid;
  const parts = tail.split(".");
  return parts.length > 2 ? parts.slice(-2).join(".") : parts.slice(-1)[0] || qid;
}

export function langOf(path: string): string {
  const ext = path.split(".").pop()?.toLowerCase() ?? "";
  return ext;
}

// WebGL renderers (Sigma) parse only hex/rgb colors: an HSL color as rgba().
export function hslRgba(h: number, s: number, l: number, a = 1): string {
  s /= 100;
  l /= 100;
  const k = (n: number) => (n + h / 30) % 12;
  const f = (n: number) => l - s * Math.min(l, 1 - l) * Math.max(-1, Math.min(k(n) - 3, 9 - k(n), 1));
  const c = (n: number) => Math.round(255 * f(n));
  return `rgba(${c(0)},${c(8)},${c(4)},${a})`;
}
export const tintRgba = (s: string, l = 55, a = 1) => hslRgba(hue(s), 62, l, a);
