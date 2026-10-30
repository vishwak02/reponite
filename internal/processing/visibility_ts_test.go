//go:build treesitter

package processing

import (
	"strings"
	"testing"
)

// A C++ call reaches only what its translation unit can name: its own file,
// the headers it includes, and the definitions those headers declare.
func TestCppVisibility(t *testing.T) {
	files := map[string]string{
		// a helper with internal linkage, and a class of that source file only
		"core/src/tray.cpp": `#include "core/tray.hpp"
namespace {
std::string describe(int state) { return "x"; }
}
class Impl { public: void run() {} };
void Tray::tick() { describe(1); }
`,
		"core/include/core/tray.hpp": `class Tray { public: void tick(); };
`,
		// another file of the directory calls its own describe, and holds a
		// pimpl whose class alias is the framework's
		"core/src/screen.cpp": `#include "core/tray.hpp"
#include "core/server.hpp"
std::string describe(const Msg& m);
class Screen {
    using Impl = fw::Plan<int>;
    Impl _impl;
    void show();
};
void Screen::show() { describe(msg); _impl.run(); }
`,
		"core/include/core/server.hpp": `#include "core/tray.hpp"
`,
		// an unqualified call inside a method: a library base's member, never
		// the one unrelated class in the repo with that name
		"core/src/behaviour.cpp": `#include "core/trace.hpp"
class Dock : public fw::Behaviour { void onInit(); };
void Dock::onInit() { getTrace(); }
`,
		"core/src/trace.cpp": `class GlobalTrace { public: static int* getTrace() { return nullptr; } };
`,
		"core/include/core/trace.hpp": `#pragma once
`,
		// two headers each define a fixture of one name; each test sees the
		// one it includes
		"a/include/a/setup.hpp": `class Fixture { public: void prepare() {} };
`,
		"b/include/b/setup.hpp": `class Fixture { public: void prepare() {} };
`,
		"b/test/run.cpp": `#include "b/setup.hpp"
class RunTest : public Fixture { void go(); };
void RunTest::go() { prepare(); }
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["core/src.Tray.tick"]["core/src.describe"]; !ok {
		t.Errorf("own file's helper: %v", edges["core/src.Tray.tick"])
	}
	if m := edges["core/src.Screen.show"]["core/src.describe"]; m != "" {
		t.Errorf("another file's anonymous-namespace helper is not in scope: %v", edges["core/src.Screen.show"])
	}
	if m := edges["core/src.Screen.show"]["core/src.Impl.run"]; m != "" {
		t.Errorf("a source file's own class is not another file's Impl: %v", edges["core/src.Screen.show"])
	}
	if m := edges["core/src.Dock.onInit"]["core/src.GlobalTrace.getTrace"]; m != "" {
		t.Errorf("an unqualified call never reaches an unrelated class's method: %v", edges["core/src.Dock.onInit"])
	}
	if _, ok := edges["b/test.RunTest.go"]["b/include/b.Fixture.prepare"]; !ok {
		t.Errorf("the included fixture's method: %v", edges["b/test.RunTest.go"])
	}
	if m := edges["b/test.RunTest.go"]["a/include/a.Fixture.prepare"]; m != "" {
		t.Errorf("a fixture the file does not include: %v", edges["b/test.RunTest.go"])
	}
}

// A class template deriving from its type parameter, used through an alias,
// inherits the alias argument's methods; an auto getter returning a field
// types the calls made on its result.
func TestCppTemplateBaseAndAutoGetter(t *testing.T) {
	files := map[string]string{
		"t/include/t/fixture.hpp": `class BaseTest { public: bool step() { return true; } };
class CommonTest : public BaseTest {};
template <typename Base, typename Action>
class ActionTest : public Base { public: void send() {} };
using PickTest = ActionTest<CommonTest, int>;
class Splitter { public: bool ready() const { return true; } };
class Holder {
public:
    auto& splitter() { return _splitter; }
private:
    std::optional<Splitter> _splitter;
};
`,
		"t/test/pick.cpp": `#include "t/fixture.hpp"
class PickCase : public PickTest { void body(); };
void PickCase::body() { step(); send(); }
class HolderCase : public Holder { void body(); };
void HolderCase::body() { splitter()->ready(); }
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["t/test.PickCase.body"]["t/include/t.BaseTest.step"]; !ok {
		t.Errorf("method of the alias's template argument base: %v", edges["t/test.PickCase.body"])
	}
	if _, ok := edges["t/test.PickCase.body"]["t/include/t.ActionTest.send"]; !ok {
		t.Errorf("method of the class template: %v", edges["t/test.PickCase.body"])
	}
	if _, ok := edges["t/test.HolderCase.body"]["t/include/t.Splitter.ready"]; !ok {
		t.Errorf("a call on an auto getter's field: %v", edges["t/test.HolderCase.body"])
	}
}

// An ES module names nothing it neither declares nor imports: a bare call is
// the module's own function, else a local or a global.
func TestJSBareCallStaysInModule(t *testing.T) {
	files := map[string]string{
		"src/actions/data.ts": `export const setData = (payload) => ({ type: "SET", payload });
`,
		"src/hooks/useFetch.ts": `import { useState } from "react";
export function useFetch() {
  const [data, setData] = useState(null);
  setData(1);
  const m = new Map();
  return data;
}
`,
		"src/views/item.tsx": `import { ListItem } from "./style";
export function Row() {
  return <ListItem />;
}
`,
		"src/views/style.ts": `export const ListItem = styled.li` + "`" + `color: red;` + "`" + `;
`,
		"src/legend/styles.ts": `export const ListItem = styled.li(() => ({}));
export const Map = () => null;
`,
	}
	edges := indexAndEdges(t, files)
	if m := edges["src/hooks.useFetch.useFetch"]["src/actions.data.setData"]; m != "" {
		t.Errorf("a hook's setter is not another module's action: %v", edges["src/hooks.useFetch.useFetch"])
	}
	if m := edges["src/hooks.useFetch.useFetch"]["src/legend.styles.Map"]; m != "" {
		t.Errorf("new Map() is the global: %v", edges["src/hooks.useFetch.useFetch"])
	}
	if m := edges["src/views.item.Row"]["src/legend.styles.ListItem"]; m != "" {
		t.Errorf("a component imported from its own module is not another module's: %v", edges["src/views.item.Row"])
	}
}

// A called parameter is the argument; a fixture parameter called runs the
// fixture's code only when the fixture returns a function it defines.
func TestPythonCalledParametersAndFixtures(t *testing.T) {
	files := map[string]string{
		"tests/conftest.py": `import pytest

@pytest.fixture
def make_item():
    def _make(name):
        return name
    return _make

@pytest.fixture
def mock_call(respx_mock):
    return respx_mock.post("x").mock
`,
		"tests/test_items.py": `from typing import Callable

Maker = Callable[[str], str]

def test_make(make_item, mock_call):
    make_item("a")
    mock_call(return_value=1)

def test_typed(make_item: Maker):
    make_item("t")

def _setup(make_item: Maker):
    make_item("c")

def test_through_helper(make_item):
    _setup(make_item)
`,
		"tests/helpers.py": `def run(make_item):
    make_item("b")
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["tests.test_items.test_make"]["tests.conftest.make_item"]; !ok {
		t.Errorf("a factory fixture's call runs the fixture: %v", edges["tests.test_items.test_make"])
	}
	if m := edges["tests.test_items.test_make"]["tests.conftest.mock_call"]; m != "" {
		t.Errorf("a fixture handing over a mock runs none of its code: %v", edges["tests.test_items.test_make"])
	}
	if m := edges["tests.helpers.run"]["tests.conftest.make_item"]; m != "" {
		t.Errorf("a plain function's parameter is its argument: %v", edges["tests.helpers.run"])
	}
	if _, ok := edges["tests.test_items.test_typed"]["tests.conftest.make_item"]; !ok {
		t.Errorf("a test's annotated parameter is still its fixture: %v", edges["tests.test_items.test_typed"])
	}
	// pytest hands fixtures to tests and fixtures only: a helper's
	// parameter, annotated or not, is whatever its caller passes
	if m := edges["tests.test_items._setup"]["tests.conftest.make_item"]; m != "" {
		t.Errorf("a test module's helper takes no fixtures: %v", edges["tests.test_items._setup"])
	}
}

// Two classes each nest an AccessImpl reached through their own alias: a
// receiver typed through one class's alias calls that class's nested
// method; a cast types its receiver; a forwarded parameter pack does not
// count as one argument.
func TestCppNestedAliasesCastsAndPacks(t *testing.T) {
	files := map[string]string{
		"bb/include/bb/server.hpp": `template <typename Spec>
class ServerBoard {
public:
    class AccessImpl {
    public:
        bool hasGoal() const { return true; }
    };
    using Access = AccessImpl;
};
`,
		"bb/include/bb/item.hpp": `template <typename Spec>
class ItemBoard {
public:
    class AccessImpl {
    public:
        bool hasGoal() const { return false; }
    };
    using Access = AccessImpl;
};
`,
		"app/src/behaviour.cpp": `#include "bb/server.hpp"
#include "bb/item.hpp"
class Task { public: void write() {} };
class Behaviour {
    using Board = ServerBoard<int>;
    void run(const Task* t);
    template <typename... A> void cb(A&&... a) {}
    void wire();
};
void Behaviour::run(const Task* t) {
    auto bb = Board::Access();
    bb.hasGoal();
    const_cast<Task*>(t)->write();
}
void Behaviour::wire() {
    auto f = [this](auto&&... args) { cb(std::forward<decltype(args)>(args)...); };
}
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["app/src.Behaviour.run"]
	// two classes' AccessImpl of one directory share the id AccessImpl, as
	// overloads do; the alias reaches it through its own class's nesting
	if _, ok := run["bb/include/bb.AccessImpl.hasGoal"]; !ok {
		t.Errorf("the alias's own nested class: %v", run)
	}
	if _, ok := run["app/src.Task.write"]; !ok {
		t.Errorf("a const_cast types its receiver: %v", run)
	}
	if _, ok := edges["app/src.Behaviour.wire"]["app/src.Behaviour.cb"]; !ok {
		t.Errorf("a forwarded pack fits any parameter count: %v", edges["app/src.Behaviour.wire"])
	}
}

// `from pkg import mod` binds the submodule when pkg's __init__ binds no
// such name: calling it calls a module, not mod.py's function of that name.
func TestPythonCalledSubmoduleImport(t *testing.T) {
	files := map[string]string{
		"app/utils/__init__.py": "import json\n",
		"app/utils/report.py":   "def report(errors):\n    return str(errors)\n",
		"app/utils/shared.py":   "def shared():\n    return 1\n",
		"app/lib/__init__.py":   "from .shared import shared\n",
		"app/lib/shared.py":     "def shared():\n    return 2\n",
		"app/main.py": `from app.utils import report
from app.lib import shared

def run():
    report([1])
    shared()
`,
	}
	edges := indexAndEdges(t, files)
	run := edges["app.main.run"]
	for k := range run {
		if k == "app/utils.report" || k == "app/utils.report.report" {
			t.Errorf("a called submodule is not its function: %v", run)
		}
	}
	if _, ok := run["app/lib.shared"]; !ok {
		if _, ok2 := run["app/lib.shared.shared"]; !ok2 {
			t.Errorf("a name the package __init__ re-exports is the function: %v", run)
		}
	}
}

// A repo macro that defines members inside a class body (a recovery list)
// generates those methods, with the calls their bodies make.
func TestCppMacroGeneratedMembers(t *testing.T) {
	files := map[string]string{
		"rec/include/rec/tools.hpp": `#pragma once
class SaveStream { public: template <typename... T> void write(const T&... v) {} };
class RestoreStream { public: template <typename... T> void read(T&... v) {} };
#define _USING_STREAMS_ using Save = SaveStream;
#define _DECLARE_LIST_(...)                                   \
    _USING_STREAMS_                                           \
public:                                                       \
    void save(SaveStream& stream) const { stream.write(__VA_ARGS__); } \
    void restore(RestoreStream& stream) { stream.read(__VA_ARGS__); }
#define DECLARE_LIST(...) _DECLARE_LIST_(__VA_ARGS__)
`,
		"app/include/app/tracker.hpp": `#include "rec/tools.hpp"
class Tracker {
    int _a;
    int _b;
    DECLARE_LIST(_a, _b);
};
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["app/include/app.Tracker.save"]["rec/include/rec.SaveStream.write"]; !ok {
		t.Errorf("the macro's save() calls write: %v (have %v)", edges["app/include/app.Tracker.save"], symKeys(edges))
	}
	if _, ok := edges["app/include/app.Tracker.restore"]["rec/include/rec.RestoreStream.read"]; !ok {
		t.Errorf("the macro's restore() calls read: %v", edges["app/include/app.Tracker.restore"])
	}
}

// Overloads of one name in two files: the argument types written at the call
// (an enumerator, a typed local) pick the overload whose parameter they fit.
func TestCppOverloadByArgumentTypes(t *testing.T) {
	files := map[string]string{
		"ui/include/ui/strings.hpp": `enum class Reason { NONE, NO_TOTES };
class Action {};
std::string toString(Reason r);
std::string toString(const Action& a);
`,
		"ui/src/reason.cpp": `#include "ui/strings.hpp"
std::string toString(Reason r) { return "r"; }
`,
		"ui/src2/action.cpp": `#include "ui/strings.hpp"
std::string toString(const Action& a) { return "a"; }
`,
		"app/src/splitter.cpp": `#include "ui/strings.hpp"
void split() {
    auto s = toString(Reason::NO_TOTES);
}
void show(const Action& act) {
    auto s = toString(act);
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["app/src.split"]["ui/src.toString"]; !ok {
		t.Errorf("an enumerator picks the enum overload: %v", edges["app/src.split"])
	}
	if _, ok := edges["app/src.show"]["ui/src2.toString"]; !ok {
		t.Errorf("a typed argument picks its class's overload: %v", edges["app/src.show"])
	}
}

func TestCppClassAliasShadowsClassTemplate(t *testing.T) {
	files := map[string]string{
		"lib/include/lib/driver.hpp": `namespace mc {
template <typename Concrete>
class MotorDriver {
public:
    bool velocity(double& v) { return static_cast<Concrete&>(*this).velocity(v); }
};
class Zlib : public MotorDriver<Zlib> {
public:
    bool velocity(double& v) const;
};
}
`,
		"lib/src/zlib.cpp": `#include "lib/driver.hpp"
namespace mc {
bool Zlib::velocity(double& v) const { return true; }
}
`,
		"dt/include/dt/traits.hpp": `#include "lib/driver.hpp"
namespace dt {
struct Traits {
#if USES_ZLIB
    using MotorDriver = mc::Zlib;
#endif
};
}
`,
		"dt/include/dt/manager.hpp": `#include "dt/traits.hpp"
namespace dt {
class Manager {
public:
    using MotorDriver = Traits::MotorDriver;
    const MotorDriver& getMotorDriver(int i) const { return _drivers[i]; }
private:
    std::array<MotorDriver, 4> _drivers{};
};
}
`,
		"dt/include/dt/recovery.hpp": `#include "dt/traits.hpp"
namespace dt {
class Recovery {
public:
    using MotorDriver = Traits::MotorDriver;
    using Drivers = std::array<MotorDriver, 4>;
    void reset(Drivers& drivers);
};
}
`,
		"dt/src/recovery.cpp": `#include "dt/recovery.hpp"
namespace dt {
void Recovery::reset(Drivers& drivers) {
    double v{};
    for (auto& d : drivers) {
        d.velocity(v);
    }
}
}
`,
		"app/src/odom.cpp": `#include "dt/manager.hpp"
void copy(const dt::Manager& m) {
    const auto& d = m.getMotorDriver(0);
    double v{};
    d.velocity(v);
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["app/src.copy"]["lib/src.Zlib.velocity"]; !ok {
		t.Errorf("the class's alias names the concrete driver, not the base template: %v", edges["app/src.copy"])
	}
	if _, ok := edges["dt/src.Recovery.reset"]["lib/src.Zlib.velocity"]; !ok {
		t.Errorf("an array of the class's alias holds the concrete driver: %v", edges["dt/src.Recovery.reset"])
	}
	// two configurations, two drivers: the build picks, so no edge is drawn
	files["dt/include/dt/traits.hpp"] = strings.Replace(files["dt/include/dt/traits.hpp"], "#endif", "#else\n    using MotorDriver = mc::Other;\n#endif", 1)
	files["lib/include/lib/other.hpp"] = `namespace mc {
class Other : public MotorDriver<Other> {
public:
    bool velocity(double& v) const { return false; }
};
}
`
	edges = indexAndEdges(t, files)
	for to := range edges["app/src.copy"] {
		if strings.Contains(to, ".velocity") {
			t.Errorf("a configuration-dependent driver is not guessed: %v", edges["app/src.copy"])
		}
	}
}

func TestJSClassNamedLikeItsModule(t *testing.T) {
	files := map[string]string{
		"src/tracing/TraceManager.ts": `class TraceManager {
  startSpan(name: string) { return name; }
  recordEvent(name: string) { return this.startSpan(name); }
  recordTopic(name: string) { this.recordEvent(name); }
}
export const traceManager = new TraceManager();
export const recordEvent = (name: string) => traceManager.recordEvent(name);
export const startSpan = (name: string) => traceManager.startSpan(name);
`,
		"src/hooks/useFlow.ts": `import { startSpan } from "../tracing/TraceManager";
export function useFlow() {
  startSpan("flow");
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["src/hooks.useFlow"]["src/tracing.TraceManager.startSpan"]; !ok {
		t.Errorf("the imported function is the module's, not the class's method: %v", edges["src/hooks.useFlow"])
	}
	if _, ok := edges["src/tracing.TraceManager.recordTopic"]["src/tracing.TraceManager.TraceManager.recordEvent"]; !ok {
		t.Errorf("this.recordEvent is the class's method: %v", edges["src/tracing.TraceManager.recordTopic"])
	}
	if _, ok := edges["src/tracing.TraceManager.recordTopic"]["src/tracing.TraceManager.recordEvent"]; ok {
		t.Errorf("this.recordEvent is not the module's function: %v", edges["src/tracing.TraceManager.recordTopic"])
	}
}

func TestPythonBareCallNeedsImport(t *testing.T) {
	files := map[string]string{
		"tools/stall.py": `def _n(row, key):
    return int(row.get(key) or 0)
`,
		"reports/text.py": `def n(v):
    return str(v)
`,
		"reports/doc.py": `from reports import text

_n, _p = text.n, text.n
_k, _v = "key", text.n

def build(t):
    return _n(t)
`,
		"reports/helpers.py": `def fmt(v):
    return v
`,
		"reports/star.py": `from reports.helpers import *

def show(v):
    return fmt(v)
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["reports.doc.build"]["tools.stall._n"]; ok {
		t.Errorf("a bare _n() bound by assignment is not another module's _n: %v", edges["reports.doc.build"])
	}
	if _, ok := edges["reports.doc.build"]["reports.text.n"]; !ok {
		t.Errorf("_n = text.n binds text's n: %v", edges["reports.doc.build"])
	}
	if _, ok := edges["reports.star.show"]["reports.helpers.fmt"]; !ok {
		t.Errorf("a star import brings fmt: %v", edges["reports.star.show"])
	}
}

func TestCppOverloadByLibraryArgumentType(t *testing.T) {
	files := map[string]string{
		"fx/include/fx/fixture.hpp": `#include <ros/duration.h>
class Fixture {
public:
    void advance(double seconds) { advance(ros::Duration(seconds)); }
    void advance(ros::Duration delta);
    void stepUntil() {
        ros::Duration step(0.1);
        advance(step);
    }
    static inline const ros::Duration POST_DELAY{5};
};
struct Config { double timeout; };
`,
		"fx/src/fixture.cpp": `#include "fx/fixture.hpp"
void Fixture::advance(ros::Duration delta) {}
`,
		"fx/src/test_wait.cpp": `#include "fx/fixture.hpp"
class Waits : public Fixture {
public:
    void settle(const Config& cfg) {
        advance(cfg.timeout * 0.9);
    }
    void idle() {
        advance(POST_DELAY);
    }
};
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["fx/include/fx.Fixture.stepUntil"]["fx/src.Fixture.advance"]; !ok {
		t.Errorf("a ros::Duration argument picks the ros::Duration overload: %v", edges["fx/include/fx.Fixture.stepUntil"])
	}
	if _, ok := edges["fx/include/fx.Fixture.stepUntil"]["fx/include/fx.Fixture.advance"]; ok {
		t.Errorf("not the double overload: %v", edges["fx/include/fx.Fixture.stepUntil"])
	}
	if _, ok := edges["fx/src.Waits.settle"]["fx/include/fx.Fixture.advance"]; !ok {
		t.Errorf("a number (a double field times a literal) picks the double overload: %v", edges["fx/src.Waits.settle"])
	}
	if _, ok := edges["fx/src.Waits.idle"]["fx/src.Fixture.advance"]; !ok {
		t.Errorf("a ros::Duration static member picks the ros::Duration overload: %v", edges["fx/src.Waits.idle"])
	}
}

func TestCppTemplateMemberGetIsNotPassThrough(t *testing.T) {
	files := map[string]string{
		"idx/include/idx/index.hpp": `template <typename T>
class IndexBase {
public:
    template <typename Tag>
    const auto& get() const { return _index.template get<Tag>(); }
    auto end() const { return _index.end(); }
protected:
    boost::multi_index_container<T> _index;
};
class Partitions {
public:
    bool has(int id) const;
private:
    IndexBase<int> _index;
};
`,
		"idx/src/partitions.cpp": `#include "idx/index.hpp"
bool Partitions::has(int id) const {
    const auto& primary = _index.get<ById>();
    return primary.find(id) != primary.end();
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["idx/src.Partitions.has"]["idx/include/idx.IndexBase.end"]; ok {
		t.Errorf("get<Tag>() returns the library index, not the wrapper: %v", edges["idx/src.Partitions.has"])
	}
}

func TestCppTwoPackagesOneClassName(t *testing.T) {
	files := map[string]string{
		"v1/include/v1/Map.hpp": `#include "types.hpp"
class Map {
public:
    Elevator getElevator(const Location& l) const;
    Elevator getElevator(const NodePtr& n) const { return getElevator(Location::from(n)); }
    bool isElevator(const Location& l) const { return getElevator(l).ok; }
};
`,
		"v1/include/v1/types.hpp": `struct Location { static Location from(const NodePtr& n); };
`,
		"v1/src/Map.cpp": `#include "v1/Map.hpp"
Elevator Map::getElevator(const Location& l) const { return {}; }
`,
		"v2/include/v2/Map.hpp": `#include "types.hpp"
class Map {
public:
    Elevator getElevator(const Location& l) const;
};
`,
		"v2/include/v2/types.hpp": `struct Location { static Location from(const NodePtr& n); };
`,
		"v2/src/Map.cpp": `#include "v2/Map.hpp"
Elevator Map::getElevator(const Location& l) const { return {}; }
`,
	}
	edges := indexAndEdges(t, files)
	for _, caller := range []string{"v1/include/v1.Map.isElevator", "v1/include/v1.Map.getElevator"} {
		if _, ok := edges[caller]["v1/src.Map.getElevator"]; !ok {
			t.Errorf("%s calls its own package's Location overload: %v", caller, edges[caller])
		}
		if _, ok := edges[caller]["v2/src.Map.getElevator"]; ok {
			t.Errorf("%s never reaches the other package's Map: %v", caller, edges[caller])
		}
	}
}

func TestCppOverloadNestedParameterType(t *testing.T) {
	files := map[string]string{
		"geo/include/geo/Location.hpp": `struct Location { int x; };
`,
		"nav/include/nav/Map.hpp": `#include "geo/Location.hpp"
class Map {
public:
    struct Tile { int id; };
    static Point getPosition(const Tile& tile);
    template <typename T>
    std::optional<Point> getPosition(T t) const { return std::nullopt; }
};
`,
		"nav/src/Map.cpp": `#include "nav/Map.hpp"
Point Map::getPosition(const Tile& tile) { return {}; }
`,
		"nav/src/Utils.cpp": `#include "nav/Map.hpp"
Point toPose(Map& map, const Location& location) {
    auto p = map.getPosition(location);
    return *p;
}
`,
	}
	edges := indexAndEdges(t, files)
	if _, ok := edges["nav/src.toPose"]["nav/src.Map.getPosition"]; ok {
		t.Errorf("a Location is no Map::Tile: %v", edges["nav/src.toPose"])
	}
}

func TestCppNestedClassAfterBracedInitializer(t *testing.T) {
	files := map[string]string{
		"tr/include/tr/helper.hpp": `namespace trace::util {
class TracingHelper {
public:
    class TraceWithCounts {
    public:
        TraceWithCounts(const std::string name, const Context& ctx)
                : _name(name)
                , _trace(name, ctx){};
        ~TraceWithCounts() {
            for (const auto& [key, value] : _counts) {
                _trace.setTag(key, value);
            }
        }
        TraceWithCounts(TraceWithCounts&&) = default;
        int incrementCount(const std::string& key) { return ++_counts[key]; }
    private:
        std::map<std::string, int> _counts;
    };
    int incrementCount(const std::string& key) { return getTraceWithCounts().incrementCount(key); }
    TraceWithCounts& getTraceWithCounts() { return *_current; }
private:
    std::optional<TraceWithCounts> _current;
};
}
`,
		"est/src/estimator.cpp": `#include "tr/helper.hpp"
class Estimator {
public:
    void update();
private:
    trace::util::TracingHelper _tracing;
};
void Estimator::update() {
    _tracing.incrementCount("imu");
}
`,
	}
	edges := indexAndEdges(t, files)
	t.Logf("%v", edges)
	if _, ok := edges["tr/include/tr.TracingHelper.incrementCount"]["tr/include/tr.TraceWithCounts.incrementCount"]; !ok {
		t.Errorf("the helper's incrementCount calls the nested class's: %v", edges["tr/include/tr.TracingHelper.incrementCount"])
	}
}
