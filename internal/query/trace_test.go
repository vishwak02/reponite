package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

type fixSym struct {
	qid     string
	callees []query.Callee
}

func call(name, method string, conf float64) query.Callee {
	return query.Callee{Name: name, ResolutionMethod: method, Confidence: conf}
}

func putRepo(m *storage.Mem, repo string, syms []fixSym, files ...query.File) {
	for _, s := range syms {
		m.Put(repo, "v1", s.qid, storage.SymbolRecord{Callees: s.callees})
	}
	for _, f := range files {
		m.PutFile(repo, "v1", f)
	}
}

func find(nodes []*query.TraceNode, pred func(*query.TraceNode) bool) *query.TraceNode {
	for _, n := range nodes {
		if pred(n) {
			return n
		}
		if c := find(n.Children, pred); c != nil {
			return c
		}
	}
	return nil
}

func bySym(sym string) func(*query.TraceNode) bool {
	return func(n *query.TraceNode) bool { return n.Symbol == sym }
}

func hasEffect(n *query.TraceNode, kind, text string) bool {
	for _, e := range n.Effects {
		if e.Kind == kind && strings.Contains(e.Text, text) {
			return true
		}
	}
	return false
}

// shopFleet: a Django-style backend and a frontend calling it.
func shopFleet() *storage.Mem {
	m := storage.NewMem()
	urls := `from django.urls import path
from shop import views
urlpatterns = [
    path("api/orders/", views.OrderView.as_view()),
    path("api/carts/", views.CartViewSet.as_view()),
]`
	views := `import logging
logger = logging.getLogger(__name__)

class OrderView(APIView):
    def post(self, request):
        if not request.data:
            raise ValidationError("empty order")
        order = Order.objects.create(total=request.data["total"])
        logger.info("created order %s", order.id)
        notify(order)
        return Response(order.id)

class CartViewSet(viewsets.ModelViewSet):
    queryset = Cart.objects.all()
    serializer_class = CartSerializer

    def perform_create(self, serializer):
        serializer.save(owner=self.request.user)
`
	events := `def notify(order):
    subprocess.run(["notify-send", str(order.id)])
    with open("/tmp/orders.log", "a") as f:
        f.write(str(order.id))
`
	putRepo(m, "shop", []fixSym{
		{qid: "shop.OrderView"},
		{qid: "shop.OrderView.post", callees: []query.Callee{call("shop.notify", "name-resolved", 0.9), call("create", "unresolved-external", 0.6)}},
		{qid: "shop.CartViewSet"},
		{qid: "shop.CartViewSet.perform_create"},
		{qid: "shop.notify"},
	},
		query.File{Path: "shop/urls.py", Content: urls},
		query.File{Path: "shop/views.py", Content: views, Symbols: []query.SymbolSpan{
			{Name: "OrderView", StartLine: 4, EndLine: 11}, {Name: "post", StartLine: 5, EndLine: 11},
			{Name: "CartViewSet", StartLine: 13, EndLine: 18}, {Name: "perform_create", StartLine: 17, EndLine: 18}}},
		query.File{Path: "shop/events.py", Content: events, Symbols: []query.SymbolSpan{{Name: "notify", StartLine: 1, EndLine: 4}}},
	)
	ui := `export const createOrder = (order) => api.post("/api/orders/", order);
export function CartPage() {
  const onSubmit = () => createOrder({ total: 3 });
  return onSubmit;
}
`
	putRepo(m, "web", []fixSym{
		{qid: "src/api.createOrder", callees: []query.Callee{call("post", "unresolved-external", 0.6)}},
		{qid: "src/api.CartPage", callees: []query.Callee{call("src/api.createOrder", "name-resolved", 0.9)}},
	}, query.File{Path: "src/api/orders.ts", Content: ui, Symbols: []query.SymbolSpan{
		{Name: "createOrder", StartLine: 1, EndLine: 1}, {Name: "CartPage", StartLine: 2, EndLine: 5}}})
	return m
}

