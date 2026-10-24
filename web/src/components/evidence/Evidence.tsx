// Evidence: one place in the code an answer rests on — what it is, where it
// is (file:line, linking into the view that shows it), how sure the index is,
// and the line itself.
import type { ReactNode } from "react";
import { Link } from "react-router-dom";
import { FileCode2 } from "lucide-react";
import { askHref, symbolHref } from "@/lib/routes";
import { useScope } from "@/lib/scope";
import { cn, tint } from "@/lib/utils";

export function RepoChip({ repo, className }: { repo: string; className?: string }) {
  const { dark } = useScope();
  return (
    <span className={cn("shrink-0 rounded px-1.5 py-0.5 text-[11px] font-medium", className)} style={{ color: tint(repo, dark ? 70 : 38), background: tint(repo, 55).replace(")", dark ? " / .16)" : " / .12)") }}>
      {repo}
    </span>
  );
}

// Loc is a file:line that opens where it points: the symbol's code when the
// enclosing symbol is known, else what the index says is at that line.
export function Loc({ repo, path, line, symbol, refName = "HEAD" }: { repo?: string; path: string; line?: number; symbol?: string; refName?: string }) {
  const at = line ? `${path}:${line}` : path;
  const to = symbol && repo ? symbolHref(repo, refName, symbol, "code") : askHref(at, repo);
  return (
    <Link to={to} className="inline-flex min-w-0 max-w-full items-center gap-1 font-mono text-[11.5px] text-muted-foreground hover:text-primary hover:underline" title={at}>
      <FileCode2 className="size-3 shrink-0" />
      <span className="truncate">{at}</span>
    </Link>
  );
}

export function Snippet({ text, line, className }: { text: string; line?: number; className?: string }) {
  const lines = text.replace(/\s+$/, "").split("\n");
  return (
    <pre className={cn("overflow-x-auto rounded-md bg-muted/60 py-1.5 font-mono text-[11.5px] leading-relaxed", className)}>
      {lines.map((l, i) => (
        <div key={i} className="flex px-2">
          {line != null && <span className="mr-3 w-8 shrink-0 select-none text-right tabular-nums text-muted-foreground/60">{line + i}</span>}
          <span className="whitespace-pre">{l || " "}</span>
        </div>
      ))}
    </pre>
  );
}

export interface EvidenceProps {
  icon?: ReactNode;
  title: ReactNode;
  // the view the title opens
  to?: string;
  repo?: string;
  path?: string;
  line?: number;
  // the enclosing symbol, when known: the location opens its code
  symbol?: string;
  refName?: string;
  badges?: ReactNode;
  snippet?: string;
  snippetLine?: number;
  children?: ReactNode;
  className?: string;
}

export function EvidenceCard({ icon, title, to, repo, path, line, symbol, refName, badges, snippet, snippetLine, children, className }: EvidenceProps) {
  return (
    <div data-evidence className={cn("group rounded-xl border bg-card px-4 py-3 shadow-[0_1px_2px_rgb(0_0_0/0.04)] transition-colors hover:border-primary/30", className)}>
      <div className="flex min-w-0 items-center gap-2">
        {icon && <span className="shrink-0 text-muted-foreground [&_svg]:size-4">{icon}</span>}
        <div className="min-w-0 flex-1 truncate text-sm font-medium">
          {to ? (
            <Link to={to} className="hover:text-primary hover:underline">
              {title}
            </Link>
          ) : (
            title
          )}
        </div>
        <div className="flex shrink-0 items-center gap-1.5">{badges}</div>
      </div>
      {(repo || path) && (
        <div className="mt-1 flex min-w-0 items-center gap-2">
          {repo && <RepoChip repo={repo} />}
          {path && <Loc repo={repo} path={path} line={line} symbol={symbol} refName={refName} />}
        </div>
      )}
      {snippet && <Snippet text={snippet} line={snippetLine} className="mt-2" />}
      {children && <div className="mt-2 text-xs text-muted-foreground">{children}</div>}
    </div>
  );
}

// A section of an answer: a heading, a count, and its evidence.
export function EvidenceGroup({ title, count, sub, children }: { title: ReactNode; count?: number; sub?: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <div className="flex items-baseline gap-2 px-0.5">
        <h2 className="text-[13px] font-semibold">{title}</h2>
        {count != null && <span className="text-xs tabular-nums text-muted-foreground">{count}</span>}
        {sub && <span className="truncate text-xs text-muted-foreground">{sub}</span>}
      </div>
      <div className="space-y-2">{children}</div>
    </section>
  );
}

// A query's note, with the list of repos a fleet-wide answer read folded into
// a count (the full list is in the tooltip).
export function QueryNote({ text, className }: { text?: string; className?: string }) {
  if (!text) return null;
  const m = /^(.*?)\s*—\s*read \[([^\]]*)\]\s*$/s.exec(text);
  const main = m ? m[1] : text;
  const read = m ? m[2].split(/\s+/).filter(Boolean) : [];
  return (
    <p className={cn("rounded-lg border border-dashed px-3 py-2 text-xs leading-relaxed text-muted-foreground", className)}>
      {main}
      {read.length > 0 && (
        <span title={read.join("\n")} className="ml-1 cursor-help whitespace-nowrap underline decoration-dotted underline-offset-2">
          read {read.length === 1 ? read[0] : `${read.length} repos`}
        </span>
      )}
    </p>
  );
}
