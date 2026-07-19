package processing

import "testing"

func TestIgnoreDefaults(t *testing.T) {
	ig := NewIgnore("", nil)
	for _, p := range []string{
		"vendor/lib/x.go",
		"external/pkg/node_modules/left-pad/index.js",
		"third_party/grpc/server.cc",
		"a/b/testdata/fixture.go",
	} {
		if !ig.Excluded(p, false) {
			t.Errorf("default set must exclude %q", p)
		}
	}
	for _, p := range []string{
		"src/main.go",
		"vendored/notvendor.go", // "vendored" != "vendor"
		"a/vendor.go",           // a FILE named vendor.go (dir-only pattern)
	} {
		if ig.Excluded(p, false) {
			t.Errorf("default set must NOT exclude %q", p)
		}
	}
	// The dir probe (filepath.Walk SkipDir) matches the directory itself.
	if !ig.Excluded("external/ros_comm/vendor", true) || !ig.Excluded("vendor", true) {
		t.Error("dir-only default must match the directory probe")
	}
}

func TestIgnoreGitignoreSyntax(t *testing.T) {
	content := `
# vendored ROS stack
external/
*.log
!important.log
/build
docs/**/*.tmp
`
	ig := NewIgnore(content, nil)
	cases := []struct {
		path  string
		isDir bool
		want  bool
	}{
		{"external", true, true},                  // dir-only
		{"external/ros_comm/roscpp/init.cpp", false, true}, // everything under an excluded dir
		{"pkg/external/x.cpp", false, true},       // basename dir pattern matches at depth... anchored? "external/" has no inner slash -> basename at any depth
		{"debug.log", false, true},                // *.log anywhere
		{"a/b/debug.log", false, true},            // ...at depth
		{"important.log", false, false},           // negation re-includes
		{"a/important.log", false, false},         // ...at depth
		{"build", true, true},                     // anchored /build
		{"src/build", true, false},                // anchored: only at root
		{"docs/a/b/c.tmp", false, true},           // ** spans directories
		{"docs/c.tmp", false, true},               // ** spans zero directories
		{"docs/a/c.md", false, false},
		{"src/main.cpp", false, false},
	}
	for _, c := range cases {
		if got := ig.Excluded(c.path, c.isDir); got != c.want {
			t.Errorf("Excluded(%q, dir=%v) = %v, want %v", c.path, c.isDir, got, c.want)
		}
	}
}

// git's hard rule: a file below an excluded DIRECTORY cannot be re-included.
func TestIgnoreParentExclusionWins(t *testing.T) {
	ig := NewIgnore("external/\n!external/ours/keep.cpp\n", nil)
	if !ig.Excluded("external/ours/keep.cpp", false) {
		t.Error("negation below an excluded parent directory must not re-include")
	}
	// But a same-level FILE negation works (parent not excluded).
	ig2 := NewIgnore("*.gen.go\n!api.gen.go\n", nil)
	if ig2.Excluded("pkg/api.gen.go", false) {
		t.Error("later negation must re-include a file")
	}
	if !ig2.Excluded("pkg/other.gen.go", false) {
		t.Error("non-negated generated file stays excluded")
	}
}

func TestIgnoreExcludeFlagPatterns(t *testing.T) {
	ig := NewIgnore("", []string{"external/**", "*.bag"})
	if !ig.Excluded("external/ros_comm/tools/rosgraph/network.py", false) {
		t.Error("--exclude external/** must exclude the vendored tree")
	}
	if !ig.Excluded("logs/run1.bag", false) {
		t.Error("--exclude *.bag must exclude at any depth")
	}
	if ig.Excluded("src/externals.py", false) {
		t.Error("external/** must not match a similarly-named file")
	}
}
