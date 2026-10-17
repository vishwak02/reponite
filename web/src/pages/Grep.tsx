import { useEffect, useMemo, useState } from "react";
import { useInfiniteQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { FileCode2, Loader2, Regex, Search } from "lucide-react";
import { api } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { cn, fmt } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Rows } from "@/components/ui/skeleton";

interface Match { repo: string; path: string; line: number; text: string; symbol?: string }
interface GrepPage { matches?: Match[]; total: number; truncated: boolean; scanned?: number }
const PAGE = 200;

function highlight(text: string, pat: string, fixed: boolean) {
  if (!pat) return text;
  let re: RegExp;
  try {
    re = new RegExp(fixed ? pat.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") : pat, "g");
  } catch {
    return text;
  }
  const out: React.ReactNode[] = [];
  let last = 0;
  for (const m of text.matchAll(re)) {
    if (!m[0]) break;
    out.push(text.slice(last, m.index), <mark key={m.index} className="rounded-sm bg-warn/30 text-foreground">{m[0]}</mark>);
    last = m.index! + m[0].length;
  }
  out.push(text.slice(last));
  return out;
}

export default function Grep() {
  const { repo, ref } = useScope();
  const [input, setInput] = useState("");
  const [pat, setPat] = useState("");
  const [fixed, setFixed] = useState(true);
  useEffect(() => {
    const t = setTimeout(() => setPat(input.trim()), 300);
    return () => clearTimeout(t);
  }, [input]);
  const g = useInfiniteQuery({
    queryKey: ["grep", repo, ref, pat, fixed],
    queryFn: ({ pageParam }) => api<GrepPage>("grep", { repo, ref, pattern: pat, fixed, limit: PAGE, offset: pageParam }),
    initialPageParam: 0,
    getNextPageParam: (last, all) => (last.truncated ? all.length * PAGE : undefined),
    enabled: pat.length >= 2 && !!repo,
  });
  const matches = useMemo(() => g.data?.pages.flatMap((p) => p.matches ?? []) ?? [], [g.data]);
  const byFile = useMemo(() => {
    const m = new Map<string, Match[]>();
    for (const x of matches) m.set(x.path, [...(m.get(x.path) ?? []), x]);
    return [...m.entries()];
  }, [matches]);
  const total = g.data?.pages[0]?.total;
  return (
    <Page title="Grep" sub={<>Search the stored source of <b>{repo}</b> at <code className="font-mono text-xs">{ref}</code>.</>}>
      <div className="flex max-w-3xl items-center gap-2">
        <div className="relative flex-1">
          <Search className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input autoFocus className="h-10 pl-9 font-mono" placeholder={fixed ? "literal text" : "regular expression (RE2)"} value={input} onChange={(e) => setInput(e.target.value)} />
        </div>
        <Button variant={fixed ? "outline" : "default"} onClick={() => setFixed(!fixed)} title="Toggle regular expression">
          <Regex />
          {fixed ? "Literal" : "Regex"}
        </Button>
      </div>
      <div className="mt-4">
        {g.isLoading && <Rows />}
        {g.error && <Empty error title="Search failed">{String(g.error)}</Empty>}
        {g.data && !matches.length && <Empty title={`No match for “${pat}”`} />}
        {!!matches.length && (
          <div className="mb-2 text-xs text-muted-foreground">
            {fmt(total)} matches in {byFile.length}{g.hasNextPage ? "+" : ""} files
          </div>
        )}
        <div className="space-y-3">
          {byFile.map(([path, ms]) => (
            <Card key={path} className="overflow-hidden">
              <div className="flex items-center gap-2 border-b bg-muted/40 px-3 py-1.5 font-mono text-xs">
                <FileCode2 className="size-3.5 text-muted-foreground" />
                {path}
                <span className="ml-auto text-muted-foreground">{ms.length}</span>
              </div>
              {ms.map((m) => (
                <div key={m.line} className="flex gap-3 px-3 py-1 font-mono text-[12px] hover:bg-muted/40">
                  <span className="w-10 shrink-0 text-right text-muted-foreground">{m.line}</span>
                  <span className="min-w-0 flex-1 truncate whitespace-pre">{highlight(m.text, pat, fixed)}</span>
                  {m.symbol && <Link to={symbolHref(repo, ref, m.symbol)} className={cn("shrink-0 font-sans text-[11px] text-muted-foreground hover:text-primary")}>in {m.symbol}</Link>}
                </div>
              ))}
            </Card>
          ))}
        </div>
        {g.hasNextPage && (
          <Button variant="outline" className="mt-3" onClick={() => g.fetchNextPage()} disabled={g.isFetchingNextPage}>
            {g.isFetchingNextPage && <Loader2 className="animate-spin" />}Load more
          </Button>
        )}
      </div>
    </Page>
  );
}
