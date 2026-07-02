# Reponite

Ref-aware code intelligence for AI agents and teams. One binary, local or
shared, honest about what it knows.

**Status:** early build. Scaffold stage `S0.1` complete — module structure,
CLI router, and the `version` command build and pass tests. The flagship
Compatibility Oracle and agent-facing reads (editing brief, root-cause
drill-down) are built across sessions M0→M5; see `PROGRESS.md` for the map.

## Build

```
make build      # -> bin/reponite
make test
./bin/reponite version
```

Requires Go 1.22+ (the module pins `go 1.18` only so the dependency-free core
compiles in a constrained build sandbox).

## Docs

- `PROGRESS.md` — build cursor, invariants, interface index, session log (read this first).
- `docs/adr/` — architecture decision records.
- Architecture spec + build/session plans live alongside this repo in the
  planning docs (`reponite-build-plan.md`, `reponite-claude-build-plan.md`,
  `reponite-architecture-extension.md`).
