package processing

import "github.com/vishwak02/reponite/internal/content"

// Python abstract methods. A call through a class whose method is abstract
// never runs that method's body: the instance is of a concrete subclass, so
// one of the subclasses' overrides runs. Symbol.PyStub records what the
// definition says; whether the class is abstract (Symbol.PyClass) decides
// whether a stub body means "abstract".
const (
	// pyAbstractMethod: decorated @abstractmethod (abc.abstractmethod, or
	// the older abstractclassmethod / abstractstaticmethod).
	pyAbstractMethod uint8 = 1
	// pyStubEllipsis: a body of only `...` (and pass or a docstring) —
	// abstract in an ABC or a Protocol; elsewhere it runs and returns None.
	pyStubEllipsis uint8 = 2
	// pyStubPass: a body of only pass or a docstring — abstract in a
	// Protocol; in an ABC it is a hook that does nothing, and runs.
	pyStubPass uint8 = 3
	// pyStubNotImpl: a body that only raises NotImplementedError — the
	// language's way for any base class to say a subclass must override it
	pyStubNotImpl uint8 = 4
)

// pyStubKind classifies a Python function definition fn (decorated is its
// decorated_definition parent, or nil): pyAbstractMethod, pyStubNotImpl,
// pyStubEllipsis, pyStubPass, or 0 for a method with code of its own.
func pyStubKind(decorated, fn content.AST) uint8 {
	if decorated != nil && decorated.Type() == "decorated_definition" {
		for _, d := range decorated.Children() {
			if d.Type() != "decorator" {
				continue
			}
			for _, e := range namedKids(d) {
				if parts := dottedRef(e); len(parts) > 0 && abstractDecorators[parts[len(parts)-1]] {
					return pyAbstractMethod
				}
			}
		}
	}
	body := firstChildAny(fn, []string{"block"})
	if body == nil {
		return 0
	}
	kind := pyStubPass
	stmts := 0
	for _, st := range body.Children() {
		if !st.IsNamed() || isCommentType(st.Type()) {
			continue
		}
		stmts++
		switch st.Type() {
		case "pass_statement":
		case "expression_statement":
			k := namedKids(st)
			if len(k) != 1 {
				return 0
			}
			switch k[0].Type() {
			case "string", "concatenated_string":
				if stmts > 1 {
					return 0 // a string expression after code is not a docstring
				}
			case "ellipsis":
				if kind != pyStubNotImpl {
					kind = pyStubEllipsis
				}
			default:
				return 0
			}
		case "raise_statement":
			if !pyRaisesNotImplemented(st) {
				return 0
			}
			kind = pyStubNotImpl
		default:
			return 0
		}
	}
	if stmts == 0 {
		return 0
	}
	return kind
}

// abstractDecorators are the abc module's decorators that declare a method
// abstract.
var abstractDecorators = map[string]bool{"abstractmethod": true, "abstractclassmethod": true, "abstractstaticmethod": true}

// pyRaisesNotImplemented: raise NotImplementedError / NotImplementedError(...).
func pyRaisesNotImplemented(st content.AST) bool {
	k := namedKids(st)
	if len(k) == 0 {
		return false
	}
	e := k[0]
	if e.Type() == "call" {
		if c := namedKids(e); len(c) > 0 {
			e = c[0]
		}
	}
	parts := dottedRef(e)
	return len(parts) > 0 && parts[len(parts)-1] == "NotImplementedError"
}

// pyClassKind reads whether a Python class declaration is abstract: a base
// ABC (abc.ABC), metaclass=ABCMeta (pyClassABC), or a Protocol
// (pyClassProtocol); 0 otherwise.
func pyClassKind(decl content.AST) uint8 {
	var kind uint8
	for _, c := range decl.Children() {
		if c.Type() != "argument_list" {
			continue
		}
		for _, a := range namedKids(c) {
			var parts []string
			if a.Type() == "keyword_argument" {
				k := namedKids(a)
				if len(k) != 2 || k[0].Text() != "metaclass" {
					continue
				}
				parts = dottedRef(k[1])
			} else {
				parts = dottedRef(a)
			}
			if len(parts) == 0 {
				continue
			}
			switch parts[len(parts)-1] {
			case "Protocol":
				return pyClassProtocol
			case "ABC", "ABCMeta":
				kind = pyClassABC
			}
		}
	}
	return kind
}

const (
	pyClassABC      uint8 = 1
	pyClassProtocol uint8 = 2
)

// pyAbstract reports whether a method of stub kind stub on a class of
// kind cls never runs when called through that class.
func pyAbstract(stub, cls uint8) bool {
	switch stub {
	case pyAbstractMethod, pyStubNotImpl:
		return true
	case pyStubEllipsis:
		return cls != 0
	case pyStubPass:
		return cls == pyClassProtocol
	}
	return false
}
