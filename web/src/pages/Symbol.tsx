// The symbol hub: everything reponite knows about one symbol, one tab per
// question, each tab its own lazily loaded chunk and its own URL (?tab=).
import { lazy, Suspense, useState, type ComponentType } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowLeftRight, Braces, Code2, FileCode2, FlaskConical, GitBranch, Layers, ListTree, Network, Search } from "lucide-react";
import { searchSymbols } from "@/lib/api";
import { useSymbolParams } from "@/lib/symbolParams";
import { shortName, tint } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/lib/routes";
import { Badge } from "@/components/ui/badge";
import { Input } from "@/components/ui/input";
import { Empty, Rows, Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useSymbolExplain, type TabProps } from "@/components/symbol/data";

const CallGraph = lazy(() => import("@/components/graph/CallGraph"));

// the tabs, in order; overview and compat are the earlier names of explain
// and history, kept so old links still open the right tab
export const HUB_TABS: { id: string; label: string; icon: ComponentType<{ className?: string }>; load: () => Promise<{ default: ComponentType<TabProps & { setTab: (t: string) => void }> }> }[] = [
  { id: "explain", label: "Explain", icon: ListTree, load: () => import("@/components/symbol/ExplainTab") },
  { id: "calls", label: "Callers & callees", icon: ArrowLeftRight, load: () => import("@/components/symbol/CallsTab") },
  { id: "usages", label: "Usages", icon: Search, load: () => import("@/components/symbol/UsagesTab") },
  { id: "impls", label: "Implementations", icon: Layers, load: () => import("@/components/symbol/ImplsTab") },
  { id: "tests", label: "Tests", icon: FlaskConical, load: () => import("@/components/symbol/TestsTab") },
  { id: "history", label: "History", icon: GitBranch, load: () => import("@/components/symbol/HistoryTab") },
  { id: "code", label: "Code", icon: Code2, load: () => import("@/components/symbol/CodeTab") },
];
const ALIAS: Record<string, string> = { overview: "explain", compat: "history" };
const LAZY = Object.fromEntries(HUB_TABS.map((t) => [t.id, lazy(t.load)]));

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

function SymbolHub({ repo, refName, s, tab: asked, setTab }: { repo: string; refName: string; s: string; tab: string; setTab: (t: string) => void }) {
  const ex = useSymbolExplain(repo, refName, s);
  const t = ex.data?.symbol;
  const name = shortName(s);
  const tab = ALIAS[asked] ?? (asked === "graph" || LAZY[asked] ? asked : "explain");
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
          <code className="font-mono text-xs">{ex.data?.ref || refName}</code>
          {t?.path && (
            <span className="flex items-center gap-1 font-mono text-xs">
              <FileCode2 className="size-3.5" />
              {t.path}:{t.start_line}
              {t.end_line && t.end_line !== t.start_line ? `–${t.end_line}` : ""}
            </span>
          )}
          {t?.kind && <Badge variant="outline">{t.kind}</Badge>}
          {t?.exported && <Badge variant="ok">exported</Badge>}
        </span>
      }
    >
      <div className="mb-1 truncate font-mono text-xs text-muted-foreground" title={s}>
        {s}
      </div>
      <Tabs value={tab} onValueChange={setTab}>
        <div className="-mx-1 overflow-x-auto px-1 pb-1">
          <TabsList>
            {HUB_TABS.map((x) => (
              <TabsTrigger key={x.id} value={x.id} onPointerEnter={() => x.load()}>
                <x.icon />
                {x.label}
              </TabsTrigger>
            ))}
            <TabsTrigger value="graph"><Network />Call graph</TabsTrigger>
          </TabsList>
        </div>
        {HUB_TABS.map((x) => {
          const Tab = LAZY[x.id];
          return (
            <TabsContent key={x.id} value={x.id}>
              {tab === x.id && (
                <Suspense fallback={<Rows />}>
                  <Tab repo={repo} refName={refName} s={s} setTab={setTab} />
                </Suspense>
              )}
            </TabsContent>
          );
        })}
        <TabsContent value="graph">
          <Suspense fallback={<Skeleton className="h-[70vh]" />}>
            <CallGraph repo={repo} refName={refName} symbol={s} />
          </Suspense>
        </TabsContent>
      </Tabs>
    </Page>
  );
}
