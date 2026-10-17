#!/usr/bin/env python3
"""Run reponite's commands over every git repository under a directory and
grade every call — a regression check on real code of any shape.

For each repo: index HEAD, `map`, then for its three most connected symbols
explain / context / usages / trace / tests / why / brief, then models / config /
errors / topics / semsearch / ui / coverage; finally the fleet-wide map, routes
and topics. Each call is graded crash (panic, signal, timeout), error (non-zero
exit), slow (over --slow seconds), or ok, with its time and output size, into
<out>.jsonl. Compare two runs with --compare to list what got worse.

    python3 tools/battery.py ~/src --bin bin/reponite --store /tmp/battery-store --out /tmp/run1
    python3 tools/battery.py --compare /tmp/run0.jsonl /tmp/run1.jsonl
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import time
from pathlib import Path


def grade(rc: int, err: str, secs: float, slow: float) -> str:
    if rc == -9 or re.search(r"^(panic:|fatal error:|SIGSEGV)", err, re.M) or rc >= 2 and "usage" not in err:
        return "crash"
    if rc != 0:
        return "error"
    return "slow" if secs > slow else "ok"


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("root", nargs="?")
    ap.add_argument("--bin", default="reponite")
    ap.add_argument("--store", help="REPONITE_STORE_DIR for this run (default: a temp dir)")
    ap.add_argument("--out", default="battery")
    ap.add_argument("--slow", type=float, default=30)
    ap.add_argument("--compare", nargs=2, metavar=("BEFORE", "AFTER"))
    a = ap.parse_args()
    if a.compare:
        return compare(*a.compare)
    if not a.root:
        ap.error("a directory of repositories is required")
    store = Path(a.store or f"/tmp/reponite-battery-{os.getpid()}")
    store.mkdir(parents=True, exist_ok=True)
    env = dict(os.environ, REPONITE_STORE_DIR=str(store), REPONITE_FLEET=str(store / "fleet.json"))
    out = open(f"{a.out}.jsonl", "w")
    counts: dict[str, int] = {}

    def run(repo: str, args: list[str], cwd: Path) -> str:
        t = time.time()
        try:
            p = subprocess.run([a.bin, *args], cwd=cwd, env=env, capture_output=True, text=True, timeout=600)
            rc, so, se = p.returncode, p.stdout, p.stderr
        except subprocess.TimeoutExpired:
            rc, so, se = -9, "", "TIMEOUT"
        secs = round(time.time() - t, 2)
        v = grade(rc, se, secs, a.slow)
        counts[v] = counts.get(v, 0) + 1
        out.write(json.dumps({"repo": repo, "args": args, "rc": rc, "secs": secs, "bytes": len(so), "verdict": v,
                              "err": se[-800:]}) + "\n")
        out.flush()
        print(f"{v:5s} {secs:7.2f}s {repo:30s} {' '.join(args)[:80]}", flush=True)
        return so

    repos = [d for d in sorted(Path(a.root).expanduser().iterdir()) if (d / ".git").exists()]
    for d in repos:
        run(d.name, ["index", ".", "--git", "HEAD"], d)
        try:
            m = json.loads(run(d.name, ["map", "--repo", d.name], d))
        except ValueError:
            m = {}
        for h in (m.get("hubs") or [])[:3]:
            sym = h["symbol"]
            for verb in (["explain", sym], ["context", sym, "--local"], ["usages", sym, "--local"],
                         ["trace", sym, "--local"], ["tests", sym], ["why", sym], ["brief", sym, "--local"]):
                run(d.name, verb, d)
        for verb in (["models", "--local"], ["config", "--local"], ["errors", "not found", "--local"],
                     ["topics", "--local"], ["semsearch", "start", "--local"], ["ui", "--local"],
                     ["coverage", "--local"]):
            run(d.name, verb, d)
    if repos:
        for verb in (["map"], ["routes"], ["topics"]):
            run("FLEET", verb, repos[0])
    print(json.dumps(counts))
    return 1 if counts.get("crash") or counts.get("error") else 0


def compare(before: str, after: str) -> int:
    def load(p):
        return {(r["repo"], " ".join(r["args"])): r for r in map(json.loads, open(p))}
    a, b = load(before), load(after)
    rank = {"ok": 0, "slow": 1, "error": 2, "crash": 3}
    worse = [(k, a[k], b[k]) for k in b if k in a and rank[b[k]["verdict"]] > rank[a[k]["verdict"]]]
    slower = [(k, a[k], b[k]) for k in b if k in a and b[k]["secs"] > 2 * a[k]["secs"] + 1]
    for k, x, y in worse:
        print(f"worse  {k[0]} {k[1]}: {x['verdict']} -> {y['verdict']}")
    for k, x, y in slower:
        print(f"slower {k[0]} {k[1]}: {x['secs']}s -> {y['secs']}s")
    print(f"{len(worse)} worse, {len(slower)} slower, of {len(b)} calls")
    return 1 if worse else 0


if __name__ == "__main__":
    raise SystemExit(main())
