package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Suggest returns close names and, importantly, does NOT return noise: a short
// common fragment ("for" inside "reposFor") is not a "did you mean", while a
// substantial shared prefix ("Repos") is.
func TestSuggestQuality(t *testing.T) {
	m := storage.NewMem()
	m.Put("r", "HEAD", "internal/storage.Repos", storage.SymbolRecord{})
	m.Put("r", "HEAD", "internal/query.reposByName", storage.SymbolRecord{})
	m.Put("r", "HEAD", "x.for", storage.SymbolRecord{})   // noise: short substring
	m.Put("r", "HEAD", "x.position", storage.SymbolRecord{}) // noise: unrelated

	names := map[string]bool{}
	for _, h := range query.Suggest(m, query.FleetRepo, "HEAD", "reposFor", 5) {
		names[h.Name] = true
	}
	if names["x.for"] {
		t.Error("short common fragment \"for\" must not be suggested for \"reposFor\"")
	}
	if names["x.position"] {
		t.Error("unrelated \"position\" must not be suggested")
	}
	if !names["internal/storage.Repos"] && !names["internal/query.reposByName"] {
		t.Errorf("a substantial near-name should be suggested; got %v", names)
	}
}
