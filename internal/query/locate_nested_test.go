package query

import "testing"

func TestLocateSymbolInnermostClass(t *testing.T) {
	src := `class TracingHelper {
public:
    class TraceWithCounts {
    public:
        int incrementCount(const std::string& key) { return ++_n; }
    };
    int incrementCount(const std::string& key) { return 0; }
};
`
	f := File{Path: "nav/include/nav/tracing_helper.hpp", Content: src, Symbols: []SymbolSpan{
		{Name: "TracingHelper", StartLine: 1, EndLine: 8},
		{Name: "TraceWithCounts", StartLine: 3, EndLine: 6},
		{Name: "incrementCount", StartLine: 5, EndLine: 5},
		{Name: "incrementCount", StartLine: 7, EndLine: 7},
	}}
	_, start, _, ok := LocateSymbol([]File{f}, "nav/include/nav.TracingHelper.incrementCount")
	if !ok || start != 7 {
		t.Errorf("TracingHelper.incrementCount is its own method (line 7), got line %d", start)
	}
	// members a macro generates share the macro's line: none encloses another
	g := File{Path: "rec/src/handler.cpp", Content: "class Impl {\n    DECLARE_RECOVERY(_a, _b);\n};\n", Symbols: []SymbolSpan{
		{Name: "Impl", StartLine: 1, EndLine: 3},
		{Name: "save", StartLine: 2, EndLine: 2},
		{Name: "restore", StartLine: 2, EndLine: 2},
	}}
	if _, start, _, ok := LocateSymbol([]File{g}, "rec/src.Impl.save"); !ok || start != 2 {
		t.Errorf("Impl.save is the macro's member (line 2), got %d %v", start, ok)
	}
	_, start, _, ok = LocateSymbol([]File{f}, "nav/include/nav.TraceWithCounts.incrementCount")
	if !ok || start != 5 {
		t.Errorf("TraceWithCounts.incrementCount is the nested class's (line 5), got line %d", start)
	}
}
