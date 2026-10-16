package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func topicGroup(t *testing.T, res query.CommGraphResult, name string) query.CommGroup {
	t.Helper()
	for _, g := range res.Groups {
		if g.Family == "topic" && g.Name == name {
			return g
		}
	}
	t.Fatalf("no topic group %q; groups=%+v unbound=%+v", name, res.Groups, res.Unbound)
	return query.CommGroup{}
}

// A publisher whose topic is a params-struct member filled by a loader in
// another file of the same package, with the value in a YAML file a launch
// file loads for that package's node: the production publisher must be linked
// under the runtime name, cited, and less confident than a literal.
func TestParamTopicFromStructMemberAndRosparamFile(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "v1", query.File{
		Path: "planner/src/planner.cpp",
		Content: `#include "planner/planner.hpp"
Planner::Planner(ros::NodeHandle nh, const Params& params)
    : status_pub_(nh.advertise<platform_msgs::RobotStatus>(params.status_topic, 10))
{
}
`,
		Symbols: []query.SymbolSpan{{Name: "Planner.Planner", StartLine: 2, EndLine: 5}},
	})
	m.PutFile("platform", "v1", query.File{
		Path: "planner/src/planner_node.cpp",
		Content: `int main(int argc, char** argv) {
  ros::NodeHandle pnh("~");
  Planner::Params params{};
  get_ros_param(pnh, "status_topic", params.status_topic);
}
`,
	})
	m.PutFile("platform", "v1", query.File{
		Path: "planner/launch/planner.launch",
		Content: `<launch>
  <arg name="config" default="$(find planner)/config/topics.yaml"/>
  <node pkg="planner" type="planner_node" name="planner">
    <rosparam file="$(arg config)" command="load"/>
  </node>
</launch>
`,
	})
	m.PutFile("platform", "v1", query.File{
		Path:    "planner/config/topics.yaml",
		Content: "loop_rate: 5\n\nstatus_topic: /robot_status  # published by the planner\n",
	})
	// A literal consumer in another repo, and a test publisher with a literal.
	m.PutFile("monitor", "v1", query.File{
		Path:    "monitor/src/watch.cpp",
		Content: "void Watch::init() {\n  sub_ = nh.subscribe(\"/robot_status\", 10, &Watch::cb, this);\n}\n",
	})

	res := query.CommGraph(m, query.FleetRepo, "v1")
	g := topicGroup(t, res, "robot_status")
	if len(g.Producers) != 1 || g.Producers[0].Path != "planner/src/planner.cpp" {
		t.Fatalf("the param-named publisher must be linked: %+v", g.Producers)
	}
	p := g.Producers[0]
	if p.Param != "status_topic" || p.Raw != "params.status_topic" {
		t.Fatalf("param/raw: %+v", p)
	}
	if !strings.HasPrefix(p.NameResolution, "param:status_topic=/robot_status (planner/config/topics.yaml:3 via planner/launch/planner.launch:4") {
		t.Fatalf("name_resolution must cite the value's file and the launch that loads it: %q", p.NameResolution)
	}
	if p.MsgType != "platform_msgs::RobotStatus" {
		t.Errorf("template type must still be captured: %q", p.MsgType)
	}
	if p.NameConfidence <= 0 || p.NameConfidence >= 0.75 || g.Confidence > p.NameConfidence {
		t.Errorf("a param-resolved name is less certain than a literal: name %.2f group %.2f", p.NameConfidence, g.Confidence)
	}
	if !g.Connected() {
		t.Fatalf("the literal subscriber must link to it: %+v", g)
	}
	if res.ParamResolved != 1 || len(res.Unbound) != 0 {
		t.Errorf("param_resolved=%d unbound=%+v", res.ParamResolved, res.Unbound)
	}
}

// The value comes from a launch <param> whose value is an $(arg) with a
// default; the variable is filled by get_ros_param in the same function.
func TestParamTopicFromLaunchParamArg(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{
		Path: "monitor/src/monitor.cpp",
		Content: `void Monitor::init() {
  std::string status_topic;
  get_ros_param(pnh, "status_topic", status_topic);
  status_sub_ = nh.subscribe(status_topic, 10, &Monitor::onStatus, this);
}
void Monitor::onStatus(const platform_msgs::RobotStatus::ConstPtr& msg) {}
`,
	})
	m.PutFile("platform", "HEAD", query.File{
		Path: "monitor/launch/monitor.launch",
		Content: `<launch>
  <arg name="status_topic" default="/robot_status"/>
  <node pkg="monitor" type="monitor_node" name="monitor">
    <param name="status_topic" type="string" value="$(arg status_topic)"/>
  </node>
</launch>
`,
	})
	res := query.CommGraph(m, "platform", "HEAD")
	g := topicGroup(t, res, "robot_status")
	c := g.Consumers[0]
	if c.NameResolution != "param:status_topic=/robot_status (monitor/launch/monitor.launch:4)" {
		t.Fatalf("name_resolution: %q", c.NameResolution)
	}
	if c.MsgType != "platform_msgs::RobotStatus" || c.MsgTypeSource != "callback-param" {
		t.Errorf("callback type must be recovered for a variable-named subscriber too: %+v", c)
	}
}

