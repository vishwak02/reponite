//go:build treesitter

package processing

import "testing"

const gmockStoreHeader = `#pragma once
#include <string>
class Store {
public:
    virtual ~Store() = default;
    virtual int get(const std::string& key) const = 0;
    virtual bool put(const std::string& key, int value) = 0;
    virtual void flush() = 0;
};
`

// MOCK_METHOD declares f and gmock_f on the mock class; EXPECT_CALL(obj, f(…))
// and ON_CALL(…) call obj.gmock_f, never f; a StrictMock<T> is a T.
func TestGmockExpectationsCallGmockMembers(t *testing.T) {
	files := map[string]string{
		"shop/include/shop/store.hpp": gmockStoreHeader,
		"shop/test/store_mock.hpp": `#pragma once
#include <gmock/gmock.h>
#include "shop/store.hpp"
class StoreMock : public Store {
public:
    MOCK_METHOD(int, get, (const std::string& key), (const, override));
    MOCK_METHOD(bool, put,
            (const std::string& key, int value), (override));
    MOCK_METHOD0(flush, void());
};
`,
		"shop/test/test_orders.cpp": `#include <gtest/gtest.h>
#include "store_mock.hpp"
using ::testing::_;
class OrdersTest : public ::testing::Test {
protected:
    testing::StrictMock<StoreMock> store;
};
TEST_F(OrdersTest, ReadsStock) {
    EXPECT_CALL(store, get(_)).WillOnce(::testing::Return(3));
    ON_CALL(store, put(_, _)).WillByDefault(::testing::Return(true));
    EXPECT_CALL(store, flush);
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["shop/test.StoreMock.gmock_get"]; !ok {
		t.Fatalf("MOCK_METHOD declares gmock_get on the mock; have %v", symKeys(edges))
	}
	for _, m := range []string{"get", "put", "flush", "gmock_put", "gmock_flush"} {
		if _, ok := edges["shop/test.StoreMock."+m]; !ok {
			t.Errorf("the mock's %s is not a member: %v", m, symKeys(edges))
		}
	}
	test := edges["shop/test.OrdersTest.ReadsStock"]
	wantMethod(t, test, "shop/test.StoreMock.gmock_get", MethodTyped)
	wantMethod(t, test, "shop/test.StoreMock.gmock_put", MethodTyped)
	wantMethod(t, test, "shop/test.StoreMock.gmock_flush", MethodTyped)
	for _, bad := range []string{"shop/test.StoreMock.get", "shop/include/shop.Store.get", "shop/test.StoreMock.put"} {
		if _, ok := test[bad]; ok {
			t.Errorf("an expectation calls gmock_f, not f: %s in %v", bad, test)
		}
	}
	// the matcher-less overload calls the matcher one
	wantMethod(t, edges["shop/test.StoreMock.gmock_get"], "shop/test.StoreMock.gmock_get", MethodResolved)
}

// MATCHER_P(name, p, desc) { … } defines name(p); its body's calls are its own.
func TestGmockMatcherIsItsFunction(t *testing.T) {
	files := map[string]string{
		"geo/test/matchers.hpp": `#pragma once
#include <gmock/gmock.h>
double distance(double a, double b);
MATCHER_P(IsNear, target, "is near " + testing::PrintToString(target)) {
    return distance(arg, target) < 0.1;
}
MATCHER(IsOrigin, "") { return distance(arg, 0.0) == 0.0; }
`,
		"geo/test/geo.cpp": `#include "matchers.hpp"
double distance(double a, double b) { return a - b; }
void check(double x) {
    EXPECT_THAT(x, IsNear(1.0));
    EXPECT_THAT(x, IsOrigin());
}
`,
	}
	edges := indexAndEdges(t, files)
	wantMethod(t, edges["geo/test.IsNear"], "geo/test.distance", MethodResolved)
	wantMethod(t, edges["geo/test.IsOrigin"], "geo/test.distance", MethodResolved)
	wantMethod(t, edges["geo/test.check"], "geo/test.IsNear", MethodResolved)
	wantMethod(t, edges["geo/test.check"], "geo/test.IsOrigin", MethodResolved)
	if _, ok := edges["geo/test.MATCHER_P"]; ok {
		t.Errorf("a matcher is named by its first argument: %v", symKeys(edges))
	}
}

func TestGmockMembersShapes(t *testing.T) {
	for _, c := range []struct {
		macro string
		args  []string
		want  string
	}{
		{"MOCK_METHOD", []string{"(std::map<int, int>)", "load", "((std::pair<int, int>) p, int)", "(const)"},
			"std::map<int, int> load(std::pair<int, int> p, int) const {}\n"},
		{"MOCK_CONST_METHOD2", []string{"find", "int(const std::string&, int)"}, "int find(const std::string&, int) const {}\n"},
		{"MOCK_METHOD1_WITH_CALLTYPE", []string{"STDMETHODCALLTYPE", "ping", "void(int)"}, "void ping(int) {}\n"},
		{"MOCK_METHOD", []string{"void", "reset", "()"}, "void reset() {}\n"},
	} {
		got, ok := gmockMembers(c.macro, c.args)
		if !ok || len(got) < len(c.want) || got[:len(c.want)] != c.want {
			t.Errorf("%s%v: got %q", c.macro, c.args, got)
		}
	}
	if _, ok := gmockMembers("MOCK_METHOD", []string{"int", "f"}); ok {
		t.Error("MOCK_METHOD needs its parameter list")
	}
}

// A repo macro used in a function body is expanded there: the calls it
// makes are the function's. Each use takes the definition its file sees.
func TestBodyMacrosCallWhatTheyExpandTo(t *testing.T) {
	files := map[string]string{
		"rig/test/include/rig/fixture.hpp": `#pragma once
#define WAIT_UNTIL(cond, ...) waitUntil([&]() { return (cond); }, ##__VA_ARGS__)
#define WAIT_UNTIL_TRUE(cond) \
    {                         \
        WAIT_UNTIL(cond);     \
        EXPECT_TRUE(cond);    \
    }
#define CALLER_NAME callerName(__func__)
const char* callerName(const char* f);
class RigFixture {
public:
    template <typename P>
    void waitUntil(P pred, int steps = 10) {}
    bool ready() const { return true; }
};
`,
		"rig/test/rig_test.cpp": `#include "rig/fixture.hpp"
const char* callerName(const char* f) { return f; }
TEST_F(RigFixture, Starts) {
    WAIT_UNTIL_TRUE(ready());
}
void trace() {
    const char* n = CALLER_NAME;
}
`,
		"rig/include/rig/trace.hpp": `#pragma once
namespace rig::trace {
inline const char* label(const char* f) { return f; }
#define TRACE_LABEL label(__func__)
}  // namespace rig::trace
`,
		"rig/src/trace_use.cpp": `#include "rig/trace.hpp"
namespace rig::trace {
const char* inside() { return TRACE_LABEL; }
}
const char* outside() { return TRACE_LABEL; }
namespace other {
using namespace rig::trace;
const char* opened() { return TRACE_LABEL; }
}
`,
		"rig/test/a_test.cpp": `void left() {}
#define STEP() left()
void runA() { STEP(); }
`,
		"rig/test/b_test.cpp": `void right() {}
#define STEP() right()
void runB() { STEP(); }
`,
		"rig/test/c_test.cpp": `void one() {}
void two() {}
#ifdef FAST
#define GO() one()
#else
#define GO() two()
#endif
void runC() { GO(); }
`,
	}
	edges := indexAndEdges(t, files)
	starts := edges["rig/test.RigFixture.Starts"]
	if _, ok := starts["rig/test/include/rig.RigFixture.waitUntil"]; !ok {
		t.Errorf("WAIT_UNTIL_TRUE expands to a waitUntil call: %v", starts)
	}
	if _, ok := starts["rig/test/include/rig.RigFixture.ready"]; !ok {
		t.Errorf("the macro's argument is still called: %v", starts)
	}
	for c := range starts {
		if c == "WAIT_UNTIL_TRUE" || c == "WAIT_UNTIL" {
			t.Errorf("a macro is not a function it calls: %v", starts)
		}
	}
	wantMethod(t, edges["rig/test.trace"], "rig/test.callerName", MethodResolved)
	wantMethod(t, edges["rig/src.inside"], "rig/include/rig.label", MethodResolved)
	wantMethod(t, edges["rig/src.opened"], "rig/include/rig.label", MethodResolved)
	if _, bad := edges["rig/src.outside"]["rig/include/rig.label"]; bad {
		t.Errorf("label is not found from the global namespace: %v", edges["rig/src.outside"])
	}
	wantMethod(t, edges["rig/test.runA"], "rig/test.left", MethodResolved)
	wantMethod(t, edges["rig/test.runB"], "rig/test.right", MethodResolved)
	if _, bad := edges["rig/test.runA"]["rig/test.right"]; bad {
		t.Errorf("a.cpp's STEP is its own: %v", edges["rig/test.runA"])
	}
	for _, f := range []string{"rig/test.one", "rig/test.two"} {
		if _, bad := edges["rig/test.runC"][f]; bad {
			t.Errorf("which of two #ifdef variants applies is not known: %v", edges["rig/test.runC"])
		}
	}
}

func TestSubstituteMacroVariadicComma(t *testing.T) {
	m := cppMacro{params: []string{"c"}, variadic: true, body: "f(c, ##__VA_ARGS__)"}
	if got := substituteMacro(m, []string{"x"}); got != "f(x)" {
		t.Errorf("empty __VA_ARGS__ takes its comma: %q", got)
	}
	if got := substituteMacro(m, []string{"x", "1", "2"}); got != "f(x,1, 2)" {
		t.Errorf("non-empty: %q", got)
	}
	o := cppMacro{params: []string{"c"}, variadic: true, body: "g(c __VA_OPT__(,) __VA_ARGS__)"}
	if got := substituteMacro(o, []string{"x"}); got != "g(x  )" {
		t.Errorf("__VA_OPT__ empty: %q", got)
	}
}

func TestScanCodeSkipsLiteralsAndDirectives(t *testing.T) {
	src := "#define A(x) \\\n  B(x)\nint f() { /* C */ auto s = R\"q(D \" E)q\"; // F\n return G(\"H\", 'I'); }\n"
	var got []string
	scanCode(src, func(start, end int) int {
		got = append(got, src[start:end])
		return end
	})
	want := []string{"int", "f", "auto", "s", "return", "G"}
	if len(got) != len(want) {
		t.Fatalf("identifiers %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("identifiers %v, want %v", got, want)
		}
	}
}
