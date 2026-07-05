package processing

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func TestRosFileParsing(t *testing.T) {
	// .msg -> one type with the field contract (comments/spacing stripped).
	pf, ok := rosFile("msg/Point.msg", "float64 x   # the x coord\n\nfloat64 y\nint32 KIND=2\n")
	if !ok || pf.Lang != "ros" || len(pf.Symbols) != 1 {
		t.Fatalf("Point.msg -> %+v (ok=%v)", pf.Symbols, ok)
	}
	if pf.Symbols[0].Name != "Point" || pf.Symbols[0].Kind != "type" {
		t.Fatalf("symbol = %+v", pf.Symbols[0])
	}
	if pf.Symbols[0].Signature != "float64 x\nfloat64 y\nint32 KIND=2" {
		t.Fatalf("signature = %q", pf.Symbols[0].Signature)
	}

	// .srv -> Request + Response sections split on "---".
	srv, _ := rosFile("srv/AddTwoInts.srv", "int64 a\nint64 b\n---\nint64 sum\n")
	if len(srv.Symbols) != 2 || srv.Symbols[0].Name != "AddTwoIntsRequest" || srv.Symbols[1].Name != "AddTwoIntsResponse" {
		t.Fatalf("srv symbols = %+v", srv.Symbols)
	}
}

// A field change is shape_changed; a comment-only change is compatible — the
// ROS interface-compat use case, exercised through the real hash pipeline.
func TestRosMsgCompatAcrossRefs(t *testing.T) {
	index := func(m *storage.Mem, ref, content string) {
		pf, ok := rosFile("msg/Point.msg", content)
		if !ok {
			t.Fatal("Point.msg not recognized as ROS")
		}
		if err := IndexFiles(m, "r", ref, 1, []ParsedFile{pf}); err != nil {
			t.Fatal(err)
		}
	}
	m := storage.NewMem()
	index(m, "v1", "float64 x\nfloat64 y\n")
	index(m, "v2", "float64 x\nfloat64 y\nfloat64 z\n")       // added a field
	index(m, "cmt", "float64 x  # x coordinate\nfloat64 y\n") // comment-only

	v1, _ := m.SymbolAt("r", "msg.Point", "v1")
	v2, _ := m.SymbolAt("r", "msg.Point", "v2")
	cmt, _ := m.SymbolAt("r", "msg.Point", "cmt")

	if v := query.Compat(v2, v1).Verdict; v != query.ShapeChanged {
		t.Fatalf("adding a ROS field must be shape_changed, got %s", v)
	}
	if v := query.Compat(v1, cmt).Verdict; v != query.Compatible {
		t.Fatalf("comment-only ROS change must be compatible, got %s", v)
	}
}
