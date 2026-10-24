// The repo picker's searchable list: a popover over cmdk. Loaded the first
// time the picker is pointed at or focused (RepoPicker), so the command menu
// stays out of the app shell.
import type { ReactNode } from "react";
import * as Popover from "@radix-ui/react-popover";
import { Command } from "cmdk";
import { Check, FolderGit2 } from "lucide-react";
import { useScope } from "@/lib/scope";
import { cn, fmt, tint } from "@/lib/utils";

export default function RepoMenu({ open, setOpen, trigger }: { open: boolean; setOpen: (o: boolean) => void; trigger: ReactNode }) {
  const { repos, repo, setRepo, overview } = useScope();
  const syms = new Map(overview.map((o) => [o.repo, Math.max(0, ...o.refs.map((r) => r.symbols ?? 0))]));
  return (
    <Popover.Root open={open} onOpenChange={setOpen}>
      <Popover.Trigger asChild>{trigger}</Popover.Trigger>
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
