package query

import (
	"reflect"
	"testing"
)

func TestResolveSymbolPartlyQualified(t *testing.T) {
	syms := map[string]SymbolRef{
		"splitter/src.Splitter.forget": {Present: true},
		"gbc/src.Default.forget":       {Present: true},
		"splitter/src.helper":          {Present: true},
	}
	cases := map[string][]string{
		"Splitter.forget":              {"splitter/src.Splitter.forget"},
		"forget":                       {"gbc/src.Default.forget", "splitter/src.Splitter.forget"},
		"splitter/src.Splitter.forget": {"splitter/src.Splitter.forget"},
		"litter.forget":                nil, // not on a "." boundary
	}
	for q, want := range cases {
		if got := resolveIn(syms, q); !reflect.DeepEqual(got, want) {
			t.Errorf("%q: got %v want %v", q, got, want)
		}
	}
}
