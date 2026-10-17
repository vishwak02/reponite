package query

import "testing"

func TestIsGeneratedPath(t *testing.T) {
	for p, want := range map[string]bool{
		"api/v1/order.pb.go": true, "proto/order_pb2.py": true, "ui/moc_window.cpp": true,
		"pkg/generated/client.go": true, "my_msgs/msg/_Pose.py": true, "src/order.go": false,
		"generator/main.go": false, "msg/Pose.msg": false,
	} {
		if got := IsGeneratedPath(p); got != want {
			t.Errorf("%s: %v, want %v", p, got, want)
		}
	}
}
