// Every route renders against `reponite serve` on the fixture repo with no
// console errors, and loads only the heavy libraries it needs.
import { expect, test, type Page } from "@playwright/test";

const SYM = "repo=shop&ref=HEAD&s=orders.Charge";
const HEAVY = ["codemirror", "lang-", "flow", "sigma", "elk-worker"] as const;

interface Route {
  path: string;
  heading: RegExp;
  needs?: string[]; // heavy chunks this view must load
  ready?: (p: Page) => Promise<void>;
}

const ROUTES: Route[] = [
  { path: "/", heading: /Overview|Fleet/ },
  { path: "/repos", heading: /Repos/ },
  { path: "/fleet", heading: /Fleet map/, needs: ["sigma"], ready: (p) => expect(p.locator("canvas").first()).toBeVisible() },
  { path: "/topics", heading: /ROS communication/ },
  { path: "/coverage", heading: /Index coverage/, ready: (p) => expect(p.getByText("shop").first()).toBeVisible() },
  { path: "/symbol", heading: /Symbol/ },
  { path: `/symbol?${SYM}`, heading: /Charge/, ready: (p) => expect(p.getByText("handleOrders").first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=code`, heading: /Charge/, needs: ["codemirror", "lang-go"], ready: (p) => expect(p.locator(".cm-editor .tok-keyword, .cm-editor span[class^=ͼ]").first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=usages`, heading: /Charge/, ready: (p) => expect(p.getByText(/usages/).first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=compat`, heading: /Charge/, ready: (p) => expect(p.getByText("behavior changed").first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=explain`, heading: /Charge/, ready: (p) => expect(p.getByText("func Charge(o Order) error").first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=calls`, heading: /Charge/, ready: (p) => expect(p.locator("[data-tier=proven]").first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=impls`, heading: /Charge/, ready: (p) => expect(p.getByText(/Nothing implements or overrides/).first()).toBeVisible() },
  { path: `/symbol?${SYM}&tab=tests`, heading: /Charge/, ready: (p) => expect(p.locator("[data-command]").first()).toContainText("go test ./orders") },
  { path: `/symbol?${SYM}&tab=history`, heading: /Charge/, ready: (p) => expect(p.getByText("Commits that shaped it")).toBeVisible() },
  { path: "/ask", heading: /Ask/ },
  { path: "/ask?q=what+breaks+if+I+change+Charge", heading: /Ask/, ready: (p) => expect(p.getByText("Tests to run")).toBeVisible() },
  { path: "/ask?q=what+runs+on+POST+%2Fapi%2Forders", heading: /Ask/, ready: (p) => expect(p.getByText("placeOrder").first()).toBeVisible() },
  { path: "/ask?q=%5BERROR%5D+charging+order+a1+failed", heading: /Ask/, ready: (p) => expect(p.locator("[data-evidence]").filter({ hasText: "orders/orders.go:21" })).toBeVisible() },
  { path: "/ask?q=how+is+an+order+charged", heading: /Ask/, ready: (p) => expect(p.locator("[data-evidence]").first()).toBeVisible() },
  { path: "/flow?entry=POST+%2Fapi%2Forders", heading: /Flow/ },
  { path: `/symbol?${SYM}&tab=graph`, heading: /Charge/, needs: ["flow", "elk-worker"], ready: (p) => expect(p.locator(".react-flow__node").first()).toBeVisible() },
  { path: `/graph?${SYM}`, heading: /Call graph/, needs: ["flow", "elk-worker"], ready: (p) => expect(p.locator(".react-flow__node").first()).toBeVisible() },
  { path: "/graph", heading: /Call graph/ },
  { path: "/links", heading: /Links/ },
  { path: "/grep", heading: /Grep/ },
  { path: "/diff", heading: /Diff/ },
  { path: "/verify", heading: /Verify an edit/ },
];

// The views a heavy library is allowed on (beyond those that need it).
const ALLOWED: Record<string, RegExp> = {
  codemirror: /tab=code|^\/verify/,
  "lang-": /tab=code|^\/verify/,
  flow: /tab=graph|^\/graph\?|^\/links|^\/topics/,
  "elk-worker": /tab=graph|^\/graph\?|^\/links|^\/topics/,
  sigma: /^\/fleet|^\/topics/,
};

function watch(page: Page) {
  const errors: string[] = [];
  const scripts: string[] = [];
  page.on("console", (m) => m.type() === "error" && errors.push(m.text()));
  page.on("pageerror", (e) => errors.push(String(e)));
  page.on("request", (r) => {
    const m = /\/assets\/([^/?]+\.js)$/.exec(r.url());
    if (m) scripts.push(m[1]);
  });
  page.on("response", (r) => r.status() >= 500 && errors.push(`${r.status()} ${r.url()}`));
  return { errors, scripts };
}

for (const theme of ["light", "dark"]) {
  for (const r of ROUTES) {
    test(`${theme} ${r.path}`, async ({ page }) => {
      await page.addInitScript((t) => localStorage.setItem("reponite.theme", t), theme);
      const w = watch(page);
      await page.goto("/#" + r.path);
      await expect(page.getByRole("heading", { level: 1, name: r.heading })).toBeVisible();
      if (r.ready) await r.ready(page);
      await page.waitForLoadState("networkidle");
      expect(await page.evaluate(() => document.documentElement.classList.contains("dark"))).toBe(theme === "dark");
      expect(w.errors, w.errors.join("\n")).toEqual([]);
      for (const h of HEAVY) {
        const loaded = w.scripts.some((s) => s.startsWith(h));
        if (r.needs?.some((n) => n.startsWith(h))) expect(loaded, `${r.path} must load ${h}`).toBe(true);
        else if (!ALLOWED[h].test(r.path)) expect(loaded, `${r.path} loaded ${h}: ${w.scripts.join(" ")}`).toBe(false);
      }
      for (const n of r.needs ?? []) expect(w.scripts.some((s) => s.startsWith(n)), `${r.path} must load ${n}`).toBe(true);
      const dir = process.env.REPONITE_E2E_SHOTS;
      if (dir) await page.screenshot({ path: `${dir}/${theme}-${r.path.replace(/[^a-z0-9]+/gi, "_") || "home"}.png`, fullPage: false });
    });
  }
}

test("expanding a graph node keeps every node where it was", async ({ page }) => {
  const w = watch(page);
  await page.goto(`/#/graph?${SYM}`);
  const node = (id: string) => page.locator(`.react-flow__node[data-id="${id}"]`);
  // one hop: validateCard is drawn, its callee limit is not
  await page.getByRole("button", { name: "1 hop" }).click();
  await expect(node("orders.limit")).toHaveCount(0);
  await expect(node("orders.validateCard")).toBeVisible();
  await page.waitForTimeout(500); // the fit-view animation settles
  const before = await page.locator(".react-flow__node").evaluateAll((ns) => ns.map((n) => [n.getAttribute("data-id"), (n as HTMLElement).style.transform]));
  // select it and expand from the side panel (the panel opens over the
  // right-hand column, where a second click of a double-click would land)
  await node("orders.validateCard").click();
  await page.getByRole("button", { name: "Expand" }).click();
  await expect(node("orders.limit")).toBeVisible();
  const after = new Map(await page.locator(".react-flow__node").evaluateAll((ns) => ns.map((n) => [n.getAttribute("data-id"), (n as HTMLElement).style.transform] as const)));
  for (const [id, tf] of before) expect(after.get(id!), `${id} moved`).toBe(tf);
  expect(after.size).toBeGreaterThan(before.length);
  expect(w.errors).toEqual([]);
});

test("the Ask bar routes a question and its evidence opens the symbol hub", async ({ page }) => {
  const w = watch(page);
  await page.goto("/#/");
  const bar = page.getByRole("textbox", { name: "Ask" });
  await bar.fill("who calls validateCard");
  await expect(page.locator("[data-intent=callers]")).toBeVisible();
  await bar.press("Enter");
  await expect(page.getByRole("heading", { level: 1, name: "Ask" })).toBeVisible();
  await expect(page.locator("[data-read-as=callers]")).toBeVisible();
  const card = page.locator("[data-evidence]").filter({ hasText: "orders.Charge" }).first();
  await expect(card.locator("[data-tier=proven]")).toBeVisible();
  await card.getByRole("link", { name: "orders.Charge" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /Charge/ })).toBeVisible();
  // a bare symbol goes straight to its hub
  await page.goto("/#/ask?q=validateCard");
  await expect(page.getByRole("heading", { level: 1, name: /validateCard/ })).toBeVisible();
  expect(page.url()).toContain("s=orders.validateCard");
  expect(w.errors).toEqual([]);
});

test("every symbol tab has its own URL and the code tab marks call sites", async ({ page }) => {
  const w = watch(page);
  await page.goto(`/#/symbol?${SYM}`);
  for (const [name, tab] of [["Callers & callees", "calls"], ["Tests", "tests"], ["History", "history"], ["Code", "code"]]) {
    await page.getByRole("tab", { name }).click();
    await expect(page).toHaveURL(new RegExp(`tab=${tab}`));
  }
  await expect(page.locator(".cm-callsite-proven").first()).toBeVisible();
  await expect(page.locator(".cm-callsite-guessed").first()).toBeVisible();
  await page.reload();
  await expect(page.getByRole("tab", { name: "Code", selected: true })).toBeVisible();
  expect(w.errors).toEqual([]);
});

test("the home shows what changed since the last index", async ({ page }) => {
  await page.goto("/#/");
  const panel = page.locator("[data-since-index]");
  await expect(panel.getByText("v1 → HEAD")).toBeVisible();
  await expect(panel.getByText(/behavior changed/).first()).toBeVisible();
  await panel.getByRole("link", { name: "validateCard" }).click();
  await expect(page.getByRole("heading", { level: 1, name: /validateCard/ })).toBeVisible();
  await expect(page).toHaveURL(/tab=history/);
});

test("the command palette opens on Ctrl+K and finds a symbol", async ({ page }) => {
  const w = watch(page);
  await page.goto("/#/");
  await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
  await page.keyboard.press("Control+k");
  await page.getByPlaceholder(/Search symbols/).fill("Charge");
  await page.getByText("orders.Charge").first().click();
  await expect(page.getByRole("heading", { level: 1, name: /Charge/ })).toBeVisible();
  expect(w.errors).toEqual([]);
});

test("assets and API answers are compressed and conditional", async ({ request }) => {
  const html = await (await request.get("/")).text();
  const js = /\/assets\/index-[^"]+\.js/.exec(html)![0];
  const a = await request.get(js, { headers: { "Accept-Encoding": "br" } });
  expect(a.headers()["content-encoding"]).toBe("br");
  expect(a.headers()["cache-control"]).toContain("immutable");
  const v2 = await request.get("/api/v2/explain?target=orders.Charge&repo=shop", { headers: { "Accept-Encoding": "gzip" } });
  expect(v2.status()).toBe(200);
  expect(v2.headers()["etag"]).toMatch(/^W\//);
  expect((await v2.json()).kind).toBe("symbol");
  const api = await request.get("/api/coverage?ref=HEAD", { headers: { "Accept-Encoding": "gzip" } });
  const tag = api.headers()["etag"];
  expect(tag).toMatch(/^W\//);
  const again = await request.get("/api/coverage?ref=HEAD", { headers: { "If-None-Match": tag } });
  expect(again.status()).toBe(304);
});
