// The tests that reach the symbol — through the call graph, through a request
// a test sends to the route serving it, or by naming it — each with the
// command that runs just it, and one command per framework for all of them.
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { FlaskConical, Terminal } from "lucide-react";
import { api, isSuggestion, type Suggestion, type TestsResult } from "@/lib/api";
import { symbolHref } from "@/lib/routes";
import { Loc } from "@/components/evidence/Evidence";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { CopyButton } from "@/components/ui/copy";
import { Empty, Rows } from "@/components/ui/skeleton";
import type { TabProps } from "./data";

const HOW: Record<string, string> = { call_graph: "calls it", http: "through its route", reference: "names it" };

export default function TestsTab({ repo, refName, s }: TabProps) {
  const t = useQuery({ queryKey: ["tests", repo, refName, s], queryFn: ({ signal }) => api<TestsResult | Suggestion>("v2/tests", { repo, ref: refName, symbol: s }, signal) });
  if (t.isLoading) return <Rows />;
  if (t.error) return <Empty error title="Tests failed">{String(t.error)}</Empty>;
  if (isSuggestion(t.data)) return <Empty title={`No symbol ${s} in ${repo}`} />;
  const d = t.data as TestsResult;
  if (!d.tests.length) return <Empty title="No test reaches it">{d.note}</Empty>;
  return (
    <div className="space-y-4">
      <Card>
        <CardHeader title={<span className="flex items-center gap-1.5"><Terminal className="size-4 text-primary" />Run them</span>} sub="one command per repo and framework" />
        <div className="divide-y">
          {d.commands.map((c) => (
            <div key={c.repo + c.framework + c.command} className="flex items-center gap-3 px-4 py-2.5">
              <Badge variant="outline">{c.framework}</Badge>
              <code data-command className="min-w-0 flex-1 overflow-x-auto whitespace-nowrap rounded-md bg-muted/70 px-2.5 py-1.5 font-mono text-xs">{c.command}</code>
              <span className="shrink-0 text-xs tabular-nums text-muted-foreground">{c.tests} tests</span>
              <CopyButton text={c.command} label="Copy command" />
            </div>
          ))}
        </div>
      </Card>
      <Card className="overflow-hidden">
        <CardHeader title={`${d.total} tests reach ${d.symbol.split(".").pop()}`} sub={d.note} right={d.truncated ? <Badge variant="warn">truncated</Badge> : undefined} />
        <div className="divide-y">
          {d.tests.map((x) => (
            <div key={x.repo + x.path + x.line + x.name} className="flex min-w-0 items-center gap-3 px-4 py-2.5">
              <FlaskConical className="size-4 shrink-0 text-warn" />
              <div className="min-w-0 flex-1">
                <div className="flex min-w-0 items-center gap-2">
                  {x.handle ? (
                    <Link to={symbolHref(x.repo, refName, x.handle)} className="truncate font-mono text-[13px] font-medium hover:text-primary hover:underline">
                      {x.name}
                    </Link>
                  ) : (
                    <span className="truncate font-mono text-[13px] font-medium">{x.name}</span>
                  )}
                  <Badge variant={x.how === "call_graph" ? "ok" : x.how === "http" ? "default" : "outline"} title={x.via?.length ? "via " + x.via.join(" → ") : undefined}>
                    {HOW[x.how] ?? x.how}
                    {x.depth > 1 ? ` · ${x.depth} hops` : ""}
                  </Badge>
                  {x.repo !== repo && <Badge variant="outline">{x.repo}</Badge>}
                </div>
                <Loc repo={x.repo} path={x.path} line={x.line} symbol={x.handle} refName={refName} />
              </div>
              <CopyButton text={x.command} label={`Copy: ${x.command}`} />
            </div>
          ))}
        </div>
      </Card>
    </div>
  );
}
