// The classes that implement or override the symbol, fleet-wide (C++ class
// heads and plugin registrations; impls.go).
import { useQuery } from "@tanstack/react-query";
import { Layers } from "lucide-react";
import { api, type ImplsResult } from "@/lib/api";
import { symbolHref } from "@/lib/routes";
import { shortName } from "@/lib/utils";
import { EvidenceCard, EvidenceGroup, QueryNote } from "@/components/evidence/Evidence";
import { Badge } from "@/components/ui/badge";
import { Empty, Rows } from "@/components/ui/skeleton";
import { useSymbolExplain, type TabProps } from "./data";

// the name impls asks about: a class by its name, a method as Class.method
export function implsBase(s: string): string {
  return shortName(s);
}

export default function ImplsTab({ repo, refName, s }: TabProps) {
  const ex = useSymbolExplain(repo, refName, s);
  const base = implsBase(s);
  const r = useQuery({ queryKey: ["impls", base, "", refName], queryFn: ({ signal }) => api<ImplsResult>("v2/impls", { base, ref: refName }, signal) });
  if (r.isLoading) return <Rows />;
  if (r.error) return <Empty error title="Implementations failed">{String(r.error)}</Empty>;
  const kind = ex.data?.symbol?.kind;
  if (!r.data!.impls.length)
    return (
      <Empty title={`Nothing implements or overrides ${base}`}>
        {kind === "function" ? `${shortName(s)} is a function; only a class or a virtual method has implementations. ` : ""}
        {r.data!.note}
      </Empty>
    );
  return <ImplsList r={r.data!} refName={refName} />;
}

export function ImplsList({ r, refName }: { r: ImplsResult; refName: string }) {
  return (
    <div className="space-y-4">
      <EvidenceGroup title={`Implementations of ${r.method ? r.base + "." + r.method : r.base}`} count={r.impls.length}>
        {r.impls.map((x, i) => (
          <EvidenceCard
            key={i}
            icon={<Layers className={x.test ? "text-warn" : "text-primary"} />}
            title={<span className="font-mono text-[13px]">{x.method_def || x.class}</span>}
            to={x.method_def && x.repo ? symbolHref(x.repo, refName, x.method_def) : undefined}
            repo={x.repo}
            path={x.path}
            line={x.line}
            symbol={x.method_def}
            refName={refName}
            badges={
              <>
                {x.depth > 1 && <Badge variant="outline">{x.depth} levels down</Badge>}
                {x.plugin && <Badge variant="shape">plugin</Badge>}
                {x.test && <Badge variant="warn">test</Badge>}
              </>
            }
          >
            derives from {x.bases.join(", ")}
          </EvidenceCard>
        ))}
        {!r.impls.length && <Empty title={`Nothing implements ${r.base}`} />}
      </EvidenceGroup>
      <QueryNote text={r.note} />
    </div>
  );
}
