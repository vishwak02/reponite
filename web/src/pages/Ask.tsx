// Ask: the answer to what was typed in the Ask bar, as evidence — each card a
// place in the code (file:line), how sure the index is of it, and a link into
// the view that shows it. The intent router (lib/intent.ts) picks the
// analysis; ?as= overrides it, ?repo= scopes it (fleet-wide otherwise).
import { useEffect, type ReactNode } from "react";
import { Link, useNavigate, useSearchParams } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowRight,
  Braces,
  CircleAlert,
  FileCode2,
  FlaskConical,
  GitBranch,
  Globe,
  Layers,
  ScrollText,
  Search,
  Settings2,
  ShieldAlert,
  Sparkles,
  Workflow,
} from "lucide-react";
import {
  api,
  isSuggestion,
  searchSymbols,
  type BlastRadius,
  type ConfigResult,
  type ErrorsResult,
  type ExplainEdge,
  type ExplainResult,
  type ExplainSymbol,
  type ImplsResult,
  type InvestigateResult,
  type LogSitesResult,
  type SearchHit,
  type Suggestion,
} from "@/lib/api";
import { INTENT_LABEL, pickSymbol, routeIntent, type Intent, type IntentKind } from "@/lib/intent";
import { askHref, flowHref, symbolHref } from "@/lib/routes";
import { FLEET_REF, useScope } from "@/lib/scope";
import { cn, shortName } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { AskBar } from "@/components/ask/AskBar";
import { EvidenceCard, EvidenceGroup, QueryNote, RepoChip } from "@/components/evidence/Evidence";
import { ProvenanceBadge } from "@/components/ui/provenance";
import { ImplsList } from "@/components/symbol/ImplsTab";
import { Badge } from "@/components/ui/badge";
import { Empty, Skeleton } from "@/components/ui/skeleton";

const REF = FLEET_REF;

const ICON: Record<IntentKind, ReactNode> = {
  symbol: <Braces />,
  impact: <ShieldAlert />,
  callers: <ArrowRight />,
  tests: <FlaskConical />,
  impls: <Layers />,
  history: <GitBranch />,
  explain: <FileCode2 />,
  route: <Globe />,
  logline: <ScrollText />,
  config: <Settings2 />,
  errors: <CircleAlert />,
  investigate: <Sparkles />,
};

export default function AskPage() {
  const [sp] = useSearchParams();
  const q = sp.get("q") ?? "";
  const repo = sp.get("repo") ?? "";
  const as = sp.get("as") as IntentKind | null;
  const routed = routeIntent(q);
  const intent: Intent | null = routed && as && as in INTENT_LABEL ? { kind: as, arg: as === "investigate" ? q : routed.arg, method: routed.method } : routed;
  return (
    <Page title="Ask" sub="Answers from the index, each resting on the code it cites.">
      <AskBar key={q} initial={q} repo={repo || undefined} autoFocus={!q} examples={!q} className="max-w-3xl" />
      {intent && (
        <div className="mt-4 max-w-3xl">
          <ReadAs intent={intent} q={q} repo={repo} />
          <div className="mt-4">
            <Answer intent={intent} q={q} repo={repo} />
          </div>
        </div>
      )}
      {!intent && <AskHelp />}
    </Page>
  );
}

function ReadAs({ intent, q, repo }: { intent: Intent; q: string; repo: string }) {
  const nav = useNavigate();
  const { repo: scopeRepo } = useScope();
  const scope = (r: string) => nav(r ? askHref(q, r) + (intent.kind === "investigate" ? "&as=investigate" : "") : askHref(q), { replace: true });
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span className="inline-flex items-center gap-1.5 rounded-md bg-accent px-2 py-1 font-medium text-accent-foreground [&_svg]:size-3.5" data-read-as={intent.kind}>
        {ICON[intent.kind]}
        {INTENT_LABEL[intent.kind]}
      </span>
      {intent.kind !== "investigate" && intent.kind !== "logline" && (
        <code className="max-w-[40ch] truncate rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-foreground">
          {intent.method ? intent.method + " " : ""}
          {intent.arg}
        </code>
      )}
      <span>in</span>
      <div className="flex rounded-md bg-muted p-0.5">
        {["", repo || scopeRepo].filter((r, i) => i === 0 || r).map((r) => (
          <button key={r || "fleet"} onClick={() => scope(r)} className={cn("rounded px-2 py-0.5 font-medium", (repo || "") === r ? "bg-card text-foreground shadow-sm" : "hover:text-foreground")}>
            {r || "the fleet"}
          </button>
        ))}
      </div>
      {intent.kind !== "investigate" && (
        <Link to={askHref(q, repo || undefined) + "&as=investigate"} className="ml-auto hover:text-primary hover:underline">
          Ask it as a plain question instead
        </Link>
      )}
    </div>
  );
}