// An HTTP route entry starts at the server handler the route names and
// reports its ORM write, log line, raised error and the effects of what it
// calls.
func TestTraceRouteToHandlerAndORMWrite(t *testing.T) {
	r := query.Trace(shopFleet(), "POST /api/orders/", "v1", query.TraceOptions{})
	if r.EntryKind != "route" || len(r.Roots) != 1 || r.Roots[0].Kind != query.NodeRoute {
		t.Fatalf("entry: %s roots %+v", r.EntryKind, r.Roots)
	}
	post := find(r.Roots, bySym("shop.OrderView.post"))
	if post == nil || post.Edge != query.EdgeHandler || post.Method != "http-method-dispatch" {
		t.Fatalf("handler via the class's post(): %s", query.TraceText(r))
	}
	if !hasEffect(post, "db", "Order.objects.create") || !hasEffect(post, "log", "created order") || !hasEffect(post, "raise", "ValidationError") {
		t.Errorf("handler effects: %+v", post.Effects)
	}
	for _, e := range post.Effects {
		if e.Kind == "db" && e.Op != "write" {
			t.Errorf("objects.create is a write: %+v", e)
		}
	}
	notify := find(r.Roots, bySym("shop.notify"))
	if notify == nil || !hasEffect(notify, "exec", "subprocess.run") || !hasEffect(notify, "file", "open") {
		t.Errorf("callee effects: %+v", notify)
	}
	// A DRF viewset's inherited create() is a framework node whose children
	// are the hooks the class overrides, with the framework's write inferred.
	c := query.Trace(shopFleet(), "POST /api/carts/", "v1", query.TraceOptions{})
	fw := find(c.Roots, func(n *query.TraceNode) bool { return n.Kind == query.NodeFramework })
	if fw == nil || !strings.Contains(fw.Label, "create") || !hasEffect(fw, "db", "INSERT Cart") {
		t.Fatalf("inherited create: %s", query.TraceText(c))
	}
	if h := find(fw.Children, bySym("shop.CartViewSet.perform_create")); h == nil || h.Edge != query.EdgeFramework {
		t.Errorf("perform_create hook under the inherited action: %s", query.TraceText(c))
	}
}

// From the UI the walk crosses the HTTP call into the backend; walking UP
// from the handler reaches the UI function that calls it, and its caller.
func TestTraceCrossesClientToServerAndBack(t *testing.T) {
	m := shopFleet()
	r := query.Trace(m, "CartPage", "v1", query.TraceOptions{})
	route := find(r.Roots, func(n *query.TraceNode) bool { return n.Kind == query.NodeRoute })
	if route == nil || route.Edge != query.EdgeHTTP || route.Repo != "shop" {
		t.Fatalf("http crossing: %s", query.TraceText(r))
	}
	if find(route.Children, bySym("shop.OrderView.post")) == nil {
		t.Errorf("route -> handler: %s", query.TraceText(r))
	}
	ui := find(r.Roots, bySym("src/api.createOrder"))
	if ui == nil || ui.Effects[0].Kind != "http" || !strings.Contains(ui.Effects[0].Target, "shop:") {
		t.Errorf("http effect linked to its route: %+v", ui)
	}
	up := query.Trace(m, "OrderView.post", "v1", query.TraceOptions{Direction: "up"})
	client := find(up.Roots, bySym("src/api.createOrder"))
	if client == nil || client.Edge != query.EdgeClient {
		t.Fatalf("up: handler -> route -> client: %s", query.TraceText(up))
	}
	if find(client.Children, bySym("src/api.CartPage")) == nil {
		t.Errorf("up: the client's caller: %s", query.TraceText(up))
	}
}

func rosFleet() *storage.Mem {
	m := storage.NewMem()
	driver := `class Driver:
    def __init__(self):
        self.pub = rospy.Publisher("cmd_vel", Twist, queue_size=1)

    def tick(self):
        self.pub.publish(Twist())
`
	base := `class Base:
    def __init__(self):
        rospy.Subscriber("/cmd_vel", Twist, self.on_cmd)

    def on_cmd(self, msg):
        logging.warning("got cmd %s", msg)
`
	putRepo(m, "planner", []fixSym{{qid: "nav.Driver"}, {qid: "nav.Driver.__init__"}, {qid: "nav.Driver.tick"}},
		query.File{Path: "nav/driver.py", Content: driver, Symbols: []query.SymbolSpan{
			{Name: "Driver", StartLine: 1, EndLine: 6}, {Name: "__init__", StartLine: 2, EndLine: 3}, {Name: "tick", StartLine: 5, EndLine: 6}}})
	putRepo(m, "base", []fixSym{{qid: "motor.Base"}, {qid: "motor.Base.__init__"}, {qid: "motor.Base.on_cmd"}},
		query.File{Path: "motor/base.py", Content: base, Symbols: []query.SymbolSpan{
			{Name: "Base", StartLine: 1, EndLine: 6}, {Name: "__init__", StartLine: 2, EndLine: 3}, {Name: "on_cmd", StartLine: 5, EndLine: 6}}})
	return m
}

