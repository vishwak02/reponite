// The symbol hub's shared answer: explain for the symbol (signature, doc,
// body, callers and callees with their provenance, tests). The header and the
// Explain, Calls and Code tabs read the same cached answer.
import { useQuery } from "@tanstack/react-query";
import { api, type ExplainResult } from "@/lib/api";

export function useSymbolExplain(repo: string, ref: string, s: string) {
  return useQuery({
    queryKey: ["explain", repo, ref, s],
    queryFn: ({ signal }) => api<ExplainResult>("v2/explain", { target: s, repo, ref }, signal),
    enabled: !!s,
  });
}

export interface TabProps {
  repo: string;
  refName: string;
  s: string;
}
