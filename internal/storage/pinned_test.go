package storage

import (
	"reflect"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

// A deployed system reads each repo at its own ref; a repo not indexed at
// its ref must be reported, not silently counted as "no results".
func TestPinnedReadsEachRepoAtItsRef(t *testing.T) {
	a, b := NewMem(), NewMem()
	_ = a.Put("app", "3.7.2", "pkg.Caller", SymbolRecord{Lang: "cpp"})
	_ = a.Put("app", "3.7.3", "pkg.Newer", SymbolRecord{Lang: "cpp"})
	_ = b.Put("lib", "pin-5.6.6", "work.forgetWork", SymbolRecord{Lang: "cpp"})
	_ = a.PutFile("app", "3.7.2", query.File{Path: "pkg/a.cpp", Content: "x"})
	fleet := NewMultiStore(a, b)

	p := &Pinned{Inner: fleet, Pins: map[string]string{"app": "3.7.2", "lib": "pin-5.6.6"}}
	if _, ok := p.SymbolsAt("app", "HEAD")["pkg.Caller"]; !ok {
		t.Fatal("app should be read at its pin 3.7.2")
	}
	if _, ok := p.SymbolsAt("lib", "HEAD")["work.forgetWork"]; !ok {
		t.Fatal("lib should be read at its pin")
	}
	if n := len(p.Files("app", "HEAD")); n != 1 {
		t.Fatalf("files at pin: %d", n)
	}

	read, missing := Coverage(p, "HEAD")
	if !reflect.DeepEqual(read, []string{"app@3.7.2", "lib@pin-5.6.6"}) || len(missing) != 0 {
		t.Fatalf("coverage read=%v missing=%v", read, missing)
	}
	read, missing = Coverage(fleet, "HEAD")
	if len(read) != 0 || !reflect.DeepEqual(missing, []string{"app@HEAD", "lib@HEAD"}) {
		t.Fatalf("unpinned HEAD: read=%v missing=%v", read, missing)
	}
}

func TestParsePins(t *testing.T) {
	got, err := ParsePins(" app=3.7.2, lib=ec4818b ")
	if err != nil || !reflect.DeepEqual(got, map[string]string{"app": "3.7.2", "lib": "ec4818b"}) {
		t.Fatalf("got %v %v", got, err)
	}
	for _, bad := range []string{"app", "=x", "app="} {
		if _, err := ParsePins(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// Pins stand in for the default ref only: an explicit other ref (a compat
// target) is read as asked, and answers name the pinned label, never "HEAD".
func TestPinnedOnlyDefaultRefAndLabels(t *testing.T) {
	m := NewMem()
	_ = m.Put("platform", "5.6.8", "planner.Planner.run", SymbolRecord{Lang: "cpp"})
	_ = m.Put("platform", "5.6.7", "planner.Planner.old", SymbolRecord{Lang: "cpp"})
	_ = m.Put("monitor", "HEAD", "watch.Watch.cb", SymbolRecord{Lang: "cpp"})
	p := &Pinned{Inner: m, Pins: map[string]string{"platform": "5.6.8"}, Only: "HEAD"}
	if _, ok := p.SymbolsAt("platform", "HEAD")["planner.Planner.run"]; !ok {
		t.Fatal("HEAD must read the pin")
	}
	if _, ok := p.SymbolsAt("platform", "5.6.7")["planner.Planner.old"]; !ok {
		t.Fatal("an explicit ref must be read as asked")
	}
	if got := query.NewMeta(p, "platform", "HEAD"); got.Ref != "5.6.8" {
		t.Errorf("single-repo meta ref = %q, want the pin label", got.Ref)
	}
	fleet := query.NewMeta(p, query.FleetRepo, "HEAD")
	if fleet.Ref == "HEAD" || fleet.Refs["platform"] != "5.6.8" || fleet.Refs["monitor"] != "HEAD" {
		t.Errorf("fleet meta must list each repo's ref: %+v", fleet)
	}
	hits := query.SearchName(p, query.FleetRepo, "HEAD", "Planner", false)
	if len(hits) != 1 || hits[0].Ref != "5.6.8" {
		t.Errorf("hits carry the ref read: %+v", hits)
	}
	if unpinned := query.NewMeta(m, "platform", "HEAD"); unpinned.Ref != "HEAD" || unpinned.Refs != nil {
		t.Errorf("no pins, no change: %+v", unpinned)
	}
}
