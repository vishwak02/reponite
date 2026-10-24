// The symbol's source (lazy CodeMirror), its body or the whole file with the
// body marked, and a gutter dot at each call site coloured by how sure the
// index is of that call (lib/callsites.ts).
import { lazy, Suspense, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "@/lib/api";
import { callSites } from "@/lib/callsites";
import { TIER_LABEL, TIERS } from "@/lib/provenance";
import { cn } from "@/lib/utils";
import { Card, CardHeader } from "@/components/ui/card";
import { Empty, Rows, Skeleton } from "@/components/ui/skeleton";
import { useSymbolExplain, type TabProps } from "./data";

const CodeView = lazy(() => import("@/components/code/CodeView"));

export default function CodeTab({ repo, refName, s }: TabProps) {
  const ex = useSymbolExplain(repo, refName, s);
  const [whole, setWhole] = useState(false);
  const t = ex.data?.symbol;
  const file = useQuery({
    queryKey: ["file", repo, refName, t?.path],
    queryFn: ({ signal }) => api<{ path: string; content: string }>("file", { repo, ref: refName, path: t!.path }, signal),
    enabled: whole && !!t?.path,
  });
  const text = whole ? file.data?.content : t?.body;
  const first = whole ? 1 : (t?.start_line ?? 1);
  const sites = useMemo(() => (text && t ? callSites(text, first, t.callees ?? [], t.start_line, t.end_line) : []), [text, first, t]);
  if (ex.isLoading) return <Rows />;
  if (ex.error) return <Empty error title="Could not load the symbol">{String(ex.error)}</Empty>;
  if (!t?.path) return <Empty title="No source stored for this symbol" />;
  const mark: [number, number] | undefined = t.start_line ? [t.start_line, t.end_line ?? t.start_line] : undefined;
  return (
    <Card className="overflow-hidden">
      <CardHeader
        title={<span className="font-mono text-xs">{t.path}</span>}
        sub={
          <span className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <span>{whole ? "whole file, the symbol highlighted" : `lines ${t.start_line}–${t.end_line}`}</span>
            {sites.length > 0 && (
              <span className="flex items-center gap-2" data-legend>
                {sites.length} call sites:
                {TIERS.filter((k) => sites.some((x) => x.tier === k)).map((k) => (
                  <span key={k} className="inline-flex items-center gap-1">
                    <span className={`cm-callsite cm-callsite-${k}`} />
                    {TIER_LABEL[k]}
                  </span>
                ))}
              </span>
            )}
          </span>
        }
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
          t.body ? <CodeView code={t.body} path={t.path} firstLine={t.start_line} sites={sites} /> : <div className="p-6 text-sm text-muted-foreground">No body stored; open the whole file.</div>
        ) : file.isLoading ? (
          <Skeleton className="m-4 h-64" />
        ) : file.error ? (
          <div className="p-6 text-sm text-bad">{String(file.error)}</div>
        ) : (
          <CodeView key="whole" code={file.data!.content} path={t.path} mark={mark} sites={sites} />
        )}
      </Suspense>
    </Card>
  );
}
