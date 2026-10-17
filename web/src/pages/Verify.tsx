// Check an edit before making it: paste or edit a file's new content, and see
// which symbols it adds, changes or removes, and what in the fleet would break.
import { Suspense, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { FileCode2, Loader2, ShieldAlert, ShieldCheck } from "lucide-react";
import { api, verifyEdit, type VerifyResult } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { shortName } from "@/lib/utils";
import CodeView from "@/components/code/CodeView";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardHeader } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Empty, Skeleton } from "@/components/ui/skeleton";


export default function Verify() {
  const { repo, ref } = useScope();
  const [path, setPath] = useState("");
  const [loaded, setLoaded] = useState("");
  const [text, setText] = useState("");
  const file = useQuery({
    queryKey: ["file", repo, ref, loaded],
    queryFn: async () => {
      const f = await api<{ path: string; content: string }>("file", { repo, ref, path: loaded });
      setText(f.content);
      return f;
    },
    enabled: !!loaded,
    staleTime: Infinity,
  });
  const run = useMutation({ mutationFn: () => verifyEdit(loaded, repo, ref, text) });
  return (
    <Page title="Verify an edit" sub={<>Load a file of <b>{repo}</b> at <code className="font-mono text-xs">{ref}</code>, edit it here, and check what the change would break before you make it.</>}>
      <form
        className="mb-4 flex max-w-3xl gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          run.reset();
          setLoaded(path.trim());
        }}
      >
        <div className="relative flex-1">
          <FileCode2 className="absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
          <Input className="h-10 pl-9 font-mono text-[13px]" placeholder="path/in/repo.cpp" value={path} onChange={(e) => setPath(e.target.value)} />
        </div>
        <Button type="submit" variant="outline" className="h-10">Load file</Button>
      </form>
      {!loaded ? (
        <Empty title="Load a file to start">The path as stored in the index, relative to the repo root (Grep or a symbol's Code tab shows it).</Empty>
      ) : file.isLoading ? (
        <Skeleton className="h-96" />
      ) : file.error ? (
        <Empty error title="Could not load the file">{String(file.error)}</Empty>
      ) : (
        <div className="grid gap-4 xl:grid-cols-[1fr_400px]">
          <Card className="overflow-hidden">
            <CardHeader
              title={<span className="font-mono text-xs">{loaded}</span>}
              sub={text === file.data?.content ? "unchanged" : "edited"}
              right={
                <div className="flex gap-2">
                  <Button size="sm" variant="ghost" disabled={text === file.data?.content} onClick={() => setText(file.data!.content)}>Reset</Button>
                  <Button size="sm" disabled={run.isPending} onClick={() => run.mutate()}>
                    {run.isPending && <Loader2 className="animate-spin" />}Verify
                  </Button>
                </div>
              }
            />
            <Suspense fallback={<Skeleton className="m-4 h-96" />}>
              <CodeView key={loaded} code={text} path={loaded} editable onChange={setText} maxHeight="68vh" />
            </Suspense>
          </Card>
          <div>{run.error ? <Empty error title="Verify failed">{String(run.error)}</Empty> : run.data ? <Result r={run.data} repo={repo} refName={ref} /> : <Empty title="Edit, then Verify">Nothing is written anywhere; the check runs against the index.</Empty>}</div>
        </div>
      )}
    </Page>
  );
}

function Result({ r, repo, refName }: { r: VerifyResult; repo: string; refName: string }) {
  const list = (title: string, xs: string[] | undefined, v: "ok" | "warn" | "bad") =>
    !!xs?.length && (
      <div>
        <div className="mb-1 text-xs font-semibold text-muted-foreground">{title}</div>
        <div className="flex flex-wrap gap-1">{xs.map((x) => <Badge key={x} variant={v} title={x}>{shortName(x)}</Badge>)}</div>
      </div>
    );
  return (
    <Card>
      <div className={`flex items-center gap-2 border-b px-4 py-3 ${r.safe ? "text-ok" : "text-bad"}`}>
        {r.safe ? <ShieldCheck className="size-5" /> : <ShieldAlert className="size-5" />}
        <span className="font-semibold">{r.safe ? "Safe: nothing else breaks" : "This edit breaks callers"}</span>
      </div>
      <div className="space-y-3 p-4">
        {list("Added", r.added, "ok")}
        {list("Changed", r.changed, "warn")}
        {list("Removed", r.removed, "bad")}
        {!r.added?.length && !r.changed?.length && !r.removed?.length && <div className="text-sm text-muted-foreground">No symbol changed.</div>}
        {r.impacts?.map((im) => (
          <div key={im.symbol} className="rounded-lg border">
            <div className="flex items-center gap-2 border-b px-3 py-2 text-sm">
              <Link to={symbolHref(repo, refName, im.symbol)} className="font-medium hover:text-primary">{shortName(im.symbol)}</Link>
              <Badge variant="bad">{im.kind}</Badge>
              <span className="ml-auto text-xs text-muted-foreground">{im.breaks?.length ?? 0} site(s)</span>
            </div>
            {im.breaks?.map((b, i) => (
              <div key={i} className="px-3 py-1.5 text-xs">
                <div className="font-mono text-muted-foreground">{b.repo ? b.repo + " · " : ""}{b.path}:{b.line}{b.in ? ` in ${b.in}` : ""}</div>
                <div className="truncate font-mono">{b.text}</div>
              </div>
            ))}
          </div>
        ))}
        {r.note && <p className="text-xs text-muted-foreground">{r.note}</p>}
      </div>
    </Card>
  );
}
