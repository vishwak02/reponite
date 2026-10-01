package query

import "testing"

func TestRoutesLinkClientsToServers(t *testing.T) {
	root := `from django.urls import include, path
urlpatterns = [
    path("admin/", admin.site.urls),
    path("v1/", include("api_v1.urls")),
]`
	v1 := `urlpatterns = [
    path("store/", include("store.v1.urls", namespace="store")),
]`
	wh := `from rest_framework import routers
router = routers.DefaultRouter()
router.register(r"containers", ContainerViewSet)
urlpatterns = [
    path("labels/", LabelCreateView.as_view(), name="labels"),
    re_path(r"^labels/(?P<pk>[0-9]+)/reprint/$", reprint_label),
    path("", include(router.urls)),
]`
	api := `from fastapi import APIRouter
router = APIRouter(prefix="/orders")

@router.get("/{order_id}")
async def get_order(order_id: int):
    return {}

@router.post(
    "/upload",
    status_code=201,
)
def upload():
    pass
`
	views := `class ContainerViewSet(viewsets.ModelViewSet):
    @action(
        detail=False, methods=["post"], url_path=r"apply:dry_run"
    )
    def dry(self, request):
        pass

    @action(detail=True, methods=["get"])
    def history(self, request, pk=None):
        pass
`
	wmsURLs := `from rest_framework_nested import routers
v2_router = routers.SimpleRouter()
v2_router.register(
    r"operations",  # one per upload
    DocumentOperationViewSetV2,
    basename="v2-documentoperation",
)
v2_err = routers.NestedSimpleRouter(
    v2_router, r"operations", lookup="operation"
)
v2_err.register(r"errors", ErrorViewSet, basename="e")
urlpatterns = [
    path(
        r"health/",
        HealthCheck.as_view(),
    ),
]
urlpatterns += [
    path(r"v2/", include(router.urls))
    for router in [
        v2_router,
        v2_err,
    ]
]`
	main := `app.include_router(orders.router, prefix="/api")`
	ui := `const INV_URLS = {
  CREATE_LABEL: 'v1/store/labels/',
  CONTAINERS: 'v1/store/containers/',
};
export const createLabel = (p) => inv.post(INV_URLS.CREATE_LABEL, p);
export const reprint = (id) => inv.post(` + "`v1/store/labels/${id}/reprint/`" + `);
export const container = (id) => inv.get("v1/store/containers/" + id + "/");
export const order = (id) => fetch(` + "`${BASE}/api/orders/${id}`" + `);
const x = fragmentsMap.get(fragmentId);
export const hist = (id) => inv.get<IHistory>(` + "`${INV_URLS.CONTAINERS}${id}/history/`" + `);
export const post = () => fetch("/api/orders/upload", {
  method: "POST",
});
const OTHER = { CREATE_LABEL: 'v9/elsewhere/' };
export const errs = (id) => wms.get(` + "`v2/operations/${id}}/errors/`" + `);
export const gone = () => inv.get('v1/nowhere/at/all/');`
	s := filesStore{files: map[string][]File{
		"inventory": {{Path: "project/urls.py", Content: root}, {Path: "api_v1/urls.py", Content: v1},
			{Path: "store/v1/urls.py", Content: wh}, {Path: "store/v1/views.py", Content: views}},
		"wms": {{Path: "app/orders.py", Content: api}, {Path: "app/main.py", Content: main}, {Path: "wms/urls.py", Content: wmsURLs}},
		"ui":  {{Path: "src/api/label.ts", Content: ui}},
	}}
	r := Routes(s, FleetRepo, "v1", "", 0)
	byPat := map[string]RouteLink{}
	for _, l := range r.Links {
		byPat[l.Route.Pattern] = l
	}
	want := func(pattern, method string, clientLine int) {
		t.Helper()
		l, ok := byPat[pattern]
		if !ok {
			var have []string
			for p := range byPat {
				have = append(have, p)
			}
			t.Errorf("no route %q (have %v)", pattern, have)
			return
		}
		if method != "" && l.Route.Method != method {
			t.Errorf("%s: method %s, want %s", pattern, l.Route.Method, method)
		}
		for _, c := range l.Clients {
			if c.Line == clientLine {
				return
			}
		}
		if clientLine > 0 {
			t.Errorf("%s: client line %d not linked (clients %+v)", pattern, clientLine, l.Clients)
		}
	}
	want("v1/store/labels/", "*", 5)                      // include() chain + a named URL constant
	want("v1/store/labels/{}/reprint/", "*", 6)           // re_path group, template literal
	want("v1/store/containers/{}/", "*", 7)               // DRF router under include(router.urls), concatenation
	want("/api/orders/{}", "GET", 8)                      // FastAPI router prefix + include_router prefix, fetch
	want("/api/orders/upload", "POST", 11)                // fetch options on the next line; a same-named constant in another object
	want("v1/store/containers/apply:dry_run/", "POST", 0) // DRF @action, list-level, url_path
	want("v1/store/containers/{}/history/", "GET", 10)    // detail-level @action; TS generic call; constant in a template
	if l := byPat["/api/orders/{}"]; len(l.Clients) != 1 {
		t.Errorf("a POST client must not link to a GET route: %+v", l.Clients)
	}
	if len(r.Unmatched) != 1 || r.Unmatched[0].Line != 16 {
		t.Errorf("unmatched clients: %+v", r.Unmatched)
	}
	want("v2/operations/{}/errors/", "*", 15) // nested router, router list comprehension, multi-line register
	want("health/", "*", 0)                   // a multi-line path()
	for _, c := range byPat["v2/operations/{}/errors/"].Clients {
		if c.Warning == "" {
			t.Errorf("a stray brace in a template literal is flagged: %+v", c)
		}
	}
	f := Routes(s, FleetRepo, "v1", "/store/labels", 0)
	if len(f.Links) != 2 {
		t.Errorf("filter: %+v", f.Links)
	}
}
