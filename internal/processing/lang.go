// lang.go holds the per-language rule tables that drive the generic extractor
// (extract.go). Adding a language is (mostly) one LangRules entry here plus
// binding its tree-sitter grammar in the parser layer. Node-type names follow
// each language's tree-sitter grammar and are validated by the per-language
// parse tests in CI.
package processing

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
	DeclNameIn []string // if set, a callable's name is the last NameTypes leaf inside this child
	// node (the declarator), excluding the parameter list — resolves C/C++ names
	// nested in a function_declarator and C++ qualified names (ns::T::m -> m).
	ScopeDecl []string // node types that scope nested methods by their own name without
	// emitting a symbol themselves (e.g. a Rust `impl T { ... }` block qualifies its fns by T).
	Builtins map[string]bool
}

// languages is the registry consulted by RulesForExt.
var languages = []LangRules{GoRules, PythonRules, JavaScriptRules, TypeScriptRules, JavaRules, CRules, CppRules, RustRules}

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
	FuncDecl:      []string{"function_definition"},
	TypeDecl:      []string{"struct_specifier", "union_specifier", "enum_specifier", "type_definition"},
	NameTypes:     []string{"identifier", "type_identifier"},
	CallNameTypes: []string{"identifier", "field_identifier"}, // s->fn() function-pointer calls
	BodyTypes:     []string{"compound_statement"},
	CallTypes:     []string{"call_expression"},
	NameByDesc:    true, // type names: descend (typedef alias is not a direct child)
	DeclNameIn:    []string{"function_declarator"},
	Builtins:      cBuiltins,
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
	Builtins:      cBuiltins,
}

// RustRules extracts functions, structs/enums/unions/traits/type-aliases, and
// methods inside `impl T { ... }` blocks (ScopeDecl qualifies them by T without
// emitting T twice). Trait methods are function_signature_item bodies-less sigs.
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
