// lang.go holds the per-language rule tables that drive the generic extractor
// (extract.go). Adding a language is (mostly) one LangRules entry here plus
// binding its tree-sitter grammar in the parser layer. Node-type names follow
// each language's tree-sitter grammar and are validated by the per-language
// parse tests in CI.
package processing

import "strings"

// LangRules tells the generic extractor which AST node types matter for a language.
type LangRules struct {
	Name          string
	Exts          []string
	FuncDecl      []string // function-like declarations
	MethodDecl    []string // methods (typically nested in classes)
	TypeDecl      []string // type/class/struct/interface/enum declarations
	TypeSpec      []string // inner spec node holding a type name (Go type_spec); empty => name is a direct child of TypeDecl
	NameTypes     []string // node types holding a declared/callee name
	RecvTypes     []string // method-receiver container node types (Go: the receiver parameter_list); empty => no receiver qualification
	RecvName      []string // node types holding the receiver's type name within RecvTypes
	BodyTypes     []string // callable body node types (dropped from the signature)
	CallTypes     []string // call-expression node types
	CallNameTypes []string // node types holding a callee/member name; empty => use NameTypes.
	// Needed where the method name isn't a plain identifier: C/C++ member calls put
	// it in a field_identifier that NameTypes deliberately excludes (else type/field
	// declarations would mis-resolve), so the callee must look it up separately.
	SortChild  []string // node types whose children are order-independent (import lists)
	NameByDesc bool     // find the name via first matching descendant (e.g. C/C++ type names)
	DeclNameIn []string // if set, a callable's name is the last DeclNameTypes leaf inside this
	// child node (the declarator) BEFORE its parameter list — resolves C/C++ names
	// nested in a function_declarator and C++ qualified names (ns::T::m -> m).
	// When the declarator yields no name the callable is anonymous — the name is
	// NEVER invented from a parameter/body identifier (that misattributed C++
	// endpoints to parameter types like NodeHandle).
	DeclNameTypes []string // node types holding the declared name inside DeclNameIn; empty =>
	// NameTypes. C++ in-class method definitions name via field_identifier (and
	// destructors/operators via destructor_name/operator_name), which NameTypes
	// deliberately excludes (see CallNameTypes note).
	TypeDeclNeedsBody []string // TypeDecl node types that are only DEFINITIONS when a
	// TypeDeclBody child is present. C/C++ struct/class/union/enum specifiers appear
	// equally as bare type REFERENCES (`struct Foo x;`, forward declarations), which
	// must not become symbols or enclosing-symbol spans.
	TypeDeclBody []string // the body node types that mark such a definition
	ScopeDecl    []string // node types that scope nested methods by their own name without
	// emitting a symbol themselves (e.g. a Rust `impl T { ... }` block qualifies its fns by T).
	Builtins map[string]bool
}

// languages is the registry consulted by RulesForExt.
var languages = []LangRules{GoRules, PythonRules, JavaScriptRules, TypeScriptRules, JavaRules, CRules, CppRules, RustRules, ShellRules}

// RulesForExt returns the language rules for a file extension (".go", ".py", …).
func RulesForExt(ext string) (LangRules, bool) {
	for _, l := range languages {
		for _, e := range l.Exts {
			if e == ext {
				return l, true
			}
		}
	}
	return LangRules{}, false
}

var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"new": true, "panic": true, "print": true, "println": true, "real": true, "recover": true,
	"bool": true, "string": true, "error": true, "any": true, "rune": true, "byte": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true, "complex64": true, "complex128": true,
}

var GoRules = LangRules{
	Name: "go", Exts: []string{".go"},
	FuncDecl:   []string{"function_declaration"},
	MethodDecl: []string{"method_declaration"},
	TypeDecl:   []string{"type_declaration"},
	TypeSpec:   []string{"type_spec"},
	NameTypes:  []string{"identifier", "field_identifier", "type_identifier"},
	RecvTypes:  []string{"parameter_list"},
	RecvName:   []string{"type_identifier"},
	BodyTypes:  []string{"block"},
	CallTypes:  []string{"call_expression"},
	SortChild:  []string{"import_spec_list"},
	Builtins:   goBuiltins,
}

var pyBuiltins = map[string]bool{
	"print": true, "len": true, "range": true, "int": true, "str": true, "float": true,
	"list": true, "dict": true, "set": true, "tuple": true, "bool": true, "open": true,
	"super": true, "isinstance": true, "type": true, "enumerate": true, "zip": true,
	"map": true, "filter": true, "sorted": true, "sum": true, "min": true, "max": true, "abs": true,
}