function AskHelp() {
  const rows: [string, string][] = [
    ["a symbol", "Charge · orders.Charge · Planner::plan"],
    ["what breaks if I change …", "blast radius: callers in every repo, tests, versions"],
    ["who calls …", "callers with how sure the index is of each"],
    ["what runs on POST /path", "the route, its handler and the clients that call it"],
    ["where does key come from", "where a config key is set and read"],
    ["a pasted log line", "the statements that print it"],
    ["anything else", "a ranked, cited dossier"],
  ];
  return (
    <div className="mt-6 grid max-w-3xl gap-2 sm:grid-cols-2">
      {rows.map(([a, b]) => (
        <div key={a} className="rounded-lg border bg-card px-3 py-2">
          <div className="text-sm font-medium">{a}</div>
          <div className="text-xs text-muted-foreground">{b}</div>
        </div>
      ))}
    </div>
  );
}

function Loading() {
  return (
    <div className="space-y-2">
      {[0, 1, 2].map((i) => (
        <Skeleton key={i} className="h-[74px] rounded-xl" />
      ))}
    </div>
  );
}

function Failed({ error }: { error: unknown }) {
  return (
    <Empty error title="The analysis failed">
      {String(error)}
    </Empty>
  );
}

function Note({ children }: { children?: ReactNode }) {
  if (!children) return null;
  if (typeof children === "string") return <QueryNote text={children} />;
  return <p className="rounded-lg border border-dashed px-3 py-2 text-xs leading-relaxed text-muted-foreground">{children}</p>;
}

function Answer({ intent, q, repo }: { intent: Intent; q: string; repo: string }) {
  switch (intent.kind) {
    case "symbol":
    case "impact":
    case "callers":
    case "tests":
    case "history":
      return <BySymbol intent={intent} q={q} repo={repo} />;
    case "impls":
      return <ImplsAnswer base={intent.arg} repo={repo} />;
    case "explain":
      return <ExplainAnswer target={intent.arg} repo={repo} />;
    case "route":
      return <ExplainAnswer target={`${intent.method || "ANY"} ${intent.arg}`} repo={repo} />;
    case "logline":
      return <LogAnswer line={intent.arg} repo={repo} />;
    case "config":
      return <ConfigAnswer keyName={intent.arg} repo={repo} />;
    case "errors":
      return <ErrorsAnswer q={intent.arg} repo={repo} />;
    default:
      return <InvestigateAnswer q={q} repo={repo} />;
  }
}

// --- symbol questions: resolve the name, then answer for it -----------------

function useResolve(arg: string, repo: string) {
  const { repo: prefer } = useScope();
  return useQuery({
    queryKey: ["resolve", arg, repo],
    queryFn: ({ signal }) => searchSymbols(arg, repo || "*", REF, signal),
    select: (hits: SearchHit[]) => pickSymbol(hits, arg, prefer),
  });
}

