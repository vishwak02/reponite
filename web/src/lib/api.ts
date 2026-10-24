// Typed client for reponite's JSON API (internal/interfaces/web.go). Field
// names are the snake_case the server writes.

export class ApiError extends Error {
  constructor(public status: number, message: string) {
    super(message);
  }
}

export async function api<T>(path: string, params: Record<string, string | number | boolean | undefined> = {}, signal?: AbortSignal): Promise<T> {
  const q = new URLSearchParams();
  for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== "") q.set(k, String(v));
  const r = await fetch(`/api/${path}?${q}`, { signal });
  if (!r.ok) throw new ApiError(r.status, (await r.text()).trim() || `${path}: ${r.status}`);
  return r.json() as Promise<T>;
}

export interface Meta {
  repo?: string;
  ref?: string;
  warnings?: string[];
}

export interface RefStat { ref: string; commit?: string; symbols?: number; edges?: number; files?: number }
export interface RepoOverview { repo: string; module?: string; db_path?: string; refs: RefStat[]; tables?: { name: string; rows: number }[] }

export interface SearchHit { repo: string; name: string; ref: string; is_test?: boolean }

export interface Neighbor { name: string; handle?: string; path?: string; preview?: string; resolution_method?: string; confidence?: number }
export interface Brief {
  symbol: string;
  ref?: string;
  target?: { name: string; path?: string; exported?: boolean; start_line?: number; end_line?: number; body?: string; signature?: string };
  callees?: Neighbor[];
  callers?: Neighbor[];
  tests?: string[];
  intent?: unknown;
  _meta?: Meta;
}

export interface GraphNode { id: string; label: string; pkg: string; hop: number; test?: boolean; more?: number; external?: number }
export interface GraphEdge { from: string; to: string; method: string; confidence: number }
export interface GraphResult { focus: string; nodes: GraphNode[]; edges: GraphEdge[]; truncated: boolean; note?: string }

export interface Usage { repo?: string; path: string; line: number; text: string; in?: string; confirmed?: boolean }
export interface UsagesResult { symbol: string; total: number; usages: Usage[]; note?: string }

export interface Endpoint { repo: string; path: string; line: number; role: string; name: string; raw?: string; msg_type?: string; in?: string; text?: string; name_resolution?: string; name_confidence?: number }
export interface TopicGroup { family: string; name: string; connected: boolean; confidence: number; producers?: Endpoint[]; consumers?: Endpoint[]; declared?: Endpoint[] }
export interface TopicsResult { groups?: TopicGroup[]; note?: string; endpoints?: number; unresolved?: number; launch_files?: number; remapped?: number }

export interface Link { repo: string; path: string; line: number; kind: string; to: string; to_line?: number; text: string; resolved?: string }
export interface LinksResult { target: string; refers_to: Link[]; referred_by: Link[]; note?: string }

export interface CoverageKind { kind: string; stored: number; skipped?: Record<string, number> }
export interface CoverageRepo { repo: string; ref: string; stored: number; skipped: number; generated?: number; reasons?: Record<string, number>; kinds: CoverageKind[]; examples?: Record<string, string[]>; skips_recorded: boolean }
export interface CoverageResult { repos: CoverageRepo[]; note?: string }

export interface FleetRepo { repo: string; ref?: string; commit?: string; module?: string; summary?: string; languages?: string[] /* "cpp:745" */; files?: number; symbols?: number; routes?: number; entry_points?: Record<string, number> }
export interface FleetLink { from: string; to: string; kind: string; count: number; examples?: string[]; handle?: string }
export interface FleetMap { repos?: FleetRepo[]; links?: FleetLink[]; note?: string }

export interface CompatVerdict { repo: string; ref: string; verdict: string; confidence: number; direct_confidence?: number; detail?: string; changed_callees?: string[]; signature_from?: string; signature_to?: string }
export interface CompatResult { symbol: string; origin: { repo: string; ref: string }; verdicts?: CompatVerdict[]; note?: string }