var PythonRules = LangRules{
	Name: "python", Exts: []string{".py"},
	FuncDecl:  []string{"function_definition"},
	TypeDecl:  []string{"class_definition"},
	NameTypes: []string{"identifier"},
	BodyTypes: []string{"block"},
	CallTypes: []string{"call"},
	Builtins:  pyBuiltins,
}

var jsBuiltins = map[string]bool{"require": true, "Boolean": true, "Number": true, "String": true, "Array": true, "Object": true}

var JavaScriptRules = LangRules{
	Name: "javascript", Exts: []string{".js", ".jsx", ".mjs", ".cjs"},
	FuncDecl:   []string{"function_declaration", "generator_function_declaration"},
	MethodDecl: []string{"method_definition"},
	TypeDecl:   []string{"class_declaration"},
	NameTypes:  []string{"identifier", "property_identifier"},
	BodyTypes:  []string{"statement_block"},
	CallTypes:  []string{"call_expression"},
	Builtins:   jsBuiltins,
}

var TypeScriptRules = LangRules{
	Name: "typescript", Exts: []string{".ts", ".tsx"},
	FuncDecl:   []string{"function_declaration", "generator_function_declaration"},
	MethodDecl: []string{"method_definition", "method_signature"},
	TypeDecl:   []string{"class_declaration", "interface_declaration", "type_alias_declaration", "enum_declaration"},
	NameTypes:  []string{"identifier", "property_identifier", "type_identifier"},
	BodyTypes:  []string{"statement_block"},
	CallTypes:  []string{"call_expression"},
	Builtins:   jsBuiltins,
}

var JavaRules = LangRules{
	Name: "java", Exts: []string{".java"},
	MethodDecl: []string{"method_declaration", "constructor_declaration"},
	TypeDecl:   []string{"class_declaration", "interface_declaration", "enum_declaration", "record_declaration"},
	NameTypes:  []string{"identifier"},
	BodyTypes:  []string{"block", "constructor_body"},
	CallTypes:  []string{"method_invocation"},
	Builtins:   map[string]bool{},
}

var cBuiltins = map[string]bool{
	"printf": true, "fprintf": true, "sprintf": true, "snprintf": true, "scanf": true, "sscanf": true,
	"malloc": true, "calloc": true, "realloc": true, "free": true,
	"memcpy": true, "memmove": true, "memset": true, "memcmp": true,
	"strlen": true, "strcmp": true, "strncmp": true, "strcpy": true, "strncpy": true, "strcat": true, "strncat": true,
	"fopen": true, "fclose": true, "fread": true, "fwrite": true, "fgets": true, "fputs": true,
	"exit": true, "abort": true, "assert": true, "sizeof": true,
}

// CRules extracts C functions and struct/union/enum/typedef types. A function's
// name is nested in a function_declarator (not a direct child), so DeclNameIn
// points name resolution there — this also skips the return type, so
// `struct Point make()` is named "make", not "Point".
var CRules = LangRules{
	Name: "c", Exts: []string{".c", ".h"},
	FuncDecl:          []string{"function_definition"},
	TypeDecl:          []string{"struct_specifier", "union_specifier", "enum_specifier", "type_definition"},
	NameTypes:         []string{"identifier", "type_identifier"},
	CallNameTypes:     []string{"identifier", "field_identifier"}, // s->fn() function-pointer calls
	BodyTypes:         []string{"compound_statement"},
	CallTypes:         []string{"call_expression"},
	NameByDesc:        true, // type names: descend (typedef alias is not a direct child)
	DeclNameIn:        []string{"function_declarator"},
	TypeDeclNeedsBody: []string{"struct_specifier", "union_specifier", "enum_specifier"},
	TypeDeclBody:      []string{"field_declaration_list", "enumerator_list"},
	Builtins:          cBuiltins,
}

// CppRules extends C with classes and namespaced/qualified definitions. The same
// DeclNameIn declarator rule yields the last identifier of a qualified name
// (ns::Widget::draw -> "draw"). In-class method *definitions* (function_definition)
// are captured; forward declarations are not (they carry no body/behavior).
var CppRules = LangRules{
	Name: "cpp", Exts: []string{".cc", ".cpp", ".cxx", ".hpp", ".hh", ".hxx"},
	FuncDecl:      []string{"function_definition"},
	TypeDecl:      []string{"class_specifier", "struct_specifier", "union_specifier", "enum_specifier", "type_definition"},
	NameTypes:     []string{"identifier", "type_identifier"},
	CallNameTypes: []string{"identifier", "field_identifier"}, // obj.method()/ptr->method() member calls
	BodyTypes:     []string{"compound_statement"},
	CallTypes:     []string{"call_expression"},
	NameByDesc:    true,
	DeclNameIn:    []string{"function_declarator"},
	// In-class method definitions name via field_identifier; destructors and
	// operator overloads via their dedicated nodes. Without these the name fell
	// back to the first identifier ANYWHERE in the definition — a parameter
	// type (in=NodeHandle) or the first body identifier (P0 misattribution).
	DeclNameTypes:     []string{"identifier", "type_identifier", "field_identifier", "destructor_name", "operator_name"},
	TypeDeclNeedsBody: []string{"class_specifier", "struct_specifier", "union_specifier", "enum_specifier"},
	TypeDeclBody:      []string{"field_declaration_list", "enumerator_list"},
	Builtins:          cBuiltins,
}

