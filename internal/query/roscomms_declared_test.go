package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// A launched third-party node whose source is not indexed is still wired to a
// topic by its <remap>: the binding must surface as launch_declared — with a
// leading namespace substitution read as the root, labeled — instead of the
// topic looking producer-less.
func TestTopicLaunchDeclaredBinding(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("tools", "HEAD", query.File{Path: "viewer/launch/view.launch", Content: `<launch>
  <arg name="ns" value="$(eval ns.strip('/'))"/>
  <node pkg="mbf_costmap_nav" type="mbf_costmap_nav" name="move_base_flex">
    <remap from="~global_costmap/costmap" to="$(arg ns)move_base_flex/global_costmap/costmap"/>
    <remap from="odom" to="/odom"/>
  </node>
</launch>`})
	res := query.Topic(m, query.FleetRepo, "HEAD", "/move_base_flex/global_costmap/costmap")
	if len(res.Groups) != 1 {
		t.Fatalf("want the declared binding as a group, got %+v (note %s)", res.Groups, res.Note)
	}
	g := res.Groups[0]
	if len(g.Declared) != 1 || len(g.Producers) != 0 {
		t.Fatalf("want one launch_declared endpoint and no source producer, got %+v", g)
	}
	d := g.Declared[0]
	if d.Role != query.RoleLaunchDeclared || d.Raw != "~global_costmap/costmap" || !strings.Contains(d.In, "mbf_costmap_nav") {
		t.Fatalf("declared endpoint should cite the node and the source-side name, got %+v", d)
	}
	if !strings.Contains(d.NameResolution, "namespace prefix") {
		t.Fatalf("an unexpanded leading prefix must be labeled, got %q", d.NameResolution)
	}
	if !strings.Contains(res.Note, "launch-declared") {
		t.Fatalf("note should explain launch-declared bindings: %s", res.Note)
	}
}

// A remap already explained by an indexed source endpoint is not duplicated as
// a declaration.
func TestLaunchDeclaredNotDuplicated(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("nav", "HEAD", query.File{Path: "scan_filter/src/filter.cpp",
		Content: "void F::init() {\n  sub_ = nh.subscribe(\"scan\", 1, &F::cb, this);\n}\n",
		Symbols: []query.SymbolSpan{{Name: "F::init", StartLine: 1, EndLine: 3}}})
	m.PutFile("nav", "HEAD", query.File{Path: "scan_filter/launch/f.launch", Content: `<launch>
  <node pkg="scan_filter" type="filter" name="filter"><remap from="scan" to="raw_scan_front"/></node>
</launch>`})
	res := query.Topic(m, query.FleetRepo, "HEAD", "raw_scan_front")
	if len(res.Groups) != 1 || len(res.Groups[0].Consumers) != 1 || len(res.Groups[0].Declared) != 0 {
		t.Fatalf("remapped source endpoint only, no duplicate declaration: %+v", res.Groups)
	}
}

// rviz displays are consumers and rviz tools producers of the topics they name.
func TestTopicReadsRvizConfig(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("bringup", "HEAD", query.File{Path: "config/rviz/nav.rviz", Content: `Visualization Manager:
  Displays:
    - Alpha: 0.7
      Class: rviz/Map
      Color Scheme: costmap
      Enabled: true
      Name: Costmap
      Topic: /move_base_flex/global_costmap/costmap
    - Class: rviz/MarkerArray
      Marker Topic: /nav_server/debug_markers
      Name: NAS Markers
  Tools:
    - Class: rviz/SetInitialPose
      Topic: /initialpose
  Views:
    Current:
      Class: rviz/Orbit
`})
	res := query.Topic(m, query.FleetRepo, "HEAD", "move_base_flex/global_costmap/costmap")
	if len(res.Groups) != 1 || len(res.Groups[0].Consumers) != 1 {
		t.Fatalf("rviz Map display should be a consumer, got %+v", res.Groups)
	}
	c := res.Groups[0].Consumers[0]
	if c.MsgType != "rviz/Map" || !strings.Contains(c.In, "Costmap") || c.Line != 8 {
		t.Fatalf("display class, name and line should be cited, got %+v", c)
	}
	if r := query.Topic(m, query.FleetRepo, "HEAD", "nav_server/debug_markers"); len(r.Groups) != 1 || len(r.Groups[0].Consumers) != 1 {
		t.Fatalf("Marker Topic should be read too, got %+v", r.Groups)
	}
	if r := query.Topic(m, query.FleetRepo, "HEAD", "initialpose"); len(r.Groups) != 1 || len(r.Groups[0].Producers) != 1 {
		t.Fatalf("a tool topic is published by rviz, got %+v", r.Groups)
	}
}

// An error name defined only in a JSON catalogue is reported as data, with the
// code that loads the catalogue.
func TestErrorsDefinedAsData(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("app", "HEAD", query.File{Path: "bringup/errors.json", Content: `[
  {
    "code": 55,
    "name": "PLANNER_BLOCKED_GOAL",
    "en": "Cannot reach the goal"
  }
]`})
	m.PutFile("app", "HEAD", query.File{Path: "app/errors.py",
		Content: "import json\nERRORS = json.load(open('errors.json'))\n"})
	res := query.Errors(m, query.FleetRepo, "HEAD", "PLANNER_BLOCKED_GOAL", 0)
	if len(res.Data) != 1 {
		t.Fatalf("want the catalogue line as data, got %+v (note %s)", res.Data, res.Note)
	}
	d := res.Data[0]
	if d.Key != "name" || d.Line != 4 || len(d.LoadedBy) != 1 || !strings.HasPrefix(d.LoadedBy[0], "app:app/errors.py:2") {
		t.Fatalf("key, line and loader should be cited, got %+v", d)
	}
	if !strings.Contains(strings.Join(d.Context, " "), `"code": 55`) {
		t.Fatalf("sibling keys (the code) should be in context, got %v", d.Context)
	}
	if !strings.Contains(res.Note, "DEFINED as data") {
		t.Fatalf("note should say it is defined as data: %s", res.Note)
	}
}
