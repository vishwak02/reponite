// "Since last index": for each repo indexed at two or more refs, what changed
// between its two newest (lib/refs.ts) — symbols added, removed, reshaped or
// behaving differently — from /api/diff. Loaded after the home's first paint.
import { Link } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import { ArrowRight, GitCompareArrows } from "lucide-react";
import { api, type DiffReport, type RepoOverview } from "@/lib/api";
import { latestPair } from "@/lib/refs";
import { symbolHref } from "@/lib/routes";
import { useScope } from "@/lib/scope";
import { fmt, shortName, tint } from "@/lib/utils";
import { Card, CardHeader } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";

// how much a change matters to a reader, most first
const ORDER = ["behavior_changed", "shape_changed", "removed", "added"];
const VARIANT: Record<string, "warn" | "shape" | "bad" | "ok"> = { behavior_changed: "warn", shape_changed: "shape", removed: "bad", added: "ok" };
const DOT: Record<string, string> = { behavior_changed: "bg-warn", shape_changed: "bg-shape", removed: "bg-bad", added: "bg-ok" };
const MAX_REPOS = 8;

export default function SinceIndex() {
  const { overview } = useScope();
  const size = (o: RepoOverview) => Math.max(0, ...o.refs.map((r) => r.symbols ?? 0));
  const pairs = overview
    .map((o) => ({ repo: o.repo, size: size(o), pair: latestPair(o.refs.map((r) => r.ref)) }))
    .filter((x): x is { repo: string; size: number; pair: [string, string] } => !!x.pair)
    .sort((a, b) => b.size - a.size);
  return (
    <Card data-since-index>
      <CardHeader
        title={
          <span className="flex items-center gap-1.5">
            <GitCompareArrows className="size-4 text-primary" />
            Since last index
          </span>
        }
        sub="Per repo: its newest ref against the one before it (working ref, then versions by number)"
        right={pairs.length > 0 && <Badge variant="outline">{pairs.length} repos</Badge>}
      />
      {pairs.length === 0 ? (
        <div className="px-4 py-5 text-sm text-muted-foreground">
          Every repo is indexed at one ref. Index another (a release tag, the main branch) to see which symbols changed between them.
        </div>
      ) : (
        <div className="divide-y">
          {pairs.slice(0, MAX_REPOS).map((p) => (
            <RepoChanges key={p.repo} repo={p.repo} from={p.pair[0]} to={p.pair[1]} />
          ))}
          {pairs.length > MAX_REPOS && <div className="px-4 py-2 text-xs text-muted-foreground">{pairs.length - MAX_REPOS} more repos: open Diff & impact.</div>}
        </div>
      )}
    </Card>
  );
}

function RepoChanges({ repo, from, to }: { repo: string; from: string; to: string }) {
  const d = useQuery({
    queryKey: ["diff", repo, from, to, true],
    queryFn: ({ signal }) => api<DiffReport>("diff", { repo, from, to, changed_only: true }, signal),
    staleTime: 5 * 60_000,
  });
  const changes = (d.data?.changes ?? []).filter((c) => c.change !== "unchanged");
  const counts = new Map<string, number>();
  for (const c of changes) counts.set(c.change, (counts.get(c.change) ?? 0) + 1);
  const rank = (c: string) => (ORDER.indexOf(c) < 0 ? ORDER.length : ORDER.indexOf(c));
  const top = [...changes].sort((a, b) => rank(a.change) - rank(b.change) || a.name.localeCompare(b.name)).slice(0, 6);
  return (
    <div className="px-4 py-3">
      <div className="flex flex-wrap items-center gap-2">
        <span className="size-2 rounded-full" style={{ background: tint(repo) }} />
        <span className="text-sm font-medium">{repo}</span>
        <span className="font-mono text-[11px] text-muted-foreground">
          {from} → {to}
        </span>
        <div className="ml-auto flex flex-wrap gap-1">
          {d.isLoading && <Skeleton className="h-5 w-40" />}
          {[...counts.entries()]
            .sort((a, b) => rank(a[0]) - rank(b[0]))
            .map(([k, n]) => (
              <Badge key={k} variant={VARIANT[k] ?? "outline"}>
                {fmt(n)} {k.replace(/_/g, " ")}
              </Badge>
            ))}
          {d.data && !changes.length && <span className="text-xs text-muted-foreground">no symbol changed</span>}
          {d.error && <span className="text-xs text-bad">{String(d.error)}</span>}
        </div>
      </div>
      {top.length > 0 && (
        <div className="mt-2 flex flex-wrap gap-1.5">
          {top.map((c) => (
            <Link
              key={c.name}
              to={symbolHref(repo, to, c.name, c.change === "removed" ? undefined : "history")}
              title={`${c.name}: ${c.change.replace(/_/g, " ")}`}
              className="inline-flex max-w-[260px] items-center gap-1.5 rounded-md border bg-card px-2 py-0.5 text-xs hover:border-primary/40 hover:text-primary"
            >
              <span className={`size-1.5 shrink-0 rounded-full ${DOT[c.change] ?? "bg-muted-foreground"}`} />
              <span className="truncate font-mono">{shortName(c.name)}</span>
            </Link>
          ))}
          {changes.length > top.length && (
            <Link to="/diff" className="inline-flex items-center gap-1 px-1 text-xs text-primary hover:underline">
              all {fmt(changes.length)} <ArrowRight className="size-3" />
            </Link>
          )}
        </div>
      )}
    </div>
  );
}