// RustRules extracts functions, structs/enums/unions/traits/type-aliases, and
// methods inside `impl T { ... }` blocks (ScopeDecl qualifies them by T without
// emitting T twice). Trait methods are function_signature_item bodies-less sigs.
// shBuiltins are POSIX/bash builtins and ubiquitous coreutils. Filtering them
// keeps a shell call graph meaningful: without it every function "calls" echo,
// cd, and local, and the graph says nothing.
var shBuiltins = map[string]bool{
	"echo": true, "printf": true, "print": true, "read": true, "cd": true, "pwd": true,
	"local": true, "declare": true, "typeset": true, "export": true, "unset": true, "readonly": true,
	"set": true, "shift": true, "eval": true, "exec": true, "exit": true, "return": true, "trap": true,
	"test": true, "true": true, "false": true, "shopt": true, "source": true, "alias": true,
	"break": true, "continue": true, "wait": true, "sleep": true, "command": true, "builtin": true,
	"cat": true, "grep": true, "sed": true, "awk": true, "cut": true, "tr": true, "sort": true,
	"uniq": true, "head": true, "tail": true, "wc": true, "ls": true, "cp": true, "mv": true,
	"rm": true, "mkdir": true, "rmdir": true, "touch": true, "chmod": true, "chown": true,
	"ln": true, "find": true, "xargs": true, "tee": true, "which": true, "basename": true,
	"dirname": true, "date": true, "env": true, "sudo": true, "getopts": true, "seq": true,
}

// ShellRules extracts shell functions (`f() { … }` and `function f { … }`).
// Shell has no type declarations and no signature beyond the name, so a
// changed body is a behavior change and the signature never moves — the Oracle
// therefore reports shell edits as behavior_changed, never shape_changed, which
// is the honest reading for a language with no declared parameter list.
//
// The callee name is the `command_name` node, deliberately NOT `word`: a
// command's arguments are bare `word` nodes too, so matching `word` would make
// the LAST argument look like the callee.
var ShellRules = LangRules{
	Name: "shell", Exts: []string{".sh", ".bash", ".zsh", ".ksh"},
	FuncDecl:      []string{"function_definition"},
	NameTypes:     []string{"word"},
	CallNameTypes: []string{"command_name"},
	BodyTypes:     []string{"compound_statement"},
	CallTypes:     []string{"command"},
	Builtins:      shBuiltins,
}

// RulesForShebang maps a script's first line to its language, for files that
// carry no extension. CLI entry points (`installer/rdt`, `bin/deploy`) are
// exactly that shape — and are usually the most valuable file in the tree to
// index, since they are where a reader starts.
func RulesForShebang(firstLine string) (LangRules, bool) {
	if !strings.HasPrefix(firstLine, "#!") {
		return LangRules{}, false
	}
	line := firstLine
	if i := strings.IndexAny(line, "\r\n"); i >= 0 {
		line = line[:i]
	}
	// Take the last path segment of each token so "/usr/bin/env bash" and
	// "/bin/sh" both reduce to the interpreter name.
	for _, tok := range strings.Fields(line) {
		if i := strings.LastIndexByte(tok, '/'); i >= 0 {
			tok = tok[i+1:]
		}
		switch {
		case tok == "sh", tok == "bash", tok == "zsh", tok == "ksh", tok == "dash":
			return ShellRules, true
		case strings.HasPrefix(tok, "python"):
			return PythonRules, true
		case tok == "node", tok == "nodejs":
			return JavaScriptRules, true
		}
	}
	return LangRules{}, false
}

var RustRules = LangRules{
	Name: "rust", Exts: []string{".rs"},
	FuncDecl:  []string{"function_item", "function_signature_item"},
	TypeDecl:  []string{"struct_item", "enum_item", "union_item", "trait_item", "type_item"},
	NameTypes: []string{"identifier", "type_identifier"},
	BodyTypes: []string{"block"},
	CallTypes: []string{"call_expression"},
	ScopeDecl: []string{"impl_item"},
	Builtins:  map[string]bool{},
}
