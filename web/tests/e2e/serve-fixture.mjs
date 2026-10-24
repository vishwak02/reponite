// Indexes the golden fixture repo (cmd/reponite/testdata/shop) at two refs into
// a fresh store and serves it with `reponite serve` — the server the Playwright
// smoke drives. REPONITE_BIN names the binary (default ../bin/reponite, from
// `make cli`); REPONITE_E2E_DIR the work directory (default a new temp dir);
// REPONITE_E2E_PORT the port (default 18990).
import { spawn, spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const bin = resolve(process.env.REPONITE_BIN || join(here, "../../../bin/reponite"));
const fixture = join(here, "../../../cmd/reponite/testdata/shop");
const work = process.env.REPONITE_E2E_DIR ? resolve(process.env.REPONITE_E2E_DIR) : mkdtempSync(join(tmpdir(), "reponite-e2e-"));
const port = process.env.REPONITE_E2E_PORT || "18990";

rmSync(join(work, "store"), { recursive: true, force: true });
rmSync(join(work, "shop"), { recursive: true, force: true });
mkdirSync(work, { recursive: true });
const env = {
  ...process.env,
  REPONITE_STORE_DIR: join(work, "store"),
  REPONITE_FLEET: join(work, "fleet.json"),
  XDG_CACHE_HOME: join(work, "cache"),
  XDG_CONFIG_HOME: join(work, "config"),
};
const index = (ref) => {
  const r = spawnSync(bin, ["index", "shop", ref], { cwd: work, env, stdio: "inherit" });
  if (r.status !== 0) process.exit(r.status ?? 1);
};
cpSync(join(fixture, "v1"), join(work, "shop"), { recursive: true });
index("v1");
rmSync(join(work, "shop"), { recursive: true, force: true });
cpSync(join(fixture, "v2"), join(work, "shop"), { recursive: true });
index("HEAD");

const srv = spawn(bin, ["serve", "shop", "--addr", "127.0.0.1:" + port], { cwd: work, env, stdio: "inherit" });
for (const sig of ["SIGINT", "SIGTERM"]) process.on(sig, () => srv.kill(sig));
srv.on("exit", (code) => process.exit(code ?? 0));
