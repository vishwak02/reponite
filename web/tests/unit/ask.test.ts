// The Ask bar's intent router, symbol picking, the provenance tiers, the
// "since last index" ref pair and call-site marking.
import { describe, expect, it } from "vitest";
import { ASK_EXAMPLES, nameScore, pickSymbol, routeIntent } from "@/lib/intent";
import { tierOf } from "@/lib/provenance";
import { compareVersions, isBranch, latestPair } from "@/lib/refs";
import { calledName, callSites } from "@/lib/callsites";

const kind = (q: string) => routeIntent(q)?.kind;
const arg = (q: string) => routeIntent(q)?.arg;

describe("routeIntent", () => {
  it("reads nothing from an empty box", () => {
    expect(routeIntent("")).toBeNull();
    expect(routeIntent("   ")).toBeNull();
  });
  it("sends a bare name or qualified id to the symbol hub", () => {
    for (const q of ["Charge", "orders.Charge", "Planner::plan", "pkg/sub.Type.method", "  validateCard  ", "`Charge`", "Charge()"]) expect(kind(q), q).toBe("symbol");
    expect(arg("`Charge`")).toBe("Charge");
    expect(arg("Charge()")).toBe("Charge");
  });
  it("sends a path to explain", () => {
    expect(routeIntent("orders/orders.go")).toEqual({ kind: "explain", arg: "orders/orders.go" });
    expect(routeIntent("orders/orders.go:21")).toEqual({ kind: "explain", arg: "orders/orders.go:21" });
    expect(kind("src/planner/")).toBe("explain");
  });
  it("reads impact questions as blast radius", () => {
    for (const q of ["what breaks if I change Charge", "What would break if I rename Charge?", "blast radius of Charge", "impact of changing Charge", "if I change Charge what happens"]) {
      expect(kind(q), q).toBe("impact");
      expect(arg(q), q).toBe("Charge");
    }
  });
  it("reads caller questions", () => {
    for (const q of ["who calls validateCard", "callers of validateCard", "where is validateCard used?", "who uses `validateCard`"]) {
      expect(kind(q), q).toBe("callers");
      expect(arg(q), q).toBe("validateCard");
    }
  });
  it("reads route questions with and without a method", () => {
    expect(routeIntent("what runs on POST /api/orders")).toEqual({ kind: "route", arg: "/api/orders", method: "POST" });
    expect(routeIntent("GET /health")).toEqual({ kind: "route", arg: "/health", method: "GET" });
    expect(routeIntent("what handles /api/orders/{id}?")).toEqual({ kind: "route", arg: "/api/orders/{id}", method: "" });
    expect(routeIntent("/api/orders")).toEqual({ kind: "route", arg: "/api/orders", method: "" });
  });
  it("reads a pasted log line before anything else in it", () => {
    for (const q of [
      "[ERROR] charging order a1 failed",
      "[ INFO] [1690000000.123]: GET /api/orders 200",
      "2026-10-24 10:01:02,123 WARNING retrying",
      "E1024 10:01:02.123456 planner.cc:42] no path",
      "ERROR: card declined for order 7",
    ])
      expect(kind(q), q).toBe("logline");
    expect(arg("[ERROR] charging order a1 failed")).toBe("[ERROR] charging order a1 failed");
  });
  it("reads config keys", () => {
    for (const [q, k] of [
      ["where does max_speed come from", "max_speed"],
      ["where does the param `robot.max_speed` come from?", "robot.max_speed"],
      ["where is BILLING_LIMIT set", "BILLING_LIMIT"],
      ["who sets max_speed", "max_speed"],
      ["config: max_speed", "max_speed"],
    ])
      expect(routeIntent(q), q).toEqual({ kind: "config", arg: k });
  });
  it("reads error messages and types", () => {
    expect(routeIntent('where does "card declined" come from')).toEqual({ kind: "errors", arg: "card declined" });
    expect(routeIntent("error: card declined")).toEqual({ kind: "errors", arg: "card declined" });
    expect(routeIntent("who throws ErrDeclined")).toEqual({ kind: "errors", arg: "ErrDeclined" });
  });
  it("reads tests, implementations, history and explain questions", () => {
    expect(routeIntent("which tests cover Charge")).toEqual({ kind: "tests", arg: "Charge" });
    expect(routeIntent("tests for orders.Charge")).toEqual({ kind: "tests", arg: "orders.Charge" });
    expect(routeIntent("what implements Gateway")).toEqual({ kind: "impls", arg: "Gateway" });
    expect(routeIntent("implementations of Gateway::pay")).toEqual({ kind: "impls", arg: "Gateway::pay" });
    expect(routeIntent("who changed Charge")).toEqual({ kind: "history", arg: "Charge" });
    expect(routeIntent("history of Charge")).toEqual({ kind: "history", arg: "Charge" });
    expect(routeIntent("what does Charge do?")).toEqual({ kind: "explain", arg: "Charge" });
    expect(routeIntent("explain orders/orders.go")).toEqual({ kind: "explain", arg: "orders/orders.go" });
  });
  it("sends everything else to investigate, whole", () => {
    for (const q of ["how is an order charged", "why does the robot stop at the charger", "what is the order flow"]) expect(routeIntent(q), q).toEqual({ kind: "investigate", arg: q });
  });
  it("routes each suggested example to a specific analysis", () => {
    const kinds = ASK_EXAMPLES.map(kind);
    expect(kinds).toEqual(["impact", "callers", "route", "config", "logline", "investigate"]);
  });
});

