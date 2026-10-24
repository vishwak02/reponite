import { afterEach, describe, expect, it, vi } from "vitest";
import { api, ApiError, searchSymbols } from "@/lib/api";
import { langFor } from "@/lib/lang";
import { placeAround, type Rect } from "@/lib/place";
import { symbolHref } from "@/lib/routes";
import { cn, fmt, hslRgba, hue, shortName, tint } from "@/lib/utils";

describe("utils", () => {
  it("shortName keeps the last segment and a Class.method pair", () => {
    expect(shortName("pkg/sub.Charge")).toBe("Charge");
    expect(shortName("pkg/sub.Order.Charge")).toBe("Order.Charge");
    expect(shortName("Charge")).toBe("Charge");
  });
  it("hue is stable and in range", () => {
    expect(hue("billing")).toBe(hue("billing"));
    expect(hue("billing")).toBeGreaterThanOrEqual(0);
    expect(hue("billing")).toBeLessThan(360);
    expect(tint("billing")).toMatch(/^hsl\(\d+ 62% 55%\)$/);
  });
  it("hslRgba converts primaries", () => {
    expect(hslRgba(0, 100, 50)).toBe("rgba(255,0,0,1)");
    expect(hslRgba(120, 100, 50, 0.5)).toBe("rgba(0,255,0,0.5)");
  });
  it("fmt and cn", () => {
    expect(fmt(undefined)).toBe("–");
    expect(fmt(1234)).toBe((1234).toLocaleString());
    expect(cn("px-2", "px-4")).toBe("px-4");
  });
});

describe("langFor", () => {
  it("maps extensions to one lazily loaded mode", () => {
    expect(langFor("src/a.cpp")).toBe("cpp");
    expect(langFor("a/b.HPP")).toBe("cpp");
    expect(langFor("x.tsx")).toBe("typescript");
    expect(langFor("x.mjs")).toBe("javascript");
    expect(langFor("launch/robot.launch")).toBe("xml");
    expect(langFor("cfg.yml")).toBe("yaml");
    expect(langFor("Makefile")).toBeNull();
    expect(langFor("notes.txt")).toBeNull();
    expect(langFor("dir.go/README")).toBeNull();
  });
});

describe("symbolHref", () => {
  it("encodes the symbol and tab", () => {
    expect(symbolHref("shop", "HEAD", "orders.Charge")).toBe("/symbol?repo=shop&ref=HEAD&s=orders.Charge");
    expect(symbolHref("shop", "v1", "a b", "graph")).toBe("/symbol?repo=shop&ref=v1&s=a+b&tab=graph");
  });
});

describe("placeAround", () => {
  const anchor: Rect = { x: 0, y: 0, width: 100, height: 40 };
  const hit = (a: Rect, b: Rect) => a.x < b.x + b.width && b.x < a.x + a.width && a.y < b.y + b.height && b.y < a.y + a.height;

  it("puts callers left and callees right, centred on the anchor", () => {
    const p = placeAround(anchor, [anchor], [
      { id: "caller", width: 80, height: 40, side: -1 },
      { id: "callee", width: 60, height: 40, side: 1 },
    ]);
    expect(p.get("caller")).toEqual({ x: -90 - 80, y: 0 });
    expect(p.get("callee")).toEqual({ x: 100 + 90, y: 0 });
  });

  it("never overlaps what is already on screen or each other", () => {
    const taken: Rect[] = [anchor, { x: 190, y: -10, width: 120, height: 40 }, { x: 190, y: 50, width: 120, height: 40 }];
    const add = Array.from({ length: 5 }, (_, i) => ({ id: "n" + i, width: 90, height: 40, side: 1 as const }));
    const p = placeAround(anchor, taken, add);
    const rects = add.map((b) => ({ ...p.get(b.id)!, width: b.width, height: b.height }));
    for (const r of rects) for (const t of taken) expect(hit(r, t)).toBe(false);
    for (let i = 0; i < rects.length; i++) for (let j = i + 1; j < rects.length; j++) expect(hit(rects[i], rects[j])).toBe(false);
  });

  it("places nothing for nothing", () => {
    expect(placeAround(anchor, [anchor], []).size).toBe(0);
  });
});

describe("api client", () => {
  afterEach(() => vi.unstubAllGlobals());

  it("drops empty params and reads JSON", async () => {
    const f = vi.fn(async () => new Response(JSON.stringify({ repos: ["a"] }), { status: 200 }));
    vi.stubGlobal("fetch", f);
    expect(await api("repos", { repo: "", ref: "HEAD", n: 3, x: undefined })).toEqual({ repos: ["a"] });
    expect(f).toHaveBeenCalledWith("/api/repos?ref=HEAD&n=3", { signal: undefined });
  });

  it("raises the server's message with its status", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("no stored file x.go\n", { status: 404 })));
    const err = (await api("file", { path: "x.go" }).catch((e: unknown) => e)) as ApiError;
    expect(err).toBeInstanceOf(ApiError);
    expect(err.status).toBe(404);
    expect(err.message).toBe("no stored file x.go");
  });

  it("searchSymbols turns a suggestion envelope into hits", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(JSON.stringify({ found: false, did_you_mean: [{ name: "orders.Charge", repo: "shop" }, { name: "Refund" }] }))));
    expect(await searchSymbols("Chrage", "shop", "HEAD")).toEqual([
      { repo: "shop", name: "orders.Charge", ref: "HEAD" },
      { repo: "shop", name: "Refund", ref: "HEAD" },
    ]);
  });
});
