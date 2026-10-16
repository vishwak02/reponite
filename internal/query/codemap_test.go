package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestRepoDependenciesAcrossManifests(t *testing.T) {
	files := []query.File{
		{Path: "pyproject.toml", Content: "[project]\nname = \"shop\"\ndependencies = [\n  \"fastapi>=0.100\",\n  \"httpx\",\n]\n[project.optional-dependencies]\ntest = [\"pytest\"]\n[tool.poetry.dependencies]\npython = \"^3.11\"\nsqlalchemy = \"^2.0\"\n"},
		{Path: "pkg_a/package.xml", Content: "<package><name>pkg_a</name><depend>roscpp</depend><depend>pkg_b</depend><test_depend>rostest</test_depend></package>"},
		{Path: "pkg_b/package.xml", Content: "<package><name>pkg_b</name><depend>roscpp</depend></package>"},
		{Path: "pkg_b/CMakeLists.txt", Content: "find_package(catkin REQUIRED COMPONENTS roscpp std_msgs)\nfind_package(Eigen3 3.3 REQUIRED) # math\n"},
		{Path: "crate/Cargo.toml", Content: "[package]\nname = \"crate\"\n[dependencies]\nserde = { version = \"1.0\", features = [\"derive\"] }\ntokio = \"1\"\n[dev-dependencies]\nproptest = \"1\"\n"},
		{Path: "svc/pom.xml", Content: "<project><dependencies><dependency><groupId>org.x</groupId><artifactId>web</artifactId><version>2.1</version></dependency><dependency><groupId>junit</groupId><artifactId>junit</artifactId><scope>test</scope></dependency></dependencies></project>"},
		{Path: "tests/requirements.txt", Content: "pytest\n"}, // test manifests are not the product's dependencies
	}
	deps, manifests, internal := query.RepoDependencies(files)
	got := map[string]query.Dependency{}
	for _, d := range deps {
		got[d.Ecosystem+":"+d.Name] = d
	}
	if d := got["ros:roscpp"]; d.Manifests != 3 {
		t.Errorf("roscpp declared by both package.xml files and a catkin COMPONENTS list: %+v", d)
	}
	if deps[0].Name != "roscpp" {
		t.Errorf("the most-declared dependency ranks first: %+v", deps[0])
	}
	if _, ok := got["ros:pkg_b"]; ok || internal != 1 {
		t.Errorf("a dependency on the repo's own package is internal (internal=%d)", internal)
	}
	for _, k := range []string{"pypi:fastapi", "pypi:httpx", "pypi:sqlalchemy", "cmake:Eigen3", "cargo:serde", "cargo:tokio", "maven:org.x:web", "ros:std_msgs"} {
		if _, ok := got[k]; !ok {
			t.Errorf("missing %s in %+v", k, deps)
		}
	}
	for _, k := range []string{"pypi:pytest", "ros:rostest", "cargo:proptest", "maven:junit:junit"} {
		if !got[k].Dev {
			t.Errorf("%s should be a dev dependency: %+v", k, got[k])
		}
	}
	if got["cargo:serde"].Version != "1.0" || got["pypi:python"].Name != "" {
		t.Errorf("serde %+v / python must not be a dependency", got["cargo:serde"])
	}
	if len(manifests) != 6 {
		t.Errorf("manifests read = %v", manifests)
	}
}

