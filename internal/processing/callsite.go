// callsite.go keeps what a C/C++ call site says about its target, so resolution
// no longer reduces every call to a bare name. `it->second.erase(pos)` and
// `erase(pos)` are different claims: the first calls a member of an object of
// unknown type — in practice usually a standard container — the second a
// function in scope. Name-only resolution matched both to the repo's one
// `erase` (an unrelated class's method) at name-resolved confidence, which is
// worse than no edge. Pure over content.AST (ADR-018).
package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
)

// CallKind is how a call site names its target.
type CallKind string

const (
	CallPlain  CallKind = "plain"  // f(x): a function in scope (or an implicit this->f inside a method)
	CallSelf   CallKind = "self"   // this->f(x), (*this).f(x): the caller's own class
	CallMember CallKind = "member" // obj.f(x), ptr->f(x): a member of an object whose type is not known here
	CallScoped CallKind = "scoped" // A::f(x), ns::A::f(x): named through a namespace or class scope
)

// CallSite is one call site reduced to what resolution needs.
type CallSite struct {
	Name  string
	Kind  CallKind
	Scope string // CallScoped: the immediate scope (A in ns::A::f)
	Root  string // CallScoped: the outermost scope (ns in ns::A::f) — std, boost, ros, ...
}

// clikeCallSites returns the deduped call sites in a C/C++ body.
func clikeCallSites(body content.AST, r LangRules) []CallSite {
	seen := map[CallSite]bool{}
	var out []CallSite
	for _, call := range descendantsAny(body, r.CallTypes) {
		kids := call.Children()
		if len(kids) == 0 {
			continue
		}
		cs, ok := classifyCallee(kids[0], r)
		if !ok || cs.Name == "" || r.Builtins[cs.Name] || seen[cs] {
			continue
		}
		seen[cs] = true
		out = append(out, cs)
	}
	return out
}

func classifyCallee(fn content.AST, r LangRules) (CallSite, bool) {
	switch fn.Type() {
	case "identifier":
		return CallSite{Name: fn.Text(), Kind: CallPlain}, true
	case "template_function":
		if id := firstChildAny(fn, []string{"identifier"}); id != nil {
			return CallSite{Name: id.Text(), Kind: CallPlain}, true
		}
	case "field_expression":
		kids := fn.Children()
		if len(kids) == 0 {
			return CallSite{}, false
		}
		name := memberName(kids[len(kids)-1])
		if name == "" {
			return CallSite{}, false
		}
		if isThis(kids[0]) {
			return CallSite{Name: name, Kind: CallSelf}, true
		}
		return CallSite{Name: name, Kind: CallMember}, true
	case "qualified_identifier":
		var scopes []string
		n := fn
		for n != nil && n.Type() == "qualified_identifier" {
			kids := n.Children()
			if len(kids) == 0 {
				break
			}
			if s := scopeName(kids[0]); s != "" {
				scopes = append(scopes, s)
			}
			n = kids[len(kids)-1]
		}
		if n == nil {
			return CallSite{}, false
		}
		name := ""
		switch n.Type() {
		case "identifier", "field_identifier", "destructor_name", "operator_name":
			name = n.Text()
		case "template_function":
			if id := firstChildAny(n, []string{"identifier"}); id != nil {
				name = id.Text()
			}
		}
		if name == "" || len(scopes) == 0 {
			return CallSite{Name: name, Kind: CallPlain}, name != ""
		}
		return CallSite{Name: name, Kind: CallScoped, Scope: scopes[len(scopes)-1], Root: scopes[0]}, true
	}
	// Anything else — a call through a function pointer, a subscript, a
	// lambda call — keeps the old identifier rule as a plain call.
	ids := identLeaves(fn, callNameTypes(r))
	if len(ids) == 0 {
		return CallSite{}, false
	}
	return CallSite{Name: ids[len(ids)-1], Kind: CallPlain}, true
}

// memberName is the invoked member of a field_expression's field part.
func memberName(n content.AST) string {
	switch n.Type() {
	case "field_identifier", "destructor_name", "operator_name", "identifier":
		return n.Text()
	case "template_method":
		if id := firstChildAny(n, []string{"field_identifier"}); id != nil {
			return id.Text()
		}
	}
	return ""
}

// isThis: `this` itself, or `(*this)`.
func isThis(n content.AST) bool {
	switch n.Type() {
	case "this":
		return true
	case "parenthesized_expression", "pointer_expression":
		for _, c := range n.Children() {
			if c.IsNamed() {
				return isThis(c)
			}
		}
	}
	return false
}

