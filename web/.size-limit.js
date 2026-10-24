// Bundle budgets (gzip), enforced in CI by `npm run size` after a build:
// the app shell — every script index.html loads or preloads — stays under
// 120 KB, and each page's own chunk under 80 KB. Heavy libraries load only on
// the pages that need them (tests/e2e/smoke.spec.ts checks which).
import { readdirSync, readFileSync } from "node:fs";

const dist = "../internal/interfaces/webui/dist";
const html = readFileSync(`${dist}/index.html`, "utf8");
const shell = [...html.matchAll(/(?:src|href)="\/(assets\/[^"]+\.js)"/g)].map((m) => `${dist}/${m[1]}`);
const assets = readdirSync(`${dist}/assets`);
const pages = readdirSync("src/pages")
  .filter((f) => f.endsWith(".tsx"))
  .map((f) => f.slice(0, -4));

// a page without a chunk of its own (Overview, the landing page) is in the shell
const own = pages.map((p) => [p, assets.find((a) => a.startsWith(p + "-") && a.endsWith(".js"))]).filter(([, chunk]) => chunk);

export default [
  { name: "app shell (JS)", path: shell, limit: "120 kB", gzip: true },
  ...own.map(([p, chunk]) => ({ name: `page ${p}`, path: [`${dist}/assets/${chunk}`], limit: "80 kB", gzip: true })),
];
