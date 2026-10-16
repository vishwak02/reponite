package processing

import "testing"

func TestDataFileRouting(t *testing.T) {
	for p, want := range map[string]string{
		"db/001_init.sql": "sql", "api/order.proto": "proto", "prisma/schema.prisma": "prisma",
		".env": "dotenv", "web/.env.production": "dotenv", "deploy/prod.env": "dotenv", ".env-dev": "dotenv",
		"Dockerfile": "dockerfile", "svc/Dockerfile.dev": "dockerfile", "build/api.dockerfile": "dockerfile",
		"src/main/resources/application.properties": "properties", "src/main.go": "", "README.md": "",
	} {
		if got := dataLang(p); got != want {
			t.Errorf("dataLang(%q) = %q, want %q", p, got, want)
		}
	}
	for p, want := range map[string]bool{
		".env": true, "web/.env.local": true, ".venv/lib/.env": false, ".github/workflows/ci.yml": true,
		".github/workflows/sub/x.yml": false, ".github/dependabot.yml": false, ".gitlab-ci.yml": true,
		".circleci/config.yml": true, ".vscode/settings.json": false, ".eslintrc.js": false,
	} {
		if got := IsDataDotPath(p); got != want {
			t.Errorf("IsDataDotPath(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestDataFileSpans(t *testing.T) {
	sql := "-- schema\nCREATE TABLE IF NOT EXISTS public.orders (\n  id int\n);\ncreate view open_orders as select * from orders;\n"
	pf, ok := dataFile("db/init.sql", sql)
	if !ok || len(pf.Spans) != 2 || pf.Spans[0].Name != "orders" || pf.Spans[0].StartLine != 2 || pf.Spans[0].EndLine != 4 || pf.Spans[1].Name != "open_orders" {
		t.Errorf("sql spans: %+v", pf.Spans)
	}
	proto := "syntax = \"proto3\";\nmessage Order {\n  string id = 1;\n  message Line {\n    int32 qty = 1;\n  }\n}\n"
	pf, _ = dataFile("a.proto", proto)
	if len(pf.Spans) != 2 || pf.Spans[0].Name != "Order" || pf.Spans[0].EndLine != 7 || pf.Spans[1].Name != "Line" || pf.Spans[1].EndLine != 6 {
		t.Errorf("proto spans: %+v", pf.Spans)
	}
	big := make([]byte, maxDataFileBytes+1)
	if _, ok := dataFile("dump.sql", string(big)); ok {
		t.Error("an oversized data file must not be stored")
	}
}
