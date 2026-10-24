// The Ask bar: one box for a symbol, a question, a route, a config key or a
// pasted log line. It shows which analysis the text will run as it is typed
// (lib/intent.ts) and opens the answer on the Ask page.
import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { CornerDownLeft, Sparkles } from "lucide-react";
import { ASK_EXAMPLES, INTENT_LABEL, routeIntent } from "@/lib/intent";
import { askHref } from "@/lib/routes";
import { cn } from "@/lib/utils";

export function AskBar({ initial = "", repo, examples, autoFocus, className }: { initial?: string; repo?: string; examples?: boolean; autoFocus?: boolean; className?: string }) {
  const [q, setQ] = useState(initial);
  useEffect(() => setQ(initial), [initial]);
  const nav = useNavigate();
  const input = useRef<HTMLInputElement>(null);
  const intent = useMemo(() => routeIntent(q), [q]);
  const submit = () => {
    const t = q.trim();
    if (t) nav(askHref(t, repo));
  };
  return (
    <div className={className}>
      <form
        role="search"
        onSubmit={(e) => {
          e.preventDefault();
          submit();
        }}
        className="group relative flex items-center rounded-xl border border-input bg-card shadow-sm transition-shadow focus-within:border-primary/50 focus-within:ring-4 focus-within:ring-primary/10"
      >
        <Sparkles className="pointer-events-none absolute left-3.5 size-[18px] text-primary" />
        <input
          ref={input}
          autoFocus={autoFocus}
          value={q}
          onChange={(e) => setQ(e.target.value)}
          aria-label="Ask"
          placeholder="Ask about a symbol, a route, a config key — or paste a log line"
          className="h-12 min-w-0 flex-1 bg-transparent pl-11 pr-2 text-[15px] outline-none placeholder:text-muted-foreground/80"
        />
        {intent && (
          <span data-intent={intent.kind} className="mr-2 hidden shrink-0 items-center gap-1 rounded-md bg-accent px-2 py-1 text-[11px] font-medium text-accent-foreground sm:inline-flex">
            {INTENT_LABEL[intent.kind]}
          </span>
        )}
        <button type="submit" aria-label="Ask" disabled={!q.trim()} className="mr-1.5 grid size-9 shrink-0 place-items-center rounded-lg bg-primary text-primary-foreground shadow-sm transition-opacity hover:bg-primary/90 disabled:opacity-40">
          <CornerDownLeft className="size-4" />
        </button>
      </form>
      {examples && (
        <div className="mt-2.5 flex flex-wrap items-center gap-1.5">
          <span className="text-xs text-muted-foreground">Try</span>
          {ASK_EXAMPLES.map((x) => (
            <button
              key={x}
              type="button"
              onClick={() => {
                setQ(x);
                input.current?.focus();
              }}
              className={cn("max-w-full truncate rounded-full border bg-card px-2.5 py-1 text-xs text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground", x.startsWith("[") && "font-mono text-[11px]")}
            >
              {x}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
