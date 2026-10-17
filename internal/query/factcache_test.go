package query_test

import (
	"bytes"
	"encoding/gob"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

type mapCache struct {
	m     map[string][]byte
	loads int
}

func (c *mapCache) Load(key string, v any) bool {
	b, ok := c.m[key]
	if ok && gob.NewDecoder(bytes.NewReader(b)).Decode(v) == nil {
		c.loads++
		return true
	}
	return false
}

func (c *mapCache) Save(key string, v any) {
	var buf bytes.Buffer
	if gob.NewEncoder(&buf).Encode(v) == nil {
		c.m[key] = buf.Bytes()
	}
}

// A cached communication graph is served only for the content it was built
// from: a re-indexed ref (new manifest) recomputes.
func TestFactCacheKeyedByContent(t *testing.T) {
	c := &mapCache{m: map[string][]byte{}}
	query.SetFactCache(c)
	defer query.SetFactCache(nil)
	m := storage.NewMem()
	put := func(topic, blob string) {
		m.PutFile("r", "v1", query.File{Path: "src/a.cpp", Content: "void A::init() {\n  pub_ = nh.advertise<std_msgs::String>(\"" + topic + "\", 1);\n}\n"})
		m.PutManifest("r", "v1", content.Manifest{Ref: "v1", Commit: "c1", Blobs: []content.Hash{content.Hash(blob)}})
	}
	put("/first", "b1")
	if g := query.CommGraph(m, "r", "v1"); len(g.Groups) != 1 || g.Groups[0].Name != "first" {
		t.Fatalf("groups %+v", g.Groups)
	}
	if g := query.CommGraph(m, "r", "v1"); c.loads != 1 || g.Groups[0].Name != "first" {
		t.Fatalf("second call must come from the cache: loads=%d", c.loads)
	}
	put("/second", "b2")
	found := false
	for _, g := range query.CommGraph(m, "r", "v1").Groups {
		found = found || g.Name == "second"
	}
	if !found || c.loads != 1 {
		t.Fatalf("new content must recompute, not load: loads=%d", c.loads)
	}
}
