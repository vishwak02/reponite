// Links into the app's pages, built in one place so every view agrees on the
// URL shape (the pages read it back with useSymbolParams).

export function symbolHref(repo: string, ref: string, name: string, tab?: string) {
  const q = new URLSearchParams({ repo, ref, s: name });
  if (tab) q.set("tab", tab);
  return `/symbol?${q}`;
}

// the Ask page answering q (fleet-wide unless repo scopes it)
export function askHref(q: string, repo?: string) {
  const p = new URLSearchParams({ q });
  if (repo) p.set("repo", repo);
  return `/ask?${p}`;
}

// the flow view of an entry point (a route, a UI handler); it arrives with
// the flow pages, and until then shows what is known about the entry
export function flowHref(entry: string) {
  return `/flow?${new URLSearchParams({ entry })}`;
}
