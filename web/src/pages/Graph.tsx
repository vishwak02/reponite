import { lazy, Suspense } from "react";
import { Link } from "react-router-dom";
import { Braces } from "lucide-react";
import { shortName } from "@/lib/utils";
import { Page } from "@/components/layout/AppShell";
import { symbolHref } from "@/components/layout/CommandPalette";
import { Empty, Skeleton } from "@/components/ui/skeleton";
import { useSymbolParams } from "./Symbol";

const CallGraph = lazy(() => import("@/components/graph/CallGraph"));

export default function GraphPage() {
  const { repo, ref, s } = useSymbolParams();
  return (
    <Page
      wide
      title={s ? <>Call graph · {shortName(s)}</> : "Call graph"}
      sub={s ? <>Callers flow in from the left, callees out to the right. Double-click a node to expand it; click to inspect.</> : undefined}
      actions={s && <Link to={symbolHref(repo, ref, s)} className="flex items-center gap-1 text-sm font-medium text-primary hover:underline"><Braces className="size-4" />Symbol page</Link>}
    >
      {s ? (
        <Suspense fallback={<Skeleton className="h-[calc(100vh-180px)]" />}>
          <CallGraph repo={repo} refName={ref} symbol={s} height="calc(100vh - 180px)" />
        </Suspense>
      ) : (
        <Empty title="Pick a symbol to draw">Press ⌘K and choose a symbol, or open one from the Symbol page and switch to its Call graph tab.</Empty>
      )}
    </Page>
  );
}
