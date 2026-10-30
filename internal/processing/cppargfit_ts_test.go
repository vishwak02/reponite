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
class Handler {
public:
    using LocationType = decltype(lib::Reservation::location_type);
};
inline const char* describe(Handler::LocationType t) { return "x"; }
enum class LocationType { LOAD };
typedef int Count;
class Splitter {
public:
    std::map<int, Result> results(int w);
    lib::ResultList libResults();
};
class Board {
public:
    class TaskTracker { public: int all(); };
};
class Code {
public:
    operator TransportReason() const;
};
class Label {
public:
    int n;
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
void check4(const lib::Response& resp) {
    describe(resp.reservation.location_type);
}
void check5(Splitter* s) {
    auto r = s->libResults().at(0);
    toString(r.reason);
}
lib::Work makeWork();
class WorkTest : public ::testing::Test {
public:
    void run() {
        auto w = makeWork();
        toString(w.kind);
    }
};
struct Fixture {
    std::optional<lib::TaskTracker> tracker_;
    void run() {
        for (const auto& [id, task] : tracker_->all()) {
            toString(task.type);
        }
    }
};
void check6(const Code& c, const Label& l, int n) {
    toString(c);
}
void check7(const Label& l) {
    toString(l);
}
void check8(int n) {
    toString(n);
}
void check3(Splitter* s) {
    auto result = std::move(s->results(1).at(2));
    toString(result.reason);
}
`,
	}
	edges := indexAndEdges(t, files)
	const repoToString = "split/include/split.toString"
	for _, f := range []string{"split/test.check", "split/test.check2", "split/test.check4", "split/test.check5", "split/test.Fixture.run", "split/test.WorkTest.run"} {
		if len(edges[f]) == 0 {
			t.Fatalf("%s: no calls indexed (%v)", f, edges)
		}
	}
	if m := edges["split/test.check"][repoToString]; m != "" {
		t.Errorf("a library element's member is not the repo's enum: %v", edges["split/test.check"])
	}
	if m := edges["split/test.check2"][repoToString]; m != "" {
		t.Errorf("a library parameter's member is not the repo's enum: %v", edges["split/test.check2"])
	}
	if m := edges["split/test.check4"]["split/include/split.describe"]; m != MethodResolved {
		t.Errorf("a parameter named like an enum but declared as an alias is no enum: %v", edges["split/test.check4"])
	}
	if m := edges["split/test.check5"][repoToString]; m != "" {
		t.Errorf("an element of a library's container is the library's: %v", edges["split/test.check5"])
	}
	if m := edges["split/test.Fixture.run"][repoToString]; m != "" {
		t.Errorf("lib::TaskTracker is not the repo's Board::TaskTracker: %v", edges["split/test.Fixture.run"])
	}
	if m := edges["split/test.WorkTest.run"][repoToString]; m != "" {
		t.Errorf("a free function's library value in a library-derived class's method: %v", edges["split/test.WorkTest.run"])
	}
	if m := edges["split/test.check6"][repoToString]; m != MethodResolved {
		t.Errorf("a class with a conversion function may convert to the enum: %v", edges["split/test.check6"])
	}
	for _, f := range []string{"split/test.check7", "split/test.check8"} {
		if len(edges[f]) == 0 {
			t.Fatalf("%s: no calls indexed (%v)", f, edges)
		}
		if m := edges[f][repoToString]; m != "" {
			t.Errorf("%s: no class without a conversion function, and no int, converts to an enum: %v", f, edges[f])
		}
	}
	if m := edges["split/test.check3"][repoToString]; m != MethodResolved {
		t.Errorf("the repo's enum field takes the repo's overload: %v", edges["split/test.check3"])
	}
}

// A member call on a library's class (lib::Problem, which no class of the
// repo derives from) is that class's method, never a repo method that
// shares its name — even when the repo nests a class of the same last name
// elsewhere. A library base the repo derives from still reaches the repo's
// overrides.
func TestCppLibraryReceiverIsTheLibrarys(t *testing.T) {
	files := map[string]string{
		"solve/include/solve/pair.hpp": `#pragma once
namespace solve {
class PairSolver {
public:
    struct Problem { int n; };
    int run();
};
}
`,
		"solve/include/solve/fit.hpp": `#pragma once
#include <memory>
#include <lib/problem.hpp>
namespace solve {
class Residual : public lib::CostFunction {
public:
    bool Evaluate(double const* p, double* r) const override;
};
class Fitter {
public:
    double cost();
    bool step(lib::CostFunction* f);
private:
    std::unique_ptr<lib::Problem> problem_;
};
}
`,
		"solve/src/fit.cpp": `#include "solve/fit.hpp"
namespace solve {
bool Residual::Evaluate(double const* p, double* r) const { return true; }
double Fitter::cost() {
    double c;
    problem_->Evaluate(&c);
    return c;
}
bool Fitter::step(lib::CostFunction* f) {
    return f->Evaluate(nullptr, nullptr);
}
}
`,
	}
	edges := indexAndEdges(t, files)
	const eval = "solve/src.Residual.Evaluate"
	if len(edges["solve/src.Fitter.cost"]) == 0 {
		t.Fatalf("no calls indexed: %v", edges)
	}
	if m := edges["solve/src.Fitter.cost"][eval]; m != "" {
		t.Errorf("lib::Problem's Evaluate is the library's: %v", edges["solve/src.Fitter.cost"])
	}
	if m := edges["solve/src.Fitter.step"][eval]; m == "" {
		t.Errorf("a library base the repo derives from may run the repo's override: %v", edges["solve/src.Fitter.step"])
	}
}
