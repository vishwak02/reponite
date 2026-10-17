import { lazy, Suspense } from "react";
import { Route, Routes } from "react-router-dom";
import { AppShell } from "@/components/layout/AppShell";
import { Rows } from "@/components/ui/skeleton";
import Overview from "./pages/Overview";

const Repos = lazy(() => import("./pages/Repos"));
const SymbolPage = lazy(() => import("./pages/Symbol"));
const GraphPage = lazy(() => import("./pages/Graph"));
const Fleet = lazy(() => import("./pages/Fleet"));
const Topics = lazy(() => import("./pages/Topics"));
const Coverage = lazy(() => import("./pages/Coverage"));
const Links = lazy(() => import("./pages/Links"));
const Grep = lazy(() => import("./pages/Grep"));
const Diff = lazy(() => import("./pages/Diff"));
const Verify = lazy(() => import("./pages/Verify"));

export default function App() {
  return (
    <AppShell>
      <Suspense fallback={<div className="p-6"><Rows /></div>}>
        <Routes>
          <Route path="/" element={<Overview />} />
          <Route path="/repos" element={<Repos />} />
          <Route path="/symbol" element={<SymbolPage />} />
          <Route path="/graph" element={<GraphPage />} />
          <Route path="/fleet" element={<Fleet />} />
          <Route path="/topics" element={<Topics />} />
          <Route path="/coverage" element={<Coverage />} />
          <Route path="/links" element={<Links />} />
          <Route path="/grep" element={<Grep />} />
          <Route path="/diff" element={<Diff />} />
          <Route path="/verify" element={<Verify />} />
          <Route path="*" element={<Overview />} />
        </Routes>
      </Suspense>
    </AppShell>
  );
}
