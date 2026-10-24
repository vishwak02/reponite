import { defineConfig } from "@playwright/test";

const port = process.env.REPONITE_E2E_PORT || "18990";

// Smoke over every route against `reponite serve` on the fixture repo
// (tests/e2e/serve-fixture.mjs). Build the CLI first (`make cli`, or point
// REPONITE_BIN at a binary built with the current dist).
export default defineConfig({
  testDir: "tests/e2e",
  timeout: 60_000,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    // the full Chromium build in new headless mode
    channel: "chromium",
    viewport: { width: 1360, height: 860 },
  },
  webServer: {
    command: "node tests/e2e/serve-fixture.mjs",
    url: `http://127.0.0.1:${port}/api/repos`,
    timeout: 120_000,
    reuseExistingServer: false,
    stdout: "pipe",
  },
});
