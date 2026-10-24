// Why the symbol is the way it is: the commits that shaped its lines (from
// the repo's git history, when the server can read it) and what it is in
// every other indexed version (compat).
import { useQuery } from "@tanstack/react-query";
import { GitCommitHorizontal } from "lucide-react";
import { api, isSuggestion, type CompatResult, type Suggestion, type WhyResult } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Badge, Confidence } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Empty, Rows } from "@/components/ui/skeleton";
import type { TabProps } from "./data";

const verdictVariant = (v?: string) => (v === "compatible" ? "ok" : v === "behavior_changed" ? "warn" : v === "shape_changed" ? "shape" : v === "absent" || !v ? "outline" : "bad") as "ok" | "warn" | "shape" | "outline" | "bad";

export default function HistoryTab(p: TabProps) {
  return (
    <div className="space-y-4">
      <Commits {...p} />
      <Versions {...p} />
    </div>
  );
}

function Commits({ repo, refName, s }: TabProps) {
  const w = useQuery({ queryKey: ["why", repo, refName, s], queryFn: ({ signal }) => api<WhyResult | Suggestion>("v2/why", { repo, ref: refName, symbol: s }, signal) });
  if (w.isLoading) return <Rows n={4} />;
  if (w.error) return <Empty error title="History failed">{String(w.error)}</Empty>;
  if (isSuggestion(w.data)) return <Empty title={`No symbol ${s} in ${repo}`} />;
  const d = w.data as WhyResult;
  return (
    <Card className="overflow-hidden">
      <CardHeader title={<span className="flex items-center gap-1.5"><GitCommitHorizontal className="size-4 text-primary" />Commits that shaped it</span>} sub={d.path ? `${d.path}${d.lines ? ":" + d.lines : ""}` : undefined} right={<Badge variant="outline">{d.total}</Badge>} />
      {!d.commits.length ? (
        <div className="px-4 py-5 text-sm text-muted-foreground">{d.note || "No commit history for this symbol."}</div>
      ) : (
        <ol className="relative px-4 py-3">
          {d.commits.map((c, i) => (
            <li key={c.commit} className="relative flex gap-3 pb-4 last:pb-0">
              <div className="flex flex-col items-center">
                <span className={cn("mt-1 size-2.5 rounded-full border-2", c.kind === "introduced" ? "border-ok bg-ok/30" : "border-primary bg-card")} />
                {i < d.commits.length - 1 && <span className="mt-1 w-px flex-1 bg-border" />}
              </div>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-2">
                  <span className="text-sm font-medium">{c.subject}</span>
                  {c.prs?.map((pr) => <Badge key={pr} variant="default">{pr}</Badge>)}
                  {c.tickets?.map((t) => <Badge key={t} variant="outline">{t}</Badge>)}
                </div>
                <div className="mt-0.5 flex flex-wrap items-center gap-x-2 text-xs text-muted-foreground">
                  <code className="font-mono">{c.commit.slice(0, 10)}</code>
                  <span>{c.author}</span>
                  <span>{c.date.slice(0, 10)}</span>
                  <span className="tabular-nums"><span className="text-ok">+{c.added}</span> <span className="text-bad">−{c.removed}</span></span>
                  {c.kind && <Badge variant="outline">{c.kind}</Badge>}
                  {c.first_in && <span>first in {c.first_in}</span>}
                </div>
              </div>
            </li>
          ))}
        </ol>
      )}
      {d.commits.length > 0 && d.note && <div className="border-t px-4 py-2 text-xs text-muted-foreground">{d.note}</div>}
    </Card>
  );
}

function Versions({ repo, refName, s }: TabProps) {
  const c = useQuery({ queryKey: ["compat", repo, refName, s], queryFn: ({ signal }) => api<CompatResult>("compat", { repo, ref: refName, symbol: s }, signal) });
  if (c.isLoading) return <Rows n={3} />;
  if (c.error) return <Empty error title="Compat failed">{String(c.error)}</Empty>;
  const vs = c.data?.verdicts ?? [];
  if (!vs.length) return <Empty title="No other version to compare">{c.data?.note || "Index another ref of this repo to compare."}</Empty>;
  return (
    <Card className="overflow-hidden">
      <CardHeader title="This symbol in every indexed version" sub={`Compared with ${c.data!.origin.ref}`} />
      <div className="overflow-x-auto">
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
                <td className="px-4 py-2.5 text-xs text-muted-foreground">
                  {v.detail}
                  {v.changed_callees?.map((x) => <div key={x} className="mt-1 truncate font-mono text-[11px]">{x}</div>)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Card>
  );
}