func TestEntryPointsPerFramework(t *testing.T) {
	files := []query.File{
		{Path: "cmd/tool/main.go", Content: "package main\n\nvar root = &cobra.Command{\n\tUse: \"tool\",\n}\nvar sync = &cobra.Command{Use: \"sync [dir]\"}\n\nfunc main() {}\n"},
		{Path: "cli.py", Content: "import click\n\n@click.group()\ndef cli():\n    pass\n\n@cli.command()\ndef import_orders():\n    pass\n"},
		{Path: "src/main.rs", Content: "#[derive(Parser)]\nstruct Cli {}\n\n#[derive(Subcommand)]\nenum Commands {\n    Build,\n    Run { name: String },\n}\n\nfn main() {}\n"},
		{Path: "nodes/talker.cpp", Content: "int main(int argc, char** argv) {\n  ros::init(argc, argv, \"talker\");\n}\n"},
		{Path: "nodes/listener.py", Content: "import rospy\nrospy.init_node('listener')\n"},
		{Path: "src/plugin.cpp", Content: "PLUGINLIB_EXPORT_CLASS(shop::Fast, shop::Planner)\n"},
		{Path: "launch/all.launch", Content: "<launch>\n  <node name=\"talker\" pkg=\"demo\" type=\"talker\"/>\n  <node name=\"listener\" pkg=\"demo\" type=\"listener.py\"/>\n</launch>\n"},
		{Path: "CMakeLists.txt", Content: "add_executable(talker nodes/talker.cpp)\n"},
		{Path: "src/router.ts", Content: "import { createRouter } from 'vue-router';\nconst routes = [\n  { path: '/orders', component: OrdersPage },\n  { path: '/cart',\n    component: () => import('./Cart.vue') },\n];\n"},
		{Path: "app/orders/page.tsx", Content: "export default function Page() {}\n"},
		{Path: "package.json", Content: `{"name":"x","dependencies":{"next":"14"},"bin":{"shop":"bin/shop.js"}}`},
		{Path: ".gitlab-ci.yml", Content: "stages: [test]\nunit:\n  script: make test\n.hidden:\n  script: x\n"},
		{Path: "tests/main_test.go", Content: "package main\nfunc main() {}\n"},
	}
	eps := query.EntryPoints("r", files)
	byKind := map[string][]string{}
	for _, e := range eps {
		byKind[e.Kind] = append(byKind[e.Kind], e.Framework+"|"+e.Name+"|"+strings.Join(e.Items, ","))
		if strings.HasPrefix(e.Path, "tests/") {
			t.Errorf("a test file is not an entry point: %+v", e)
		}
	}
	want := map[string][]string{
		query.EntryCLI:      {"cobra|tool|tool,sync", "click|cli.py|cli,import-orders", "clap|src/main.rs|build,run", "npm bin|shop → bin/shop.js|shop → bin/shop.js"},
		query.EntryROSNode:  {"roscpp|talker|", "rospy|listener|"},
		query.EntryPlugin:   {"pluginlib|shop::Fast|"},
		query.EntryLaunch:   {"roslaunch|all.launch|talker (demo/talker),listener (demo/listener.py)"},
		query.EntryBinary:   {"cmake|CMakeLists.txt|talker"},
		query.EntryUIRoutes: {"vue-router|src/router.ts|/orders → OrdersPage,/cart → ./Cart.vue", "next|/orders|/orders"},
		query.EntryCI:       {"gitlab-ci|.gitlab-ci.yml|unit"},
	}
	for k, ws := range want {
		for _, w := range ws {
			if !containsStr(byKind[k], w) {
				t.Errorf("%s: want %q in %q", k, w, byKind[k])
			}
		}
	}
	for _, e := range eps {
		if e.Kind == query.EntryMain && e.Path == "nodes/talker.cpp" {
			t.Errorf("a ROS node's main is reported as the node, not a bare main: %+v", e)
		}
	}
}

func containsStr(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}

