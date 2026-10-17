// The symbol hub: everything reponite knows about one symbol, in tabs.
import { lazy, Suspense, useEffect, useRef, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { ArrowDownRight, ArrowUpLeft, Braces, Code2, FileCode2, FlaskConical, GitCompareArrows, ListTree, Network, Search } from "lucide-react";
import { api, searchSymbols, type Brief, type CompatResult, type Neighbor, type UsagesResult } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { cn, shortName, tint } from "@/lib/utils";
import CodeView from "@/components/code/CodeView";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Badge, Confidence } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows, Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";

const CallGraph = lazy(() => import("@/components/graph/CallGraph"));

export function useSymbolParams() {
  const [sp, setSp] = useSearchParams();
  const scope = useScope();
  const repo = sp.get("repo") || scope.repo;
  const ref = sp.get("ref") || scope.ref;
  const s = sp.get("s") || "";
  // a link into another repo moves the global scope with it
  useEffect(() => {
    if (sp.get("repo") && sp.get("repo") !== scope.repo) scope.setRepo(sp.get("repo")!);
  }, [sp.get("repo")]); // eslint-disable-line react-hooks/exhaustive-deps
  return { repo, ref, s, tab: sp.get("tab") || "overview", sp, setSp };
}

export default function SymbolPage() {
  const { repo, ref, s, tab, sp, setSp } = useSymbolParams();
  if (!s) return <SymbolSearch repo={repo} refName={ref} />;
  return (
    <SymbolHub
      repo={repo}
      refName={ref}
      s={s}
      tab={tab}
      setTab={(t) => {
        const n = new URLSearchParams(sp);
        n.set("tab", t);
        setSp(n, { replace: true });
      }}
    />
  );
}

function SymbolSearch({ repo, refName }: { repo: string; refName: string }) {
  const [q, setQ] = useState("");
  const nav = useNavigate();
  const hits = useQuery({
    queryKey: ["search", q, repo, refName],
    queryFn: ({ signal }) => searchSymbols(q, repo, refName, signal),
    enabled: q.trim().length >= 2 && !!repo,
  });
  return (
    <Page title="Symbol" sub={<>Find a function, method or type in <b>{repo}</b> at <code className="font-mono text-xs">{refName}</code>. Press ⌘K to search the whole fleet.</>}>
      <div className="relative max-w-xl">
        <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
        <Input autoFocus className="h-11 pl-9 text-[15px]" placeholder="e.g. computeVelocityCmd" value={q} onChange={(e) => setQ(e.target.value)} />
      </div>
      <div className="mt-4 max-w-3xl space-y-1">
        {hits.isFetching && !hits.data && <Rows n={5} />}
        {hits.data?.slice(0, 80).map((h) => (
          <button
            key={h.name}
            onClick={() => nav(symbolHref(h.repo, refName, h.name))}
            className="flex w-full items-center gap-3 rounded-lg border bg-card px-3 py-2 text-left hover:border-primary/40 hover:bg-accent/40"
          >
            {h.is_test ? <FlaskConical className="size-4 text-warn" /> : <Braces className="size-4 text-primary" />}
            <span className="font-medium">{shortName(h.name)}</span>
            <span className="truncate font-mono text-xs text-muted-foreground">{h.name}</span>
          </button>
        ))}
        {hits.data?.length === 0 && <Empty title={`No symbol matches “${q}” in ${repo}`}>Try ⌘K for a fleet-wide search.</Empty>}
      </div>
    </Page>
  );
}

function SymbolHub({ repo, refName, s, tab, setTab }: { repo: string; refName: string; s: string; tab: string; setTab: (t: string) => void }) {
  const brief = useQuery({ queryKey: ["brief", repo, refName, s], queryFn: () => api<Brief>("brief", { repo, ref: refName, symbol: s }) });
  const t = brief.data?.target;
  const name = shortName(s);
  return (
    <Page
      wide={tab === "graph"}
      title={
        <span className="flex items-center gap-2">
          <Braces className="size-5 text-primary" />
          {name}
        </span>
      }
      sub={
        <span className="flex flex-wrap items-center gap-2">
          <span className="rounded px-1.5 py-0.5 text-xs font-medium" style={{ color: tint(repo, 42), background: tint(repo, 55).replace(")", " / .12)") }}>
            {repo}
          </span>
          <code className="font-mono text-xs">{brief.data?.ref || refName}</code>
          {t?.path && (
            <span className="flex items-center gap-1 font-mono text-xs">
              <FileCode2 className="size-3.5" />
              {t.path}:{t.start_line}
              {t.end_line && t.end_line !== t.start_line ? `–${t.end_line}` : ""}
            </span>
          )}
          {t?.exported && <Badge variant="ok">exported</Badge>}
        </span>
      }
    >
      <div className="mb-1 truncate font-mono text-xs text-muted-foreground" title={s}>
        {s}
      </div>
      <Tabs value={tab} onValueChange={setTab}>
        <TabsList>
          <TabsTrigger value="overview"><ListTree />Overview</TabsTrigger>
          <TabsTrigger value="code"><Code2 />Code</TabsTrigger>
          <TabsTrigger value="graph"><Network />Call graph</TabsTrigger>
          <TabsTrigger value="usages"><Search />Usages</TabsTrigger>
          <TabsTrigger value="compat"><GitCompareArrows />Across versions</TabsTrigger>
        </TabsList>
        <TabsContent value="overview">
          {brief.isLoading ? <Rows /> : brief.error ? <Empty error title="Could not load the symbol">{String(brief.error)}</Empty> : <BriefView b={brief.data!} repo={repo} refName={refName} />}
        </TabsContent>
        <TabsContent value="code">
          {brief.isLoading ? <Rows /> : t?.path ? <SourceView repo={repo} refName={brief.data?.ref || refName} t={t} /> : <Empty title="No source stored for this symbol" />}
        </TabsContent>
        <TabsContent value="graph">
          <Suspense fallback={<Skeleton className="h-[70vh]" />}>
            <CallGraph repo={repo} refName={refName} symbol={s} />
          </Suspense>
        </TabsContent>
        <TabsContent value="usages">
          <UsagesView repo={repo} refName={refName} s={s} />
        </TabsContent>
        <TabsContent value="compat">
          <CompatView repo={repo} refName={refName} s={s} />
        </TabsContent>
      </Tabs>
    </Page>
  );
}

