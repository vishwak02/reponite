package interfaces

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

func TestTopicsTextIsCompact(t *testing.T) {
	r := query.CommGraphResult{Groups: []query.CommGroup{{Family: "topic", Name: "scan", Confidence: 0.9,
		Producers: []query.CommEndpoint{{Repo: "drv", Path: "src/lidar.cpp", Line: 42, Role: "publisher", Name: "scan", Raw: "scan", MsgType: "sensor_msgs::LaserScan"}},
		Consumers: []query.CommEndpoint{{Repo: "nav", Path: "nav.py", Line: 8, Role: "subscriber", Name: "scan", Raw: "/scan", NameResolution: "as-written"}}}}}
	txt := TopicsText(r)
	js, _ := TopicsJSON(r)
	if !strings.Contains(txt, "pub drv:src/lidar.cpp:42") || !strings.Contains(txt, "sub nav:nav.py:8") {
		t.Fatalf("text: %s", txt)
	}
	if len(txt)*2 > len(js) {
		t.Errorf("text (%d bytes) should be well under half the JSON (%d)", len(txt), len(js))
	}
}
