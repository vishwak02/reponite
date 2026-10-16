package interfaces

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

func dataMem() *storage.Mem {
	m := storage.NewMem()
	m.PutFile("shop", "HEAD", query.File{Path: "shop/models.py", Content: "from django.db import models\n\nclass Order(models.Model):\n    number = models.CharField(max_length=9, unique=True)\n"})
	m.PutFile("shop", "HEAD", query.File{Path: "shop/jobs.py", Content: "def close():\n    Order.objects.filter(number=1).delete()\n    port = os.getenv(\"APP_PORT\", \"8000\")\n",
		Symbols: []query.SymbolSpan{{Name: "close", StartLine: 1, EndLine: 3}}})
	m.PutFile("shop", "HEAD", query.File{Path: ".env", Content: "APP_PORT=9000\nSTALE=1\n"})
	return m
}

func TestModelsAndConfigTools(t *testing.T) {
	ts := &ToolServer{Store: dataMem(), Repo: "shop"}
	out, err := ts.Call("reponite_models", map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	var list map[string]interface{}
	if err := json.Unmarshal([]byte(out), &list); err != nil || list["total"].(float64) != 1 {
		t.Fatalf("models list: %v %s", err, out)
	}
	out, _ = ts.Call("reponite_models", map[string]string{"model": "shop_order"})
	if !strings.Contains(out, `"op": "delete"`) || !strings.Contains(out, `"table": "shop_order"`) || !strings.Contains(out, `"unique": true`) {
		t.Fatalf("models detail: %s", out)
	}
	txt, _ := ts.Call("reponite_models", map[string]string{"model": "Order", "format": "text"})
	if !strings.Contains(txt, "W delete") || !strings.Contains(txt, "shop/jobs.py:2") {
		t.Fatalf("models text: %s", txt)
	}
	out, _ = ts.Call("reponite_config", map[string]string{})
	if !strings.Contains(out, `"set_never_read"`) || !strings.Contains(out, `"STALE"`) {
		t.Fatalf("config list: %s", out)
	}
	txt, _ = ts.Call("reponite_config", map[string]string{"key": "app_port", "format": "text"})
	if !strings.Contains(txt, "R shop:shop/jobs.py:3") || !strings.Contains(txt, "S shop:.env:1 dotenv = 9000") {
		t.Fatalf("config text: %s", txt)
	}
}
