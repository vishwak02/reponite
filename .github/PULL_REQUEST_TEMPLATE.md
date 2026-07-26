<!--
Thanks for contributing. reponite ships one focused change per PR — it keeps
review honest and makes a revert cheap. Delete any section that doesn't apply.
-->

## What this changes

<!-- One or two sentences. If it fixes a wrong answer, lead with the wrong answer. -->

## Why

<!-- The root cause, not just the symptom. For a bug: what made the old behavior
     possible? That's usually the more interesting half. -->

## Evidence

<!-- Before/after output from a real run. Numbers beat adjectives. For example:

     | check | before | after |
     |---|---|---|
     | `grep "TODO\|FIXME"` | 0 matches | 542 matches |
-->

## Checklist

- [ ] `go build ./... && go test ./...` passes (the pure core, zero dependencies)
- [ ] The adapter checks that apply pass: `make sqlite` / `treesitter` / `mcp` / `e2e` / `neural`
- [ ] A test covers the change — and would have failed before it
- [ ] Docs updated in this same PR (README / `docs/` / `PROGRESS.md`) if behavior changed
- [ ] No dependency added to the pure core, and no `go.mod`/`go.sum` change my code doesn't require
- [ ] Every new result carries its `resolution_method` and `confidence` — nothing overclaims

<!-- If this PR makes reponite less certain about something, say so explicitly.
     Reporting "unknown" is always preferred over guessing. -->
