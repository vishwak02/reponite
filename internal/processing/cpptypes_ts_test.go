//go:build treesitter

package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Member calls resolved by the receiver's declared type: a ros::Publisher
// member's publish() is the library's, not the repo's one publish(); a
// shared_ptr<Base> member's step() reaches Base::step and every override; a
// pure-virtual call through the base reaches only the overrides.
func TestCppReceiverTyping(t *testing.T) {
	files := map[string]string{
		// A C++ header named .h (the ROS convention) must parse as C++.
		"mgr/include/mgr/manager.h": `
#pragma once
#include <memory>
namespace mgr {
class Task {
 public:
  virtual ~Task() {}
  virtual void step() { log(); }
  virtual void assign() = 0;
  void log() {}
  void run() { assign(); }
};
using TaskPtr = std::shared_ptr<Task>;
class Manager {
 public:
  void tick(const Worker& w, Task::Ptr tp, int n);
 private:
  ros::Publisher pub_;
  std::shared_ptr<Task> task_;
  TaskPtr alias_;
  Worker* worker_;
};
}`,
		"mgr/src/manager.cpp": `
namespace mgr {
std::ostream& operator<<(std::ostream& out, const Manager& m) { return out; }
void Manager::tick(const Worker& w, Task::Ptr tp, int n) {
  pub_.publish(n);
  this->task_->step();
  alias_->log();
  worker_->publish(n);
  w.publish(n);
  tp->assign();
  auto made = std::make_shared<Worker>();
  made->work();
}
}`,
		"tasks/src/tracker.cpp": `
class Tracker {
 public:
  WorkerPtr getWorker() const;
  void scan(const std::vector<Worker>& ws);
 private:
  std::map<int, std::shared_ptr<Worker>> works_;
  std::vector<WorkerPtr> list_;
  std::unordered_map<std::string, Holder> holders_;
};
class Holder { public: Worker w; };
WorkerPtr Tracker::getWorker() const { return nullptr; }
void Tracker::scan(const std::vector<Worker>& ws) {
  for (const auto& [id, w] : works_) { w->work(); }
  for (auto& p : list_) { p->publish(1); }
  for (const Worker& q : ws) { q.stop(); }
  auto it = works_.find(3);
  it->second->rest();
  works_[4]->sleep();
  holders_.at("a").w.eat();
  getWorker()->drink();
  list_.push_back(nullptr);
  std::for_each(list_.begin(), list_.end(), [](const WorkerPtr& p) { p->lick(); }); // p declared twice, same type
  if (const auto h = holders_.at("b").w.buddy()) { h->hug(); }                 // condition declaration
  auto& hw = ns::Holder(1).w;                                                     // a temporary's field
  hw.nap();
}`,
		"tasks/src/tasks.cpp": `
class PickTask : public mgr::Task {
 public:
  void step() override {}
  void assign() override {}
};
class DropTask : public PickTask {
 public:
  void assign() override {}
};
class Worker : public Base {
 public:
  void publish(int n) {}
  void work() {}
  void stop() {} void rest() {} void sleep() {} void eat() {} void drink() {} void lick() {} void hug() {} void nap() {} std::optional<Worker> buddy();
};
using WorkerPtr = std::shared_ptr<Worker>;
class MockTask : public mgr::Task {
 public:
  void assign() override {}
};
class Unrelated {
 public:
  void publish(int n) {}
};`,
	}
	var parsed []ParsedFile
	for path, src := range files {
		ext := path[len(path)-4:]
		if path[len(path)-2:] == ".h" {
			ext = ".h"
		}
		rules, gext, _ := RulesForSource(ext, []byte(src))
		root, spans, err := parseFileRules([]byte(src), gext, rules)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, ParsedFile{Path: path, Content: src, Lang: rules.Name,
			Symbols: Extract(root, rules, 1), Spans: spans, Types: CppTypeFacts(root, rules)})
	}
	m := storage.NewMem()
	if err := IndexFiles(m, "r", "v1", 1, parsed); err != nil {
		t.Fatal(err)
	}
	snap := m.Snapshot("r", "v1")
	if _, ok := snap.Symbols["mgr/src.operator<<"]; !ok {
		t.Errorf("a function returning a reference is named after itself, not its return type; symbols: %v", symKeys(snap.Symbols))
	}
	got := map[string]query.Callee{}
	for _, c := range snap.Callees["mgr/src.Manager.tick"] {
		got[c.Name] = c
	}
	want := func(name, method string) {
		t.Helper()
		c, ok := got[name]
		if !ok {
			t.Errorf("missing edge %s (have %v)", name, got)
			return
		}
		if c.ResolutionMethod != method {
			t.Errorf("%s: method %s, want %s", name, c.ResolutionMethod, method)
		}
	}
	want("ros::Publisher::publish", MethodExternal)   // pub_ is a ros::Publisher
	want("mgr/include/mgr.Task.step", MethodTyped)    // shared_ptr<Task> field
	want("tasks/src.PickTask.step", MethodOverride)   // ... and its override
	want("mgr/include/mgr.Task.log", MethodTyped)     // through the TaskPtr alias
	want("tasks/src.Worker.publish", MethodTyped)     // Worker* field, const Worker& param
	want("tasks/src.PickTask.assign", MethodOverride) // Task::Ptr param, pure virtual
	want("tasks/src.DropTask.assign", MethodOverride) // transitive subclass
	want("tasks/src.Worker.work", MethodTyped)        // auto = make_shared<Worker>
	if _, ok := got["tasks/src.MockTask.assign"]; ok {
		t.Errorf("production code dispatches to a mock's override: %v", got)
	}
	if _, ok := got["tasks/src.Unrelated.publish"]; ok {
		t.Errorf("typed receiver still pinned on an unrelated class: %v", got)
	}
	scan := map[string]string{}
	for _, c := range snap.Callees["tasks/src.Tracker.scan"] {
		scan[c.Name] = c.ResolutionMethod
	}
	for _, m := range []string{"work", "publish", "stop", "rest", "sleep", "eat", "drink", "lick", "hug", "nap"} {
		if scan["tasks/src.Worker."+m] != MethodTyped {
			t.Errorf("Tracker::scan -> Worker::%s through a container/iterator/return: %v", m, scan)
		}
	}
	if scan["std::container::push_back"] != MethodExternal {
		t.Errorf("a container's own member is the standard library's: %v", scan)
	}
	// A call to a pure virtual from the base's own method reaches the overrides.
	run := map[string]string{}
	for _, c := range snap.Callees["mgr/include/mgr.Task.run"] {
		run[c.Name] = c.ResolutionMethod
	}
	if run["tasks/src.PickTask.assign"] != MethodOverride || run["tasks/src.DropTask.assign"] != MethodOverride {
		t.Errorf("Task::run -> assign overrides: %v", run)
	}
	step := map[string]string{}
	for _, c := range snap.Callees["mgr/include/mgr.Task.step"] {
		step[c.Name] = c.ResolutionMethod
	}
	if step["mgr/include/mgr.Task.log"] != MethodResolved {
		t.Errorf("own-class call from an inline header method: %v", step)
	}
}
