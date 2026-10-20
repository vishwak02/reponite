package query_test

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// catalogRepo: a read-only DRF viewset whose serializer has a method field
// and a nested serializer with its own method field, plus an unrelated view
// whose get() is the only method of that name in the repo.
func catalogRepo() *storage.Mem {
	m := storage.NewMem()
	urls := `from django.urls import path
from shop import views
urlpatterns = [
    path("api/items/", views.ItemViewSet.as_view({"get": "list"})),
]`
	views := `class ItemViewSet(viewsets.ReadOnlyModelViewSet):
    queryset = Item.objects.all()
    serializer_class = ItemSerializer

    def get_queryset(self):
        if pk := self.kwargs.get("pk"):
            return self.queryset.filter(pk=pk)
        return self.queryset
`
	serializers := `class FileSerializer(serializers.ModelSerializer):
    size = serializers.SerializerMethodField()

    def get_size(self, instance):
        return instance.file.size


class ItemSerializer(serializers.ModelSerializer):
    attachment = FileSerializer(read_only=True)
    count = serializers.SerializerMethodField()

    def get_count(self, instance):
        return 1
`
	other := `class SchemaView(APIView):
    def get(self, request):
        return Response({})
`
	putRepo(m, "shop", []fixSym{
		{qid: "shop.views.ItemViewSet"},
		{qid: "shop.views.ItemViewSet.get_queryset", callees: []query.Callee{call("get", "ambiguous", 0.5)}},
		{qid: "shop.serializers.FileSerializer"},
		{qid: "shop.serializers.FileSerializer.get_size"},
		{qid: "shop.serializers.ItemSerializer"},
		{qid: "shop.serializers.ItemSerializer.get_count"},
		{qid: "shop.other.SchemaView"},
		{qid: "shop.other.SchemaView.get"},
	},
		query.File{Path: "shop/urls.py", Content: urls},
		query.File{Path: "shop/views.py", Content: views, Symbols: []query.SymbolSpan{
			{Name: "ItemViewSet", StartLine: 1, EndLine: 8}, {Name: "get_queryset", StartLine: 5, EndLine: 8}}},
		query.File{Path: "shop/serializers.py", Content: serializers, Symbols: []query.SymbolSpan{
			{Name: "FileSerializer", StartLine: 1, EndLine: 5}, {Name: "get_size", StartLine: 4, EndLine: 5},
			{Name: "ItemSerializer", StartLine: 8, EndLine: 13}, {Name: "get_count", StartLine: 12, EndLine: 13}}},
		query.File{Path: "shop/other.py", Content: other, Symbols: []query.SymbolSpan{
			{Name: "SchemaView", StartLine: 1, EndLine: 3}, {Name: "get", StartLine: 2, EndLine: 3}}},
	)
	return m
}

// A list route reaches what the serializer renders per row — its method
// fields and a nested serializer's — and never follows kwargs.get() into the
// one class of the repo that happens to define get().
func TestTraceSerializerFieldsAndNoLoneMethodGuess(t *testing.T) {
	r := query.Trace(catalogRepo(), "GET /api/items/", "v1", query.TraceOptions{})
	size := find(r.Roots, bySym("shop.serializers.FileSerializer.get_size"))
	if size == nil || !strings.Contains(size.Note, "nested serializer field attachment") {
		t.Fatalf("nested serializer's method field not reached: %s", query.TraceText(r))
	}
	if !hasEffect(size, "file", "instance.file.size") {
		t.Errorf("a stored file's size is a file read: %+v", size.Effects)
	}
	if find(r.Roots, bySym("shop.serializers.ItemSerializer.get_count")) == nil {
		t.Errorf("serializer's own method field not reached: %s", query.TraceText(r))
	}
	if find(r.Roots, bySym("shop.other.SchemaView.get")) != nil {
		t.Errorf("kwargs.get() followed into an unrelated view: %s", query.TraceText(r))
	}
}

// context names the framework wiring as callers of a method field's getter:
// the serializer declaring the field, and the serializer nesting it.
func TestContextFrameworkCallersOfMethodField(t *testing.T) {
	c := query.Context(catalogRepo(), "shop", "v1", "shop.serializers.FileSerializer.get_size", false)
	via := map[string]string{}
	for _, e := range c.CallerEdges {
		via[e.Name] = e.Via
	}
	if !strings.Contains(via["shop.serializers.FileSerializer"], "SerializerMethodField size") {
		t.Errorf("declaring serializer: %+v", c.CallerEdges)
	}
	if !strings.Contains(via["shop.serializers.ItemSerializer"], "nested serializer field attachment") {
		t.Errorf("nesting serializer: %+v", c.CallerEdges)
	}
	v := query.Context(catalogRepo(), "shop", "v1", "shop.serializers.ItemSerializer.get_count", false)
	found := false
	for _, e := range v.CallerEdges {
		found = found || e.Name == "shop.views.ItemViewSet" && strings.Contains(e.Via, "serializer_class")
	}
	if !found {
		t.Errorf("the view using the serializer: %+v", v.CallerEdges)
	}
}
