// What changed between two indexed versions of a repo, and who in the fleet
// calls each changed symbol.
import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { Link } from "react-router-dom";
import { ArrowRight, Search } from "lucide-react";
import { api, type DiffResult, type XImpact } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { cn, fmt, shortName } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Badge, Confidence } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows } from "@/components/ui/skeleton";

const CHANGE: Record<string, "ok" | "bad" | "warn" | "shape"> = { added: "ok", removed: "bad", behavior_changed: "warn", shape_changed: "shape" };

export default function Diff() {
  const { repo, refs } = useScope();
  // newest first, comparing version numbers ("3.5.0-rc4" after "3.3.0-rc17")
  const real = refs.filter((r) => r !== "HEAD").sort((a, b) => b.split("@").pop()!.localeCompare(a.split("@").pop()!, undefined, { numeric: true }));
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  useEffect(() => {
    setFrom(real[1] ?? real[0] ?? "");
    setTo(real[0] ?? "");
  }, [repo, real.join(",")]); // eslint-disable-line react-hooks/exhaustive-deps
  const d = useQuery({ queryKey: ["diff", repo, from, to], queryFn: () => api<DiffResult>("diff", { repo, from, to, changed_only: true }), enabled: !!from && !!to && from !== to });
  const [kind, setKind] = useState("all");
  // documents and config sections are named "file:Heading:Key"; code is "pkg.Name"
  const [codeOnly, setCodeOnly] = useState(true);
  const [q, setQ] = useState("");
  const [sel, setSel] = useState<string | null>(null);
  const counts = useMemo(() => {
    const m = new Map<string, number>();
    for (const c of d.data?.changes ?? []) if (!codeOnly || !c.name.includes(":")) m.set(c.change, (m.get(c.change) ?? 0) + 1);
    return m;
  }, [d.data, codeOnly]);
  const rows = useMemo(
    () => (d.data?.changes ?? []).filter((c) => (!codeOnly || !c.name.includes(":")) && (kind === "all" || c.change === kind) && (!q || c.name.toLowerCase().includes(q.toLowerCase()))),
    [d.data, codeOnly, kind, q],
  );
  const total = [...counts.values()].reduce((a, b) => a + b, 0);
  const list = useRef<HTMLDivElement>(null);
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => list.current, estimateSize: () => 37, overscan: 16 });
  const sel2 = rows.find((c) => c.name === sel);

  return (
    <Page wide title="Diff & impact" sub={<>Symbols that changed in <b>{repo}</b> between two indexed versions.</>}>
      {real.length < 2 ? (
        <Empty title={`${repo} has only ${real.length} indexed version`}>Index another ref of this repo (reponite index --git &lt;ref&gt;) to compare.</Empty>
      ) : (
        <>
          <div className="mb-4 flex flex-wrap items-center gap-2">
            <RefSelect value={from} set={setFrom} refs={real} />
            <ArrowRight className="size-4 text-muted-foreground" />
            <RefSelect value={to} set={setTo} refs={real} />
            <div className="ml-4 flex flex-wrap gap-1">
              {(["all", "added", "removed", "shape_changed", "behavior_changed"] as const).map((k) => (
                <button key={k} onClick={() => setKind(k)} className={cn("rounded-md border px-2 py-1 text-xs font-medium", kind === k ? "border-primary/40 bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted")}>
                  {k.replace("_", " ")} <span className="tabular-nums opacity-70">{k === "all" ? fmt(total) : fmt(counts.get(k) ?? 0)}</span>
                </button>
              ))}
            </div>
            <label className="ml-2 flex items-center gap-1.5 text-xs text-muted-foreground">
              <input type="checkbox" checked={codeOnly} onChange={(e) => setCodeOnly(e.target.checked)} />
              code only
            </label>
            <div className="relative ml-auto w-64"><Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input className="h-8 pl-8" placeholder="Filter symbols…" value={q} onChange={(e) => setQ(e.target.value)} /></div>
          </div>
          {d.isLoading ? <Rows n={10} /> : d.error ? <Empty error title="Diff failed">{String(d.error)}</Empty> : (
            <div className="grid gap-4 lg:grid-cols-[1fr_420px]">
              <Card className="overflow-hidden">
                <div ref={list} className="h-[calc(100vh-260px)] overflow-y-auto">
                  {!rows.length && <div className="p-8 text-center text-sm text-muted-foreground">No {codeOnly ? "code symbol" : "symbol"} {kind !== "all" ? `${kind.replace("_", " ")} ` : "changed "}between {from} and {to}{codeOnly && d.data?.changes?.length ? ` (${d.data.changes.length} document/config changes hidden)` : ""}.</div>}
                  <div style={{ height: v.getTotalSize(), position: "relative" }}>
                    {v.getVirtualItems().map((it) => {
                      const c = rows[it.index];
                      return (
                        <button key={c.name} onClick={() => setSel(c.name)} style={{ top: it.start, height: it.size }} className={cn("absolute inset-x-0 flex items-center gap-3 border-b px-4 text-left hover:bg-muted/40", sel === c.name && "bg-accent/60")}>
                          <Badge variant={CHANGE[c.change] ?? "outline"} className="w-28 shrink-0 justify-center">{c.change.replace("_", " ")}</Badge>
                          <span className="shrink-0 text-sm font-medium">{shortName(c.name)}</span>
                          <span className="truncate font-mono text-[11px] text-muted-foreground">{c.name}</span>
                        </button>
                      );
                    })}
                  </div>
                </div>
              </Card>
              {sel2 ? <Impact name={sel2.name} change={sel2.change} repo={repo} to={to} from={from} /> : <Empty title="Pick a change">See who in the fleet calls it.</Empty>}
            </div>
          )}
        </>
      )}
    </Page>
  );
}