function NeighborList({ title, icon, items, repo, refName, empty }: { title: string; icon: React.ReactNode; items?: Neighbor[]; repo: string; refName: string; empty: string }) {
  return (
    <Card>
      <CardHeader title={<span className="flex items-center gap-1.5">{icon}{title}</span>} right={<Badge variant="outline">{items?.length ?? 0}</Badge>} />
      <div className="divide-y">
        {!items?.length && <div className="px-4 py-6 text-center text-sm text-muted-foreground">{empty}</div>}
        {items?.map((n, i) => (
          <div key={i} className="group px-4 py-2.5">
            <div className="flex items-center gap-2">
              {n.handle ? (
                <Link to={symbolHref(repo, refName, n.handle)} className="font-medium text-foreground hover:text-primary hover:underline">
                  {n.name}
                </Link>
              ) : (
                <span className="font-medium">{n.name}</span>
              )}
              {n.resolution_method && <span className="text-[11px] text-muted-foreground">{n.resolution_method}</span>}
              <span className="ml-auto"><Confidence value={n.confidence} /></span>
            </div>
            {n.path && <div className="truncate font-mono text-[11px] text-muted-foreground">{n.path}</div>}
            {n.preview && <pre className="mt-1.5 max-h-24 overflow-hidden rounded-md bg-muted/60 px-2 py-1.5 font-mono text-[11px] leading-relaxed text-muted-foreground">{n.preview}</pre>}
          </div>
        ))}
      </div>
    </Card>
  );
}

function BriefView({ b, repo, refName }: { b: Brief; repo: string; refName: string }) {
  return (
    <div className="space-y-4">
      {b._meta?.warnings?.map((w) => (
        <div key={w} className="rounded-lg border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">{w}</div>
      ))}
      <div className="grid gap-4 lg:grid-cols-2">
        <NeighborList title="Called by" icon={<ArrowUpLeft className="size-4 text-shape" />} items={b.callers} repo={repo} refName={refName} empty="No callers in the index." />
        <NeighborList title="Calls" icon={<ArrowDownRight className="size-4 text-primary" />} items={b.callees} repo={repo} refName={refName} empty="Calls nothing in the index." />
      </div>
      <Card>
        <CardHeader title={<span className="flex items-center gap-1.5"><FlaskConical className="size-4 text-warn" />Tests that reach it</span>} right={<Badge variant="outline">{b.tests?.length ?? 0}</Badge>} />
        <div className="flex flex-wrap gap-1.5 p-4">
          {b.tests?.length ? b.tests.map((t) => <Link key={t} to={symbolHref(repo, refName, t)} className="rounded-md border px-2 py-1 font-mono text-xs hover:border-primary/50 hover:text-primary">{shortName(t)}</Link>) : <span className="text-sm text-muted-foreground">No test reaches this symbol.</span>}
        </div>
      </Card>
    </div>
  );
}

