//go:build treesitter

package processing

import (
	"path"
	"sort"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/storage"
)

// indexCppTree indexes C, C++ and package.xml files as the indexer reads
// them (a .h header by its content) and returns caller -> callee -> method.
func indexCppTree(t *testing.T, files map[string]string) map[string]map[string]string {
	t.Helper()
	var paths []string
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths) // the indexer reads a tree in path order
	var parsed []ParsedFile
	for _, p := range paths {
		src := files[p]
		if path.Base(p) == "package.xml" {
			parsed = append(parsed, ParsedFile{Path: p, Content: src, Lang: "project"})
			continue
		}
		rules, gext, ok := RulesForSource(path.Ext(p), []byte(src))
		if !ok {
			t.Fatalf("no rules for %s", p)
		}
		root, spans, err := parseFileRules([]byte(src), gext, rules)
		if err != nil {
			t.Fatal(err)
		}
		parsed = append(parsed, ParsedFile{Path: p, Content: src, Lang: rules.Name, IsTest: IsTestPath(p),
			Symbols: Extract(root, rules, 1), Spans: spans, Types: typeFactsFor(root, rules)})
	}
	m := storage.NewMem()
	if err := IndexFiles(m, "r", "v1", 1, parsed); err != nil {
		t.Fatal(err)
	}
	out := map[string]map[string]string{}
	for caller, cs := range m.Snapshot("r", "v1").Callees {
		out[caller] = map[string]string{}
		for _, c := range cs {
			out[caller][c.Name] = c.ResolutionMethod
		}
	}
	return out
}

func wantEdge(t *testing.T, edges map[string]map[string]string, from, to string) {
	t.Helper()
	if _, ok := edges[from][to]; !ok {
		t.Errorf("missing edge %s -> %s; have %v", from, to, edges[from])
	}
}

func wantNoEdge(t *testing.T, edges map[string]map[string]string, from, to string) {
	t.Helper()
	if m, ok := edges[from][to]; ok {
		t.Errorf("unexpected edge %s -> %s (%s)", from, to, m)
	}
}

// A conversion function is named after its type, never after an identifier
// of its body: `operator bool() { return slot(); }` named "slot" was a fake
// method that caught every call of the base class's slot().
func TestCppConversionOperatorName(t *testing.T) {
	syms := extractSrc(t, ".cpp", `class Place : public Position {
public:
    explicit constexpr operator bool() const { return slot() != 0; }
    int inner() const { return slot() + 1; }
};
Place::operator const char*() const { return label(); }
`)
	for _, s := range syms {
		if s.Name == "slot" || s.Name == "label" {
			t.Errorf("a conversion function named after its body: %+v", s)
		}
	}
	if s := findRecv(syms, "operator bool", "Place"); s == nil {
		t.Errorf("operator bool not extracted: %v", symNames(syms))
	}
	if s := findRecv(syms, "operator const char *", "Place"); s == nil {
		t.Errorf("out-of-class conversion operator not extracted: %v", symNames(syms))
	}
	edges := indexCppTree(t, map[string]string{
		"shop/include/shop/place.hpp": `#pragma once
class Position { public: constexpr int slot() const { return _slot; } private: int _slot; };
class Place : public Position {
public:
    explicit constexpr operator bool() const { return slot() != 0; }
};
`,
		"shop/src/use.cpp": `#include "shop/place.hpp"
int pick(const Place& place) { return place.slot(); }
`,
	})
	wantEdge(t, edges, "shop/src.pick", "shop/include/shop.Position.slot")
}

// A pimpl: class Widget::Impl defined in the source file is Widget's nested
// Impl, distinct from every other file's Impl; its own methods name it Impl.
func TestCppPimplNestedClass(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"ui/include/ui/widget.hpp": `#pragma once
#include <memory>
class Widget {
public:
    void reset();
    class Handle { public: void off(); std::shared_ptr<class Impl> _impl; };
private:
    class Impl;
    std::unique_ptr<Impl> _impl;
};
class Panel { public: void reset(); private: class Impl; std::unique_ptr<Impl> _impl; };
`,
		"ui/src/widget.cpp": `#include "ui/widget.hpp"
class Widget::Impl {
public:
    void reset() { clear(); }
    void clear() {}
    void send() {}
    void again(std::shared_ptr<Impl> self) { self->clear(); }
};
void Widget::reset() { _impl->reset(); }
void Widget::Handle::off() { if (auto impl = _impl) { impl->send(); } }
`,
		"ui/src/panel.cpp": `#include "ui/widget.hpp"
class Panel::Impl {
public:
    void reset() {}
};
void Panel::reset() { _impl->reset(); }
`,
	})
	// panel.cpp is read first: its Impl keeps the plain id, widget.cpp's is Impl@widget
	wantEdge(t, edges, "ui/src.Widget.reset", "ui/src.Impl@widget.reset")
	wantEdge(t, edges, "ui/src.Impl.again", "ui/src.Impl.clear")
	wantEdge(t, edges, "ui/src.Handle.off", "ui/src.Impl.send")
	wantEdge(t, edges, "ui/src.Panel.reset", "ui/src.Impl.reset")
	wantNoEdge(t, edges, "ui/src.Widget.reset", "ui/src.Impl.reset")
}