function BySymbol({ intent, q, repo }: { intent: Intent; q: string; repo: string }) {
  const res = useResolve(intent.arg, repo);
  const nav = useNavigate();
  const best = res.data?.ranked[0];
  const unique = !!res.data?.unique;
  // a symbol, its tests or its history: the symbol hub is the answer
  const hubTab = intent.kind === "symbol" ? undefined : intent.kind === "tests" ? "tests" : intent.kind === "history" ? "history" : null;
  useEffect(() => {
    if (best && unique && hubTab !== null) nav(symbolHref(best.repo, REF, best.name, hubTab), { replace: true });
  }, [best?.repo, best?.name, unique, hubTab]); // eslint-disable-line react-hooks/exhaustive-deps
  if (res.isLoading) return <Loading />;
  if (res.error) return <Failed error={res.error} />;
  if (!best)
    return (
      <div className="space-y-4">
        <Note>No indexed symbol is named “{intent.arg}”. Ranked evidence for the question instead:</Note>
        <InvestigateAnswer q={q} repo={repo} />
      </div>
    );
  if (!unique || hubTab !== null)
    return (
      <div className="space-y-3">
        {!unique && <Note>Several symbols match “{intent.arg}”: choose one.</Note>}
        <EvidenceGroup title="Symbols" count={res.data!.ranked.length}>
          {res.data!.ranked.slice(0, 24).map((h) => (
            <EvidenceCard
              key={h.repo + h.name}
              icon={h.is_test ? <FlaskConical className="text-warn" /> : <Braces className="text-primary" />}
              title={<span className="font-mono text-[13px]">{h.name}</span>}
              to={intent.kind === "impact" || intent.kind === "callers" ? askHref(q.replace(intent.arg, h.name), h.repo) : symbolHref(h.repo, REF, h.name, hubTab ?? undefined)}
              badges={<RepoChip repo={h.repo} />}
            />
          ))}
        </EvidenceGroup>
      </div>
    );
  if (intent.kind === "impact") return <ImpactAnswer repo={best.repo} symbol={best.name} />;
  return <CallersAnswer repo={best.repo} symbol={best.name} />;
}

function edgeCard(e: ExplainEdge, repo: string, dir: "caller" | "callee") {
  const r = e.repo || repo;
  return (
    <EvidenceCard
      key={dir + e.symbol + (e.path ?? "") + (e.line ?? "")}
      icon={e.is_test ? <FlaskConical className="text-warn" /> : <Braces className={dir === "caller" ? "text-shape" : "text-primary"} />}
      title={<span className="font-mono text-[13px]">{e.symbol}</span>}
      to={e.external ? undefined : symbolHref(r, REF, e.symbol)}
      repo={e.repo && e.repo !== repo ? e.repo : undefined}
      path={e.path}
      line={e.line}
      symbol={e.external ? undefined : e.symbol}
      badges={<ProvenanceBadge method={e.resolution_method} confidence={e.confidence} />}
    />
  );
}

function useExplain(target: string, repo?: string) {
  return useQuery({
    queryKey: ["explain", repo ?? "", REF, target],
    queryFn: ({ signal }) => api<ExplainResult>("v2/explain", { target, repo, ref: REF }, signal),
  });
}

function HubLinks({ repo, symbol, tabs }: { repo: string; symbol: string; tabs: [string, string][] }) {
  return (
    <div className="flex flex-wrap gap-2 pt-1">
      {tabs.map(([tab, label]) => (
        <Link key={tab} to={symbolHref(repo, REF, symbol, tab)} className="inline-flex items-center gap-1 rounded-md border bg-card px-2.5 py-1 text-xs font-medium hover:border-primary/40 hover:text-primary">
          {label} <ArrowRight className="size-3" />
        </Link>
      ))}
    </div>
  );
}

function CallersAnswer({ repo, symbol }: { repo: string; symbol: string }) {
  const ex = useExplain(symbol, repo);
  if (ex.isLoading) return <Loading />;
  if (ex.error) return <Failed error={ex.error} />;
  const s = ex.data?.symbol;
  if (!s) return <Note>{ex.data?.note}</Note>;
  const callers = s.callers ?? [];
  return (
    <div className="space-y-5">
      <SymbolCard s={s} />
      <EvidenceGroup title="Called by" count={callers.length + (s.more_callers ?? 0)} sub={s.more_callers ? `${s.more_callers} more in the Callers tab` : undefined}>
        {callers.length ? callers.map((e) => edgeCard(e, repo, "caller")) : <Note>Nothing in the index calls {shortName(symbol)}. A runtime path (a route, a topic, a UI event) may still reach it — see Reached by in its hub.</Note>}
      </EvidenceGroup>
      <Reached s={s} />
      <HubLinks repo={repo} symbol={symbol} tabs={[["calls", "All callers and callees"], ["graph", "Call graph"], ["usages", "Every usage"]]} />
    </div>
  );
}