// A topic entry starts at its subscribers' callbacks; a publish in a body
// crosses to the same callbacks; UP from a callback reaches the publisher.
func TestTraceTopicToSubscriberCallback(t *testing.T) {
	m := rosFleet()
	r := query.Trace(m, "/cmd_vel", "v1", query.TraceOptions{})
	if r.EntryKind != "topic" || len(r.Roots) != 1 {
		t.Fatalf("topic entry: %s", query.TraceText(r))
	}
	cb := find(r.Roots, bySym("motor.Base.on_cmd"))
	if cb == nil || cb.Edge != query.EdgeCallback || cb.Repo != "base" || !hasEffect(cb, "log", "got cmd") {
		t.Fatalf("subscriber callback: %s", query.TraceText(r))
	}
	d := query.Trace(m, "Driver.tick", "v1", query.TraceOptions{})
	tick := d.Roots[0]
	if !hasEffect(tick, "publish", "publish") || tick.Effects[0].Target != "topic cmd_vel" {
		t.Errorf("publish effect names its topic: %+v", tick.Effects)
	}
	if find(d.Roots, bySym("motor.Base.on_cmd")) == nil {
		t.Errorf("publish -> topic -> callback: %s", query.TraceText(d))
	}
	up := query.Trace(m, "on_cmd", "v1", query.TraceOptions{Direction: "up"})
	if p := find(up.Roots, bySym("nav.Driver.tick")); p == nil || p.Edge != query.EdgePublisher {
		t.Errorf("up: callback -> topic -> publisher: %s", query.TraceText(up))
	}
}

// A call through a base class fans out to every implementation fleet-wide —
// a plugin in another repo included — each with its own effects.
func TestTraceInterfaceFansOutToImplementations(t *testing.T) {
	m := storage.NewMem()
	putRepo(m, "core", []fixSym{
		{qid: "core.run", callees: []query.Callee{call("core.Store.save", "member-name-resolved", 0.7)}},
		{qid: "core.Store"}, {qid: "core.Store.save"},
	}, query.File{Path: "core/store.py", Content: "class Store:\n    def save(self, row):\n        raise NotImplementedError\n\ndef run(s):\n    s.save(1)\n",
		Symbols: []query.SymbolSpan{{Name: "Store", StartLine: 1, EndLine: 3}, {Name: "save", StartLine: 2, EndLine: 3}, {Name: "run", StartLine: 5, EndLine: 6}}})
	putRepo(m, "plugin", []fixSym{{qid: "sql.SqlStore"}, {qid: "sql.SqlStore.save"}},
		query.File{Path: "sql/store.py", Content: "from core.store import Store\n\nclass SqlStore(Store):\n    def save(self, row):\n        cursor.execute(\"INSERT INTO orders VALUES (%s)\", row)\n",
			Symbols: []query.SymbolSpan{{Name: "SqlStore", StartLine: 3, EndLine: 5}, {Name: "save", StartLine: 4, EndLine: 5}}})
	putRepo(m, "web", []fixSym{{qid: "src.MemStore"}, {qid: "src.MemStore.save"}},
		query.File{Path: "src/mem.ts", Content: "export class MemStore extends Store {\n  save(row) {\n    this.rows.push(row);\n  }\n}\n",
			Symbols: []query.SymbolSpan{{Name: "MemStore", StartLine: 1, EndLine: 5}, {Name: "save", StartLine: 2, EndLine: 4}}})
	r := query.Trace(m, "run", "v1", query.TraceOptions{})
	base := find(r.Roots, bySym("core.Store.save"))
	if base == nil || !hasEffect(base, "raise", "NotImplementedError") {
		t.Fatalf("base method: %s", query.TraceText(r))
	}
	sql := find(base.Children, bySym("sql.SqlStore.save"))
	if sql == nil || sql.Edge != query.EdgeImpl || sql.Method != query.MethodXRepoOverride || sql.Repo != "plugin" {
		t.Fatalf("python impl in another repo: %s", query.TraceText(r))
	}
	if !hasEffect(sql, "db", "cursor.execute") {
		t.Errorf("impl effects: %+v", sql.Effects)
	}
	if find(base.Children, bySym("src.MemStore.save")) == nil {
		t.Errorf("typescript impl (extends): %s", query.TraceText(r))
	}
}

