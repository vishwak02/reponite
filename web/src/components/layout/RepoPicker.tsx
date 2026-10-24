// A searchable repo picker: the fleet runs to a hundred-plus repos, too many
// for a plain select. The list (a popover over cmdk) loads the first time the
// button is pointed at, focused or clicked, so it stays out of the app shell.
import { lazy, Suspense, useState } from "react";
import { ChevronsUpDown } from "lucide-react";
import { useScope } from "@/lib/scope";
import { tint } from "@/lib/utils";

const loadMenu = () => import("./RepoMenu");
const RepoMenu = lazy(loadMenu);

export function RepoPicker() {
  const { repo } = useScope();
  const [open, setOpen] = useState(false);
  const [wanted, setWanted] = useState(false);
  const want = () => {
    loadMenu();
    setWanted(true);
  };
  const button = (loaded: boolean) => (
    <button
      onPointerEnter={loaded ? undefined : want}
      onFocus={loaded ? undefined : want}
      onClick={
        loaded
          ? undefined
          : () => {
              want();
              setOpen(true);
            }
      }
      aria-label="Choose repository"
      className="flex h-8 min-w-[180px] max-w-[260px] items-center gap-2 rounded-md border border-input bg-card px-2.5 text-sm shadow-sm hover:bg-muted"
    >
      <span className="size-2 shrink-0 rounded-full" style={{ background: repo ? tint(repo) : undefined }} />
      <span className="truncate font-medium">{repo || "Select repo"}</span>
      <ChevronsUpDown className="ml-auto size-3.5 shrink-0 text-muted-foreground" />
    </button>
  );
  if (!wanted) return button(false);
  return (
    <Suspense fallback={button(false)}>
      <RepoMenu open={open} setOpen={setOpen} trigger={button(true)} />
    </Suspense>
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
