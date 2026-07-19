//go:build treesitter

package processing

import "testing"

// Real-grammar extraction for C, C++, and Rust (CI, -tags treesitter). Verifies
// the LangRules node-type assumptions and the declarator/scope name resolution
// against actual parse trees.
func TestCCppRustExtraction(t *testing.T) {
	t.Run("c", func(t *testing.T) {
		src := `#include <stdio.h>
struct Point { int x; int y; };
typedef struct { int a; } Alias;
int add(int a, int b) { return a + b; }
struct Point make() { return add(0,0); }
void run() { add(1, 2); printf("hi"); }
`
		syms := extractSrc(t, ".c", src)
		// declarator name resolution: even the struct-returning `make` is named "make".
		wantFn(t, syms, "add")
		wantFn(t, syms, "make")
		wantFn(t, syms, "run")
		wantType(t, syms, "Point")
		wantType(t, syms, "Alias")
		if !hasCallee(find(syms, "run").Callees, "add") {
			t.Errorf("run should call add; callees=%v", find(syms, "run").Callees)
		}
	})

	t.Run("cpp", func(t *testing.T) {
		src := `#include <vector>
namespace ns { class Widget { public: void draw(); }; }
void ns::Widget::draw() { compute(3); }
int freeFn() { return 1; }
`
		syms := extractSrc(t, ".cpp", src)
		// qualified name ns::Widget::draw resolves to the last segment "draw".
		wantFn(t, syms, "draw")
		wantFn(t, syms, "freeFn")
		wantType(t, syms, "Widget")
	})

	// P0 regression: in-class C++ method definitions name via field_identifier.
	// Before DeclNameTypes, name resolution fell back to the FIRST identifier
	// anywhere in the definition — a parameter type (in=NodeHandle) or the first
	// body identifier (in=enable_controller_subscriber_) — misattributing every
	// grep/usages/topics hit inside such a method.
	t.Run("cpp in-class definitions", func(t *testing.T) {
		src := `#include <ros/ros.h>
class Lidar {
public:
  Lidar(ros::NodeHandle& nh) : nh_(nh) {}
  ~Lidar() { shutdown(); }
  bool init(ros::NodeHandle& nh)
  {
    scan_pub_ = nh.advertise<sensor_msgs::LaserScan>("scan", 10);
    return true;
  }
  bool start()
  {
    started_flag_ = true;
    return started_flag_;
  }
  bool operator==(const Lidar& other) const { return id_ == other.id_; }
  auto getHandle() -> ros::NodeHandle { return nh_; }
private:
  ros::NodeHandle nh_;
  ros::Publisher scan_pub_;
  bool started_flag_;
  int id_;
};
`
		syms := extractSrc(t, ".cpp", src)
		wantFn(t, syms, "init")     // NOT "NodeHandle" (the parameter's type)
		wantFn(t, syms, "start")    // NOT "started_flag_" (the first body identifier)
		wantFn(t, syms, "Lidar")    // constructor, NOT an initializer identifier
		wantFn(t, syms, "~Lidar")   // destructor names via destructor_name
		wantFn(t, syms, "operator==") // operator overloads via operator_name
		wantFn(t, syms, "getHandle")  // NOT "NodeHandle" (the trailing return type)
		for _, bad := range []string{"NodeHandle", "started_flag_", "scan_pub_"} {
			if s := find(syms, bad); s != nil && s.Kind != "type" {
				t.Errorf("callable wrongly named %q (kind %s) — invented from a param/body identifier", bad, s.Kind)
			}
		}
	})

	// Bare type references and forward declarations are not definitions: they
	// must produce neither symbols nor enclosing-symbol spans.
	t.Run("cpp type references", func(t *testing.T) {
		src := `class Forward;
struct Real { int a; };
struct Real instance;
void user(struct Real r) {}
`
		syms := extractSrc(t, ".cpp", src)
		wantType(t, syms, "Real")
		wantFn(t, syms, "user")
		count := 0
		for _, s := range syms {
			if s.Name == "Real" && s.Kind == "type" {
				count++
			}
		}
		if count != 1 {
			t.Errorf("type Real must be extracted exactly once (definition), got %d (references leaked)", count)
		}
		if s := find(syms, "Forward"); s != nil {
			t.Errorf("forward declaration must not become a symbol: %+v", *s)
		}
	})

	// The span layer must mirror the same rules — this is what grep/usages/
	// topics attribute an endpoint's enclosing symbol (`in`) from.
	t.Run("cpp spans attribute to the method", func(t *testing.T) {
		src := `class BumpBlink {
public:
  bool init()
  {
    enable_sub_ = nh_.subscribe("enable", 10, &BumpBlink::enableCB, this);
    blink_publisher_ = nh_.advertise<kobuki_msgs::Led>("commands/led1", 10);
    return true;
  }
private:
  ros::Subscriber enable_sub_;
  ros::Publisher blink_publisher_;
};
`
		rules, _ := RulesForExt(".cpp")
		_, spans, err := parseFileRules([]byte(src), ".cpp", rules)
		if err != nil {
			t.Fatal(err)
		}
		const advertiseLine = 6
		inner := ""
		innerSize := 1 << 30
		for _, sp := range spans {
			if sp.Name == "enable_sub_" || sp.Name == "NodeHandle" {
				t.Errorf("spurious span named %q — spans must not be named after member/param identifiers", sp.Name)
			}
			if advertiseLine >= sp.StartLine && advertiseLine <= sp.EndLine && sp.EndLine-sp.StartLine < innerSize {
				inner, innerSize = sp.Name, sp.EndLine-sp.StartLine
			}
		}
		if inner != "init" {
			t.Errorf("the advertise line must attribute to its method: got %q, want init (spans: %+v)", inner, spans)
		}
	})

	t.Run("rust", func(t *testing.T) {
		src := `pub struct User { pub id: u32 }
pub trait Greet { fn hello(&self); }
impl User { pub fn new() -> Self { User{id:0} } fn helper(&self) {} }
pub fn compute(n: i32) -> i32 { helper(); n }
`
		syms := extractSrc(t, ".rs", src)
		wantType(t, syms, "User")
		wantType(t, syms, "Greet")
		wantFn(t, syms, "compute")
		// impl methods are qualified by the impl'd type (Recv=User).
		newM := findRecv(syms, "new", "User")
		if newM == nil {
			t.Fatalf("impl method User.new not found (recv-qualified); syms=%v", symNames(syms))
		}
		if !hasCallee(find(syms, "compute").Callees, "helper") {
			t.Errorf("compute should call helper; callees=%v", find(syms, "compute").Callees)
		}
	})
}

