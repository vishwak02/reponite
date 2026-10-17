import { useMemo } from "react";
import { Link, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, Boxes, Braces, FileCode2, GitBranch, Share2 } from "lucide-react";
import { api, type FleetMap } from "@/lib/api";
import { FLEET_REF, useScope } from "@/lib/scope";
import { fmt, tint } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { Card, CardHeader, Stat } from "@/components/ui/card";
import { Rows, Skeleton } from "@/components/ui/skeleton";

export function useFleetMap() {
  return useQuery({ queryKey: ["fleetmap"], queryFn: () => api<FleetMap>("fleetmap", { ref: FLEET_REF }), staleTime: 5 * 60_000 });
}

function parseLangs(ls?: string[]) {
  return (ls ?? []).map((l) => {
    const i = l.lastIndexOf(":");
    return i > 0 ? { lang: l.slice(0, i), n: +l.slice(i + 1) || 0 } : { lang: l, n: 0 };
  });
}

export default function Overview() {
  const { overview, setRepo } = useScope();
  const fm = useFleetMap();
  const nav = useNavigate();
  const repos = fm.data?.repos ?? [];
  const totals = useMemo(() => {
    const langs = new Map<string, number>();
    const entries = new Map<string, number>();
    let symbols = 0;
    let files = 0;
    for (const r of repos) {
      symbols += r.symbols ?? 0;
      files += r.files ?? 0;
      for (const { lang, n } of parseLangs(r.languages)) langs.set(lang, (langs.get(lang) ?? 0) + n);
      for (const [k, v] of Object.entries(r.entry_points ?? {})) entries.set(k, (entries.get(k) ?? 0) + v);
    }
    const sort = (m: Map<string, number>) => [...m.entries()].sort((a, b) => b[1] - a[1]);
    return { symbols, files, langs: sort(langs), entries: sort(entries) };
  }, [repos]);
  const versions = overview.reduce((n, o) => n + o.refs.length, 0);
  const top = [...repos].sort((a, b) => (b.symbols ?? 0) - (a.symbols ?? 0)).slice(0, 12);
  const maxSym = top[0]?.symbols ?? 1;
  const langTotal = totals.langs.reduce((n, [, v]) => n + v, 0) || 1;
  const open = (r: string) => {
    setRepo(r);
    nav("/symbol");
  };

  return (
    <Page title="Fleet overview" sub="Every indexed repository, at the version this server is pinned to.">
      <div className="grid grid-cols-2 gap-3 lg:grid-cols-5">
        <Stat label="Repositories" value={fmt(overview.length || repos.length)} icon={<Boxes />} />
        <Stat label="Versions indexed" value={fmt(versions)} icon={<GitBranch />} hint="refs across all repos" />
        <Stat label="Symbols" value={fm.isLoading ? <Skeleton className="h-7 w-24" /> : fmt(totals.symbols)} icon={<Braces />} />
        <Stat label="Source files" value={fm.isLoading ? <Skeleton className="h-7 w-24" /> : fmt(totals.files)} icon={<FileCode2 />} />
        <Stat label="Cross-repo links" value={fm.isLoading ? <Skeleton className="h-7 w-16" /> : fmt(fm.data?.links?.length)} icon={<Share2 />} hint={<Link to="/fleet" className="text-primary hover:underline">open the fleet map →</Link>} />
      </div>

      <div className="mt-4 grid gap-4 lg:grid-cols-[1.4fr_1fr]">
        <Card>
          <CardHeader title="Largest repositories" sub="by symbol count" right={<Link to="/repos" className="flex items-center gap-1 text-xs font-medium text-primary hover:underline">All repos <ArrowRight className="size-3" /></Link>} />
          <div className="space-y-1 p-3">
            {fm.isLoading && <Rows n={8} />}
            {top.map((r) => (
              <button key={r.repo} onClick={() => open(r.repo)} className="group flex w-full items-center gap-3 rounded-md px-2 py-1 text-left hover:bg-muted">
                <span className="w-44 truncate text-sm font-medium group-hover:text-primary">{r.repo}</span>
                <div className="h-2 flex-1 overflow-hidden rounded-full bg-muted">
                  <div className="h-full rounded-full" style={{ width: `${(100 * (r.symbols ?? 0)) / maxSym}%`, background: tint(r.repo) }} />
                </div>
                <span className="w-16 text-right text-xs tabular-nums text-muted-foreground">{fmt(r.symbols)}</span>
              </button>
            ))}
          </div>
        </Card>
        <div className="space-y-4">
          <Card>
            <CardHeader title="Languages" sub="files by language, fleet-wide" />
            <div className="p-4">
              <div className="flex h-3 overflow-hidden rounded-full">
                {totals.langs.slice(0, 10).map(([l, n]) => (
                  <div key={l} title={`${l}: ${fmt(n)}`} style={{ width: `${(100 * n) / langTotal}%`, background: tint(l) }} />
                ))}
              </div>
              <div className="mt-3 grid grid-cols-2 gap-x-4 gap-y-1.5">
                {totals.langs.slice(0, 10).map(([l, n]) => (
                  <div key={l} className="flex items-center gap-2 text-xs">
                    <span className="size-2 rounded-full" style={{ background: tint(l) }} />
                    <span className="truncate">{l}</span>
                    <span className="ml-auto tabular-nums text-muted-foreground">{fmt(n)}</span>
                  </div>
                ))}
              </div>
            </div>
          </Card>
          <Card>
            <CardHeader title="Entry points" sub="how the code is started" />
            <div className="flex flex-wrap gap-1.5 p-4">
              {totals.entries.map(([k, v]) => (
                <span key={k} className="flex items-center gap-1.5 rounded-md border px-2 py-1 text-xs">
                  {k}
                  <span className="font-semibold tabular-nums">{fmt(v)}</span>
                </span>
              ))}
            </div>
          </Card>
        </div>
      </div>
    </Page>
  );
}
