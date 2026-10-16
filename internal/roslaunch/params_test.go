package roslaunch

import "testing"

func TestFlattenYAML(t *testing.T) {
	got := FlattenYAML(`# topics
status_topic: /robot_status  # comment
quoted: "/a #b"
topic:
  status: /nested/status
  list:
    - x
    - y
  after_list: kept
block: |
  not: a key
items:
- name: skipped
  value: skipped
tail: 'single'
`)
	want := map[string]string{
		"status_topic": "/robot_status", "quoted": "/a #b", "topic/status": "/nested/status",
		"topic/after_list": "kept", "tail": "single",
	}
	for k, v := range want {
		if got[k].Value != v {
			t.Errorf("%s = %q, want %q", k, got[k].Value, v)
		}
	}
	for _, k := range []string{"not", "block/not", "name", "value", "items/name"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must not be read as a key (block scalar / sequence)", k)
		}
	}
	if got["status_topic"].Line != 2 {
		t.Errorf("line: %d", got["status_topic"].Line)
	}
}

func TestLaunchParams(t *testing.T) {
	f, err := Parse("planner/launch/planner.launch", `<launch>
  <arg name="status_topic" default="/robot_status"/>
  <arg name="cfg" value="$(eval find('planner') + '/config/extra.yaml')"/>
  <param name="/global_rate" value="5"/>
  <node pkg="planner" type="planner_node" name="planner">
    <param name="status_topic" value="$(arg status_topic)"/>
    <param name="unknown" value="$(env HOME)"/>
    <rosparam file="$(find planner)/config/topics.yaml" command="load" ns="io"/>
    <rosparam file="$(arg cfg)"/>
    <rosparam param="limits">max_speed: 1.5</rosparam>
    <rosparam file="$(find missing)/x.yaml"/>
  </node>
</launch>`)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"planner/config/topics.yaml": "cmd_topic: /cmd\n",
		"planner/config/extra.yaml":  "extra_topic: /extra\n",
	}
	lookup := func(pkg, rel string) (string, string, bool) {
		p := pkg + "/" + rel
		c, ok := files[p]
		return p, c, ok
	}
	vals, unresolved := LaunchParams(f, lookup)
	got := map[string]ParamValue{}
	for _, v := range vals {
		got[v.Name] = v
	}
	if v := got["status_topic"]; v.Value != "/robot_status" || v.Pkg != "planner" || v.Via != "planner/launch/planner.launch:6" {
		t.Errorf("node param: %+v", v)
	}
	if got["global_rate"].Pkg != "" || got["global_rate"].Value != "5" {
		t.Errorf("global param: %+v", got["global_rate"])
	}
	if _, ok := got["unknown"]; ok {
		t.Errorf("an unexpanded value is not a runtime value")
	}
	if v := got["io/cmd_topic"]; v.Value != "/cmd" || v.Via != "planner/config/topics.yaml:1 via planner/launch/planner.launch:8" {
		t.Errorf("rosparam ns file load: %+v", v)
	}
	if got["extra_topic"].Value != "/extra" {
		t.Errorf("$(eval find()) file load: %+v", got)
	}
	if got["limits/max_speed"].Value != "1.5" {
		t.Errorf("inline rosparam: %+v", got)
	}
	if len(unresolved) != 1 || unresolved[0] != "$(find missing)/x.yaml" {
		t.Errorf("a file that cannot be located is reported: %v", unresolved)
	}
}

func TestFindRef(t *testing.T) {
	for in, want := range map[string][2]string{
		"$(find planner)/config/a.yaml":              {"planner", "config/a.yaml"},
		"$(eval find('planner') + '/config/a.yaml')": {"planner", "config/a.yaml"},
	} {
		p, r, ok := FindRef(in)
		if !ok || p != want[0] || r != want[1] {
			t.Errorf("%s -> %s %s %v", in, p, r, ok)
		}
	}
	if _, _, ok := FindRef("/abs/path.yaml"); ok {
		t.Error("a plain path is not a find reference")
	}
}
