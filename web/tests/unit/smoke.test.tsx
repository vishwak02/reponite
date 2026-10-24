// Component smoke tests: the shell and each page render against a stubbed
// API without throwing, and a page's first data request goes out.
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router-dom";
import { ScopeProvider } from "@/lib/scope";
import { TooltipProvider } from "@/components/ui/tooltip";
import App from "@/App";

const answers: Record<string, unknown> = {
  repos: { repos: ["shop"] },
  overview: { repos: [{ repo: "shop", module: "example.com/shop", refs: [{ ref: "HEAD", symbols: 18, edges: 33, files: 7 }] }] },
  refs: { repo: "shop", refs: ["HEAD", "v1"] },
  fleetmap: { repos: [{ repo: "shop", symbols: 18, files: 7, languages: ["go:4"] }], links: [] },
  coverage: { repos: [{ repo: "shop", ref: "HEAD", stored: 7, skipped: 0, kinds: [], skips_recorded: true }] },
  topics: { groups: [], endpoints: 0 },
  links: { target: "", refers_to: [], referred_by: [] },
  brief: { symbol: "orders.Charge", ref: "HEAD", target: { name: "Charge", path: "orders/orders.go", start_line: 20, end_line: 26 }, callers: [], callees: [] },
};

function stubFetch() {
  const seen: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (u: string) => {
      const ep = u.replace(/^\/api\//, "").split("?")[0];
      seen.push(ep);
      return new Response(JSON.stringify(answers[ep] ?? {}), { status: 200 });
    }),
  );
  return seen;
}

function renderAt(path: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[path]}>
        <ScopeProvider>
          <TooltipProvider>
            <App />
          </TooltipProvider>
        </ScopeProvider>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  vi.stubGlobal("matchMedia", (q: string) => ({ matches: false, media: q, addEventListener() {}, removeEventListener() {} }));
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe("app shell", () => {
  it("renders the navigation and the overview", async () => {
    const seen = stubFetch();
    renderAt("/");
    expect(screen.getAllByText("Overview").length).toBeGreaterThan(0);
    expect(screen.getByText("Call graph")).toBeTruthy();
    await waitFor(() => expect(seen).toContain("repos"));
    expect(seen).toContain("overview");
  });
});

describe("pages", () => {
  for (const [path, heading, call] of [
    ["/repos", "Repos", "fleetmap"],
    ["/coverage", "Index coverage", "coverage"],
    ["/grep", "Grep", null],
    ["/diff", "Diff", null],
    ["/verify", "Verify an edit", null],
    ["/symbol", "Symbol", null],
    ["/symbol?repo=shop&ref=HEAD&s=orders.Charge", "Charge", "brief"],
    ["/graph", "Call graph", null],
  ] as const) {
    it(`${path} renders`, async () => {
      const seen = stubFetch();
      renderAt(path);
      await waitFor(() => expect(screen.getAllByRole("heading", { name: new RegExp(heading) }).length).toBeGreaterThan(0), { timeout: 3000 });
      if (call) await waitFor(() => expect(seen).toContain(call));
    });
  }
});
