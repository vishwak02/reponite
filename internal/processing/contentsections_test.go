package processing

import (
	"strings"
	"testing"
)

func sectionNames(p, content string) map[string][2]int {
	syms, spans := sectionSymbols(p, content)
	out := map[string][2]int{}
	for i, s := range syms {
		out[s.Name] = [2]int{spans[i].StartLine, spans[i].EndLine}
		if s.Kind != "section" || strings.Contains(s.Name, ".") {
			out["BAD:"+s.Name] = [2]int{}
		}
	}
	return out
}

func TestMarkdownSections(t *testing.T) {
	md := "# Guide\nintro\n## Install\nsteps\n```sh\n# not a heading\n```\n## Use v1.2\nmore\n# Other\nend\n"
	got := sectionNames("docs/USER_GUIDE.md", md)
	want := map[string][2]int{
		"USER_GUIDE:Guide": {1, 9}, "USER_GUIDE:Guide:Install": {3, 7}, "USER_GUIDE:Guide:Use v1_2": {8, 9},
		"USER_GUIDE:Other": {10, 12},
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s: got %v want %v (all %v)", k, got[k], v, got)
		}
	}
	if len(got) != len(want) {
		t.Errorf("a heading inside a code fence must not count: %v", got)
	}
}

func TestYAMLSections(t *testing.T) {
	y := "name: ci\non: push\njobs:\n  build:\n    runs-on: x\n    steps:\n      - name: Checkout code\n        uses: actions/checkout@v4\n      - run: make\n  test:\n    runs-on: y\n"
	got := sectionNames(".github/workflows/ci.yml", y)
	for _, k := range []string{"ci:jobs", "ci:jobs:build", "ci:jobs:build:steps", "ci:jobs:test", "ci:name", "ci:on"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %s in %v", k, got)
		}
	}
	if got["ci:jobs:build"] != [2]int{4, 9} {
		t.Errorf("jobs.build range: %v", got["ci:jobs:build"])
	}
}

func TestJSONSections(t *testing.T) {
	j := "[\n  {\n    \"code\": 55,\n    \"name\": \"PLANNER_BLOCKED_GOAL\",\n    \"en\": \"x\"\n  },\n  {\n    \"code\": 56,\n    \"name\": \"NO_PATH\"\n  }\n]\n"
	got := sectionNames("bringup/errors.json", j)
	if got["errors:PLANNER_BLOCKED_GOAL"][0] != 2 || got["errors:NO_PATH"][0] != 7 {
		t.Errorf("array entries named by their name field: %v", got)
	}
	o := "{\n  \"server\": {\n    \"port\": 80\n  },\n  \"debug\": true\n}\n"
	got = sectionNames("config.json", o)
	if _, ok := got["config:server:port"]; !ok || got["config:debug"][0] != 5 {
		t.Errorf("object keys: %v", got)
	}
}

func TestDockerMakeTableRSTSections(t *testing.T) {
	if got := sectionNames("Dockerfile", "FROM golang:1.22 AS build\nRUN make\nFROM alpine\nCOPY --from=build /x /x\n"); got["Dockerfile:build"] != [2]int{1, 2} {
		t.Errorf("docker stages: %v", got)
	}
	if got := sectionNames("Makefile", "all: build\nbuild:\n\tgo build\n.PHONY: all\nX := 1\n"); len(got) != 2 {
		t.Errorf("make targets (not .PHONY, not variables): %v", got)
	}
	if got := sectionNames("pyproject.toml", "[project]\nname = \"x\"\n[tool.ruff]\nline = 1\n"); got["pyproject:tool_ruff"][0] != 3 {
		t.Errorf("toml tables: %v", got)
	}
	if got := sectionNames("docs/index.rst", "Title\n=====\n\nSub\n---\ntext\n"); got["index:Title:Sub"][0] != 4 {
		t.Errorf("rst headings: %v", got)
	}
}

func TestHTMLSections(t *testing.T) {
	syms, _ := sectionSymbols("docs/index.html", "<html>\n<h1>Guide</h1>\n<p>x</p>\n<h2 id=\"i\">Install <b>now</b></h2>\n<p>y</p>\n")
	var names []string
	for _, s := range syms {
		names = append(names, s.Name)
	}
	if len(names) != 2 || names[0] != "index:Guide" || names[1] != "index:Guide:Install now" {
		t.Fatalf("sections: %v", names)
	}
}