export interface DiffChange { name: string; change: string }
export interface DiffResult { changes?: DiffChange[] }

export interface XCaller { repo: string; ref: string; caller: string; module?: string; resolution_method: string; confidence: number; expected_signature?: string }
export interface XImpact { callers?: XCaller[]; modules?: string[]; contract_changed?: boolean; stale_callers?: number; note?: string }

export interface VerifyImpact { symbol: string; kind: string; breaks?: Usage[] }
export interface VerifyResult { path: string; safe: boolean; added?: string[]; changed?: string[]; removed?: string[]; impacts?: VerifyImpact[]; note?: string }

export async function verifyEdit(path: string, repo: string, ref: string, content: string): Promise<VerifyResult> {
  const r = await fetch(`/api/verify_edit?${new URLSearchParams({ path, repo, ref })}`, {
    method: "POST",
    headers: { "Content-Type": "application/x-www-form-urlencoded" },
    body: new URLSearchParams({ content }),
  });
  if (!r.ok) throw new ApiError(r.status, (await r.text()).trim());
  return r.json();
}

// /api/search answers either a hit list or, when nothing matches, a
// suggestion envelope; normalize to hits.
export async function searchSymbols(q: string, repo: string, ref: string, signal?: AbortSignal): Promise<SearchHit[]> {
  const d = await api<SearchHit[] | { found: false; did_you_mean?: { name: string; repo?: string }[] }>("search", { q, repo, ref }, signal);
  if (Array.isArray(d)) return d;
  return (d.did_you_mean ?? []).map((s) => ({ repo: s.repo ?? repo, name: s.name, ref }));
}

// --- /api/v2/*: the question-shaped analyses (internal/interfaces/web_v2.go)

export interface Suggestion { found: false; did_you_mean?: { name: string; repo?: string }[] }
export const isSuggestion = (d: unknown): d is Suggestion => !!d && typeof d === "object" && (d as { found?: unknown }).found === false;

export interface ExplainEdge { symbol: string; repo?: string; path?: string; line?: number; resolution_method?: string; confidence?: number; external?: boolean; is_test?: boolean }
export interface Reach { kind: string; detail: string; repo: string; path: string; line: number; text?: string }
export interface TestRef { repo: string; path: string; line: number; in: string; calls?: boolean }
export interface ExplainSymbol {
  symbol: string;
  repo: string;
  lang?: string;
  kind?: string;
  signature?: string;
  decorators?: string[];
  doc?: string;
  path: string;
  start_line: number;
  end_line: number;
  exported?: boolean;
  is_test?: boolean;
  recursive?: boolean;
  body?: string;
  body_truncated?: boolean;
  callers?: ExplainEdge[];
  callees?: ExplainEdge[];
  more_callers?: number;
  more_callees?: number;
  reached_by?: Reach[];
  tests?: TestRef[];
}
export interface FileSymbol { symbol: string; kind: string; line: number; end_line?: number; signature?: string; in?: number; out?: number }
export interface ExplainFile { repo: string; path: string; lang?: string; lines?: number; purpose?: string; role?: string[]; symbols?: FileSymbol[]; imports?: string[]; inbound?: { repo: string; path: string; calls: number; examples?: string[] }[] }
export interface RouteDef { repo?: string; path: string; line: number; method: string; pattern: string; handler?: string; framework: string; partial?: boolean }
export interface ClientCall { repo?: string; path: string; line: number; in?: string; method: string; url: string; text: string }
export interface ExplainResult {
  target: string;
  kind: "symbol" | "file" | "directory" | "route" | "ambiguous" | "not_found" | string;
  repo?: string;
  ref?: string;
  symbol?: ExplainSymbol;
  file?: ExplainFile;
  dir?: { repo: string; path: string; purpose?: string; files?: { path: string; lang?: string; symbols: number }[] };
  routes?: { route: RouteDef; handler?: ExplainSymbol; clients?: ClientCall[] }[];
  candidates?: { repo: string; symbols: string[] }[];
  truncated?: boolean;
  next?: string[];
  note?: string;
  _meta?: Meta;
}

