package precise

import "testing"

type unit struct {
	Calls []string
}

func TestStoreRoundTripAndMiss(t *testing.T) {
	s := Open(t.TempDir())
	k := Key("tu.cpp", "blob1", "-std=c++17", "clang-21")
	var got unit
	if s.Get("cpp", k, &got) {
		t.Fatal("an empty store has nothing")
	}
	if err := s.Put("cpp", k, unit{Calls: []string{"a", "b"}}); err != nil {
		t.Fatal(err)
	}
	if !s.Get("cpp", k, &got) || len(got.Calls) != 2 {
		t.Fatalf("round trip: %v", got)
	}
	if Key("ab", "c") == Key("a", "bc") {
		t.Error("key parts are not concatenated ambiguously")
	}
	if b, n := s.Size(); n != 1 || b == 0 {
		t.Errorf("size: %d bytes, %d entries", b, n)
	}
	if s.Prune(0) != 1 || s.Get("cpp", k, &got) {
		t.Error("prune to zero removes the entry")
	}
}
