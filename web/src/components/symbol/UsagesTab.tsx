// Every mention of the symbol's name; confirmed ones are calls the call
// graph resolved to it. Virtualized: a common name runs to thousands.
import { useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { api, type UsagesResult } from "@/lib/api";
import { shortName } from "@/lib/utils";
import { Loc } from "@/components/evidence/Evidence";
import { Badge } from "@/components/ui/badge";
import { Card, CardHeader } from "@/components/ui/card";
import { Empty, Rows } from "@/components/ui/skeleton";
import type { TabProps } from "./data";

export default function UsagesTab({ repo, refName, s }: TabProps) {
  const u = useQuery({ queryKey: ["usages", repo, refName, s], queryFn: ({ signal }) => api<UsagesResult>("usages", { repo, ref: refName, symbol: shortName(s).split(".").pop() }, signal) });
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
                <div className="flex min-w-0 items-center gap-2 text-xs">
                  <Loc repo={r.repo || repo} path={r.path} line={r.line} refName={refName} />
                  {r.in && <span className="truncate text-muted-foreground">in <b className="font-medium text-foreground">{r.in}</b></span>}
                  {r.repo && r.repo !== repo && <Badge variant="outline">{r.repo}</Badge>}
                  <span className="ml-auto shrink-0">{r.confirmed ? <Badge variant="ok">call</Badge> : <Badge variant="outline">mention</Badge>}</span>
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