describe("pickSymbol", () => {
  const hits = [
    { repo: "b", name: "billing.ChargeAll" },
    { repo: "a", name: "orders.TestCharge", is_test: true },
    { repo: "a", name: "orders.Charge" },
  ];
  it("ranks the exact last segment first, production before tests", () => {
    expect(nameScore("orders.Charge", "Charge")).toBe(1);
    expect(nameScore("orders.Charge", "orders.Charge")).toBe(0);
    expect(nameScore("pkg/sub.Order.Charge", "Order.Charge")).toBe(1);
    expect(nameScore("ns::Planner::plan", "Planner::plan")).toBe(1);
    expect(nameScore("billing.ChargeAll", "Charge")).toBe(3);
    const r = pickSymbol(hits, "Charge");
    expect(r.ranked[0].name).toBe("orders.Charge");
    expect(r.unique).toBe(true);
  });
  it("is not unique when two repos define the name", () => {
    const r = pickSymbol([...hits, { repo: "c", name: "pay.Charge" }], "Charge", "c");
    expect(r.unique).toBe(false);
    expect(r.ranked[0]).toEqual({ repo: "c", name: "pay.Charge" }); // the preferred repo first
  });
  it("finds nothing in no hits", () => {
    expect(pickSymbol([], "x")).toEqual({ ranked: [], unique: false });
  });
});

describe("tierOf", () => {
  it("maps compiler methods to compiler-proven", () => {
    expect(tierOf("go-types", 1)).toBe("proven");
    expect(tierOf("clang-compdb", 1)).toBe("proven");
    expect(tierOf("clang-synth", 0.97)).toBe("proven");
    expect(tierOf("go-types")).toBe("proven");
  });
  it("demotes a compiler method the indexer doubts", () => {
    expect(tierOf("clang-compdb", 0.9)).toBe("typed");
  });
  it("maps declared-type and import resolution to typed", () => {
    expect(tierOf("receiver-typed", 0.85)).toBe("typed");
    expect(tierOf("scip-resolved", 0.95)).toBe("typed");
    expect(tierOf("import-resolved", 0.75)).toBe("typed");
  });
  it("maps the only visible definition of a name to a name match", () => {
    expect(tierOf("name-resolved", 0.9)).toBe("named");
  });
  it("maps member-name matches, overrides and external leaves to guessed", () => {
    expect(tierOf("member-name-resolved", 0.7)).toBe("guessed");
    expect(tierOf("virtual-override", 0.6)).toBe("guessed");
    expect(tierOf("unresolved-external", 0.6)).toBe("guessed");
  });
  it("maps several candidates, or a low confidence, to ambiguous", () => {
    expect(tierOf("ambiguous", 0.5)).toBe("ambiguous");
    expect(tierOf("name-resolved", 0.4)).toBe("ambiguous");
  });
  it("places an unknown method by its confidence", () => {
    expect(tierOf("future-method", 0.9)).toBe("typed");
    expect(tierOf("future-method", 0.6)).toBe("guessed");
    expect(tierOf(undefined, undefined)).toBe("guessed");
  });
});