function Reached({ s }: { s: ExplainSymbol }) {
  if (!s.reached_by?.length) return null;
  return (
    <EvidenceGroup title="Reached at runtime by" count={s.reached_by.length}>
      {s.reached_by.map((r, i) => (
        <EvidenceCard key={i} icon={<Workflow />} title={r.detail} repo={r.repo} path={r.path} line={r.line} snippet={r.text} badges={<Badge variant="outline">{r.kind}</Badge>} />
      ))}
    </EvidenceGroup>
  );
}

function SymbolCard({ s }: { s: ExplainSymbol }) {
  return (
    <EvidenceCard
      icon={<Braces className="text-primary" />}
      title={<span className="font-mono text-[13px]">{s.symbol}</span>}
      to={symbolHref(s.repo, REF, s.symbol)}
      repo={s.repo}
      path={s.path}
      line={s.start_line}
      symbol={s.symbol}
      badges={
        <>
          {s.kind && <Badge variant="outline">{s.kind}</Badge>}
          {s.exported && <Badge variant="ok">exported</Badge>}
        </>
      }
      snippet={s.signature}
    >
      {s.doc && <span className="line-clamp-2">{s.doc}</span>}
    </EvidenceCard>
  );
}

function ImpactAnswer({ repo, symbol }: { repo: string; symbol: string }) {
  const br = useQuery({
    queryKey: ["blast_radius", repo, REF, symbol],
    queryFn: ({ signal }) => api<BlastRadius | Suggestion>("blast_radius", { repo, ref: REF, symbol }, signal),
  });
  const ex = useExplain(symbol, repo);
  if (br.isLoading) return <Loading />;
  if (br.error) return <Failed error={br.error} />;
  if (isSuggestion(br.data)) return <Note>No symbol {symbol} in {repo}.</Note>;
  const b = br.data as BlastRadius;
  const rich = new Map((ex.data?.symbol?.callers ?? []).map((e) => [e.symbol, e]));
  // fleet callers at the asked ref, without the in-repo callers it already lists
  const inRepo = new Set(b.in_repo_callers ?? []);
  const fleet = (b.fleet_callers ?? []).filter((c) => (c.ref === REF || !b.fleet_callers?.some((x) => x.ref === REF)) && !(c.repo === repo && inRepo.has(c.caller)));
  return (
    <div className="space-y-5">
      <div className="flex items-start gap-3 rounded-xl border border-primary/25 bg-accent/60 px-4 py-3">
        <ShieldAlert className="mt-0.5 size-5 shrink-0 text-primary" />
        <div className="min-w-0">
          <div className="text-sm font-semibold">
            Changing <span className="font-mono">{shortName(symbol)}</span> reaches {b.summary}
          </div>
          <div className="mt-0.5 text-xs text-muted-foreground">
            {b.contract_changed ? "Its contract already differs between indexed versions." : b.compat_across_refs?.length ? "Its signature is the same in every indexed version." : "Indexed at one version only."} {b.note}
          </div>
        </div>
      </div>
      <EvidenceGroup title={`Callers in ${repo}`} count={b.in_repo_callers?.length ?? 0}>
        {(b.in_repo_callers ?? []).map((c) => {
          const e = rich.get(c);
          return e ? edgeCard(e, repo, "caller") : <EvidenceCard key={c} icon={<Braces className="text-shape" />} title={<span className="font-mono text-[13px]">{c}</span>} to={symbolHref(repo, REF, c)} />;
        })}
        {!b.in_repo_callers?.length && <Note>No caller in {repo}.</Note>}
      </EvidenceGroup>
      {fleet.length > 0 && (
        <EvidenceGroup title="Callers found through imports" count={fleet.length}>
          {fleet.map((c) => (
            <EvidenceCard
              key={c.repo + c.ref + c.caller}
              icon={<Braces className="text-shape" />}
              title={<span className="font-mono text-[13px]">{c.caller}</span>}
              to={symbolHref(c.repo, c.ref, c.caller)}
              badges={
                <>
                  <RepoChip repo={c.repo} />
                  <ProvenanceBadge method={c.resolution_method} confidence={c.confidence} />
                </>
              }
            >
              {c.module && <>through {c.module}</>}
            </EvidenceCard>
          ))}
        </EvidenceGroup>
      )}
      <EvidenceGroup title="Tests to run" count={b.covering_tests?.length ?? 0}>
        {b.covering_tests?.length ? (
          <div className="flex flex-wrap gap-1.5">
            {b.covering_tests.map((t) => (
              <Link key={t} to={symbolHref(repo, REF, t)} className="inline-flex items-center gap-1 rounded-md border bg-card px-2 py-1 font-mono text-xs hover:border-primary/50 hover:text-primary">
                <FlaskConical className="size-3 text-warn" />
                {shortName(t)}
              </Link>
            ))}
          </div>
        ) : (
          <Note>No test reaches it.</Note>
        )}
      </EvidenceGroup>
      {!!b.compat_across_refs?.length && (
        <EvidenceGroup title="In other indexed versions">
          <div className="flex flex-wrap gap-1.5">
            {b.compat_across_refs.map((c) => (
              <span key={c.ref} className="inline-flex items-center gap-1.5 rounded-md border bg-card px-2 py-1 text-xs">
                <code className="font-mono">{c.ref}</code>
                <Badge variant={c.verdict === "compatible" ? "ok" : c.verdict === "behavior_changed" ? "warn" : c.verdict === "shape_changed" ? "shape" : "bad"}>{c.verdict.replace(/_/g, " ")}</Badge>
              </span>
            ))}
          </div>
        </EvidenceGroup>
      )}
      <HubLinks repo={repo} symbol={symbol} tabs={[["calls", "Callers and callees"], ["tests", "Tests with run commands"], ["history", "History"]]} />
    </div>
  );
}

