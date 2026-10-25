//go:build treesitter

package processing

import "testing"

// A free function picked by name binds a call only when one of its
// overloads can take the arguments: an enum parameter never takes a
// library's value (a member of a class the repo does not define), so
// toString(task.type) on a library's task is the library's toString.
func TestCppFreeFunctionOverloadsTakeTheArguments(t *testing.T) {
	files := map[string]string{
		"split/include/split/reasons.hpp": `#pragma once
namespace split {
enum class TransportReason { A, B };
enum class PayloadReason { C };
constexpr const char* toString(TransportReason r) noexcept { return "a"; }
constexpr const char* toString(PayloadReason r) noexcept { return "c"; }
}
`,
		"split/include/split/splitter.hpp": `#pragma once
#include <vector>
#include <map>
#include <lib/task.hpp>
#include "split/reasons.hpp"
namespace split {
using NewTaskList = std::vector<lib::AgentTask>;
struct Result { NewTaskList new_tasks; TransportReason reason; };
class Splitter {
public:
    std::map<int, Result> results(int w);
};
}
`,
		"split/test/test_split.cpp": `#include "split/splitter.hpp"
using namespace split;
void check(Splitter* s) {
    auto result = std::move(s->results(1).at(2));
    auto tasks = std::move(result.new_tasks);
    const auto& task = tasks.at(0);
    toString(task.type);
}
void check2(lib::AgentTask other) {
    toString(other.type);
}
void check3(Splitter* s) {
    auto result = std::move(s->results(1).at(2));
    toString(result.reason);
}
`,
	}
	edges := indexAndEdges(t, files)
	const repoToString = "split/include/split.toString"
	if m := edges["split/test.check"][repoToString]; m != "" {
		t.Errorf("a library element's member is not the repo's enum: %v", edges["split/test.check"])
	}
	if m := edges["split/test.check2"][repoToString]; m != "" {
		t.Errorf("a library parameter's member is not the repo's enum: %v", edges["split/test.check2"])
	}
	if m := edges["split/test.check3"][repoToString]; m != MethodResolved {
		t.Errorf("the repo's enum field takes the repo's overload: %v", edges["split/test.check3"])
	}
}
