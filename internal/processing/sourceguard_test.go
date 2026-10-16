package processing

import (
	"strings"
	"testing"
)

func TestSkipSourceReason(t *testing.T) {
	if r := skipSourceReason("installer.sh", DefaultMaxSourceBytes+1, nil); !strings.HasPrefix(r, "larger than") {
		t.Errorf("an oversized file must be skipped: %q", r)
	}
	if r := skipSourceReason("x.sh", 10, []byte("#!/bin/sh\n\x00\x01payload")); r != "binary content" {
		t.Errorf("a NUL byte means binary: %q", r)
	}
	bundle := "!function(e){" + strings.Repeat("var a=1;", 1000) + "}"
	if r := skipSourceReason("static/scalar/standalone.js", int64(len(bundle)), []byte(bundle)); r != "minified or bundled" {
		t.Errorf("a bundled JS file must be skipped: %q", r)
	}
	if r := skipSourceReason("gen/table.c", int64(len(bundle)), []byte(bundle)); r != "" {
		t.Errorf("only web assets are judged minified: %q", r)
	}
	hand := strings.Repeat("function f(a, b) {\n  return a + b;\n}\n", 400)
	if r := skipSourceReason("src/app.js", int64(len(hand)), []byte(hand)); r != "" {
		t.Errorf("hand-written source must be parsed: %q", r)
	}
	t.Setenv("REPONITE_MAX_SOURCE_BYTES", "100")
	if r := skipSourceReason("a.go", 101, nil); r == "" {
		t.Error("REPONITE_MAX_SOURCE_BYTES must lower the limit")
	}
}
