// ⌘K: jump to any symbol in the fleet, or to any page.
import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import * as Dialog from "@radix-ui/react-dialog";
import { Command } from "cmdk";
import { Braces, CornerDownLeft, FlaskConical, Search } from "lucide-react";
import { searchSymbols } from "@/lib/api";
import { useScope } from "@/lib/scope";
import { shortName, tint } from "@/lib/utils";
import { NAV } from "./nav";

function useDebounced<T>(v: T, ms: number): T {
  const [d, setD] = useState(v);
  useEffect(() => {
    const t = setTimeout(() => setD(v), ms);
    return () => clearTimeout(t);
  }, [v, ms]);
  return d;
}

export function symbolHref(repo: string, ref: string, name: string, tab?: string) {
  const q = new URLSearchParams({ repo, ref, s: name });
  if (tab) q.set("tab", tab);
  return `/symbol?${q}`;
}

export function CommandPalette({ open, setOpen }: { open: boolean; setOpen: (o: boolean) => void }) {
  const nav = useNavigate();
  const { repo, ref } = useScope();
  const [q, setQ] = useState("");
  const [fleet, setFleet] = useState(true);
  const dq = useDebounced(q.trim(), 160);

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen(!open);
      }
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, [open, setOpen]);

  const hits = useQuery({
    queryKey: ["search", dq, fleet ? "*" : repo, fleet ? "HEAD" : ref],
    queryFn: ({ signal }) => searchSymbols(dq, fleet ? "*" : repo, fleet ? "HEAD" : ref, signal),
    enabled: dq.length >= 2,
    staleTime: 30_000,
  });

  const go = (to: string) => {
    setOpen(false);
    setQ("");
    nav(to);
  };

  return (
    <Dialog.Root open={open} onOpenChange={setOpen}>
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/40 backdrop-blur-[2px] animate-fade-in" />
        <Dialog.Content className="fixed left-1/2 top-[12vh] z-50 w-[min(680px,92vw)] -translate-x-1/2 overflow-hidden rounded-xl border bg-popover shadow-2xl animate-fade-in">
          <Dialog.Title className="sr-only">Command palette</Dialog.Title>
          <Command shouldFilter={false} loop>
            <div className="flex items-center gap-2 border-b px-3">
              <Search className="size-4 text-muted-foreground" />
              <Command.Input
                value={q}
                onValueChange={setQ}
                placeholder="Search symbols, or jump to a page…"
                className="h-12 flex-1 bg-transparent text-[15px] outline-none placeholder:text-muted-foreground"
              />
              <button
                onClick={() => setFleet(!fleet)}
                className="rounded-md border px-2 py-0.5 text-[11px] font-medium text-muted-foreground hover:bg-muted"
                title="Search the whole fleet or only the selected repo"
              >
                {fleet ? "Fleet" : repo}
              </button>
            </div>
            <Command.List className="max-h-[56vh] overflow-y-auto p-1.5">
              {dq.length >= 2 && hits.isFetching && !hits.data && <div className="px-3 py-6 text-center text-sm text-muted-foreground">Searching…</div>}
              {dq.length >= 2 && hits.data?.length === 0 && <div className="px-3 py-6 text-center text-sm text-muted-foreground">No symbol matches “{dq}”.</div>}
              {hits.data && hits.data.length > 0 && (
                <Command.Group heading="Symbols" className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground">
                  {hits.data.slice(0, 60).map((h) => (
                    <Command.Item
                      key={h.repo + h.ref + h.name}
                      value={h.repo + " " + h.name}
                      onSelect={() => go(symbolHref(h.repo, fleet ? "HEAD" : h.ref, h.name))}
                      className="group flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-muted"
                    >
                      {h.is_test ? <FlaskConical className="size-4 text-warn" /> : <Braces className="size-4 text-primary" />}
                      <div className="min-w-0 flex-1">
                        <div className="truncate font-medium">{shortName(h.name)}</div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">{h.name}</div>
                      </div>
                      <span className="shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium" style={{ color: tint(h.repo, 45), background: tint(h.repo, 55).replace(")", " / .12)") }}>
                        {h.repo}
                      </span>
                      <CornerDownLeft className="size-3.5 text-muted-foreground opacity-0 group-data-[selected=true]:opacity-100" />
                    </Command.Item>
                  ))}
                </Command.Group>
              )}
              <Command.Group heading="Pages" className="[&_[cmdk-group-heading]]:px-2 [&_[cmdk-group-heading]]:py-1.5 [&_[cmdk-group-heading]]:text-[11px] [&_[cmdk-group-heading]]:font-medium [&_[cmdk-group-heading]]:text-muted-foreground">
                {NAV.flatMap((g) => g.items)
                  .filter((i) => !q || i.label.toLowerCase().includes(q.toLowerCase()))
                  .map((i) => (
                    <Command.Item key={i.to} value={"page " + i.label} onSelect={() => go(i.to)} className="flex cursor-pointer items-center gap-2.5 rounded-md px-2 py-2 text-sm data-[selected=true]:bg-muted">
                      <i.icon className="size-4 text-muted-foreground" />
                      {i.label}
                      <span className="ml-auto text-xs text-muted-foreground">{i.hint}</span>
                    </Command.Item>
                  ))}
              </Command.Group>
            </Command.List>
            <div className="flex items-center gap-3 border-t px-3 py-2 text-[11px] text-muted-foreground">
              <span><kbd className="kbd">↑↓</kbd> move</span>
              <span><kbd className="kbd">↵</kbd> open</span>
              <span><kbd className="kbd">esc</kbd> close</span>
            </div>
          </Command>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
