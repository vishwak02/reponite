//go:build treesitter

package processing

import "testing"

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
		"tests/test_items.py": `def test_make(make_item, mock_call):
    make_item("a")
    mock_call(return_value=1)
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