func extractSrc(t *testing.T, ext, src string) []Symbol {
	t.Helper()
	rules, ok := RulesForExt(ext)
	if !ok {
		t.Fatalf("no rules for %s", ext)
	}
	root, _, err := parseFileRules([]byte(src), ext, rules)
	if err != nil {
		t.Fatal(err)
	}
	return Extract(root, rules, 1)
}

func wantFn(t *testing.T, syms []Symbol, name string) {
	t.Helper()
	if s := find(syms, name); s == nil {
		t.Errorf("function %q not extracted; got %v", name, symNames(syms))
	}
}

func wantType(t *testing.T, syms []Symbol, name string) {
	t.Helper()
	s := find(syms, name)
	if s == nil || s.Kind != "type" {
		t.Errorf("type %q not extracted; got %v", name, symNames(syms))
	}
}

// Member/method calls must resolve to the invoked method, not the receiver
// variable (regression: C++ obj.method()/ptr->m() and Java Bar.x() used to
// capture the receiver because the method name is a field_identifier / the last
// ident of a flat method_invocation).
func TestMemberCallResolvesToMethod(t *testing.T) {
	t.Run("cpp", func(t *testing.T) {
		src := `void run(Task* task, Frag frag) {
  task->getAssignedAgent();
  frag.getModel();
  freeFn();
}`
		callees := extractCalleesOf(t, ".cpp", src, "run")
		for _, want := range []string{"getAssignedAgent", "getModel", "freeFn"} {
			if !hasCallee(callees, want) {
				t.Errorf("cpp: callees %v missing %q (member call should resolve to the method)", callees, want)
			}
		}
		for _, bad := range []string{"task", "frag"} {
			if hasCallee(callees, bad) {
				t.Errorf("cpp: callees %v wrongly include the receiver %q", callees, bad)
			}
		}
	})
	t.Run("java", func(t *testing.T) {
		src := `class C { void run() { Bar.x(); obj.method(); helper(); } }`
		callees := extractCalleesOf(t, ".java", src, "run")
		for _, want := range []string{"x", "method", "helper"} {
			if !hasCallee(callees, want) {
				t.Errorf("java: callees %v missing %q", callees, want)
			}
		}
		if hasCallee(callees, "Bar") || hasCallee(callees, "obj") {
			t.Errorf("java: callees %v wrongly include a receiver", callees)
		}
	})
}

func extractCalleesOf(t *testing.T, ext, src, fn string) []string {
	t.Helper()
	s := find(extractSrc(t, ext, src), fn)
	if s == nil {
		t.Fatalf("function %q not extracted", fn)
	}
	return s.Callees
}

func findRecv(syms []Symbol, name, recv string) *Symbol {
	for i := range syms {
		if syms[i].Name == name && syms[i].Recv == recv {
			return &syms[i]
		}
	}
	return nil
}
