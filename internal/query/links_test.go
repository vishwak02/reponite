package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestLinksAcrossFileKinds(t *testing.T) {
	m := storage.NewMem()
	put := func(p, c string) { m.PutFile("r", "v1", query.File{Path: p, Content: c}) }
	put("nav/package.xml", "<package><name>nav</name></package>\n")
	put("nav/launch/inc.launch", "<launch/>\n")
	put("bringup/launch/main.launch", "<launch>\n  <include file=\"$(find nav)/launch/inc.launch\"/>\n</launch>\n")
	put("plans/Drop.pml", "{\n  \"states\": [{\"behaviour\": 1234567890}]\n}\n")
	put("behaviours/Lower.beh", "{\n  \"id\": 1234567890,\n  \"name\": \"LowerLifter\"\n}\n")
	put("chart/templates/_helpers.tpl", "{{- define \"app.labels\" -}}\napp: x\n{{- end }}\n")
	put("chart/templates/deploy.yaml", "metadata:\n  labels: {{ include \"app.labels\" . }}\n")
	put("roles/web/tasks/main.yml", "- template:\n    src: site.conf.j2\n")
	put("roles/web/templates/site.conf.j2", "server {}\n")
	put("site.yml", "- hosts: all\n  roles:\n    - web\n")
	put("src/lower.cpp", "class LowerLifter {};\n")
	m.Put("r", "v1", "src.LowerLifter", storage.SymbolRecord{Lang: "cpp"})

	want := map[string]string{ // from -> to, by kind
		"path:bringup/launch/main.launch":      "nav/launch/inc.launch",
		"id:plans/Drop.pml":                    "behaviours/Lower.beh",
		"template:chart/templates/deploy.yaml": "chart/templates/_helpers.tpl",
		"path:roles/web/tasks/main.yml":        "roles/web/templates/site.conf.j2",
		"path:site.yml":                        "roles/web/tasks/main.yml",
		"names-symbol:behaviours/Lower.beh":    "src.LowerLifter",
	}
	got := map[string]string{}
	for _, l := range query.Links(m, "r", "v1") {
		got[l.Kind+":"+l.Path] = l.To
	}
	for k, to := range want {
		if got[k] != to {
			t.Errorf("%s -> %q, want %q (all: %v)", k, got[k], to, got)
		}
	}
	if r := query.LinksFor(m, "r", "v1", "nav/launch/inc.launch"); len(r.ReferredBy) != 1 {
		t.Errorf("referred_by %+v", r.ReferredBy)
	}
}

func TestLinksAdapters(t *testing.T) {
	m := storage.NewMem()
	m.PutFile("r", "v1", query.File{Path: "chart/values.yaml", Content: "image:\n  tag: v1\n",
		Symbols: []query.SymbolSpan{{Name: "values:image", StartLine: 1, EndLine: 2}, {Name: "values:image:tag", StartLine: 2, EndLine: 2}}})
	m.PutFile("r", "v1", query.File{Path: "chart/templates/deploy.yaml", Content: "image: app:{{ .Values.image.tag }}\n"})
	m.PutFile("r", "v1", query.File{Path: "api/order.proto", Content: "service Orders {\n  rpc GetOrder (Req) returns (Resp);\n}\n"})
	m.PutFile("r", "v1", query.File{Path: "server/orders.go", Content: "func (s *Server) GetOrder() {}\n"})
	m.Put("r", "v1", "server.Server.GetOrder", storage.SymbolRecord{Lang: "go"})
	got := map[string]query.Link{}
	for _, l := range query.Links(m, "r", "v1") {
		got[l.Kind] = l
	}
	if l := got["values"]; l.To != "chart/values.yaml" || l.ToLine != 2 {
		t.Errorf("values link %+v", l)
	}
	if l := got["rpc-impl"]; l.To != "server.Server.GetOrder" {
		t.Errorf("rpc link %+v", l)
	}
}
