package storage

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

// --no-tests hides test symbols, test files and test callers from every read,
// by the same rule the indexer stamps, so all commands drop the same things.
func TestNoTestsHidesTestCode(t *testing.T) {
	m := NewMem()
	m.Put("platform", "HEAD", "planner.Planner.publish", SymbolRecord{Lang: "C++"})
	m.Put("platform", "HEAD", "planner.Planner.run", SymbolRecord{Lang: "C++",
		Callees: []query.Callee{{Name: "planner.Planner.publish"}}})
	m.Put("platform", "HEAD", "planner/test.PlannerTest.run", SymbolRecord{Lang: "C++", IsTest: true,
		Callees: []query.Callee{{Name: "planner.Planner.publish"}}})
	m.Put("platform", "HEAD", "planner/test.MockPlanner.fake", SymbolRecord{Lang: "C++", IsTest: true})
	m.Put("platform", "HEAD", "planner.Planner.helper", SymbolRecord{Lang: "C++",
		Callees: []query.Callee{{Name: "planner/test.MockPlanner.fake"}}})
	m.PutFile("platform", "HEAD", query.File{Path: "planner/src/planner.cpp", Content: `pub_ = nh.advertise<S>("/robot_status", 1);`})
	m.PutFile("platform", "HEAD", query.File{Path: "planner/test/planner_test.cpp", Content: `pub_ = nh.advertise<S>("/robot_status", 1);`})

	n := &NoTests{Inner: m}
	if _, ok := n.SymbolAt("platform", "planner/test.PlannerTest.run", "HEAD"); ok {
		t.Error("a test symbol must be hidden")
	}
	if len(n.SymbolsAt("platform", "HEAD")) != 3 {
		t.Errorf("symbols: %v", n.SymbolsAt("platform", "HEAD"))
	}
	snap := n.Snapshot("platform", "HEAD")
	if _, ok := snap.Callees["planner/test.PlannerTest.run"]; ok {
		t.Error("a test caller must be dropped from the call graph")
	}
	if len(snap.Callees["planner.Planner.helper"]) != 0 {
		t.Errorf("a test callee must be dropped: %v", snap.Callees["planner.Planner.helper"])
	}
	if fs := n.Files("platform", "HEAD"); len(fs) != 1 || fs[0].Path != "planner/src/planner.cpp" {
		t.Errorf("test files must be hidden: %v", fs)
	}
	res := query.Topic(n, "platform", "HEAD", "/robot_status")
	if len(res.Groups) != 1 || len(res.Groups[0].Producers) != 1 {
		t.Fatalf("topics must drop the test publisher: %+v", res.Groups)
	}
	ctx := query.Context(n, "platform", "HEAD", "Planner.publish", true)
	if len(ctx.Callers) != 1 || ctx.Callers[0] != "planner.Planner.run" {
		t.Errorf("context callers with tests hidden: %v", ctx.Callers)
	}
	// Pins seen through the filter.
	p := &NoTests{Inner: &Pinned{Inner: m, Pins: map[string]string{"platform": "HEAD"}}}
	if query.EffectiveRef(p, "platform", "x") != "HEAD" {
		t.Error("RefFor must see through the filter")
	}
}
