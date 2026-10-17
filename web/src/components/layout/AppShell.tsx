import { useState, type ReactNode } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { Command as CmdIcon, Moon, Search, Sun } from "lucide-react";
import { useScope } from "@/lib/scope";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Tip } from "@/components/ui/tooltip";
import { CommandPalette } from "./CommandPalette";
import { NAV } from "./nav";
import { RefPicker, RepoPicker } from "./RepoPicker";

function Logo() {
  return (
    <div className="flex items-center gap-2 px-2">
      <div className="grid size-7 place-items-center rounded-lg bg-gradient-to-br from-primary to-shape text-primary-foreground shadow-sm">
        <svg viewBox="0 0 24 24" className="size-4" fill="none" stroke="currentColor" strokeWidth="2.4" strokeLinecap="round">
          <circle cx="6" cy="6" r="2.2" />
          <circle cx="18" cy="12" r="2.2" />
          <circle cx="6" cy="18" r="2.2" />
          <path d="M8 7l8 4M8 17l8-4" />
        </svg>
      </div>
      <span className="text-[15px] font-semibold tracking-tight">reponite</span>
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const { dark, toggleTheme, repos } = useScope();
  const [palette, setPalette] = useState(false);
  const loc = useLocation();
  const mac = navigator.platform.toLowerCase().includes("mac");
  return (
    <div className="flex h-full">
      <aside className="hidden w-[232px] shrink-0 flex-col border-r bg-panel md:flex">
        <div className="flex h-14 items-center border-b px-3">
          <Logo />
        </div>
        <nav className="flex-1 space-y-5 overflow-y-auto px-2.5 py-4">
          {NAV.map((g) => (
            <div key={g.label}>
              <div className="mb-1 px-2 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/80">{g.label}</div>
              {g.items.map((i) => (
                <NavLink
                  key={i.to}
                  to={i.to + (i.to === "/symbol" || i.to === "/graph" ? loc.search : "")}
                  end={i.to === "/"}
                  className={({ isActive }) =>
                    cn(
                      "flex items-center gap-2.5 rounded-md px-2 py-1.5 text-[13px] font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground",
                      isActive && "bg-accent text-accent-foreground hover:bg-accent hover:text-accent-foreground",
                    )
                  }
                >
                  <i.icon className="size-4" />
                  {i.label}
                </NavLink>
              ))}
            </div>
          ))}
        </nav>
        <div className="border-t px-4 py-3 text-[11px] text-muted-foreground">
          {repos.length} repos indexed · read-only
        </div>
      </aside>
      <div className="flex min-w-0 flex-1 flex-col">
        <header className="flex h-14 shrink-0 items-center gap-2 border-b bg-panel/80 px-4 backdrop-blur">
          <div className="md:hidden">
            <Logo />
          </div>
          <RepoPicker />
          <RefPicker />
          <button
            onClick={() => setPalette(true)}
            className="ml-auto flex h-8 w-full max-w-[340px] items-center gap-2 rounded-md border border-input bg-card px-2.5 text-sm text-muted-foreground shadow-sm hover:bg-muted"
          >
            <Search className="size-3.5" />
            <span className="truncate">Search symbols…</span>
            <kbd className="kbd ml-auto flex items-center gap-0.5">{mac ? <CmdIcon className="size-3" /> : "Ctrl"}K</kbd>
          </button>
          <Tip label={dark ? "Light theme" : "Dark theme"}>
            <Button variant="ghost" size="icon" onClick={toggleTheme} aria-label="Toggle theme">
              {dark ? <Sun /> : <Moon />}
            </Button>
          </Tip>
        </header>
        <main className="min-h-0 flex-1 overflow-y-auto">{children}</main>
      </div>
      <CommandPalette open={palette} setOpen={setPalette} />
    </div>
  );
}

export function Page({ title, sub, actions, children, wide }: { title: ReactNode; sub?: ReactNode; actions?: ReactNode; children: ReactNode; wide?: boolean }) {
  return (
    <div className={cn("mx-auto px-6 py-6 animate-fade-in", wide ? "max-w-none" : "max-w-[1280px]")}>
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
        <div className="min-w-0">
          <h1 className="truncate text-xl font-semibold tracking-tight">{title}</h1>
          {sub && <div className="mt-1 text-sm text-muted-foreground">{sub}</div>}
        </div>
        {actions && <div className="flex items-center gap-2">{actions}</div>}
      </div>
      {children}
    </div>
  );
}
