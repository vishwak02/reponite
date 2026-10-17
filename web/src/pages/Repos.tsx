import { useMemo, useState } from "react";
import { useNavigate } from "react-router-dom";
import { ArrowDownUp, Search } from "lucide-react";
import { useScope } from "@/lib/scope";
import { cn, fmt, tint } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Rows } from "@/components/ui/skeleton";
import { useFleetMap } from "./Overview";

type Key = "repo" | "symbols" | "files" | "refs";

export default function Repos() {
  const { overview, setRepo, repo: current } = useScope();
  const fm = useFleetMap();
  const nav = useNavigate();
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<{ k: Key; desc: boolean }>({ k: "symbols", desc: true });
  const rows = useMemo(() => {
    const info = new Map((fm.data?.repos ?? []).map((r) => [r.repo, r]));
    return overview
      .map((o) => {
        const f = info.get(o.repo);
        return { repo: o.repo, module: o.module, summary: f?.summary, ref: f?.ref ?? o.refs[0]?.ref, symbols: f?.symbols ?? Math.max(0, ...o.refs.map((r) => r.symbols ?? 0)), files: f?.files ?? 0, refs: o.refs.length, langs: (f?.languages ?? []).slice(0, 3).map((l) => l.split(":")[0]) };
      })
      .filter((r) => !q || (r.repo + " " + (r.module ?? "") + " " + (r.summary ?? "")).toLowerCase().includes(q.toLowerCase()))
      .sort((a, b) => {
        const x = a[sort.k], y = b[sort.k];
        const c = typeof x === "number" ? x - (y as number) : String(x).localeCompare(String(y));
        return sort.desc ? -c : c;
      });
  }, [overview, fm.data, q, sort]);
  const H = ({ k, children, right }: { k: Key; children: React.ReactNode; right?: boolean }) => (
    <th className={cn("px-4 py-2 font-medium", right && "text-right")}>
      <button onClick={() => setSort({ k, desc: sort.k === k ? !sort.desc : k !== "repo" })} className={cn("inline-flex items-center gap-1 hover:text-foreground", sort.k === k && "text-foreground")}>
        {children}
        <ArrowDownUp className="size-3" />
      </button>
    </th>
  );
  return (
    <Page title="Repositories" sub={`${overview.length} indexed`} actions={<div className="relative w-64"><Search className="absolute left-2.5 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" /><Input className="h-8 pl-8" placeholder="Filter…" value={q} onChange={(e) => setQ(e.target.value)} /></div>}>
      <Card className="overflow-hidden">
        {!overview.length ? (
          <div className="p-4"><Rows n={10} /></div>
        ) : (
          <table className="w-full text-sm">
            <thead className="bg-muted/50 text-left text-xs text-muted-foreground">
              <tr><H k="repo">Repository</H><th className="px-4 py-2 font-medium">Pinned version</th><th className="px-4 py-2 font-medium">Languages</th><H k="symbols" right>Symbols</H><H k="files" right>Files</H><H k="refs" right>Versions</H></tr>
            </thead>
            <tbody className="divide-y">
              {rows.map((r) => (
                <tr key={r.repo} onClick={() => { setRepo(r.repo); nav("/symbol"); }} className={cn("cursor-pointer hover:bg-muted/50", r.repo === current && "bg-accent/40")}>
                  <td className="max-w-[420px] px-4 py-2.5">
                    <div className="flex items-center gap-2 font-medium"><span className="size-2 shrink-0 rounded-full" style={{ background: tint(r.repo) }} />{r.repo}</div>
                    {r.summary && <div className="truncate pl-4 text-xs text-muted-foreground">{r.summary}</div>}
                  </td>
                  <td className="px-4 py-2.5 font-mono text-xs text-muted-foreground">{r.ref}</td>
                  <td className="px-4 py-2.5"><div className="flex gap-1">{r.langs.map((l) => <span key={l} className="rounded border px-1.5 py-0.5 text-[11px]">{l}</span>)}</div></td>
                  <td className="px-4 py-2.5 text-right tabular-nums">{fmt(r.symbols)}</td>
                  <td className="px-4 py-2.5 text-right tabular-nums">{fmt(r.files)}</td>
                  <td className="px-4 py-2.5 text-right tabular-nums">{r.refs}</td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Card>
    </Page>
  );
}
