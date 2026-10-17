import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build lands in internal/interfaces/webui/dist, which the Go binary
// embeds; `npm run dev` serves the app with /api proxied to `reponite serve`.
export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": new URL("./src", import.meta.url).pathname } },
  base: "/",
  build: {
    outDir: "../internal/interfaces/webui/dist",
    emptyOutDir: true,
    chunkSizeWarningLimit: 1600,
  },
  server: { proxy: { "/api": "http://127.0.0.1:8899" } },
});