function UsagesView({ repo, refName, s }: { repo: string; refName: string; s: string }) {
  const u = useQuery({ queryKey: ["usages", repo, refName, s], queryFn: () => api<UsagesResult>("usages", { repo, ref: refName, symbol: shortName(s).split(".").pop() }) });
  const parent = useRef<HTMLDivElement>(null);
  const rows = u.data?.usages ?? [];
  const v = useVirtualizer({ count: rows.length, getScrollElement: () => parent.current, estimateSize: () => 52, overscan: 12 });
  if (u.isLoading) return <Rows />;
  if (u.error) return <Empty error title="Usages failed">{String(u.error)}</Empty>;
  if (!rows.length) return <Empty title="No usages found">{u.data?.note}</Empty>;
  return (
    <Card className="overflow-hidden">
      <CardHeader title={`${u.data!.total} usages`} sub={u.data!.note || "Every mention of the name; confirmed ones are resolved calls."} />
      <div ref={parent} className="max-h-[65vh] overflow-y-auto">
        <div style={{ height: v.getTotalSize(), position: "relative" }}>
          {v.getVirtualItems().map((it) => {
            const r = rows[it.index];
            return (
              <div key={it.key} className="absolute inset-x-0 border-b px-4 py-2 hover:bg-muted/40" style={{ top: it.start, height: it.size }}>
                <div className="flex items-center gap-2 text-xs">
                  <span className="truncate font-mono text-muted-foreground">{r.path}:{r.line}</span>
                  {r.in && <span className="text-muted-foreground">in <b className="font-medium text-foreground">{r.in}</b></span>}
                  {r.repo && r.repo !== repo && <Badge variant="outline">{r.repo}</Badge>}
                  <span className="ml-auto">{r.confirmed ? <Badge variant="ok">call</Badge> : <Badge variant="outline">mention</Badge>}</span>
                </div>
                <div className="truncate font-mono text-[12px]">{r.text}</div>
              </div>
            );
          })}
        </div>
      </div>
    </Card>
  );
}

const verdictVariant = (v: string) => (v === "compatible" ? "ok" : v === "behavior_changed" ? "warn" : v === "shape_changed" ? "shape" : v === "absent" ? "outline" : "bad") as "ok" | "warn" | "shape" | "outline" | "bad";

function CompatView({ repo, refName, s }: { repo: string; refName: string; s: string }) {
  const c = useQuery({ queryKey: ["compat", repo, refName, s], queryFn: () => api<CompatResult>("compat", { repo, ref: refName, symbol: s }) });
  if (c.isLoading) return <Rows />;
  if (c.error) return <Empty error title="Compat failed">{String(c.error)}</Empty>;
  const vs = c.data?.verdicts ?? [];
  if (!vs.length) return <Empty title="No other version to compare">{c.data?.note || "Index another ref of this repo to compare."}</Empty>;
  return (
    <Card className="overflow-hidden">
      <CardHeader title="This symbol in every indexed version" sub={`Compared with ${c.data!.origin.ref}`} />
      <table className="w-full text-sm">
        <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
          <tr><th className="px-4 py-2 font-medium">Version</th><th className="px-4 py-2 font-medium">Verdict</th><th className="px-4 py-2 font-medium">Confidence</th><th className="px-4 py-2 font-medium">Why</th></tr>
        </thead>
        <tbody className="divide-y">
          {vs.map((v) => (
            <tr key={v.repo + v.ref} className="align-top">
              <td className="px-4 py-2.5 font-mono text-xs">{v.ref}</td>
              <td className="px-4 py-2.5"><Badge variant={verdictVariant(v.verdict)}>{v.verdict.replace(/_/g, " ")}</Badge></td>
              <td className="px-4 py-2.5"><Confidence value={v.confidence} /></td>
              <td className={cn("px-4 py-2.5 text-xs text-muted-foreground")}>
                {v.detail}
                {v.changed_callees?.map((x) => <div key={x} className="mt-1 truncate font-mono text-[11px]">{x}</div>)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Card>
  );
}

function SourceView({ repo, refName, t }: { repo: string; refName: string; t: NonNullable<Brief["target"]> }) {
  const [whole, setWhole] = useState(false);
  const file = useQuery({ queryKey: ["file", repo, refName, t.path], queryFn: () => api<{ path: string; content: string }>("file", { repo, ref: refName, path: t.path }), enabled: whole });
  const mark: [number, number] | undefined = t.start_line ? [t.start_line, t.end_line ?? t.start_line] : undefined;
  return (
    <Card className="overflow-hidden">
      <CardHeader
        title={<span className="font-mono text-xs">{t.path}</span>}
        sub={whole ? "whole file, the symbol highlighted" : `lines ${t.start_line}–${t.end_line}`}
        right={
          <div className="flex rounded-md bg-muted p-0.5 text-xs">
            {[false, true].map((w) => (
              <button key={String(w)} onClick={() => setWhole(w)} className={cn("rounded px-2.5 py-1 font-medium", whole === w ? "bg-card shadow-sm" : "text-muted-foreground")}>
                {w ? "Whole file" : "Symbol"}
              </button>
            ))}
          </div>
        }
      />
      <Suspense fallback={<Skeleton className="m-4 h-64" />}>
        {!whole ? (
          t.body ? <CodeView code={t.body} path={t.path ?? ""} firstLine={t.start_line} /> : <div className="p-6 text-sm text-muted-foreground">No body stored; open the whole file.</div>
        ) : file.isLoading ? (
          <Skeleton className="m-4 h-64" />
        ) : file.error ? (
          <div className="p-6 text-sm text-bad">{String(file.error)}</div>
        ) : (
          <CodeView key="whole" code={file.data!.content} path={t.path ?? ""} mark={mark} />
        )}
      </Suspense>
    </Card>
  );
}