// A cycle is marked, a subtree reached twice is expanded once, and depth and
// budget cuts say what was not walked and how to continue.
func TestTraceCycleRepeatDepthAndBudget(t *testing.T) {
	m := storage.NewMem()
	putRepo(m, "loop", []fixSym{
		{qid: "a", callees: []query.Callee{call("b", "name-resolved", 0.9), call("c", "name-resolved", 0.9)}},
		{qid: "b", callees: []query.Callee{call("a", "name-resolved", 0.9), call("d", "name-resolved", 0.9)}},
		{qid: "c", callees: []query.Callee{call("b", "name-resolved", 0.9)}},
		{qid: "d", callees: []query.Callee{call("e", "name-resolved", 0.9)}},
		{qid: "e"},
	})
	r := query.Trace(m, "a", "v1", query.TraceOptions{})
	b := find(r.Roots, bySym("b"))
	if cyc := find(b.Children, bySym("a")); cyc == nil || cyc.Stop != query.StopCycle {
		t.Errorf("a -> b -> a is a cycle: %s", query.TraceText(r))
	}
	c := find(r.Roots, bySym("c"))
	if rep := find(c.Children, bySym("b")); rep == nil || rep.Stop != query.StopRepeat || rep.Same != "b" || len(rep.Children) != 0 {
		t.Errorf("b under c is collapsed: %s", query.TraceText(r))
	}
	if r.Stops[query.StopCycle] != 1 || r.Stops[query.StopRepeat] != 1 || r.Truncated {
		t.Errorf("stops %v truncated %v", r.Stops, r.Truncated)
	}
	d := query.Trace(m, "a", "v1", query.TraceOptions{Depth: 1})
	if n := find(d.Roots, bySym("b")); n == nil || n.Stop != query.StopDepth || n.More != 2 || d.Next == "" {
		t.Errorf("depth cut: %s", query.TraceText(d))
	}
	bud := query.Trace(m, "a", "v1", query.TraceOptions{Budget: 12})
	if !bud.Truncated || bud.Stops[query.StopBudget] == 0 || !strings.Contains(bud.Next, "--budget") || bud.Tokens > 12 {
		t.Errorf("budget cut: %+v\n%s", bud.Stops, query.TraceText(bud))
	}
}

// An ambiguous call is resolved by the caller's import, else listed.
func TestTraceAmbiguousCallByImport(t *testing.T) {
	m := storage.NewMem()
	putRepo(m, "svc", []fixSym{
		{qid: "app.handle", callees: []query.Callee{call("notify", "ambiguous", 0.5), call("emit", "ambiguous", 0.5)}},
		{qid: "app/events.notify"}, {qid: "app/legacy.notify"}, {qid: "app/x.emit"}, {qid: "app/y.emit"},
	}, query.File{Path: "app/main.py", Content: "from app.events import notify\n\ndef handle():\n    notify()\n    emit()\n",
		Symbols: []query.SymbolSpan{{Name: "handle", StartLine: 3, EndLine: 5}}},
		query.File{Path: "app/events/__init__.py", Content: "def notify():\n    pass\n", Symbols: []query.SymbolSpan{{Name: "notify", StartLine: 1, EndLine: 2}}},
		query.File{Path: "app/legacy/notify.py", Content: "def notify():\n    pass\n", Symbols: []query.SymbolSpan{{Name: "notify", StartLine: 1, EndLine: 2}}})
	r := query.Trace(m, "handle", "v1", query.TraceOptions{})
	if n := find(r.Roots, bySym("app/events.notify")); n == nil || !strings.Contains(n.Note, "import") {
		t.Errorf("import decides: %s", query.TraceText(r))
	}
	if n := find(r.Roots, bySym("emit")); n == nil || n.Stop != query.StopAmbiguous || len(n.Alt) != 2 {
		t.Errorf("undecided stays ambiguous with candidates: %s", query.TraceText(r))
	}
}