// The fleet map lists every repo and the cross-repo links the index knows:
// an HTTP client in one repo reaching a route in another, an import-resolved
// reference, and a declared dependency on another repo's package.
func TestFleetMapLinks(t *testing.T) {
	m := storage.NewMem()
	m.SetModulePath("api", "shop-api")
	m.Put("api", "HEAD", "svc.list_orders", rec("lo", "lo"))
	m.PutFile("api", "HEAD", query.File{Path: "svc/app.py", Content: "from fastapi import APIRouter\nrouter = APIRouter(prefix=\"/api\")\n\n@router.get(\"/orders\")\ndef list_orders():\n    return []\n",
		Symbols: []query.SymbolSpan{{Name: "list_orders", StartLine: 4, EndLine: 6}}})
	m.PutFile("api", "HEAD", query.File{Path: "README.md", Content: "# api\n\nThe order service for the shop.\n"})
	m.Put("ui", "HEAD", "src.load", rec("l", "l"))
	m.PutFile("ui", "HEAD", query.File{Path: "src/orders.js", Content: "export function load() {\n  return fetch('/api/orders');\n}\n",
		Symbols: []query.SymbolSpan{{Name: "load", StartLine: 1, EndLine: 3}}})
	m.PutFile("ui", "HEAD", query.File{Path: "package.json", Content: `{"name":"shop-ui","dependencies":{"shop-api":"^1"}}`})
	m.PutExternalRefs("ui", "HEAD", []query.ExternalRef{{From: "src.load", Module: "shop-api/client", Name: "Orders", ResolutionMethod: "import-resolved", Confidence: 0.75}})

	r := query.FleetMap(m, "HEAD", 2000)
	if len(r.Repos) != 2 {
		t.Fatalf("repos = %+v", r.Repos)
	}
	for _, rs := range r.Repos {
		if rs.Repo == "api" && (rs.Summary != "The order service for the shop." || rs.Routes != 1) {
			t.Errorf("api summary = %+v", rs)
		}
	}
	kinds := map[string]bool{}
	for _, l := range r.Links {
		if l.From != "ui" || l.To != "api" {
			t.Errorf("link direction: %+v", l)
		}
		kinds[l.Kind] = true
	}
	for _, k := range []string{"http", "import", "declared-dep"} {
		if !kinds[k] {
			t.Errorf("no %s link: %+v", k, r.Links)
		}
	}
	// One repo maps in depth; a miss at an unindexed ref says so.
	if mr := query.Map(m, "api", "v9", 0); len(mr.Meta.Warnings) == 0 || !strings.Contains(mr.Meta.Warnings[0], "not indexed") {
		t.Errorf("unindexed ref: %+v", mr.Meta)
	}
}

func TestExplainAmbiguousAcrossRepos(t *testing.T) {
	m := storage.NewMem()
	for _, repo := range []string{"a", "b"} {
		m.Put(repo, "HEAD", "pkg.Run", rec("r"+repo, "r"))
		m.PutFile(repo, "HEAD", query.File{Path: "pkg/run.go", Content: "package pkg\n\nfunc Run() {}\n", Symbols: []query.SymbolSpan{{Name: "Run", StartLine: 3, EndLine: 3}}})
	}
	r := query.Explain(m, "HEAD", "Run", query.ExplainOptions{})
	if r.Kind != "ambiguous" || len(r.Candidates) != 2 {
		t.Errorf("two repos defining Run must be reported, not guessed: %+v", r)
	}
	if r = query.Explain(m, "HEAD", "Run", query.ExplainOptions{Prefer: "b"}); r.Kind != "symbol" || r.Repo != "b" {
		t.Errorf("the preferred (local) repo wins: %+v", r)
	}
}

