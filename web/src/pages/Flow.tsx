// Flow: what happens when an entry point runs, across repos. The swimlane
// view arrives with the flow pages; until then this page names the entry and
// links to what the index already answers about it.
import { Link, useSearchParams } from "react-router-dom";
import { ArrowRight, Workflow } from "lucide-react";
import { askHref } from "@/lib/routes";
import { Page } from "@/components/layout/AppShell";
import { Card } from "@/components/ui/card";

export default function FlowPage() {
  const [sp] = useSearchParams();
  const entry = sp.get("entry") ?? "";
  return (
    <Page title="Flow" sub="What runs when an entry point is called: page, route, handler and side effects, one lane per repo.">
      <Card className="max-w-2xl p-6">
        <div className="flex items-center gap-3">
          <div className="grid size-10 place-items-center rounded-xl bg-accent text-accent-foreground">
            <Workflow className="size-5" />
          </div>
          <div className="min-w-0">
            <div className="text-sm font-semibold">The flow view is not built yet</div>
            <div className="text-xs text-muted-foreground">It arrives with the swimlane sequence view.</div>
          </div>
        </div>
        {entry && (
          <div className="mt-5 space-y-2">
            <div className="text-xs text-muted-foreground">Entry point</div>
            <code className="block rounded-md bg-muted px-3 py-2 font-mono text-sm">{entry}</code>
            <Link to={askHref(entry.includes(" ") ? `what runs on ${entry}` : entry)} className="inline-flex items-center gap-1 pt-2 text-sm font-medium text-primary hover:underline">
              Its route, handler and clients <ArrowRight className="size-3.5" />
            </Link>
          </div>
        )}
      </Card>
    </Page>
  );
}