// An explicit specialization is named after its template, not after its
// template argument; a builder chain on a temporary is typed step by step.
func TestCppSpecializationNameAndBuilder(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"conv/include/conv/json.hpp": `#pragma once
#include "conv/order.hpp"
namespace shop::json {
template <typename T> T fromJson(const Json& j);
template <> Order fromJson<Order>(const Json& j);
}
`,
		"conv/include/conv/order.hpp": `#pragma once
class Order {};
class OrderBuilder {
public:
    OrderBuilder& setId(int id) { return *this; }
    OrderBuilder& setName(const std::string& n) { return *this; }
    Order build() const { return Order{}; }
};
`,
		"conv/src/json.cpp": `#include "conv/json.hpp"
namespace shop::json {
template <>
Order fromJson<Order>(const Json& j) {
    return OrderBuilder().setId(j.at("id")).setName(j.at("name")).build();
}
}
`,
		"app/src/load.cpp": `#include "conv/json.hpp"
Order load(const Json& j) { return shop::json::fromJson<Order>(j); }
`,
	})
	wantEdge(t, edges, "conv/src.fromJson", "conv/include/conv.OrderBuilder.setId")
	wantEdge(t, edges, "conv/src.fromJson", "conv/include/conv.OrderBuilder.setName")
	wantEdge(t, edges, "conv/src.fromJson", "conv/include/conv.OrderBuilder.build")
	wantEdge(t, edges, "app/src.load", "conv/src.fromJson")
}

// Several programs in one directory, each its own main: one id for the
// truth's (directory, name), but each main's calls resolved in its own file.
func TestCppProgramsShareDirectory(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"tools/alpha.cpp": `static int scan() { return 1; }
int main() { return scan(); }
`,
		"tools/beta.cpp": `#include "tools/store.hpp"
static int probe() { return 2; }
int main() { Store s; s.sweep(); return probe(); }
`,
		"tools/store.hpp": `#pragma once
class Store { public: void sweep(); };
`,
		"tools/store.cpp": `#include "tools/store.hpp"
void Store::sweep() {}
`,
	})
	wantEdge(t, edges, "tools.main", "tools.scan")
	wantEdge(t, edges, "tools.main", "tools.probe")
	wantEdge(t, edges, "tools.main", "tools.Store.sweep")
}

// A class defined in a function body (a visitor for std::visit) has methods
// of its own, which make the calls written in them.
func TestCppLocalClassMethods(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"ui/include/ui/lamp.hpp": `#pragma once
class Lamp { public: void on(); void off(); };
class Board { public: void show(const Action& a); Lamp _lamp; };
`,
		"ui/src/board.cpp": `#include "ui/lamp.hpp"
void Lamp::on() {}
void Lamp::off() {}
void Board::show(const Action& a) {
    struct Visitor {
        void operator()(const On&) { lamp->on(); }
        void operator()(const Off&) { lamp->off(); }
        Lamp* lamp;
    };
    std::visit(Visitor{&_lamp}, a);
}
`,
	})
	wantEdge(t, edges, "ui/src.Visitor.operator()", "ui/src.Lamp.on")
	wantEdge(t, edges, "ui/src.Visitor.operator()", "ui/src.Lamp.off")
	wantNoEdge(t, edges, "ui/src.Board.show", "ui/src.Lamp.on")
}