// A CLI command name resolves through its registration to the handler.
func TestTraceCLICommand(t *testing.T) {
	m := storage.NewMem()
	putRepo(m, "tool", []fixSym{{qid: "cmd.main"}, {qid: "cmd.runServe"}, {qid: "shop/management/commands.Command.handle"}},
		query.File{Path: "cmd/main.go", Content: "func main() {\n\tswitch os.Args[1] {\n\tcase \"serve\", \"s\":\n\t\trunServe(os.Args[2:])\n\t}\n}\n\nfunc runServe(args []string) {\n\tos.WriteFile(\"pid\", nil, 0o644)\n}\n",
			Symbols: []query.SymbolSpan{{Name: "main", StartLine: 1, EndLine: 6}, {Name: "runServe", StartLine: 8, EndLine: 10}}},
		query.File{Path: "shop/management/commands/import_orders.py", Content: "class Command(BaseCommand):\n    def handle(self, *a, **k):\n        Order.objects.bulk_create([])\n",
			Symbols: []query.SymbolSpan{{Name: "Command", StartLine: 1, EndLine: 3}, {Name: "handle", StartLine: 2, EndLine: 3}}})
	r := query.Trace(m, "tool serve", "v1", query.TraceOptions{})
	h := find(r.Roots, bySym("cmd.runServe"))
	if r.EntryKind != "cli" || h == nil || !hasEffect(h, "file", "os.WriteFile") {
		t.Errorf("switch dispatch: %s", query.TraceText(r))
	}
	d := query.Trace(m, "cli:import_orders", "v1", query.TraceOptions{})
	if h := find(d.Roots, bySym("shop/management/commands.Command.handle")); h == nil || !hasEffect(h, "db", "bulk_create") {
		t.Errorf("management command: %s", query.TraceText(d))
	}
}

func TestTraceMissSuggests(t *testing.T) {
	r := query.Trace(shopFleet(), "OrderVeiw.post", "v1", query.TraceOptions{})
	if len(r.Roots) != 0 || !strings.Contains(r.Note, "matched no") {
		t.Errorf("miss: %+v", r)
	}
}

// An outside edge source (UI render/handler/API-call edges) joins the walk:
// a component -> its handler -> the API call -> the route -> the view.
func TestTraceExtraEdgeSource(t *testing.T) {
	m := shopFleet()
	m.Put("web", "v1", "src/pages.OrderButton", storage.SymbolRecord{})
	m.Put("web", "v1", "src/pages.onClick", storage.SymbolRecord{})
	src := func(repo, qid string, up bool) []query.TraceEdge {
		switch {
		case !up && qid == "src/pages.OrderButton":
			return []query.TraceEdge{{Target: "src/pages.onClick", Kind: "handles", Method: "jsx-handler", Confidence: 0.8}}
		case !up && qid == "src/pages.onClick":
			return []query.TraceEdge{{Kind: "calls_api", HTTPMethod: "POST", URL: "/api/orders/", Confidence: 0.7}}
		case up && qid == "src/pages.onClick":
			return []query.TraceEdge{{Target: "src/pages.OrderButton", Kind: "handles", Method: "jsx-handler", Confidence: 0.8}}
		}
		return nil
	}
	r := query.Trace(m, "OrderButton", "v1", query.TraceOptions{Edges: src})
	h := find(r.Roots, bySym("src/pages.onClick"))
	if h == nil || h.Edge != "handles" {
		t.Fatalf("handler edge: %s", query.TraceText(r))
	}
	rt := find(h.Children, func(n *query.TraceNode) bool { return n.Kind == query.NodeRoute })
	if rt == nil || find(rt.Children, bySym("shop.OrderView.post")) == nil {
		t.Errorf("api call -> route -> view: %s", query.TraceText(r))
	}
	up := query.Trace(m, "onClick", "v1", query.TraceOptions{Direction: "up", Edges: src})
	if find(up.Roots, bySym("src/pages.OrderButton")) == nil {
		t.Errorf("up through the source: %s", query.TraceText(up))
	}
}