// --- explain: a symbol, file, directory or route -----------------------------

function ExplainAnswer({ target, repo }: { target: string; repo: string }) {
  const ex = useExplain(target, repo || undefined);
  if (ex.isLoading) return <Loading />;
  if (ex.error) return <Failed error={ex.error} />;
  const r = ex.data!;
  switch (r.kind) {
    case "symbol":
      return (
        <div className="space-y-5">
          <SymbolCard s={r.symbol!} />
          <div className="grid gap-4 md:grid-cols-2">
            <EvidenceGroup title="Called by" count={(r.symbol!.callers?.length ?? 0) + (r.symbol!.more_callers ?? 0)}>
              {r.symbol!.callers?.slice(0, 6).map((e) => edgeCard(e, r.symbol!.repo, "caller"))}
            </EvidenceGroup>
            <EvidenceGroup title="Calls" count={(r.symbol!.callees?.length ?? 0) + (r.symbol!.more_callees ?? 0)}>
              {r.symbol!.callees?.slice(0, 6).map((e) => edgeCard(e, r.symbol!.repo, "callee"))}
            </EvidenceGroup>
          </div>
          <HubLinks repo={r.symbol!.repo} symbol={r.symbol!.symbol} tabs={[["explain", "Open its hub"], ["code", "Code"]]} />
        </div>
      );
    case "file": {
      const f = r.file!;
      return (
        <div className="space-y-4">
          <EvidenceCard icon={<FileCode2 className="text-primary" />} title={<span className="font-mono text-[13px]">{f.path}</span>} badges={<><RepoChip repo={f.repo} />{f.lang && <Badge variant="outline">{f.lang}</Badge>}</>}>
            {f.purpose || `${f.lines ?? "?"} lines`}
          </EvidenceCard>
          <EvidenceGroup title="Symbols" count={f.symbols?.length ?? 0}>
            {f.symbols?.map((s) => (
              <EvidenceCard key={s.symbol} icon={<Braces className="text-primary" />} title={<span className="font-mono text-[13px]">{s.symbol}</span>} to={symbolHref(f.repo, REF, s.symbol)} badges={<><Badge variant="outline">{s.kind}</Badge><span className="text-[11px] tabular-nums text-muted-foreground">{s.in ?? 0} in · {s.out ?? 0} out</span></>} snippet={s.signature} snippetLine={s.line} />
            ))}
          </EvidenceGroup>
        </div>
      );
    }
    case "directory": {
      const d = r.dir!;
      return (
        <EvidenceGroup title={d.path} sub={d.purpose} count={d.files?.length}>
          {d.files?.map((f) => (
            <EvidenceCard key={f.path} icon={<FileCode2 />} title={<span className="font-mono text-[13px]">{f.path}</span>} to={askHref(f.path, d.repo)} badges={<span className="text-[11px] tabular-nums text-muted-foreground">{f.symbols} symbols</span>} />
          ))}
        </EvidenceGroup>
      );
    }
    case "route":
      return <RouteAnswer r={r} />;
    case "ambiguous":
      return (
        <EvidenceGroup title="Several matches" sub={r.note}>
          {r.candidates?.flatMap((c) =>
            c.symbols.map((s) => <EvidenceCard key={c.repo + s} icon={<Braces className="text-primary" />} title={<span className="font-mono text-[13px]">{s}</span>} to={symbolHref(c.repo, REF, s)} badges={<RepoChip repo={c.repo} />} />),
          )}
        </EvidenceGroup>
      );
    default:
      return (
        <Empty title={`Nothing indexed matches “${r.target}”`}>
          {r.note}
          {!!r.next?.length && <div className="mt-2 font-mono text-[11px]">{r.next.join("\n")}</div>}
        </Empty>
      );
  }
}