function RefSelect({ value, set, refs }: { value: string; set: (v: string) => void; refs: string[] }) {
  return (
    <select value={value} onChange={(e) => set(e.target.value)} className="h-8 rounded-md border bg-card px-2 font-mono text-xs">
      {refs.map((r) => <option key={r}>{r}</option>)}
    </select>
  );
}

function Impact({ name, change, repo, from, to }: { name: string; change: string; repo: string; from: string; to: string }) {
  const code = !name.includes(":");
  const x = useQuery({ queryKey: ["ximpact", name], queryFn: () => api<XImpact>("ximpact", { symbol: name, ref: "HEAD" }), enabled: code });
  return (
    <Card className="self-start">
      <CardHeader title={shortName(name)} sub={<span className="break-all font-mono">{name}</span>} />
      <div className="space-y-3 p-4 text-sm">
        <div className="flex flex-wrap gap-2">
          <Badge variant={CHANGE[change] ?? "outline"}>{change.replace("_", " ")}</Badge>
          {code && change !== "added" && <Link className="text-xs text-primary hover:underline" to={symbolHref(repo, from, name)}>open in {from}</Link>}
          {code && change !== "removed" && <Link className="text-xs text-primary hover:underline" to={symbolHref(repo, to, name)}>open in {to}</Link>}
          {code && change !== "removed" && <Link className="text-xs text-primary hover:underline" to={symbolHref(repo, to, name, "compat")}>compare versions</Link>}
        </div>
        {!code ? (
          <p className="text-xs text-muted-foreground">A document or config section; no callers to trace.</p>
        ) : x.isLoading ? (
          <Rows n={3} />
        ) : (
          <>
            <div className="text-xs font-semibold text-muted-foreground">Callers across the fleet ({x.data?.callers?.length ?? 0})</div>
            {x.data?.contract_changed && <Badge variant="bad">contract changed — {x.data.stale_callers} stale caller(s)</Badge>}
            <div className="max-h-[50vh] divide-y overflow-y-auto rounded-md border">
              {!x.data?.callers?.length && <div className="p-3 text-xs text-muted-foreground">No caller found outside this repo.</div>}
              {x.data?.callers?.map((c, i) => (
                <Link key={i} to={symbolHref(c.repo, c.ref, c.caller)} className="block px-3 py-2 text-xs hover:bg-muted/50">
                  <div className="flex items-center gap-2"><b>{c.repo}</b><span className="truncate">{shortName(c.caller)}</span><span className="ml-auto"><Confidence value={c.confidence} /></span></div>
                  <div className="font-mono text-[10px] text-muted-foreground">{c.ref} · {c.resolution_method}</div>
                </Link>
              ))}
            </div>
            {x.data?.note && <p className="text-[11px] text-muted-foreground">{x.data.note}</p>}
          </>
        )}
      </div>
    </Card>
  );
}
