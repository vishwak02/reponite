# reponite — VS Code extension

Human-facing surface for [reponite](https://github.com/vishwak02/reponite) (the
richer agent-facing backend is the MCP server, `reponite mcp`).

## Commands
- **reponite: Brief for symbol under cursor** — runs `reponite brief <symbol>`
  and shows the body + callees/callers + covering tests + compat snapshot.
- **reponite: Compat for symbol under cursor** — runs `reponite compat <symbol>`
  (verdicts across indexed refs, with `changed_callees`).
- **reponite: Open web dashboard** — starts `reponite serve` and opens it.

Right-click a symbol in the editor for Brief/Compat in the context menu.

## Setup
1. Build reponite (`make cli`) and put the `reponite` binary on your `PATH`
   (or set `reponite.binary`).
2. Index the repo once: `reponite index .`.
3. In this folder: `npm install && npm run compile`, then press **F5** to launch
   an Extension Development Host.

## Settings
- `reponite.binary` (default `reponite`) — path to the binary.
- `reponite.serveAddr` (default `127.0.0.1:8899`) — address for the dashboard.

> Status: functional scaffold. Verify with `npm install && npm run compile`
> before packaging (`vsce package`); it is not built by reponite's Go CI.
