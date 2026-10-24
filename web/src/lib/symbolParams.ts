// The symbol a page shows, read from its URL (?repo=&ref=&s=&tab=), falling
// back to the global scope. Shared by the Symbol and Graph pages; it lives
// here so neither page pulls in the other's code.
import { useEffect } from "react";
import { useSearchParams } from "react-router-dom";
import { useScope } from "./scope";

export function useSymbolParams() {
  const [sp, setSp] = useSearchParams();
  const scope = useScope();
  const repo = sp.get("repo") || scope.repo;
  const ref = sp.get("ref") || scope.ref;
  const s = sp.get("s") || "";
  // a link into another repo moves the global scope with it
  useEffect(() => {
    if (sp.get("repo") && sp.get("repo") !== scope.repo) scope.setRepo(sp.get("repo")!);
  }, [sp.get("repo")]); // eslint-disable-line react-hooks/exhaustive-deps
  return { repo, ref, s, tab: sp.get("tab") || "explain", sp, setSp };
}
