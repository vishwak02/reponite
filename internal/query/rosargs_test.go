package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// A library publisher names neither its topic nor its namespace: the topic is
// a constructor argument, the node handle a pointer built by a caller as
// NodeHandle("~/" + name), and name comes from a third class constructed by
// the launched node. Following the arguments back must give the runtime name
// and link it to a literal consumer.
func TestArgTopicThroughConstructorsAndNodeHandle(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("navigation", "v1", query.File{
		Path: "costmap_2d/src/costmap_2d_publisher.cpp",
		Content: `#include <costmap_2d/costmap_2d_publisher.h>
Costmap2DPublisher::Costmap2DPublisher(ros::NodeHandle * ros_node, Costmap2D* costmap, std::string global_frame,
                                       std::string topic_name, bool always_send_full_costmap) :
    node(ros_node), costmap_(costmap)
{
  costmap_pub_ = ros_node->advertise<nav_msgs::OccupancyGrid>(topic_name, 1);
  costmap_update_pub_ = ros_node->advertise<map_msgs::OccupancyGridUpdate>(topic_name + "_updates", 1);
}
`,
		Symbols: []query.SymbolSpan{{Name: "Costmap2DPublisher.Costmap2DPublisher", StartLine: 2, EndLine: 8}},
	})
	m.PutFile("navigation", "v1", query.File{
		Path: "costmap_2d/src/costmap_2d_ros.cpp",
		Content: `#include <costmap_2d/costmap_2d_ros.h>
Costmap2DROS::Costmap2DROS(const std::string& name, tf2_ros::Buffer& tf) :
    name_(name), tf_(tf)
{
  ros::NodeHandle private_nh("~/" + name);
  publisher_ = new Costmap2DPublisher(&private_nh, layered_costmap_->getCostmap(), global_frame_, "costmap",
                                      always_send_full_costmap);
}
`,
		Symbols: []query.SymbolSpan{{Name: "Costmap2DROS.Costmap2DROS", StartLine: 2, EndLine: 8}},
	})
	m.PutFile("move_base_flex", "v1", query.File{
		Path: "mbf_costmap_nav/src/mbf_costmap_nav/costmap_wrapper.cpp",
		Content: `CostmapWrapper::CostmapWrapper(const std::string &name, const TFPtr &tf_listener_ptr) :
    costmap_2d::Costmap2DROS(name, *tf_listener_ptr)
{
}
`,
		Symbols: []query.SymbolSpan{{Name: "CostmapWrapper.CostmapWrapper", StartLine: 1, EndLine: 4}},
	})
	m.PutFile("move_base_flex", "v1", query.File{
		Path: "mbf_costmap_nav/src/mbf_costmap_nav/costmap_navigation_server.cpp",
		Content: `CostmapNavigationServer::CostmapNavigationServer(const TFPtr &tf_listener_ptr) :
    global_costmap_ptr_(new CostmapWrapper("global_costmap", tf_listener_ptr)),
    local_costmap_ptr_(new CostmapWrapper("local_costmap", tf_listener_ptr))
{
}
`,
		Symbols: []query.SymbolSpan{{Name: "CostmapNavigationServer.CostmapNavigationServer", StartLine: 1, EndLine: 5}},
	})
	m.PutFile("robot", "v1", query.File{
		Path:    "robot_nav/launch/nav.launch",
		Content: "<launch>\n  <node pkg=\"mbf_costmap_nav\" type=\"mbf_costmap_nav\" name=\"move_base_flex\"/>\n</launch>\n",
	})
	m.PutFile("robot", "v1", query.File{
		Path:    "robot_nav/src/watch.cpp",
		Content: "void Watch::init() {\n  sub_ = nh.subscribe(\"/move_base_flex/global_costmap/costmap\", 1, &Watch::cb, this);\n}\n",
	})

	res := query.CommGraph(m, query.FleetRepo, "v1")
	g := topicGroup(t, res, "move_base_flex/global_costmap/costmap")
	if len(g.Producers) != 1 || g.Producers[0].Path != "costmap_2d/src/costmap_2d_publisher.cpp" || !g.Connected() {
		t.Fatalf("the library publisher must be linked under its runtime name: %+v", g)
	}
	p := g.Producers[0]
	if !strings.HasPrefix(p.NameResolution, "args:") || !strings.Contains(p.NameResolution, "costmap_2d_ros.cpp") ||
		!strings.Contains(p.NameResolution, "node move_base_flex") {
		t.Errorf("name_resolution must cite the chain: %q", p.NameResolution)
	}
	if p.NameConfidence <= 0 || p.NameConfidence >= 0.75 {
		t.Errorf("an argument-resolved name is less certain than a literal: %.2f", p.NameConfidence)
	}
	topicGroup(t, res, "move_base_flex/local_costmap/costmap")
	topicGroup(t, res, "move_base_flex/global_costmap/costmap_updates")
	for _, g := range res.Groups {
		if g.Name == "_updates" {
			t.Errorf("a concatenation must never be linked as its literal part: %+v", g)
		}
	}
	if res.ArgResolved != 2 {
		t.Errorf("arg_resolved=%d unbound=%+v", res.ArgResolved, res.Unbound)
	}
}

// A parameter nobody passes a literal for stays unresolved, never guessed.
func TestArgTopicUnresolvedStaysUnbound(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("lib", "v1", query.File{
		Path:    "pub/src/pub.cpp",
		Content: "void Pub::init(ros::NodeHandle& nh, const std::string& topic) {\n  pub_ = nh.advertise<std_msgs::String>(topic, 1);\n}\n",
		Symbols: []query.SymbolSpan{{Name: "Pub.init", StartLine: 1, EndLine: 3}},
	})
	m.PutFile("lib", "v1", query.File{
		Path:    "pub/src/main.cpp",
		Content: "int main() {\n  Pub p;\n  p.init(nh, readTopicFromSomewhere());\n}\n",
		Symbols: []query.SymbolSpan{{Name: "main", StartLine: 1, EndLine: 4}},
	})
	res := query.CommGraph(m, query.FleetRepo, "v1")
	if res.ArgResolved != 0 || len(res.Unbound) != 1 {
		t.Fatalf("arg_resolved=%d unbound=%+v groups=%+v", res.ArgResolved, res.Unbound, res.Groups)
	}
}
