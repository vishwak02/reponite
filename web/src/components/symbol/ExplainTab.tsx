// The symbol hub's default tab: what the symbol is (signature, doc), who
// calls it and what it calls with how sure the index is of each edge, what
// reaches it at runtime, the tests that reach it, and the start of its body.
import { Link } from "react-router-dom";
import { ArrowDownRight, ArrowRight, ArrowUpLeft, Code2, FlaskConical, Workflow } from "lucide-react";
import type { ReactNode } from "react";
import type { ExplainEdge } from "@/lib/api";
import { symbolHref } from "@/lib/routes";
import { shortName } from "@/lib/utils";
import { Loc, Snippet } from "@/components/evidence/Evidence";
import { ProvenanceBadge } from "@/components/ui/provenance";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Empty, Rows } from "@/components/ui/skeleton";
import { useSymbolExplain, type TabProps } from "./data";

const PREVIEW_LINES = 18;

export default function ExplainTab({ repo, refName, s, setTab }: TabProps & { setTab: (t: string) => void }) {
  const ex = useSymbolExplain(repo, refName, s);
  if (ex.isLoading) return <Rows />;
  if (ex.error) return <Empty error title="Could not explain the symbol">{String(ex.error)}</Empty>;
  const r = ex.data!;
  const sym = r.symbol;
  if (!sym)
    return (
      <Empty title={r.kind === "ambiguous" ? `“${s}” names several symbols` : `No symbol “${s}” in ${repo}`}>
        {r.note}
        {r.candidates?.map((c) => (
          <div key={c.repo} className="mt-2 flex flex-wrap justify-center gap-1.5">
            {c.symbols.map((x) => (
              <Link key={x} to={symbolHref(c.repo, refName, x)} className="rounded-md border px-2 py-0.5 font-mono text-[11px] hover:text-primary">
                {x}
              </Link>
            ))}
          </div>
        ))}
      </Empty>
    );
  const body = (sym.body ?? "").split("\n");
  return (
    <div className="space-y-4">
      {r._meta?.warnings?.map((w) => (
        <div key={w} className="rounded-lg border border-warn/30 bg-warn/10 px-3 py-2 text-sm text-warn">
          {w}
        </div>
      ))}
      <Card className="p-4">
        <div className="flex flex-wrap items-center gap-1.5">
          {sym.kind && <Badge variant="outline">{sym.kind}</Badge>}
          {sym.lang && <Badge variant="outline">{sym.lang}</Badge>}
          {sym.is_test && <Badge variant="warn">test</Badge>}
          {sym.recursive && <Badge variant="shape">recursive</Badge>}
          {sym.decorators?.map((d) => (
            <Badge key={d} variant="default">
              {d}
            </Badge>
          ))}
        </div>
        {sym.signature && <pre className="mt-3 overflow-x-auto whitespace-pre-wrap break-words font-mono text-[13px] leading-relaxed">{sym.signature}</pre>}
        {sym.doc && <p className="mt-2 max-w-3xl whitespace-pre-line text-sm text-muted-foreground">{sym.doc}</p>}
        <div className="mt-4 grid grid-cols-2 gap-2 sm:grid-cols-4">
          <Count label="Callers" n={(sym.callers?.length ?? 0) + (sym.more_callers ?? 0)} onClick={() => setTab("calls")} />
          <Count label="Callees" n={(sym.callees?.length ?? 0) + (sym.more_callees ?? 0)} onClick={() => setTab("calls")} />
          <Count label="Tests reaching it" n={sym.tests?.length ?? 0} onClick={() => setTab("tests")} />
          <Count label="Runtime entries" n={sym.reached_by?.length ?? 0} />
        </div>
      </Card>

      <div className="grid gap-4 lg:grid-cols-2">
        <EdgeCard title="Called by" icon={<ArrowUpLeft className="size-4 text-shape" />} edges={sym.callers} more={sym.more_callers} repo={repo} refName={refName} empty="Nothing in the index calls it." onAll={() => setTab("calls")} />
        <EdgeCard title="Calls" icon={<ArrowDownRight className="size-4 text-primary" />} edges={sym.callees} more={sym.more_callees} repo={repo} refName={refName} empty="It calls nothing in the index." onAll={() => setTab("calls")} />
      </div>

      {!!sym.reached_by?.length && (
        <Card>
          <CardHeader title={<span className="flex items-center gap-1.5"><Workflow className="size-4 text-primary" />Reached at runtime by</span>} sub="paths the call graph cannot see: routes, topics, UI events" />
          <div className="divide-y">
            {sym.reached_by.map((x, i) => (
              <div key={i} className="flex flex-wrap items-center gap-2 px-4 py-2">
                <Badge variant="outline">{x.kind}</Badge>
                <span className="text-sm font-medium">{x.detail}</span>
                <span className="ml-auto"><Loc repo={x.repo} path={x.path} line={x.line} refName={refName} /></span>
              </div>
            ))}
          </div>
        </Card>
      )}

      <Card>
        <CardHeader
          title={<span className="flex items-center gap-1.5"><FlaskConical className="size-4 text-warn" />Tests that reach it</span>}
          right={<button onClick={() => setTab("tests")} className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline">Run commands <ArrowRight className="size-3" /></button>}
        />
        <div className="flex flex-wrap gap-1.5 p-4">
          {sym.tests?.length ? (
            sym.tests.map((t) => (
              <Link key={t.repo + t.path + t.line} to={symbolHref(t.repo, refName, t.in)} title={`${t.path}:${t.line}`} className="rounded-md border px-2 py-1 font-mono text-xs hover:border-primary/50 hover:text-primary">
                {shortName(t.in)}
              </Link>
            ))
          ) : (
            <span className="text-sm text-muted-foreground">No test reaches it.</span>
          )}
        </div>
      </Card>

      {sym.body && (
        <Card className="overflow-hidden">
          <CardHeader
            title={<Loc repo={repo} path={sym.path} line={sym.start_line} />}
            sub={`lines ${sym.start_line}–${sym.end_line}`}
            right={<button onClick={() => setTab("code")} className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"><Code2 className="size-3.5" />Open in Code</button>}
          />
          <Snippet text={body.slice(0, PREVIEW_LINES).join("\n") + (body.length > PREVIEW_LINES || sym.body_truncated ? "\n…" : "")} line={sym.start_line} className="m-3 bg-muted/40" />
        </Card>
      )}
    </div>
  );
}

function Count({ label, n, onClick }: { label: string; n: number; onClick?: () => void }) {
  const inner = (
    <>
      <div className="text-lg font-semibold tabular-nums">{n.toLocaleString()}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </>
  );
  return onClick ? (
    <button onClick={onClick} className="rounded-lg border bg-background/50 px-3 py-2 text-left transition-colors hover:border-primary/40">
      {inner}
    </button>
  ) : (
    <div className="rounded-lg border bg-background/50 px-3 py-2">{inner}</div>
  );
}

export function EdgeRow({ e, repo, refName }: { e: ExplainEdge; repo: string; refName: string }) {
  const r = e.repo || repo;
  return (
    <div className="flex min-w-0 items-center gap-2 px-4 py-2">
      <div className="min-w-0 flex-1">
        <div className="flex min-w-0 items-center gap-2">
          {e.external ? (
            <span className="truncate font-mono text-[13px] text-muted-foreground">{e.symbol}</span>
          ) : (
            <Link to={symbolHref(r, refName, e.symbol)} className="truncate font-mono text-[13px] font-medium hover:text-primary hover:underline">
              {shortName(e.symbol)}
            </Link>
          )}
          {e.is_test && <Badge variant="warn">test</Badge>}
          {e.repo && e.repo !== repo && <Badge variant="outline">{e.repo}</Badge>}
        </div>
        {e.path && <Loc repo={r} path={e.path} line={e.line} symbol={e.external ? undefined : e.symbol} refName={refName} />}
      </div>
      {(e.resolution_method || e.confidence != null) && <ProvenanceBadge method={e.resolution_method} confidence={e.confidence} />}
    </div>
  );
}

function EdgeCard({ title, icon, edges, more, repo, refName, empty, onAll }: { title: string; icon: ReactNode; edges?: ExplainEdge[]; more?: number; repo: string; refName: string; empty: string; onAll: () => void }) {
  const shown = edges?.slice(0, 6) ?? [];
  const rest = (edges?.length ?? 0) - shown.length + (more ?? 0);
  return (
    <Card>
      <CardHeader title={<span className="flex items-center gap-1.5">{icon}{title}</span>} right={<Badge variant="outline">{(edges?.length ?? 0) + (more ?? 0)}</Badge>} />
      <div className="divide-y">
        {!shown.length && <div className="px-4 py-6 text-center text-sm text-muted-foreground">{empty}</div>}
        {shown.map((e) => (
          <EdgeRow key={e.symbol + (e.line ?? "")} e={e} repo={repo} refName={refName} />
        ))}
        {rest > 0 && (
          <button onClick={onAll} className="flex w-full items-center gap-1 px-4 py-2 text-xs font-medium text-primary hover:bg-muted/40">
            {rest} more <ArrowRight className="size-3" />
          </button>
        )}
      </div>
    </Card>
  );
}

