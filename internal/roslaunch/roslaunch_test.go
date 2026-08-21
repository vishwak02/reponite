package roslaunch

import "testing"

const sample = `<launch>
  <arg name="robot" default="amr1"/>
  <remap from="diagnostics" to="/global_diagnostics"/>
  <group ns="front">
    <node pkg="rr_navigation" type="scan_filter" name="filter">
      <remap from="scan" to="raw_scan_front"/>
      <remap from="scan_filtered" to="scan_front"/>
    </node>
  </group>
  <node pkg="rr_perception" type="detector" name="detect" ns="cam">
    <remap from="image" to="camera/image_raw"/>
  </node>
  <include file="$(find rr_bringup)/launch/base.launch"/>
</launch>`

func TestParseNodesRemapsNamespaces(t *testing.T) {
	f, err := Parse("bringup.launch", sample)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Nodes) != 2 {
		t.Fatalf("want 2 nodes, got %d (%+v)", len(f.Nodes), f.Nodes)
	}
	filter := f.Nodes[0]
	if filter.Pkg != "rr_navigation" || filter.Type != "scan_filter" || filter.Name != "filter" {
		t.Fatalf("node attrs: %+v", filter)
	}
	if filter.Ns != "front" {
		t.Errorf("a <group ns> must scope its children, got ns=%q", filter.Ns)
	}
	// Its own two remaps plus the launch-level one it inherits.
	got := map[string]string{}
	for _, r := range filter.Remaps {
		got[r.From] = r.To
	}
	if got["scan"] != "raw_scan_front" || got["scan_filtered"] != "scan_front" {
		t.Errorf("node remaps lost: %+v", filter.Remaps)
	}
	if got["diagnostics"] != "/global_diagnostics" {
		t.Errorf("a launch-level remap must be inherited by nodes: %+v", filter.Remaps)
	}

	// The second node's own ns attribute composes with the enclosing scope, and
	// the group's ns must NOT leak to it (the group closed).
	detect := f.Nodes[1]
	if detect.Ns != "cam" {
		t.Errorf("group ns leaked past its close: got %q, want cam", detect.Ns)
	}
	if len(f.Includes) != 1 {
		t.Errorf("includes must be recorded so the gap can be reported: %v", f.Includes)
	}
}

// A malformed launch file must still yield the remaps that parsed. Losing them
// silently would return the comms graph to pre-remap names with no signal.
func TestParseMalformedKeepsWhatItRead(t *testing.T) {
	broken := `<launch>
  <node pkg="p" type="t" name="n"><remap from="a" to="b"/></node>
  <node pkg="q" type=unquoted>`
	f, _ := Parse("broken.launch", broken)
	if len(f.Nodes) == 0 {
		t.Fatal("a partially valid launch file must still contribute its nodes")
	}
	if f.Nodes[0].Remaps[0].To != "b" {
		t.Fatalf("the valid remap must survive: %+v", f.Nodes[0])
	}
}

func TestIsLaunchFile(t *testing.T) {
	for _, p := range []string{"a/b.launch", "x.test", "y.launch.xml", "A/B.LAUNCH"} {
		if !IsLaunchFile(p) {
			t.Errorf("IsLaunchFile(%q) must be true", p)
		}
	}
	for _, p := range []string{"a.xml", "a.py", "launch", "a.launchx"} {
		if IsLaunchFile(p) {
			t.Errorf("IsLaunchFile(%q) must be false", p)
		}
	}
}

// The point of the package: a source endpoint's RUNTIME name.
func TestTableEffective(t *testing.T) {
	f, _ := Parse("bringup.launch", sample)
	tbl := NewTable([]File{f})

	eff, res, via := tbl.Effective("rr_navigation/rr_nav_core/src/filter.cpp", "scan")
	if eff != "raw_scan_front" || res != ResolvedRemapped {
		t.Fatalf("a remapped topic must resolve to its runtime name: %q %q", eff, res)
	}
	if via != "bringup.launch" {
		t.Errorf("the resolution must cite the launch file, got %q", via)
	}
	// Absolute and relative spellings compare equal.
	if eff, _, _ := tbl.Effective("rr_navigation/src/x.cpp", "/scan"); eff != "raw_scan_front" {
		t.Errorf("/scan and scan must resolve the same, got %q", eff)
	}
	// A package with no remap for that name keeps the source name.
	if eff, res, _ := tbl.Effective("rr_other/src/x.cpp", "scan"); eff != "scan" || res != ResolvedAsWritten {
		t.Errorf("an unrelated package must not be remapped: %q %q", eff, res)
	}
	// A name nobody remaps is as-written.
	if _, res, _ := tbl.Effective("rr_navigation/src/x.cpp", "odom"); res != ResolvedAsWritten {
		t.Errorf("unremapped name got %q", res)
	}
	if tbl.Unresolved != 1 {
		t.Errorf("the unexpanded <include> must be counted, got %d", tbl.Unresolved)
	}
}