describe("latestPair", () => {
  it("compares the working ref with the newest version", () => {
    expect(latestPair(["HEAD", "v1"])).toEqual(["v1", "HEAD"]);
    expect(latestPair(["v1.9.0", "v1.10.0", "v1.2.0"])).toEqual(["v1.9.0", "v1.10.0"]);
    expect(latestPair(["origin/main", "v2.0.0", "v1.0.0"])).toEqual(["v2.0.0", "origin/main"]);
    expect(latestPair(["HEAD", "origin/devel", "v3"])).toEqual(["origin/devel", "HEAD"]);
    expect(latestPair(["e81d2bac4f20", "v1"])).toEqual(["e81d2bac4f20", "v1"]);
  });
  it("needs two refs; a pinned HEAD is not one", () => {
    expect(latestPair(["origin/devel"])).toBeNull();
    expect(latestPair([])).toBeNull();
    expect(latestPair(["HEAD", "origin/devel"], true)).toBeNull();
  });
  it("orders versions by number and releases after their candidates", () => {
    expect(compareVersions("v1.10", "v1.9")).toBeGreaterThan(0);
    expect(compareVersions("1.0.0", "1.0.0-rc1")).toBeGreaterThan(0);
    expect(compareVersions("v2", "v2")).toBe(0);
    expect(isBranch("HEAD")).toBe(true);
    expect(isBranch("origin/devel")).toBe(true);
    expect(isBranch("v1.2.3")).toBe(false);
    expect(isBranch("e81d2bac4f20")).toBe(false);
  });
});

describe("callSites", () => {
  const body = "func Charge(o Order) error {\n\tlog.Printf(\"charging %s\", o.ID)\n\tif err := validateCard(o); err != nil {\n\t\treturn err\n\t}\n\treturn save(o)\n}";
  const callees = [
    { symbol: "orders.save", resolution_method: "go-types", confidence: 1 },
    { symbol: "orders.validateCard", resolution_method: "go-types", confidence: 1 },
    { symbol: "Printf", resolution_method: "unresolved-external", confidence: 0.6 },
  ];
  it("marks each line that calls a callee, by the edge's tier", () => {
    const s = callSites(body, 20, callees);
    expect(s.map((x) => [x.line, x.tier])).toEqual([
      [21, "guessed"],
      [22, "proven"],
      [25, "proven"],
    ]);
    expect(s[1].title).toContain("orders.validateCard");
  });
  it("keeps to the symbol's lines and ignores a name that is not called", () => {
    expect(callSites(body, 20, callees, 22, 23).map((x) => x.line)).toEqual([22]);
    expect(callSites("x := saveAll(o)\nsaver(o)", 1, callees)).toEqual([]);
  });
  it("marks a line with several calls by its least sure edge", () => {
    expect(callSites("save(validateCard(o)); log.Printf(x)", 1, callees)[0].tier).toBe("guessed");
  });
  it("reads the called name from a qualified id", () => {
    expect(calledName("ns::Planner::plan")).toBe("plan");
    expect(calledName("pkg/sub.Type.method")).toBe("method");
    expect(calledName("save()")).toBe("save");
  });
});
