import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";
import { defineConfig } from "vitest/config";
import type { Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Packages grouped into one long-cached vendor chunk each. A group is loaded
// only by the pages that use it; CodeMirror's language modes stay out of it,
// so each is its own chunk, fetched for the file being shown.
const LAZY_UI = new Set(["@radix-ui/react-tabs", "@radix-ui/react-roving-focus", "@radix-ui/react-collection", "@radix-ui/react-direction"]);
const VENDOR: [string, (pkg: string) => boolean][] = [
  ["react", (p) => ["react", "react-dom", "scheduler", "react-router", "react-router-dom", "@remix-run/router", "use-sync-external-store"].includes(p)],
  ["query", (p) => p.startsWith("@tanstack/")],
  // the shell's popover, tooltip and command menu (the Symbol page's tabs load
  // with that page)
  ["ui", (p) => (p.startsWith("@radix-ui/") && !LAZY_UI.has(p)) || p.startsWith("@floating-ui/") || ["cmdk", "react-remove-scroll", "react-remove-scroll-bar", "aria-hidden", "use-sidecar", "use-callback-ref", "react-style-singleton", "get-nonce", "tslib"].includes(p)],
  ["codemirror", (p) => (p.startsWith("@codemirror/") && !p.startsWith("@codemirror/lang-")) || p.startsWith("@uiw/") || ["@lezer/common", "@lezer/highlight", "@lezer/lr", "style-mod", "w3c-keyname", "crelt"].includes(p)],
  ["flow", (p) => p.startsWith("@xyflow/") || p.startsWith("d3-") || ["zustand", "classcat"].includes(p)],
  ["sigma", (p) => p === "sigma" || p.startsWith("graphology")],
];

// A language mode and its grammar: lang-cpp holds @codemirror/lang-cpp and
// @lezer/cpp.
const LEZER_CORE = new Set(["common", "highlight", "lr"]);

function vendorChunk(id: string): string | undefined {
  const at = id.lastIndexOf("node_modules/");
  if (at < 0) return undefined;
  const parts = id.slice(at + "node_modules/".length).split("/");
  const pkg = parts[0].startsWith("@") ? parts[0] + "/" + parts[1] : parts[0];
  if (pkg.startsWith("@codemirror/lang-")) return pkg.slice("@codemirror/".length);
  if (pkg.startsWith("@lezer/") && !LEZER_CORE.has(parts[1])) return "lang-" + parts[1];
  return VENDOR.find(([, has]) => has(pkg))?.[0];
}

// Writes a brotli (.br) and a gzip (.gz) copy beside every compressible
// asset; the Go server answers with the best one the browser accepts
// (internal/interfaces/web_assets.go). A copy is kept only when it is
// clearly smaller than the file.
function precompress(): Plugin {
  const compressible = /\.(js|css|svg|json|txt|html|map)$/;
  return {
    name: "reponite-precompress",
    apply: "build",
    writeBundle(opts) {
      const walk = (dir: string): string[] =>
        readdirSync(dir).flatMap((f) => {
          const p = join(dir, f);
          return statSync(p).isDirectory() ? walk(p) : [p];
        });
      for (const f of walk(join(opts.dir!, "assets"))) {
        if (!compressible.test(f)) continue;
        const raw = readFileSync(f);
        if (raw.length < 1024) continue;
        const br = brotliCompressSync(raw, { params: { [constants.BROTLI_PARAM_QUALITY]: 11, [constants.BROTLI_PARAM_SIZE_HINT]: raw.length } });
        const gz = gzipSync(raw, { level: 9 });
        if (br.length < raw.length * 0.9) writeFileSync(f + ".br", br);
        if (gz.length < raw.length * 0.9) writeFileSync(f + ".gz", gz);
      }
    },
  };
}

// The build lands in internal/interfaces/webui/dist, which the Go binary
// embeds; `npm run dev` serves the app with /api proxied to `reponite serve`.
export default defineConfig({
  plugins: [react(), precompress()],
  resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
  base: "/",
  build: {
    outDir: "../internal/interfaces/webui/dist",
    emptyOutDir: true,
    // only the ELK worker (loaded by the graph views, off the main thread) is
    // larger than this
    chunkSizeWarningLimit: 600,
    rollupOptions: { output: { manualChunks: vendorChunk } },
  },
  server: { proxy: { "/api": "http://127.0.0.1:8899" } },
  test: {
    environment: "jsdom",
    include: ["tests/unit/**/*.test.{ts,tsx}"],
  },
});