// Two launch files remapping the same name in the same package to DIFFERENT
// targets: which applies depends on which launch ran, so assert nothing.
func TestTableAmbiguousRemapIsNotGuessed(t *testing.T) {
	a, _ := Parse("a.launch", `<launch><node pkg="p" type="t" name="n"><remap from="scan" to="front"/></node></launch>`)
	b, _ := Parse("b.launch", `<launch><node pkg="p" type="t" name="n"><remap from="scan" to="rear"/></node></launch>`)
	tbl := NewTable([]File{a, b})
	eff, res, via := tbl.Effective("p/src/x.cpp", "scan")
	if res != ResolvedAmbiguous {
		t.Fatalf("conflicting remaps must be reported ambiguous, got %q", res)
	}
	if eff != "scan" {
		t.Errorf("an ambiguous remap must fall back to the source name, got %q", eff)
	}
	if via != "front | rear" {
		t.Errorf("the competing targets must be listed, got %q", via)
	}
}

// A nil table is safe and behaves as "nothing is remapped".
func TestNilTable(t *testing.T) {
	var tbl *Table
	if eff, res, _ := tbl.Effective("a/b.cpp", "/scan"); eff != "scan" || res != ResolvedAsWritten {
		t.Fatalf("nil table: %q %q", eff, res)
	}
}

// A remap target that keeps an unexpanded substitution is NOT a runtime name.
// Applying it would group endpoints under the literal "$(arg scan_topic)" —
// worse than not remapping. It must be reported, not applied.
func TestUnexpandedSubstitutionIsNotApplied(t *testing.T) {
	f, _ := Parse("x.launch", `<launch>
  <arg name="scan_topic" default="scan_front"/>
  <node pkg="p" type="t" name="n">
    <remap from="scan" to="$(arg scan_topic)"/>
    <remap from="map"  to="$(arg clean_ns)map"/>
    <remap from="cfg"  to="$(find pkg)/cfg"/>
  </node>
</launch>`)
	tbl := NewTable([]File{f})

	// $(arg scan_topic) HAS a default here, so it expands to a real name.
	if eff, res, _ := tbl.Effective("p/src/a.cpp", "scan"); eff != "scan_front" || res != ResolvedRemapped {
		t.Errorf("an $(arg) with a default must expand: got %q %q", eff, res)
	}
	// $(arg clean_ns) has no default and $(find …) is unknowable: keep the
	// source name and label it, rather than applying a literal.
	for _, from := range []string{"map", "cfg"} {
		eff, res, via := tbl.Effective("p/src/a.cpp", from)
		if eff != from {
			t.Errorf("%s: unexpandable remap must keep the source name, got %q", from, eff)
		}
		if res != ResolvedUnexpanded {
			t.Errorf("%s: must be labeled launch-unexpanded, got %q", from, res)
		}
		if via == "" {
			t.Errorf("%s: the unexpanded target should be reported for context", from)
		}
	}
	if tbl.Unexpanded != 2 {
		t.Errorf("both unexpandable remaps must be counted, got %d", tbl.Unexpanded)
	}
}

func TestSubstitute(t *testing.T) {
	args := map[string]string{"a": "one", "b": "two"}
	if got := Substitute("$(arg a)/x/$(arg b)", args); got != "one/x/two" {
		t.Errorf("Substitute = %q", got)
	}
	// Unknown arg and non-arg substitutions are left intact, so
	// HasSubstitution can catch them.
	got := Substitute("$(arg missing)/$(find pkg)", args)
	if !HasSubstitution(got) {
		t.Errorf("unexpandable substitutions must remain detectable: %q", got)
	}
}