function RouteAnswer({ r }: { r: ExplainResult }) {
  return (
    <div className="space-y-5">
      {r.routes?.map((x, i) => (
        <div key={i} className="space-y-3">
          <EvidenceCard
            icon={<Globe className="text-primary" />}
            title={
              <span className="font-mono text-[13px]">
                {x.route.method === "*" ? "ANY" : x.route.method} {x.route.pattern}
              </span>
            }
            repo={x.route.repo}
            path={x.route.path}
            line={x.route.line}
            badges={
              <>
                <Badge variant="outline">{x.route.framework}</Badge>
                {x.route.partial && <Badge variant="warn" title="the pattern matched part of the path">partial match</Badge>}
                <Link to={flowHref(`${x.route.method === "*" ? "ANY" : x.route.method} ${x.route.pattern}`)} className="inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 text-[11px] font-medium hover:border-primary/40 hover:text-primary">
                  <Workflow className="size-3" />
                  Flow
                </Link>
              </>
            }
          />
          {x.handler && (
            <EvidenceGroup title="Handler">
              <SymbolCard s={x.handler} />
              {x.handler.callees?.filter((c) => !c.external).slice(0, 6).map((e) => edgeCard(e, x.handler!.repo, "callee"))}
            </EvidenceGroup>
          )}
          <EvidenceGroup title="Called from" count={x.clients?.length ?? 0} sub="client calls paired by URL path and method">
            {x.clients?.map((c, j) => (
              <EvidenceCard key={j} icon={<ArrowRight className="text-shape" />} title={c.in ? <span className="font-mono text-[13px]">{c.in}</span> : c.url} repo={c.repo} path={c.path} line={c.line} snippet={c.text} badges={<Badge variant="outline">{c.method} {c.url}</Badge>} />
            ))}
            {!x.clients?.length && <Note>No indexed client calls this route.</Note>}
          </EvidenceGroup>
        </div>
      ))}
      {!r.routes?.length && <Empty title={`No indexed route serves ${r.target}`}>{r.note}</Empty>}
      <Note>{r.note}</Note>
    </div>
  );
}

// --- discovery answers -------------------------------------------------------

function LogAnswer({ line, repo }: { line: string; repo: string }) {
  const r = useQuery({ queryKey: ["logsite", line, repo], queryFn: ({ signal }) => api<LogSitesResult>("v2/logsite", { line, repo, ref: REF }, signal) });
  if (r.isLoading) return <Loading />;
  if (r.error) return <Failed error={r.error} />;
  const sites = r.data?.sites ?? [];
  return (
    <div className="space-y-4">
      <EvidenceGroup title="Printed by" count={sites.length}>
        {sites.map((s, i) => (
          <EvidenceCard
            key={i}
            icon={<ScrollText className={s.logging_call ? "text-primary" : "text-muted-foreground"} />}
            title={s.in ? <span className="font-mono text-[13px]">{s.in}</span> : s.call || s.path}
            repo={s.repo}
            path={s.path}
            line={s.line}
            snippet={s.text}
            snippetLine={s.line}
            badges={
              <>
                {s.call && <Badge variant="outline">{s.call}</Badge>}
                {s.fixed_chars_matched != null && (
                  <Badge variant={s.fixed_chars_matched >= Math.min(20, line.length / 2) ? "ok" : "warn"} title="characters of the line's fixed text this statement prints">
                    {s.fixed_chars_matched} chars matched
                  </Badge>
                )}
              </>
            }
          />
        ))}
        {!sites.length && <Empty title="No statement prints this line" />}
      </EvidenceGroup>
      <Note>{r.data?.note}</Note>
    </div>
  );
}