// scopeName is the name a scope node contributes (ns, Cls, Tmpl<T> -> Tmpl).
func scopeName(n content.AST) string {
	switch n.Type() {
	case "namespace_identifier", "type_identifier", "identifier":
		return n.Text()
	case "template_type":
		if id := firstChildAny(n, []string{"type_identifier"}); id != nil {
			return id.Text()
		}
	}
	return ""
}

// declScope is the class (or namespace) an out-of-class C++ definition is
// scoped to: `void Splitter::forgetWork()` -> "Splitter", `ns::Cls::run` ->
// "Cls". Without it every out-of-class method lost its class, so two classes'
// `init()` in one directory collapsed onto one id.
func declScope(fn content.AST, r LangRules) string {
	d := firstChildAny(fn, r.DeclNameIn)
	if d == nil {
		return ""
	}
	q := firstDescBefore(d, "qualified_identifier", "parameter")
	scope := ""
	for q != nil && q.Type() == "qualified_identifier" {
		kids := q.Children()
		if len(kids) == 0 {
			break
		}
		if s := scopeName(kids[0]); s != "" {
			scope = s
		}
		q = kids[len(kids)-1]
	}
	return scope
}

// firstDescBefore finds the first descendant of type t, not looking past a
// child whose type contains stop (the declarator's parameter list).
func firstDescBefore(n content.AST, t, stop string) content.AST {
	for _, c := range n.Children() {
		if strings.Contains(c.Type(), stop) {
			return nil
		}
		if c.Type() == t {
			return c
		}
		if d := firstDescBefore(c, t, stop); d != nil {
			return d
		}
	}
	return nil
}

// externalScopes are namespaces that are never this repo's own code. A call
// scoped through one is an opaque external leaf, whatever the repo happens to
// define under the same bare name (a repo `sort` is not std::sort).
var externalScopes = map[string]bool{
	"std": true, "boost": true, "ros": true, "rclcpp": true, "Eigen": true, "cv": true,
	"tf": true, "tf2": true, "tf2_ros": true, "pcl": true, "fmt": true, "spdlog": true,
	"absl": true, "google": true, "grpc": true, "nlohmann": true, "YAML": true,
	"pluginlib": true, "actionlib": true, "message_filters": true, "dynamic_reconfigure": true,
	"image_transport": true, "cv_bridge": true, "rosbag": true, "diagnostic_updater": true,
}

// stdMembers are member names of the standard library's containers, strings,
// smart pointers, iterators, streams, atomics and threads. A member call with
// one of these names on an object of unknown type is far more often the
// standard library than a repo class, so it is never resolved to the repo's
// same-named method at name-resolved confidence.
var stdMembers = map[string]bool{
	"begin": true, "end": true, "cbegin": true, "cend": true, "rbegin": true, "rend": true,
	"size": true, "empty": true, "clear": true, "insert": true, "erase": true, "emplace": true,
	"emplace_back": true, "emplace_front": true, "emplace_hint": true, "push_back": true, "push_front": true,
	"pop_back": true, "pop_front": true, "front": true, "back": true, "at": true, "find": true,
	"count": true, "contains": true, "lower_bound": true, "upper_bound": true, "equal_range": true,
	"reserve": true, "resize": true, "capacity": true, "shrink_to_fit": true, "swap": true, "data": true,
	"c_str": true, "substr": true, "append": true, "compare": true, "length": true, "str": true,
	"get": true, "reset": true, "release": true, "lock": true, "unlock": true, "try_lock": true,
	"use_count": true, "expired": true, "load": true, "store": true, "exchange": true,
	"fetch_add": true, "fetch_sub": true, "notify_one": true, "notify_all": true, "wait": true,
	"wait_for": true, "wait_until": true, "join": true, "detach": true, "joinable": true,
	"value": true, "value_or": true, "has_value": true, "push": true, "pop": true, "top": true,
	"assign": true, "replace": true, "starts_with": true, "ends_with": true, "rfind": true,
	"find_first_of": true, "find_last_of": true, "merge": true, "splice": true, "remove": true,
	"remove_if": true, "unique": true, "sort": true, "reverse": true, "extract": true,
	"try_emplace": true, "insert_or_assign": true, "max_size": true, "key_comp": true,
	"what": true, "flush": true, "write": true, "read": true, "good": true, "fail": true,
	"time_since_epoch": true,
}
