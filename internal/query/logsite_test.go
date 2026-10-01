package query

import "testing"

type filesStore struct {
	Store
	files map[string][]File
}

func (f filesStore) Repos() []string {
	var out []string
	for r := range f.files {
		out = append(out, r)
	}
	return out
}
func (f filesStore) Refs(string) []string          { return []string{"v1"} }
func (f filesStore) Files(repo, ref string) []File { return f.files[repo] }

func TestLogSitesFindsTheEmitter(t *testing.T) {
	cpp := `void WorkManager::tick() {
  ROS_INFO("Creating %lu new tasks for %s", n, agent.c_str());
  ROS_WARN_STREAM("Forgetting work " << id << " of " << wf);
  ROS_INFO_NAMED("wm", "Queue size %d", q);
  RCLCPP_ERROR(get_logger(), "Failed to split work %s: %s", id.c_str(), err.what());
  throw std::runtime_error("unsplittable work");
}`
	py := `def upload(rows):
    logger.info("Uploaded %(count)d rows to %(site)s", {"count": n, "site": s})
    log.warning(f"order {order_id} rejected: {reason}")
    print("done {}".format(x))
`
	ts := "export function f() { console.error(`Action is ${a.type} but expected ${b}`); }\n"
	s := filesStore{files: map[string][]File{
		"gbc": {{Path: "wm/src/work_manager.cpp", Content: cpp, Symbols: []SymbolSpan{{Name: "tick", StartLine: 1, EndLine: 7}}}},
		"wms": {{Path: "core/upload.py", Content: py}},
		"ui":  {{Path: "src/app.tsx", Content: ts}},
	}}
	cases := []struct {
		log, path string
		line      int
		call      string
	}{
		{"[ INFO] [1727.1]: Creating 5 new tasks for amr07", "wm/src/work_manager.cpp", 2, "ROS_INFO"},
		{"Forgetting work 42 of picking", "wm/src/work_manager.cpp", 3, "ROS_WARN_STREAM"},
		{"[wm] Queue size 17", "wm/src/work_manager.cpp", 4, "ROS_INFO_NAMED"},
		{"Failed to split work W1: timeout", "wm/src/work_manager.cpp", 5, "RCLCPP_ERROR"},
		{"what():  unsplittable work", "wm/src/work_manager.cpp", 6, "std::runtime_error"},
		{"2026-09-30 INFO Uploaded 120 rows to site001", "core/upload.py", 2, "logger.info"},
		{"WARNING order 991 rejected: missing sku", "core/upload.py", 3, "log.warning"},
		{"Action is PICK but expected LOAD", "src/app.tsx", 1, "console.error"},
	}
	for _, c := range cases {
		r := LogSites(s, FleetRepo, "v1", c.log, 3)
		if len(r.Sites) == 0 {
			t.Errorf("%q: no site (%s)", c.log, r.Note)
			continue
		}
		got := r.Sites[0]
		if got.Path != c.path || got.Line != c.line || got.Call != c.call {
			t.Errorf("%q: got %s:%d %s, want %s:%d %s", c.log, got.Path, got.Line, got.Call, c.path, c.line, c.call)
		}
	}
	if r := LogSites(s, FleetRepo, "v1", "Creating 5 new tasks for amr07", 3); r.Sites[0].In != "tick" {
		t.Errorf("enclosing symbol: %q", r.Sites[0].In)
	}
	// A line built by a stream operator<<: the log call that starts it wins
	// over the helper literal with more fixed text, and the ROS logger's
	// package outranks a same-text site elsewhere.
	solver := `void Solver::solve() {
  ROS_INFO_STREAM_NAMED(LOGNAME, "Solver result: " << result);
}
std::ostream& operator<<(std::ostream& out, const Result& r) {
  out << "Agents to offline: ";
  return out;
}`
	s.files["gbc"] = append(s.files["gbc"], File{Path: "battery_planner/src/solver.cpp", Content: solver},
		File{Path: "other_pkg/src/copy.cpp", Content: `void f() { ROS_INFO_STREAM("Solver result: " << x << "Agents to offline: "); }`})
	r := LogSites(s, FleetRepo, "v1", "[ INFO] [1.2] [ros.battery_planner.Planner]: Solver result: Agents to work: 1, Agents to offline: Status: Okay", 5)
	if len(r.Sites) < 2 || r.Sites[0].Path != "battery_planner/src/solver.cpp" || r.Sites[0].Line != 2 || !r.Sites[0].InPackage {
		t.Errorf("assembled line: %+v", r.Sites)
	}
	if r := LogSites(s, FleetRepo, "v1", "something nobody prints at all", 3); len(r.Sites) != 0 {
		t.Errorf("unrelated line matched: %+v", r.Sites)
	}
}
