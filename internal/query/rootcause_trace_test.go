package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestParseStackTraceLanguages(t *testing.T) {
	cases := []struct {
		name, trace string
		want        []query.TraceFrame
	}{
		{
			name: "go",
			trace: "goroutine 1 [running]:\n" +
				"billing.validateCard(...)\n" +
				"\t/app/billing/charge.go:8 +0x1b\n" +
				"billing.Charge(...)\n" +
				"\t/app/billing/charge.go:4 +0x2a\n",
			want: []query.TraceFrame{
				{File: "/app/billing/charge.go", Function: "billing.validateCard", Line: 8},
				{File: "/app/billing/charge.go", Function: "billing.Charge", Line: 4},
			},
		},
		{
			name: "python",
			trace: "Traceback (most recent call last):\n" +
				"  File \"/app/svc.py\", line 10, in charge\n" +
				"    return validate()\n" +
				"  File \"/app/svc.py\", line 4, in validate\n" +
				"    raise ValueError(\"boom\")\n" +
				"ValueError: boom\n",
			want: []query.TraceFrame{
				{File: "/app/svc.py", Function: "charge", Line: 10},
				{File: "/app/svc.py", Function: "validate", Line: 4},
			},
		},
		{
			name: "javascript",
			trace: "Error: boom\n" +
				"    at validate (/app/svc.js:4:10)\n" +
				"    at charge (/app/svc.js:10:5)\n",
			want: []query.TraceFrame{
				{File: "/app/svc.js", Function: "validate", Line: 4},
				{File: "/app/svc.js", Function: "charge", Line: 10},
			},
		},
		{
			name: "java",
			trace: "Exception in thread \"main\" java.lang.RuntimeException: boom\n" +
				"\tat com.app.Svc.charge(Svc.java:10)\n" +
				"\tat com.app.Main.main(Main.java:20)\n",
			want: []query.TraceFrame{
				{File: "Svc.java", Function: "com.app.Svc.charge", Line: 10},
				{File: "Main.java", Function: "com.app.Main.main", Line: 20},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := query.ParseStackTrace(c.trace)
			if len(got) != len(c.want) {
				t.Fatalf("%s: got %d frames, want %d: %+v", c.name, len(got), len(c.want), got)
			}
			for i := range c.want {
				if got[i] != c.want[i] {
					t.Fatalf("%s frame %d = %+v, want %+v", c.name, i, got[i], c.want[i])
				}
			}
		})
	}
}

func traceStore() *storage.Mem {
	m := storage.NewMem()
	// validateCard: text changes (symbol hash), same signature -> KindText origin.
	m.Put("billing", "prod", "billing.validateCard", rc("vc", "sig", "vcBEH", 1))
	m.Put("billing", "HEAD", "billing.validateCard", rc("vcNEW", "sig", "vcBEHNEW", 1))
	// Charge: identical text, behavior changes only via its callee -> propagation.
	m.Put("billing", "prod", "billing.Charge", rc("charge", "csig", "chBEH", 1, "billing.validateCard"))
	m.Put("billing", "HEAD", "billing.Charge", rc("charge", "csig", "chBEHNEW", 1, "billing.validateCard"))
	m.PutFile("billing", "HEAD", query.File{
		Path:    "billing/charge.go",
		Content: briefSrc,
		Symbols: []query.SymbolSpan{
			{Name: "Charge", StartLine: 3, EndLine: 5},
			{Name: "validateCard", StartLine: 7, EndLine: 9},
		},
	})
	return m
}

func TestRootCauseTraceSeedsFromFrames(t *testing.T) {
	trace := "goroutine 1 [running]:\n" +
		"runtime.gopanic(0x1)\n" +
		"\t/usr/local/go/src/runtime/panic.go:914 +0x21b\n" +
		"billing.validateCard(...)\n" +
		"\t/app/billing/charge.go:8 +0x1b\n" +
		"billing.Charge(...)\n" +
		"\t/app/billing/charge.go:4 +0x2a\n"

	res := query.RootCauseTrace(traceStore(), "billing", "prod", "HEAD", trace)

	if !res.Result.Changed {
		t.Fatalf("expected a behavior change on the traced path; note=%q", res.Note)
	}
	// The runtime frame maps to no indexed symbol -> reported, not dropped.
	if len(res.Unmapped) == 0 {
		t.Fatalf("expected the runtime frame in Unmapped, got %+v", res.Unmapped)
	}
	// The mutation site is validateCard's text change.
	found := false
	for _, o := range res.Result.Origins {
		if o.Name == "billing.validateCard" && o.Kind == query.KindText {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected validateCard text_changed origin, got %+v", res.Result.Origins)
	}
	// In-repo frames resolved to their qualified ids.
	var mapped int
	for _, f := range res.Frames {
		if f.Symbol != "" {
			mapped++
		}
	}
	if mapped != 2 {
		t.Fatalf("expected 2 mapped frames (Charge, validateCard), got %d: %+v", mapped, res.Frames)
	}
}

func TestRootCauseTraceNoChange(t *testing.T) {
	// Same trace but comparing a ref to itself -> nothing changed.
	trace := "billing.Charge(...)\n\t/app/billing/charge.go:4 +0x2a\n"
	res := query.RootCauseTrace(traceStore(), "billing", "HEAD", "HEAD", trace)
	if res.Result.Changed || res.Note == "" {
		t.Fatalf("identical refs must report no change with a note, got %+v", res)
	}
}
