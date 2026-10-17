import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { HashRouter } from "react-router-dom";
import { ScopeProvider } from "@/lib/scope";
import { TooltipProvider } from "@/components/ui/tooltip";
import App from "./App";
import "./index.css";

const qc = new QueryClient({
  defaultOptions: { queries: { retry: 1, refetchOnWindowFocus: false, staleTime: 30_000 } },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={qc}>
      <HashRouter>
        <ScopeProvider>
          <TooltipProvider>
            <App />
          </TooltipProvider>
        </ScopeProvider>
      </HashRouter>
    </QueryClientProvider>
  </StrictMode>,
);
