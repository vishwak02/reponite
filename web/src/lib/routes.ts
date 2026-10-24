// Links into the app's pages, built in one place so every view agrees on the
// URL shape (the pages read it back with useSymbolParams).

export function symbolHref(repo: string, ref: string, name: string, tab?: string) {
  const q = new URLSearchParams({ repo, ref, s: name });
  if (tab) q.set("tab", tab);
  return `/symbol?${q}`;
}