function ConfigAnswer({ keyName, repo }: { keyName: string; repo: string }) {
  const r = useQuery({ queryKey: ["config", keyName, repo], queryFn: ({ signal }) => api<ConfigResult>("v2/config", { key: keyName, repo, ref: REF }, signal) });
  if (r.isLoading) return <Loading />;
  if (r.error) return <Failed error={r.error} />;
  const keys = r.data?.keys ?? [];
  return (
    <div className="space-y-5">
      {keys.map((k) => (
        <div key={k.kind + k.key} className="space-y-3">
          <div className="flex flex-wrap items-center gap-2">
            <Settings2 className="size-4 text-primary" />
            <code className="font-mono text-sm font-semibold">{k.key}</code>
            <Badge variant="outline">{k.kind}</Badge>
            {k.status && <Badge variant={k.status === "ok" ? "ok" : "warn"}>{k.status.replace(/_/g, " ")}</Badge>}
            {!!k.defaults?.length && <span className="text-xs text-muted-foreground">default {k.defaults.join(", ")}</span>}
          </div>
          <EvidenceGroup title="Set in" count={k.sets_count}>
            {k.sets?.map((s, i) => (
              <EvidenceCard key={i} icon={<FileCode2 />} title={<span className="font-mono text-[13px]">{s.value || "(empty)"}</span>} repo={s.repo} path={s.path} line={s.line} badges={<Badge variant="outline">{s.source}</Badge>} />
            ))}
            {!k.sets?.length && <Note>Not set in any indexed file.</Note>}
          </EvidenceGroup>
          <EvidenceGroup title="Read by" count={k.reads_count}>
            {k.reads?.map((x, i) => (
              <EvidenceCard
                key={i}
                icon={<Braces className="text-primary" />}
                title={x.in ? <span className="font-mono text-[13px]">{x.in}</span> : x.path}
                to={x.handle ? symbolHref(x.repo, REF, x.handle) : undefined}
                repo={x.repo}
                path={x.path}
                line={x.line}
                symbol={x.handle}
                snippet={x.text}
                badges={
                  <>
                    <Badge variant="outline">{x.via}</Badge>
                    {x.default && <Badge variant="default">default {x.default}</Badge>}
                  </>
                }
              />
            ))}
          </EvidenceGroup>
        </div>
      ))}
      {r.data?.flow && (
        <EvidenceGroup title={`Defined in ${r.data.flow.n_defined} places`} sub={r.data.flow.key}>
          {r.data.flow.defined.slice(0, 6).map((x, i) => (
            <EvidenceCard key={i} icon={<FileCode2 />} title={<span className="font-mono text-[13px]">{x.text}</span>} repo={x.repo} path={x.path} line={x.line} badges={x.kind ? <Badge variant="outline">{x.kind}</Badge> : undefined} />
          ))}
          {r.data.flow.templates.slice(0, 4).map((x, i) => (
            <EvidenceCard key={"t" + i} icon={<Settings2 />} title={<span className="font-mono text-[13px]">{x.text}</span>} repo={x.repo} path={x.path} line={x.line} badges={<Badge variant="shape">template</Badge>} />
          ))}
        </EvidenceGroup>
      )}
      {!keys.length && <Empty title={`No configuration key “${keyName}” is read or set`}>Ranked evidence for the question may still help: ask it as a plain question.</Empty>}
      <Note>{r.data?.note}</Note>
    </div>
  );
}

