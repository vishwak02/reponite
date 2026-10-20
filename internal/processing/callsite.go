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
	CallModule CallKind = "module" // f() imported from, or m.f() on, a module of the repo (Scope: its package)
)

// CallSite is one call site reduced to what resolution needs.
type CallSite struct {
	Name  string
	Kind  CallKind
	Scope string // CallScoped: the immediate scope (A in ns::A::f); CallTyped: the receiver's class
	Root  string // CallScoped: the outermost scope (ns in ns::A::f) — std, boost, ros, ...
	// Recv is a CallMember receiver's expression (cpptypes.go recvExpr),
	// typed at index time once every file's class facts are known.
	Recv string
	// NArgs is a C/C++ call's argument count plus one (0: not counted), to
	// tell overloads of one name apart.
	NArgs int
}

// CallTyped: a member call whose receiver's declared type is known (a
// parameter, local, or member field) — resolved on that class, its bases, and
// the overrides of its subclasses.
const CallTyped CallKind = "typed"

// clikeCallSites returns the deduped call sites in a C/C++ body; vars (may be
// nil) are the function's parameter and local types.
func clikeCallSites(body content.AST, r LangRules, vars map[string]string) []CallSite {
	seen := map[CallSite]bool{}
	var out []CallSite
	for _, call := range descendantsAny(body, r.CallTypes) {
		kids := call.Children()
		if len(kids) == 0 {
			continue
		}
		cs, ok := classifyCallee(kids[0], r)
		if ok {
			for _, k := range kids {
				if k.Type() == "argument_list" {
					n := 0
					for _, a := range k.Children() {
						if a.IsNamed() && !isCommentType(a.Type()) {
							n++
						}
					}
					cs.NArgs = n + 1
				}
			}
		}
		if ok && cs.Kind == CallMember && r.Name == "cpp" {
			if k := kids[0].Children(); len(k) > 0 {
				cs.Recv = recvExpr(k[0], vars)
			}
		}
		if !ok || cs.Name == "" || cs.Kind == CallPlain && r.Builtins[cs.Name] || seen[cs] {
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
	inner := fn
	for inner.Type() == "parenthesized_expression" {
		var next content.AST
		for _, c := range inner.Children() {
			if c.IsNamed() {
				next = c
			}
		}
		if next == nil {
			break
		}
		inner = next
	}
	if isFunctionLiteral(inner.Type()) {
		return CallSite{}, false // [&]{ … }(): the body's names are not the callee
	}
	// Anything else — a call through a function pointer, a subscript — keeps
	// the old identifier rule as a plain call.
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
	d := declaratorOf(fn, r)
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

// CallExternal: the target is decided at the call site to be outside the repo
// (bound by an import of an external module, or a language global such as
// console/Math/JSON). Scope carries the module or global.
const CallExternal CallKind = "external"

// sitesFromQualified derives call-site shapes for the languages whose calls
// are extracted as (qualifier, name) pairs — JS/TS, Python, Java, Rust — using
// the caller file's import bindings. Before this, a plain `put(...)` that
// redux-saga's effects module bound, or `console.error(...)`, was matched to
// whatever repo function happened to share the bare name.
// modPkg resolves an import's module to the package of a repo module (nil:
// no module resolution).
func sitesFromQualified(qcs []QualifiedCall, byLocal map[string]ImportBinding, lang string, inRepo func(module string) bool, modPkg func(module string) (string, bool)) []CallSite {
	if modPkg == nil {
		modPkg = func(string) (string, bool) { return "", false }
	}
	globals := langGlobals[lang]
	seen := map[CallSite]bool{}
	var out []CallSite
	add := func(cs CallSite) {
		if cs.Name != "" && !seen[cs] {
			seen[cs] = true
			out = append(out, cs)
		}
	}
	for _, qc := range qcs {
		b, imported := byLocal[qc.Qualifier]
		switch {
		case qc.Qualifier == selfQualifier:
			add(CallSite{Name: qc.Name, Kind: CallSelf})
		case qc.Qualifier == superQualifier:
			add(CallSite{Name: qc.Name, Kind: CallSelf, Scope: superScope})
		case qc.Qualifier == "":
			if b, ok := byLocal[qc.Name]; ok {
				if pkg, ok := modPkg(b.Module); ok {
					name := b.Symbol
					if name == "" {
						name = qc.Name // a default import names the module's default export
					}
					add(CallSite{Name: name, Kind: CallModule, Scope: pkg})
				} else if !isRelativeModule(b.Module) && !inRepo(b.Module) {
					add(CallSite{Name: qc.Name, Kind: CallExternal, Scope: b.Module})
				} else {
					add(CallSite{Name: qc.Name, Kind: CallPlain})
				}
			} else {
				add(CallSite{Name: qc.Name, Kind: CallPlain})
			}
		case imported && lang == "python" && qc.Chain == 3 && qc.Attr != "" && b.Symbol == "" && pySubmodule(b, qc.Attr, modPkg) != "":
			// pkg.sub.f() on an imported package: f of its submodule sub
			add(CallSite{Name: qc.Name, Kind: CallModule, Scope: pySubmodule(b, qc.Attr, modPkg)})
		case imported && !b.Default && qc.Chain <= 2 && moduleCall(b, lang, modPkg) != "":
			// m.f() on an imported repo module (import x as m, * as m,
			// from pkg import submodule)
			add(CallSite{Name: qc.Name, Kind: CallModule, Scope: moduleCall(b, lang, modPkg)})
		case globals[qc.Qualifier]:
			add(CallSite{Name: qc.Name, Kind: CallExternal, Scope: qc.Qualifier})
		default:
			if b, ok := byLocal[qc.Qualifier]; ok {
				_, repoModule := modPkg(b.Module)
				switch {
				case (repoModule || isRelativeModule(b.Module)) && b.Symbol != "" && qc.Chain <= 2 && isTypeName(b.Symbol):
					add(CallSite{Name: qc.Name, Kind: CallScoped, Scope: b.Symbol}) // Cls.method() on an imported repo class
				case repoModule || isRelativeModule(b.Module):
					add(CallSite{Name: qc.Name, Kind: CallMember}) // f().then(), obj.method(): a method on a value
				case inRepo(b.Module):
					add(CallSite{Name: qc.Name, Kind: CallPlain}) // a namespace import of repo code
				default:
					add(CallSite{Name: qc.Name, Kind: CallExternal, Scope: b.Module})
				}
				continue
			}
			cs := CallSite{Name: qc.Name, Kind: CallMember}
			if lang == "java" && qc.Chain == 2 {
				cs.Recv = qc.Qualifier // typed later from the method's variables or its class's fields
			}
			add(cs)
		}
	}
	return out
}

// moduleCall is the package of the repo module a qualifier names: the module
// itself for a whole-module import, or a submodule imported by name; "" when
// the qualifier is not a module of the repo.
func moduleCall(b ImportBinding, lang string, modPkg func(string) (string, bool)) string {
	if b.Symbol == "" {
		if pkg, ok := modPkg(b.Module); ok {
			return pkg
		}
		return ""
	}
	if lang == "python" {
		sub := b.Module + "." + b.Symbol
		if strings.HasSuffix(b.Module, ".") {
			sub = b.Module + b.Symbol // from . import users
		}
		if pkg, ok := modPkg(sub); ok {
			return pkg
		}
	}
	return ""
}

// langGlobals are receivers that always name the runtime, never repo code.
var langGlobals = map[string]map[string]bool{
	"javascript": jsGlobals, "typescript": jsGlobals,
	"python": {"os": true, "sys": true, "json": true, "re": true, "math": true, "time": true, "logging": true,
		"subprocess": true, "shutil": true, "itertools": true, "functools": true, "collections": true, "datetime": true,
		"np": true, "pd": true, "rospy": true, "rclpy": true, "yaml": true, "pathlib": true, "random": true, "str": true,
		"dict": true, "list": true, "set": true, "tuple": true, "int": true, "float": true},
	"java": {"System": true, "Math": true, "String": true, "Integer": true, "Long": true, "Double": true, "Boolean": true,
		"Arrays": true, "Collections": true, "Objects": true, "List": true, "Map": true, "Set": true, "Optional": true,
		"Thread": true, "LocalDateTime": true, "Instant": true, "Duration": true, "UUID": true},
}

var jsGlobals = map[string]bool{
	"console": true, "Math": true, "JSON": true, "Object": true, "Array": true, "Promise": true, "Number": true,
	"String": true, "Boolean": true, "Date": true, "RegExp": true, "Symbol": true, "Reflect": true, "Intl": true,
	"window": true, "document": true, "navigator": true, "localStorage": true, "sessionStorage": true,
	"location": true, "history": true, "process": true, "Buffer": true, "globalThis": true, "performance": true,
	"crypto": true, "URL": true, "URLSearchParams": true, "setTimeout": true, "setInterval": true, "fetch": true,
	"module": true, "require": true, "exports": true, "jest": true, "expect": true, "cy": true,
}

// memberBuiltins are method names of each language's standard types (arrays,
// maps, strings, promises, dicts, lists, streams). A member call with one of
// these on an object of unknown type is not pinned on the repo's same-named
// method — the object is at least as likely to be a builtin.
var memberBuiltins = map[string]map[string]bool{
	"cpp": stdMembers, "c": stdMembers,
	"javascript": jsMembers, "typescript": jsMembers, "rust": rustMembers, "go": goMembers,
	"python": {"append": true, "extend": true, "insert": true, "remove": true, "pop": true, "clear": true, "copy": true,
		"count": true, "index": true, "sort": true, "reverse": true, "get": true, "items": true, "keys": true, "values": true,
		"update": true, "setdefault": true, "add": true, "discard": true, "split": true, "join": true, "strip": true,
		"lstrip": true, "rstrip": true, "replace": true, "startswith": true, "endswith": true, "format": true, "lower": true,
		"upper": true, "encode": true, "decode": true, "find": true, "read": true, "write": true, "close": true,
		"readlines": true, "exists": true, "open": true, "sleep": true, "info": true, "debug": true, "warning": true,
		"error": true, "exception": true, "put": true, "wait": true, "start": true, "run": true,
		// set, str and Django QuerySet methods: on a value of unknown type
		// they are almost always these, not the repo's method of the name
		"intersection": true, "union": true, "difference": true, "symmetric_difference": true,
		"issubset": true, "issuperset": true, "isdisjoint": true, "title": true,
		"capitalize": true, "isdigit": true, "isalpha": true, "splitlines": true, "partition": true,
		"rpartition": true, "zfill": true, "rfind": true, "filter": true, "exclude": true,
		"order_by": true, "values_list": true, "annotate": true, "aggregate": true,
		"select_related": true, "prefetch_related": true, "first": true, "last": true,
		"distinct": true, "only": true, "defer": true, "get_or_create": true, "update_or_create": true,
		"bulk_create": true, "bulk_update": true, "iterator": true, "using": true},
	"java": {"get": true, "put": true, "add": true, "remove": true, "contains": true, "size": true, "isEmpty": true,
		"clear": true, "equals": true, "hashCode": true, "toString": true, "stream": true, "map": true, "filter": true,
		"collect": true, "forEach": true, "orElse": true, "isPresent": true, "length": true, "append": true, "close": true,
		"println": true, "format": true, "keySet": true, "values": true, "entrySet": true, "iterator": true, "next": true, "hasNext": true},
}

// goMembers are the methods of the standard library's interfaces (error,
// io, http, sync, context, sort, fmt.Stringer) and of loggers: on a value
// whose type is not known, one of these is no more the repo's method than
// any other type's.
var goMembers = map[string]bool{
	"Error": true, "String": true, "Read": true, "Write": true, "Close": true, "Seek": true, "ReadFrom": true,
	"WriteTo": true, "WriteString": true, "ReadByte": true, "WriteByte": true, "Flush": true,
	"Header": true, "WriteHeader": true, "ServeHTTP": true, "Lock": true, "Unlock": true, "RLock": true,
	"RUnlock": true, "Wait": true, "Done": true, "Err": true, "Value": true, "Deadline": true, "Len": true,
	"Less": true, "Swap": true, "Unwrap": true, "Is": true, "As": true, "Format": true, "MarshalJSON": true,
	"UnmarshalJSON": true, "Info": true, "Debug": true, "Warn": true, "Errorf": true, "Infof": true,
	"Debugf": true, "Warnf": true, "Printf": true, "Println": true, "Fatal": true, "Fatalf": true,
}

var jsMembers = map[string]bool{
	"map": true, "filter": true, "forEach": true, "reduce": true, "push": true, "pop": true, "shift": true,
	"unshift": true, "slice": true, "splice": true, "find": true, "findIndex": true, "includes": true,
	"indexOf": true, "join": true, "split": true, "concat": true, "sort": true, "some": true, "every": true,
	"flat": true, "flatMap": true, "fill": true, "keys": true, "values": true, "entries": true, "get": true,
	"set": true, "has": true, "delete": true, "add": true, "clear": true, "then": true, "catch": true,
	"finally": true, "toString": true, "trim": true, "replace": true, "replaceAll": true, "match": true,
	"test": true, "startsWith": true, "endsWith": true, "toLowerCase": true, "toUpperCase": true,
	"padStart": true, "padEnd": true, "toFixed": true, "on": true, "off": true, "emit": true, "once": true,
	"addEventListener": true, "removeEventListener": true, "preventDefault": true, "stopPropagation": true,
	"json": true, "text": true, "subscribe": true, "unsubscribe": true, "next": true, "error": true,
	"complete": true, "pipe": true, "focus": true, "blur": true, "click": true, "querySelector": true,
	"getItem": true, "setItem": true, "removeItem": true, "log": true, "warn": true, "info": true,
	"debug": true, "call": true, "apply": true, "bind": true, "resolve": true, "reject": true, "all": true,
	"put": true, "post": true, "patch": true, "request": true, "send": true, "close": true, "open": true,
}

// qualifiedSiteLangs resolve calls by shape + imports (sitesFromQualified).
// Go keeps name rules refined by its type checker; C/C++ use clikeCallSites.
var qualifiedSiteLangs = map[string]bool{"javascript": true, "typescript": true, "python": true, "java": true}

// selfAsPlain marks self./this./cls. calls, so they resolve on the caller's
// own class, and super calls, which resolve on its bases. self.x.f() is a
// call on a field's value, not on the class.
func selfAsPlain(qcs []QualifiedCall) []QualifiedCall {
	out := make([]QualifiedCall, 0, len(qcs))
	for _, q := range qcs {
		if q.Chain <= 2 {
			switch q.Qualifier {
			case "self", "this", "cls":
				q.Qualifier = selfQualifier
			case "super":
				q.Qualifier = superQualifier
			}
		}
		out = append(out, q)
	}
	return out
}

// selfQualifier marks a self./this./cls. call: it resolves on the caller's
// own class, unlike a bare call in Python or JavaScript. superQualifier marks
// super().f() / super.f(): the caller's bases, never its own class.
const (
	selfQualifier  = "\x00self"
	superQualifier = "\x00super"
)

// moduleInRepo says whether an import names the repo's own code: an absolute
// Python import of one of its packages (mypkg.utils), or a JS/TS path alias
// (@/utils, ~/store, src/api). Relative imports never reach here.
func moduleInRepo(module string, dirs map[string]bool) bool {
	m := module
	for _, p := range []string{"@/", "~/", "#/", "./"} {
		m = strings.TrimPrefix(m, p)
	}
	if !strings.Contains(m, "/") {
		m = strings.ReplaceAll(m, ".", "/") // python dotted module
	}
	if m == "" {
		return false
	}
	first := strings.SplitN(m, "/", 2)[0]
	for d := range dirs {
		if d == m || strings.HasSuffix(d, "/"+m) || strings.HasPrefix(d, m+"/") || strings.Contains(d, "/"+m+"/") {
			return true
		}
		if d == first || strings.HasPrefix(d, first+"/") && first != "src" {
			return true
		}
	}
	if m != module && strings.HasPrefix(module, "@/") {
		return true // an explicit repo alias with no matching dir yet
	}
	return aliasInRepo(module, dirs)
}

// aliasInRepo: a tsconfig/bundler path alias whose root we cannot see
// (`@app/components/Button`, `$lib/Counter.svelte`, `~/store/cart`) names repo
// code when, with the alias segment dropped, the remaining path's directory
// is one of the repo's directories. A component import through an alias is
// how most apps import their own components; calling it external dropped
// every such render edge.
func aliasInRepo(module string, dirs map[string]bool) bool {
	segs := strings.Split(module, "/")
	if len(segs) < 2 || !(strings.HasPrefix(segs[0], "@") || strings.HasPrefix(segs[0], "$") || segs[0] == "~" || segs[0] == "#") {
		return false
	}
	rest := segs[1:]
	if segs[0] == "$lib" {
		rest = append([]string{"lib"}, rest...) // SvelteKit's $lib is src/lib
	}
	if len(rest) < 2 {
		return false // @scope/pkg: a package, never a repo directory
	}
	dir := strings.Join(rest[:len(rest)-1], "/")
	full := strings.Join(rest, "/")
	if i := strings.LastIndexByte(full, '.'); i > strings.LastIndexByte(full, '/') {
		full = full[:i]
	}
	for d := range dirs {
		if d == dir || strings.HasSuffix(d, "/"+dir) || d == full || strings.HasSuffix(d, "/"+full) {
			return true
		}
	}
	return false
}

// isTypeName: an identifier written like a class (CamelCase).
// isTypeName: a capitalized name, as classes are written — but not an
// ALL_CAPS constant (SETTINGS.get(...) is a call on a value).
func isTypeName(s string) bool {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return false
	}
	if len(s) > 1 && strings.ToUpper(s) == s {
		return false
	}
	return true
}

// rustMembers are methods every standard type has (Vec, Option, Result,
// String, iterators, Clone/ToString…): on a value of unknown type they are
// not proven to be the crate's own method of that name.
var rustMembers = map[string]bool{"new": true, "len": true, "is_empty": true, "push": true, "pop": true, "insert": true,
	"remove": true, "get": true, "get_mut": true, "contains": true, "iter": true, "iter_mut": true, "into_iter": true,
	"map": true, "filter": true, "collect": true, "unwrap": true, "expect": true, "unwrap_or": true, "ok": true,
	"err": true, "clone": true, "to_string": true, "to_owned": true, "as_ref": true, "as_str": true, "into": true,
	"from": true, "push_str": true, "join": true, "split": true, "trim": true, "starts_with": true, "ends_with": true,
	"extend": true, "clear": true, "first": true, "last": true, "next": true, "send": true, "recv": true, "lock": true,
	"read": true, "write": true, "flush": true, "fmt": true, "eq": true, "cmp": true, "partial_cmp": true, "hash": true,
	"default": true, "build": true, "run": true, "spawn": true, "take": true, "as_path": true, "display": true,
	"is_some": true, "is_none": true, "is_ok": true, "is_err": true, "and_then": true, "map_err": true, "sort": true}

// pySubmodule is the package of submodule attr of the module b imports
// (import flask -> flask.json), "" when it is not a module of the repo.
func pySubmodule(b ImportBinding, attr string, modPkg func(string) (string, bool)) string {
	if pkg, ok := modPkg(b.Module + "." + attr); ok {
		return pkg
	}
	return ""
}