export interface ContextResult { symbol: string; ref: string; caller_edges?: { name: string; is_test?: boolean; repo?: string; via?: string }[]; callee_edges?: { name: string; resolution_method?: string; confidence?: number; repo?: string }[]; _meta?: Meta }

export interface Impl { repo?: string; path: string; line: number; class: string; bases: string[]; depth: number; plugin?: boolean; method_def?: string; test?: boolean }
export interface ImplsResult { base: string; method?: string; base_defs?: string[]; impls: Impl[]; note?: string }

export interface TestHit { repo: string; path: string; line: number; name: string; handle?: string; framework: string; depth: number; via?: string[]; how: string; command: string }
export interface TestsResult { symbol: string; repo: string; ref: string; tests: TestHit[]; commands: { repo: string; framework: string; tests: number; command: string }[]; total: number; truncated: boolean; note?: string }

export interface WhyCommit { commit: string; date: string; author: string; subject: string; body?: string; prs?: string[]; tickets?: string[]; kind: string; path?: string; added: number; removed: number; first_in?: string; in_refs: number }
export interface WhyResult { symbol: string; repo: string; ref: string; path?: string; lines?: string; commits: WhyCommit[]; total: number; compat_history: { ref: string; date?: string; present: boolean; verdict?: string }[]; truncated: boolean; note?: string }

export interface ErrSite { repo: string; path: string; line: number; in?: string; handle?: string; kind: string; type?: string; status?: number; message?: string; text?: string; match?: string; test?: boolean; callers?: string[] }
export interface ErrHandler { repo: string; path: string; line: number; in?: string; kind: string; types?: string[]; match: string; on_path?: boolean; text: string; test?: boolean }
export interface ErrorsResult { query?: string; mode: string; producers: ErrSite[]; handlers?: ErrHandler[]; total: number; truncated: boolean; note?: string }

export interface LogSite { repo: string; path: string; line: number; in?: string; call?: string; format?: string; text: string; fixed_chars_matched?: number; logging_call?: boolean }
export interface LogSitesResult { line: string; sites: LogSite[]; note?: string }

export interface Finding { repo: string; path: string; symbol: string; line: number; score: number; preview?: string; uses?: string[]; used_by?: string[] }
export interface InvestigateResult { question: string; dossier?: string; findings?: Finding[]; omitted?: number; considered?: number; top_score?: number; _meta?: Meta }

export interface CfgRead { repo: string; path: string; line: number; in?: string; handle?: string; default?: string; via: string; text?: string }
export interface CfgSet { repo: string; path: string; line?: number; value: string; source: string }
export interface CfgKey { kind: string; key: string; reads_count: number; sets_count: number; defaults?: string[]; status?: string; note?: string; reads?: CfgRead[]; sets?: CfgSet[] }
export interface CfgFlowSite { repo: string; path: string; line: number; text: string; in?: string; via?: string; kind?: string }
export interface ConfigResult { query?: string; total: number; keys: CfgKey[]; note?: string; next?: string; flow?: { key: string; n_defined: number; defined: CfgFlowSite[]; templates: CfgFlowSite[]; readers: CfgFlowSite[]; uses: CfgFlowSite[]; note?: string } }

export interface BlastRadius {
  symbol: string;
  repo: string;
  summary?: string;
  contract_changed?: boolean;
  in_repo_callers?: string[];
  fleet_callers?: XCaller[];
  covering_tests?: string[];
  compat_across_refs?: { ref: string; verdict: string; confidence: number }[];
  note?: string;
}

export interface DiffChangeC { name: string; change: string; confidence?: number }
export interface DiffReport { repo?: string; from?: string; to?: string; changes?: DiffChangeC[] }
