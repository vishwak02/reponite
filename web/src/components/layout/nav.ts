import { Boxes, Braces, FileStack, GitCompare, LayoutDashboard, Link2, Network, Radio, Share2, ShieldCheck, TextSearch } from "lucide-react";

export const NAV = [
  {
    label: "Fleet",
    items: [
      { to: "/", label: "Overview", icon: LayoutDashboard, hint: "repos, refs, size" },
      { to: "/fleet", label: "Fleet map", icon: Share2, hint: "who imports whom" },
      { to: "/topics", label: "Topics", icon: Radio, hint: "ROS pub/sub, services" },
      { to: "/coverage", label: "Coverage", icon: FileStack, hint: "what was indexed" },
    ],
  },
  {
    label: "Repo",
    items: [
      { to: "/symbol", label: "Symbol", icon: Braces, hint: "brief, code, callers" },
      { to: "/graph", label: "Call graph", icon: Network, hint: "neighborhood" },
      { to: "/links", label: "Links", icon: Link2, hint: "file references" },
      { to: "/grep", label: "Grep", icon: TextSearch, hint: "search the source" },
      { to: "/diff", label: "Diff & impact", icon: GitCompare, hint: "between refs" },
      { to: "/verify", label: "Verify an edit", icon: ShieldCheck, hint: "before you change code" },
      { to: "/repos", label: "Repos", icon: Boxes, hint: "all repos" },
    ],
  },
];