// Defaults written in the call: roscpp param() with a default, rclcpp
// declare_parameter returning into a member, and an inline rospy get_param.
func TestParamTopicFromCallDefaults(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{
		Path: "planner/src/a.cpp",
		Content: `void A::init() {
  pnh.param("status_topic", status_topic_, std::string("/robot_status"));
  pub_ = nh.advertise<platform_msgs::RobotStatus>(status_topic_, 1);
}
`,
	})
	m.PutFile("platform", "HEAD", query.File{
		Path: "relay/src/b.cpp",
		Content: `B::B() : Node("b") {
  topic_ = this->declare_parameter<std::string>("status_topic", "/robot_status");
  sub_ = this->create_subscription<platform_msgs::msg::RobotStatus>(
      topic_, 10, std::bind(&B::cb, this, _1));
}
`,
	})
	m.PutFile("platform", "HEAD", query.File{
		Path:    "tools/scripts/echo.py",
		Content: "def main():\n    rospy.Subscriber(rospy.get_param('~status_topic', '/robot_status'), RobotStatus, cb)\n",
	})
	res := query.CommGraph(m, "platform", "HEAD")
	g := topicGroup(t, res, "robot_status")
	if len(g.Producers) != 1 || len(g.Consumers) != 2 {
		t.Fatalf("want 1 producer, 2 consumers: %+v", g)
	}
	for _, ep := range append(g.Producers, g.Consumers...) {
		if !strings.HasPrefix(ep.NameResolution, "param:status_topic=/robot_status (default at ") {
			t.Errorf("%s: %q", ep.Path, ep.NameResolution)
		}
	}
	for _, c := range g.Consumers {
		if c.Path == "tools/scripts/echo.py" && c.MsgType != "RobotStatus" {
			t.Errorf("python positional type: %+v", c)
		}
	}
}

// An unresolvable name is reported, never dropped: the full graph lists it,
// and a focused query for a topic named like its parameter shows it.
func TestParamTopicUnresolvedIsVisible(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{
		Path: "planner/src/c.cpp",
		Content: `void C::init(const std::string& topic_name) {
  get_ros_param(pnh, "status_topic", status_topic_);
  pub_ = nh.advertise<platform_msgs::RobotStatus>(status_topic_, 1);
  other_ = nh.advertise<platform_msgs::RobotStatus>(topic_name, 1);
}
`,
	})
	res := query.CommGraph(m, "platform", "HEAD")
	if len(res.Unbound) != 2 || res.Unresolved != 2 {
		t.Fatalf("both endpoints must be reported unresolved: %+v (unresolved=%d)", res.Unbound, res.Unresolved)
	}
	var labels []string
	for _, ep := range res.Unbound {
		labels = append(labels, ep.NameResolution)
	}
	joined := strings.Join(labels, " | ")
	if !strings.Contains(joined, "param:status_topic (unresolved)") || !strings.Contains(joined, "var:topic_name (unresolved") {
		t.Fatalf("labels: %s", joined)
	}
	focused := query.Topic(m, "platform", "HEAD", "/status")
	if len(focused.Unbound) != 1 || focused.Unbound[0].Param != "status_topic" {
		t.Fatalf("a focused query must show the unresolved endpoint named like the topic: %+v", focused.Unbound)
	}
}

// Several launch files giving different values: listed under each candidate,
// labeled ambiguous, at the lowest confidence.
func TestParamTopicAmbiguousLaunchValues(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{
		Path:    "planner/src/d.cpp",
		Content: "void D::init() {\n  pnh.getParam(\"status_topic\", topic_);\n  pub_ = nh.advertise<S>(topic_, 1);\n}\n",
	})
	for i, v := range []string{"/robot_status", "/sim/robot_status"} {
		m.PutFile("platform", "HEAD", query.File{
			Path:    []string{"planner/launch/real.launch", "planner/launch/sim.launch"}[i],
			Content: `<launch><node pkg="planner" type="d" name="d"><param name="status_topic" value="` + v + `"/></node></launch>`,
		})
	}
	res := query.CommGraph(m, "platform", "HEAD")
	a, b := topicGroup(t, res, "robot_status"), topicGroup(t, res, "sim/robot_status")
	for _, g := range []query.CommGroup{a, b} {
		p := g.Producers[0]
		if !strings.Contains(p.NameResolution, "ambiguous, also:") || p.NameConfidence >= 0.5 {
			t.Errorf("ambiguity must be labeled and scored low: %+v", p)
		}
	}
}

// A rostest launch file configures the test, not production: its value is
// ignored for a production endpoint.
func TestParamTopicIgnoresTestLaunchForProduction(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("platform", "HEAD", query.File{
		Path:    "planner/src/e.cpp",
		Content: "void E::init() {\n  pnh.param(\"status_topic\", topic_, std::string(\"/robot_status\"));\n  pub_ = nh.advertise<S>(topic_, 1);\n}\n",
	})
	m.PutFile("platform", "HEAD", query.File{
		Path:    "checks/test/planner.test",
		Content: `<launch><param name="status_topic" value="/fake_status"/></launch>`,
	})
	res := query.CommGraph(m, "platform", "HEAD")
	g := topicGroup(t, res, "robot_status")
	if !strings.Contains(g.Producers[0].NameResolution, "default at planner/src/e.cpp:2") {
		t.Fatalf("the test launch value must not win: %q", g.Producers[0].NameResolution)
	}
}