// CRTP: a base's T& with…() returns the derived builder its instantiation
// names, so a chain keeps its type across base and derived methods; a
// source file's own alias of a fixture name wins over another file's class.
func TestCppCrtpBuilderChain(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"lab/include/lab/builder.hpp": `#pragma once
template <typename T> struct crtp { T& self() { return static_cast<T&>(*this); } };
template <typename T>
class BuilderBase : public crtp<T> {
public:
    T& withName(const std::string& n) { return this->self(); }
    T& withSize(int s) { return this->self(); }
};
class BoxBuilder : public BuilderBase<BoxBuilder> {
public:
    BoxBuilder& withLid(bool lid) { return *this; }
    int build() { return 0; }
};
class BagBuilder : public BuilderBase<BagBuilder> {
public:
    BagBuilder& withLid(bool lid) { return *this; }
    int build() { return 1; }
};
`,
		"lab/test/test_box.cpp": `#include "lab/builder.hpp"
class BoxFixture { public: BoxBuilder getBuilder() { return BoxBuilder(); } };
using Scenario = BoxFixture;
TEST_F(Scenario, Builds) { getBuilder().withName("a").withSize(2).withLid(true).build(); }
`,
		"lab/test/test_bag.cpp": `#include "lab/builder.hpp"
class Scenario { public: BagBuilder getBuilder() { return BagBuilder(); } };
TEST_F(Scenario, Builds) { getBuilder().withName("a").withLid(true).build(); }
`,
	})
	// test_bag.cpp is read first: its Scenario keeps the plain id
	bag, box := "lab/test.Scenario.Builds", "lab/test.Scenario@test_box.Builds"
	wantEdge(t, edges, box, "lab/include/lab.BuilderBase.withSize")
	wantEdge(t, edges, box, "lab/include/lab.BoxBuilder.withLid")
	wantEdge(t, edges, box, "lab/include/lab.BoxBuilder.build")
	wantNoEdge(t, edges, box, "lab/include/lab.BagBuilder.withLid")
	wantEdge(t, edges, bag, "lab/include/lab.BagBuilder.withLid")
	wantNoEdge(t, edges, bag, "lab/include/lab.BoxBuilder.withLid")
}

// Two copies of a package (a library and its successor) declare the same
// free functions in headers of the same path: a call binds to the copy the
// caller's build includes (its package.xml dependencies), whether the
// function is plain or written ns::f.
func TestCppPackageCopiesByDependency(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"robot/core/package.xml":    `<package><name>core</name></package>`,
		"robot/core2/package.xml":   `<package><name>core2</name></package>`,
		"robot/core2_t/package.xml": `<package><name>core2_t</name><depend>core2</depend></package>`,
		"robot/core/include/core/util.hpp": `#pragma once
namespace core { namespace Util { int toPose(int v); } bool ready(int v); }
`,
		"robot/core/src/util.cpp": `#include "core/util.hpp"
int core::Util::toPose(int v) { return v; }
bool core::ready(int v) { return true; }
`,
		"robot/core2/include/core/util.hpp": `#pragma once
namespace core { namespace Util { int toPose(int v); } bool ready(int v); }
`,
		"robot/core2/src/util.cpp": `#include "core/util.hpp"
int core::Util::toPose(int v) { return v + 1; }
bool core::ready(int v) { return false; }
`,
		"robot/core2_t/src/check.cpp": `#include "core/util.hpp"
bool check() { return core::ready(core::Util::toPose(1)); }
`,
	})
	wantEdge(t, edges, "robot/core2_t/src.check", "robot/core2/src.core.ready")
	wantEdge(t, edges, "robot/core2_t/src.check", "robot/core2/src.Util.toPose")
	wantNoEdge(t, edges, "robot/core2_t/src.check", "robot/core/src.core.ready")
	wantNoEdge(t, edges, "robot/core2_t/src.check", "robot/core/src.Util.toPose")
}

// A repo macro a function body uses makes the calls of its expansion.
func TestCppBodyMacroCalls(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"svc/include/svc/trace.hpp": `#pragma once
#include <string>
class Tracer { public: static Tracer& instance(); void logError(const std::string& m); };
std::string methodName(const char* pretty);
#define TRACE_ERROR(msg) Tracer::instance().logError(msg)
#define METHOD_NAME methodName(__PRETTY_FUNCTION__)
`,
		"svc/src/trace.cpp": `#include "svc/trace.hpp"
Tracer& Tracer::instance() { static Tracer t; return t; }
void Tracer::logError(const std::string& m) {}
std::string methodName(const char* pretty) { return pretty; }
`,
		"svc/src/worker.cpp": `#include "svc/trace.hpp"
void work(int n) {
    auto where = METHOD_NAME;
    TRACE_ERROR("failed " + std::to_string(n));
}
`,
	})
	wantEdge(t, edges, "svc/src.work", "svc/src.methodName")
	wantEdge(t, edges, "svc/src.work", "svc/src.Tracer.instance")
	wantEdge(t, edges, "svc/src.work", "svc/src.Tracer.logError")
}

