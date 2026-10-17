// A searchable repo picker: the fleet runs to a hundred-plus repos, too many
// for a plain select.
import { useState } from "react";
import * as Popover from "@radix-ui/react-popover";
import { Command } from "cmdk";
import { Check, ChevronsUpDown, FolderGit2 } from "lucide-react";
import { useScope } from "@/lib/scope";
import { cn, fmt, tint } from "@/lib/utils";

export function RepoPicker() {
  const { repos, repo, setRepo, overview } = useScope();
  const [open, setOpen] = useState(false);
  const syms = new Map(overview.map((o) => [o.repo, Math.max(0, ...o.refs.map((r) => r.symbols ?? 0))]));
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>
        <button className="flex h-8 min-w-[180px] max-w-[260px] items-center gap-2 rounded-md border border-input bg-card px-2.5 text-sm shadow-sm hover:bg-muted">
          <span className="size-2 shrink-0 rounded-full" style={{ background: repo ? tint(repo) : undefined }} />
          <span className="truncate font-medium">{repo || "Select repo"}</span>
          <ChevronsUpDown className="ml-auto size-3.5 shrink-0 text-muted-foreground" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content align="start" sideOffset={6} className="z-50 w-[320px] overflow-hidden rounded-lg border bg-popover shadow-xl animate-fade-in">
          <Command>
            <Command.Input autoFocus placeholder={`Filter ${repos.length} repos…`} className="h-10 w-full border-b bg-transparent px-3 text-sm outline-none placeholder:text-muted-foreground" />
            <Command.List className="max-h-[360px] overflow-y-auto p-1">
              <Command.Empty className="px-3 py-6 text-center text-sm text-muted-foreground">No repo matches.</Command.Empty>
              {repos.map((r) => (
                <Command.Item
                  key={r}
                  value={r}
                  onSelect={() => {
                    setRepo(r);
                    setOpen(false);
                  }}
                  className="flex cursor-pointer items-center gap-2 rounded-md px-2 py-1.5 text-sm data-[selected=true]:bg-muted"
                >
                  <FolderGit2 className="size-3.5" style={{ color: tint(r) }} />
                  <span className="truncate">{r}</span>
                  <span className="ml-auto text-[11px] tabular-nums text-muted-foreground">{fmt(syms.get(r))}</span>
                  <Check className={cn("size-3.5", r === repo ? "opacity-100" : "opacity-0")} />
                </Command.Item>
              ))}
            </Command.List>
          </Command>
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}

export function RefPicker() {
  const { refs, ref, setRef } = useScope();
  return (
    <div className="relative">
      <select
        value={ref}
        onChange={(e) => setRef(e.target.value)}
        className="h-8 max-w-[230px] appearance-none truncate rounded-md border border-input bg-card pl-2.5 pr-7 font-mono text-xs shadow-sm hover:bg-muted focus:outline-none focus:ring-2 focus:ring-ring"
      >
        {(refs.length ? refs : [ref]).map((r) => (
          <option key={r} value={r}>
            {r === "HEAD" && refs.length > 1 ? "HEAD (pinned)" : r}
          </option>
        ))}
      </select>
      <ChevronsUpDown className="pointer-events-none absolute right-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
    </div>
  );
}