func TestDocSummaryAndSourceFacts(t *testing.T) {
	m := storage.NewMem()
	py := "class Cart:\n    \"\"\"A shopping cart.\n\n    Holds lines.\n    \"\"\"\n\n    @property\n    def total(self) -> int:\n        '''Sum of the lines.'''\n        return 0\n"
	cpp := "/**\n * Computes the price.\n */\nint price(int qty) {\n  return qty;\n}\n"
	m.Put("r", "HEAD", "shop.Cart", rec("c", "c"))
	m.Put("r", "HEAD", "shop.Cart.total", rec("t", "t"))
	m.Put("r", "HEAD", "shop.price", rec("p", "p"))
	m.PutFile("r", "HEAD", query.File{Path: "shop/cart.py", Content: py, Symbols: []query.SymbolSpan{{Name: "Cart", StartLine: 1, EndLine: 10}, {Name: "total", StartLine: 7, EndLine: 10}}})
	m.PutFile("r", "HEAD", query.File{Path: "shop/price.cpp", Content: cpp, Symbols: []query.SymbolSpan{{Name: "price", StartLine: 4, EndLine: 6}}})
	m.PutFile("r", "HEAD", query.File{Path: "README.rst", Content: "Shop\n====\n\n.. image:: badge.svg\n\nA tiny shop used in tests. More text.\n"})
	for _, c := range []struct{ q, kind, sig, doc string }{
		{"shop.Cart", "class", "class Cart:", "A shopping cart.\n\nHolds lines."},
		{"shop.Cart.total", "method", "def total(self) -> int:", "Sum of the lines."},
		{"shop.price", "function", "int price(int qty)", "Computes the price."},
	} {
		r := query.Explain(m, "HEAD", c.q, query.ExplainOptions{})
		if r.Symbol == nil || r.Symbol.Kind != c.kind || r.Symbol.Signature != c.sig || r.Symbol.Doc != c.doc {
			t.Errorf("%s: got %+v", c.q, r.Symbol)
		}
	}
	if r := query.Explain(m, "HEAD", "shop.Cart.total", query.ExplainOptions{}); len(r.Symbol.Decorators) != 1 || r.Symbol.Decorators[0] != "@property" {
		t.Errorf("decorators = %v", r.Symbol.Decorators)
	}
	if mr := query.Map(m, "r", "HEAD", 0); mr.Summary != "A tiny shop used in tests." {
		t.Errorf("rst summary = %q", mr.Summary)
	}
}

// A viewset routed through a router: its standard actions are reached via the
// class (create on the collection route only), a custom @action only by its
// own route, and a helper method by none.
func TestExplainViewsetReach(t *testing.T) {
	m := storage.NewMem()
	urls := "from rest_framework import routers\nrouter = routers.DefaultRouter()\nrouter.register(r\"orders\", views.OrderViewSet)\nurlpatterns = [\n    path(\"api/\", include(router.urls)),\n]\n"
	views := "class OrderViewSet(viewsets.ModelViewSet):\n    def create(self, request):\n        return self.helper()\n\n    @action(detail=False, methods=[\"post\"])\n    def export(self, request):\n        pass\n\n    def helper(self):\n        pass\n"
	m.PutFile("shop", "HEAD", query.File{Path: "shop/urls.py", Content: urls})
	m.PutFile("shop", "HEAD", query.File{Path: "shop/views.py", Content: views, Symbols: []query.SymbolSpan{
		{Name: "OrderViewSet", StartLine: 1, EndLine: 10}, {Name: "create", StartLine: 2, EndLine: 3}, {Name: "export", StartLine: 5, EndLine: 7}, {Name: "helper", StartLine: 9, EndLine: 10}}})
	for _, q := range []string{"shop.OrderViewSet", "shop.OrderViewSet.create", "shop.OrderViewSet.export", "shop.OrderViewSet.helper"} {
		m.Put("shop", "HEAD", q, rec(q, q))
	}
	reach := func(q string) []string {
		r := query.Explain(m, "HEAD", q, query.ExplainOptions{})
		var out []string
		for _, x := range r.Symbol.ReachedBy {
			out = append(out, x.Detail)
		}
		return out
	}
	if got := reach("shop.OrderViewSet.create"); len(got) != 1 || !strings.Contains(got[0], "api/orders/ ") || !strings.Contains(got[0], "via its class") {
		t.Errorf("create reached by %v", got)
	}
	if got := reach("shop.OrderViewSet.export"); len(got) != 1 || !strings.Contains(got[0], "POST /api/orders/export/") {
		t.Errorf("export reached by %v", got)
	}
	if got := reach("shop.OrderViewSet.helper"); len(got) != 0 {
		t.Errorf("a helper is not reached by a route: %v", got)
	}
	if got := reach("shop.OrderViewSet"); len(got) < 2 {
		t.Errorf("the viewset class is reached by its routes: %v", got)
	}
}
