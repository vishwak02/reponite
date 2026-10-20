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