// Arrays of objects, iterators from std::begin, an optional's value_or, and
// what a free function returns type the receivers of member calls.
func TestCppReceiverForms(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"motor/include/motor/cmd.hpp": `#pragma once
#include <optional>
#include <vector>
#include <memory>
class Command { public: void stop(); };
class Reply { public: bool alarm() const; };
class Part { public: void deinit(); };
Reply lastReply(int id);
std::optional<Command> findCommand(int id);
class Driver {
public:
    void halt(int i);
private:
    Command _command[2];
    std::vector<std::shared_ptr<Part>> _parts;
};
`,
		"motor/src/cmd.cpp": `#include "motor/cmd.hpp"
void Command::stop() {}
bool Reply::alarm() const { return false; }
void Part::deinit() {}
`,
		"motor/src/driver.cpp": `#include "motor/cmd.hpp"
void Driver::halt(int i) {
    _command[i].stop();
    lastReply(i).alarm();
    findCommand(i).value_or(Command{}).stop();
    for (auto it = std::begin(_parts); it != std::end(_parts); ++it) { (*it)->deinit(); }
}
`,
	})
	from := "motor/src.Driver.halt"
	wantEdge(t, edges, from, "motor/src.Command.stop")
	wantEdge(t, edges, from, "motor/src.Reply.alarm")
	wantEdge(t, edges, from, "motor/src.Part.deinit")
}

// A C library's functions, declared in a C header a C++ source includes,
// are the C++ code's calls; C callers stay unrestricted.
func TestCppCallsIntoCLibrary(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"lib/sensor/core/inc/sensor_api.h": `#ifndef SENSOR_API_H
#define SENSOR_API_H
int Sensor_Start(void* dev);
#endif
`,
		"lib/sensor/core/src/sensor_api.c": `#include "sensor_api.h"
int Sensor_Start(void* dev) { return 0; }
static int helper(void) { return 1; }
`,
		"lib/sensor/src/sensor.cpp": `#include <sensor_api.h>
class Sensor { public: int enable(); void* _dev; };
int Sensor::enable() { return Sensor_Start(_dev); }
`,
	})
	wantEdge(t, edges, "lib/sensor/src.Sensor.enable", "lib/sensor/core/src.Sensor_Start")
	if strings.Contains(strings.Join(symKeys(edges), " "), "helper") && edges["lib/sensor/src.Sensor.enable"]["lib/sensor/core/src.helper"] != "" {
		t.Errorf("a static C function reached from another file")
	}
}

// Two classes each nest a class of one name: Board::Handle's field names
// Board, a nested class's member alias names its own nested class, and a
// namespace alias names the namespace it stands for.
func TestCppNestedNamesAndAliases(t *testing.T) {
	edges := indexCppTree(t, map[string]string{
		"ui/include/ui/board.hpp": `#pragma once
#include "view/view.hpp"
class Lights { public: void send(int id); };
class Board {
public:
    class Handle { public: bool ready() const; void show(); Board& _board; };
    Lights _lights;
};
template <typename Spec = int>
class TaskBoard {
public:
    template <typename T = int>
    class AccessImpl { public: bool hasGoal() const { return true; } };
    using Access = AccessImpl<>;
};
namespace shop::model { bool parseColor(const char* s); }
`,
		"ui/include/view/view.hpp": `#pragma once
class Screen;
class View {
public:
    class Handle { public: bool ready() const; Screen& _board; };
};
template <typename Spec = int>
class GoalBoard {
public:
    template <typename T = int>
    class AccessImpl { public: bool hasGoal() const { return false; } };
    using Access = AccessImpl<>;
};
`,
		"ui/src/board.cpp": `#include "ui/board.hpp"
void Lights::send(int id) {}
bool Board::Handle::ready() const { return true; }
void Board::Handle::show() { _board._lights.send(1); }
bool shop::model::parseColor(const char* s) { return true; }
`,
		"ui/src/use.cpp": `#include "ui/board.hpp"
using MyBoard = TaskBoard<>;
namespace fm = shop::model;
bool check(const Blackboard& bb) {
    const MyBoard::Access access(bb);
    return access.hasGoal() && fm::parseColor("#fff");
}
`,
	})
	wantEdge(t, edges, "ui/src.Handle.show", "ui/src.Lights.send")
	wantEdge(t, edges, "ui/src.check", "ui/include/ui.AccessImpl.hasGoal")
	wantNoEdge(t, edges, "ui/src.check", "ui/include/view.AccessImpl.hasGoal")
	wantEdge(t, edges, "ui/src.check", "ui/src.model.parseColor")
}
