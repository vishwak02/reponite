// The version scope every view reads: the selected repo, its ref, and the
// theme. Fleet-wide views read HEAD, which a server started with --refs maps
// to each repo's pinned version.
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, type RepoOverview } from "./api";

export const FLEET_REF = "HEAD";

const ls = {
  get: (k: string) => {
    try {
      return localStorage.getItem("reponite." + k) ?? "";
    } catch {
      return "";
    }
  },
  set: (k: string, v: string) => {
    try {
      localStorage.setItem("reponite." + k, v);
    } catch {
      /* private mode */
    }
  },
};

interface Scope {
  repos: string[];
  overview: RepoOverview[];
  repo: string;
  setRepo: (r: string) => void;
  refs: string[];
  ref: string;
  setRef: (r: string) => void;
  dark: boolean;
  toggleTheme: () => void;
  loading: boolean;
}

const Ctx = createContext<Scope | null>(null);

export function ScopeProvider({ children }: { children: ReactNode }) {
  const repos = useQuery({ queryKey: ["repos"], queryFn: () => api<{ repos: string[] }>("repos"), staleTime: Infinity });
  const overview = useQuery({ queryKey: ["overview"], queryFn: () => api<{ repos: RepoOverview[] }>("overview"), staleTime: 60_000 });
  const [repo, setRepoState] = useState(ls.get("repo"));
  const [ref, setRefState] = useState(ls.get("ref"));
  const [dark, setDark] = useState(() => {
    const t = ls.get("theme");
    return t ? t === "dark" : window.matchMedia?.("(prefers-color-scheme: dark)").matches;
  });

  const repoList = repos.data?.repos ?? [];
  const ov = overview.data?.repos ?? [];

  // default to the largest repo, not the first by name
  useEffect(() => {
    if (!repoList.length || repoList.includes(repo) || overview.isLoading) return;
    const size = (o: RepoOverview) => Math.max(0, ...o.refs.map((r) => r.symbols ?? 0));
    const best = [...ov].sort((a, b) => size(b) - size(a))[0]?.repo ?? repoList[0];
    setRepoState(best);
  }, [repoList.join(","), ov.length, overview.isLoading]); // eslint-disable-line react-hooks/exhaustive-deps

  const refsQ = useQuery({
    queryKey: ["refs", repo],
    queryFn: () => api<{ refs: string[] }>("refs", { repo }),
    enabled: !!repo,
    staleTime: 60_000,
  });
  const refs = refsQ.data?.refs ?? [];
  useEffect(() => {
    if (!refs.length || refs.includes(ref)) return;
    setRefState(refs.includes("HEAD") ? "HEAD" : refs[0]);
  }, [refs.join(",")]); // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    document.documentElement.classList.toggle("dark", dark);
  }, [dark]);

  const value = useMemo<Scope>(
    () => ({
      repos: repoList,
      overview: ov,
      repo,
      setRepo: (r) => {
        setRepoState(r);
        ls.set("repo", r);
      },
      refs,
      ref: ref || "HEAD",
      setRef: (r) => {
        setRefState(r);
        ls.set("ref", r);
      },
      dark,
      toggleTheme: () =>
        setDark((d) => {
          ls.set("theme", d ? "light" : "dark");
          return !d;
        }),
      loading: repos.isLoading || overview.isLoading,
    }),
    [repoList, ov, repo, refs, ref, dark, repos.isLoading, overview.isLoading],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useScope(): Scope {
  const s = useContext(Ctx);
  if (!s) throw new Error("useScope outside ScopeProvider");
  return s;
}
