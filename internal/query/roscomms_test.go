package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// CommGraph links a C++ publisher and a Python subscriber that bind the same
// topic name across two repos — the cross-process edge no call graph contains —
// resolves the roscpp vs rclpy arg-order difference, and merges "/scan" with
// "scan".
func TestCommGraphLinksPubSubAcrossRepos(t *testing.T) {
	m := storage.NewMem()
	// roscpp publisher in repo "driver": topic is the first quoted string,
	// message type from the template. Uses an absolute name "/scan".
	m.PutFile("driver", "HEAD", query.File{
		Path:    "src/lidar.cpp",
		Content: "void Lidar::init() {\n  pub_ = nh.advertise<sensor_msgs::LaserScan>(\"/scan\", 10);\n}\n",
		Symbols: []query.SymbolSpan{{Name: "Lidar::init", StartLine: 1, EndLine: 3}},
	})
	// rclpy subscriber in repo "planner": type is the FIRST positional arg,
	// the topic is the SECOND — so "the first quoted string" must still pick
	// the topic. Uses a relative name "scan".
	m.PutFile("planner", "HEAD", query.File{
		Path:    "planner/nav.py",
		Content: "class Nav:\n    def __init__(self):\n        self.create_subscription(LaserScan, 'scan', self.cb, 10)\n",
		Symbols: []query.SymbolSpan{{Name: "Nav.__init__", StartLine: 2, EndLine: 3}},
	})

	res := query.CommGraph(m, query.FleetRepo, "HEAD")

	var scan *query.CommGroup
	for i := range res.Groups {
		if res.Groups[i].Family == "topic" && res.Groups[i].Name == "scan" {
			scan = &res.Groups[i]
		}
	}
	if scan == nil {
		t.Fatalf("expected a linked topic 'scan' (/scan ≡ scan), got groups %+v", res.Groups)
	}
	if !scan.Connected() {
		t.Fatalf("topic 'scan' should be a connected edge (1 pub, 1 sub), got %+v", scan)
	}
	if len(scan.Producers) != 1 || scan.Producers[0].Repo != "driver" || scan.Producers[0].In != "Lidar::init" {
		t.Fatalf("publisher should be driver Lidar::init, got %+v", scan.Producers)
	}
	if scan.Producers[0].MsgType != "sensor_msgs::LaserScan" {
		t.Fatalf("C++ template type should be captured, got %q", scan.Producers[0].MsgType)
	}
	if len(scan.Consumers) != 1 || scan.Consumers[0].Repo != "planner" || scan.Consumers[0].In != "Nav.__init__" {
		t.Fatalf("subscriber should be planner Nav.__init__, got %+v", scan.Consumers)
	}
}

// A commented-out publisher is not a live edge, a dynamic (non-literal) topic is
// counted as unresolved rather than linked, and a subscriber with no publisher
// stays a one-sided (unconnected) group.
func TestCommGraphHonesty(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path: "n.cpp",
		Content: "" +
			"void f() {\n" +
			"  // pub = nh.advertise<T>(\"dead\", 1);\n" + // commented out → ignored
			"  sub = nh.subscribe(\"orphan\", 1, cb);\n" + // subscriber, no publisher
			"  pub2 = nh.advertise<T>(topic_var, 1);\n" + // dynamic name → unresolved
			"}\n",
		Symbols: []query.SymbolSpan{{Name: "f", StartLine: 1, EndLine: 5}},
	})

	res := query.CommGraph(m, "r", "HEAD")

	for _, g := range res.Groups {
		if g.Name == "dead" {
			t.Fatalf("commented-out publisher must not appear as an edge: %+v", g)
		}
		if g.Name == "orphan" && g.Connected() {
			t.Fatalf("a subscriber with no publisher is not a connected edge: %+v", g)
		}
	}
	if res.Unresolved != 1 {
		t.Fatalf("the dynamic-topic advertise should count as 1 unresolved, got %d", res.Unresolved)
	}
	if res.Endpoints != 1 { // only the "orphan" subscriber resolved
		t.Fatalf("expected 1 resolved endpoint (orphan), got %d", res.Endpoints)
	}
}

// Topic answers the focused "who produces / consumes this name" question and
// offers near names when there is no exact match.
func TestTopicFocusedAndSuggest(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "HEAD", query.File{
		Path:    "n.py",
		Content: "def main():\n    p = rospy.Publisher('cmd_vel', Twist, queue_size=1)\n",
		Symbols: []query.SymbolSpan{{Name: "main", StartLine: 1, EndLine: 2}},
	})

	hit := query.Topic(m, "r", "HEAD", "cmd_vel")
	if len(hit.Groups) != 1 || len(hit.Groups[0].Producers) != 1 {
		t.Fatalf("expected the cmd_vel publisher, got %+v", hit.Groups)
	}

	// A realistic typo (edit distance 1) self-heals to the real topic name.
	miss := query.Topic(m, "r", "HEAD", "cmd_val")
	if len(miss.Groups) != 0 {
		t.Fatalf("expected no exact match for 'cmd_val', got %+v", miss.Groups)
	}
	if !strings.Contains(miss.Note, "did you mean") || !strings.Contains(miss.Note, "cmd_vel") {
		t.Fatalf("expected a 'did you mean: cmd_vel' note, got %q", miss.Note)
	}
}