function ErrorsAnswer({ q, repo }: { q: string; repo: string }) {
  const r = useQuery({ queryKey: ["errors", q, repo], queryFn: ({ signal }) => api<ErrorsResult>("v2/errors", { q, repo, ref: REF }, signal) });
  if (r.isLoading) return <Loading />;
  if (r.error) return <Failed error={r.error} />;
  const d = r.data!;
  return (
    <div className="space-y-5">
      <EvidenceGroup title="Produced by" count={d.producers.length}>
        {d.producers.map((s, i) => (
          <EvidenceCard
            key={i}
            icon={<CircleAlert className="text-bad" />}
            title={<span className="font-mono text-[13px]">{s.type || s.message || s.kind}</span>}
            to={s.handle ? symbolHref(s.repo, REF, s.handle) : undefined}
            repo={s.repo}
            path={s.path}
            line={s.line}
            symbol={s.handle}
            snippet={s.text}
            badges={
              <>
                <Badge variant="outline">{s.kind.replace(/_/g, " ")}</Badge>
                {s.match && <Badge variant={s.match === "exact" ? "ok" : "warn"}>{s.match.replace(/_/g, " ")}</Badge>}
                {s.test && <Badge variant="warn">test</Badge>}
              </>
            }
          >
            {s.in && <>in {s.in}</>}
            {!!s.callers?.length && <> · called by {s.callers.slice(0, 4).join(", ")}</>}
          </EvidenceCard>
        ))}
        {!d.producers.length && <Empty title="Nothing indexed produces this error" />}
      </EvidenceGroup>
      {!!d.handlers?.length && (
        <EvidenceGroup title="Handled by" count={d.handlers.length}>
          {d.handlers.map((h, i) => (
            <EvidenceCard
              key={i}
              icon={<ShieldAlert className="text-ok" />}
              title={h.in ? <span className="font-mono text-[13px]">{h.in}</span> : h.path}
              repo={h.repo}
              path={h.path}
              line={h.line}
              snippet={h.text}
              badges={
                <>
                  <Badge variant="outline">{h.kind.replace(/_/g, " ")}</Badge>
                  {h.on_path && <Badge variant="ok">on the path</Badge>}
                  {h.test && <Badge variant="warn">test</Badge>}
                </>
              }
            />
          ))}
        </EvidenceGroup>
      )}
      <Note>{d.note}</Note>
    </div>
  );
}

function ImplsAnswer({ base, repo }: { base: string; repo: string }) {
  const r = useQuery({ queryKey: ["impls", base, repo, REF], queryFn: ({ signal }) => api<ImplsResult>("v2/impls", { base, repo, ref: REF }, signal) });
  if (r.isLoading) return <Loading />;
  if (r.error) return <Failed error={r.error} />;
  return <ImplsList r={r.data!} refName={REF} />;
}

function InvestigateAnswer({ q, repo }: { q: string; repo: string }) {
  const r = useQuery({ queryKey: ["investigate", q, repo], queryFn: ({ signal }) => api<InvestigateResult>("v2/investigate", { q, repo, ref: REF }, signal) });
  if (r.isLoading) return <Loading />;
  if (r.error) return <Failed error={r.error} />;
  const d = r.data!;
  const caveat = /^>\s*\*\*Caveat:\*\*\s*(.+)$/m.exec(d.dossier ?? "")?.[1];
  const top = d.top_score || 1;
  return (
    <div className="space-y-4">
      <EvidenceGroup title="Most relevant code" count={d.findings?.length ?? 0} sub={d.considered ? `ranked from ${d.considered.toLocaleString()} symbols` : undefined}>
        {d.findings?.map((f) => (
          <EvidenceCard
            key={f.repo + f.symbol + f.line}
            icon={<Search className="text-primary" />}
            title={<span className="font-mono text-[13px]">{f.symbol}</span>}
            to={symbolHref(f.repo, REF, f.symbol)}
            repo={f.repo}
            path={f.path}
            line={f.line}
            symbol={f.symbol}
            snippet={f.preview}
            snippetLine={f.line}
            badges={
              <span className="flex items-center gap-1.5" title={`similarity ${f.score.toFixed(3)}`}>
                <span className="h-1.5 w-12 overflow-hidden rounded-full bg-muted">
                  <span className="block h-full rounded-full bg-primary/70" style={{ width: `${Math.max(6, (100 * f.score) / top)}%` }} />
                </span>
                <span className="text-[11px] tabular-nums text-muted-foreground">{f.score.toFixed(2)}</span>
              </span>
            }
          >
            {!!f.used_by?.length && <>used by {f.used_by.slice(0, 5).join(", ")}</>}
            {!!f.used_by?.length && !!f.uses?.length && " · "}
            {!!f.uses?.length && <>uses {f.uses.slice(0, 5).join(", ")}</>}
          </EvidenceCard>
        ))}
        {!d.findings?.length && <Empty title="Nothing in the index looks relevant" />}
      </EvidenceGroup>
      {caveat && <Note>{caveat}</Note>}
    </div>
  );
}
