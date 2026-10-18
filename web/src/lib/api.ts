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
