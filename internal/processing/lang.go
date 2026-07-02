// lang.go holds the per-language rule tables that drive the generic extractor
// (extract.go). Adding a language is (mostly) one LangRules entry here plus
// binding its tree-sitter grammar in the parser layer. Node-type names follow
// each language's tree-sitter grammar and are validated by the per-language
// parse tests in CI.
package processing

// LangRules tells the generic extractor which AST node types matter for a language.
type LangRules struct {
	Name       string
	Exts       []string
	FuncDecl   []string // function-like declarations
	MethodDecl []string // methods (typically nested in classes)
	TypeDecl   []string // type/class/struct/interface/enum declarations
	TypeSpec   []string // inner spec node holding a type name (Go type_spec); empty => name is a direct child of TypeDecl
	NameTypes  []string // node types holding a declared/callee name
	BodyTypes  []string // callable body node types (dropped from the signature)
	CallTypes  []string // call-expression node types
	SortChild  []string // node types whose children are order-independent (import lists)
	NameByDesc bool     // find the name via first matching descendant (e.g. C/C++ declarators)
	Builtins   map[string]bool
}

// languages is the registry consulted by RulesForExt.
var languages = []LangRules{GoRules, PythonRules, JavaScriptRules, TypeScriptRules, JavaRules}

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
