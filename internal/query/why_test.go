package query

import (
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
)

func TestParseLineLogClassifies(t *testing.T) {
	out := "\x1eaaa\x1f1700000300\x1fAda\x1ffeat: quantities (#31)\x1fAdds qty.\n\nSigned-off-by: Ada <a@x>\x1d\n" +
		"diff --git a/shop/cart.py b/shop/cart.py\n--- a/shop/cart.py\n+++ b/shop/cart.py\n@@ -3,3 +3,3 @@\n-def add_item(cart, sku):\n+def add_item(cart, sku, qty=1):\n     cart.append(sku)\n     return cart\n" +
		"\x1ebbb\x1f1700000200\x1fBo\x1ffix: dedupe\x1f\x1d\n" +
		"diff --git a/shop/cart.py b/shop/cart.py\n--- a/shop/cart.py\n+++ b/shop/cart.py\n@@ -3,3 +3,4 @@\n def add_item(cart, sku):\n-    cart.append(sku)\n+    if sku not in cart:\n+        cart.append(sku)\n     return cart\n" +
		"\x1eccc\x1f1700000100\x1fAda\x1ffeat: cart (#12)\x1f\x1d\n" +
		"diff --git a/shop/cart.py b/shop/cart.py\n--- a/shop/cart.py\n+++ b/shop/cart.py\n@@ -2,0 +3,3 @@\n+def add_item(cart, sku):\n+    cart.append(sku)\n+    return cart\n"
	cs := ParseLineLog(out, "add_item")
	if len(cs) != 3 {
		t.Fatalf("want 3 changes, got %d", len(cs))
	}
	want := []HistoryKind{ChangeSignature, ChangeBody, ChangeIntroduced}
	for i, c := range cs {
		if c.Kind != want[i] {
			t.Errorf("%s: kind %s, want %s", c.Commit, c.Kind, want[i])
		}
	}
	if cs[0].Author != "Ada" || cs[0].Time != 1700000300 || cs[0].Subject != "feat: quantities (#31)" || cs[0].Added != 1 || cs[0].Removed != 1 {
		t.Errorf("header/counts misparsed: %+v", cs[0])
	}
	if len(cs[0].Paths) != 1 || cs[0].Paths[0] != "shop/cart.py" {
		t.Errorf("paths: %v", cs[0].Paths)
	}
}

func TestBodyExcerptDropsTrailers(t *testing.T) {
	got := BodyExcerpt("Why we did it.\n\nMore context here.\nCo-Authored-By: X <x@y>\nSigned-off-by: Y", 200)
	if got != "Why we did it. More context here." {
		t.Errorf("got %q", got)
	}
	long := BodyExcerpt(strings.Repeat("word ", 100), 40)
	if len(long) > 45 || !strings.HasSuffix(long, "…") {
		t.Errorf("not cut on a word boundary: %q", long)
	}
}

// fakeHistory serves canned line histories keyed by commit+path.
type fakeHistory struct {
	lines    map[string][]Change
	contains map[string][]string
	times    map[string]int64
	calls    []string
}

func (f *fakeHistory) LineHistory(repo string, r LineRange, limit int) ([]Change, error) {
	f.calls = append(f.calls, r.Commit+":"+r.Path)
	return f.lines[r.Commit+":"+r.Path], nil
}
func (f *fakeHistory) Pickaxe(repo, commit, needle string, paths []string, limit int) ([]Change, error) {
	return nil, nil
}
func (f *fakeHistory) Contains(repo string, commits []string, tips map[string]string) (map[string][]string, error) {
	return f.contains, nil
}
func (f *fakeHistory) CommitTimes(repo string, commits []string) (map[string]int64, error) {
	return f.times, nil
}

type whyStore struct {
	Store
	syms  map[string]map[string]SymbolRef // ref -> qid -> sym
	files map[string][]File
	man   map[string]string
}

func (w whyStore) Repos() []string { return []string{"shop"} }
func (w whyStore) Refs(string) []string {
	return []string{"v1", "v2"}
}
func (w whyStore) SymbolAt(repo, sym, ref string) (SymbolRef, bool) {
	s, ok := w.syms[ref][sym]
	return s, ok
}
func (w whyStore) SymbolsAt(repo, ref string) map[string]SymbolRef { return w.syms[ref] }
func (w whyStore) Files(repo, ref string) []File                   { return w.files[ref] }
func (w whyStore) Manifest(repo, ref string) (content.Manifest, bool) {
	return content.Manifest{Ref: ref, Commit: w.man[ref]}, true
}

func TestWhyFollowsAMoveAndBudgets(t *testing.T) {
	// v1: Cart.add lives in shop/cart.py; v2: moved to shop/basket.py.
	s := whyStore{
		syms: map[string]map[string]SymbolRef{
			"v1": {"shop.Cart.add": {Present: true, SignatureHash: "s1"}},
			"v2": {"shop.Cart.add": {Present: true, SignatureHash: "s2"}},
		},
		files: map[string][]File{
			"v1": {{Path: "shop/cart.py", Symbols: []SymbolSpan{{Name: "Cart", StartLine: 1, EndLine: 9}, {Name: "add", StartLine: 2, EndLine: 4}}}},
			"v2": {
				{Path: "shop/aaa.py", Symbols: []SymbolSpan{{Name: "Other", StartLine: 1, EndLine: 5}, {Name: "add", StartLine: 2, EndLine: 3}}},
				{Path: "shop/basket.py", Symbols: []SymbolSpan{{Name: "Cart", StartLine: 1, EndLine: 9}, {Name: "add", StartLine: 5, EndLine: 8}}},
			},
		},
		man: map[string]string{"v1": "c1", "v2": "c2"},
	}
	hp := &fakeHistory{
		lines: map[string][]Change{
			"c2:shop/basket.py": {
				{Commit: "m2", Time: 300, Subject: "feat: qty (#40)", Kind: ChangeSignature},
				{Commit: "m1", Time: 200, Subject: "refactor: split cart", Kind: ChangeIntroduced},
			},
			"c1:shop/cart.py": {
				{Commit: "o1", Time: 100, Subject: "feat: cart SHOP-7", Body: "First cut.", Kind: ChangeIntroduced},
			},
		},
		contains: map[string][]string{"m2": {"v2"}, "m1": {"v2"}, "o1": {"v1", "v2"}},
		times:    map[string]int64{"c1": 150, "c2": 400},
	}
	r := Why(s, hp, "shop", "v2", "Cart.add", 0)
	if r.Path != "shop/basket.py" || r.StartLine != 5 {
		t.Fatalf("located %s:%d, want the add inside Cart (shop/basket.py:5)", r.Path, r.StartLine)
	}
	if len(r.Commits) != 3 {
		t.Fatalf("want 3 commits (move followed), got %+v (calls %v)", r.Commits, hp.calls)
	}
	got := []string{}
	for _, c := range r.Commits {
		got = append(got, c.Commit+"/"+string(c.Kind)+"/"+c.FirstIn)
	}
	if strings.Join(got, " ") != "m2/signature/v2 m1/moved/v2 o1/introduced/v1" {
		t.Errorf("commits: %v", got)
	}
	if len(r.Commits[0].PRs) != 1 || r.Commits[0].PRs[0] != "40" || r.Commits[2].Tickets[0] != "SHOP-7" {
		t.Errorf("linkage: %+v / %+v", r.Commits[0], r.Commits[2])
	}
	if len(r.Compat) != 2 || r.Compat[1].Verdict != string(ShapeChanged) {
		t.Errorf("compat history: %+v", r.Compat)
	}
	if !strings.Contains(r.Note, "moved: at v1") {
		t.Errorf("move not explained: %s", r.Note)
	}

	small := Why(s, hp, "shop", "v2", "Cart.add", 60)
	if !small.Truncated || len(small.Commits) == 0 || len(small.Commits) >= 3 || small.Next == "" {
		t.Errorf("budget not applied: %d commits, truncated=%v next=%q", len(small.Commits), small.Truncated, small.Next)
	}
}

func TestWhyWithoutHistoryStillAnswersCompat(t *testing.T) {
	s := whyStore{
		syms:  map[string]map[string]SymbolRef{"v1": {"shop.f": {Present: true}}, "v2": {"shop.f": {Present: true}}},
		files: map[string][]File{"v2": {{Path: "shop/f.py", Symbols: []SymbolSpan{{Name: "f", StartLine: 1, EndLine: 2}}}}},
		man:   map[string]string{},
	}
	r := Why(s, nil, "shop", "v2", "f", 0)
	if len(r.Commits) != 0 || len(r.Compat) != 2 || !strings.Contains(r.Note, "no git history") {
		t.Errorf("got %+v", r)
	}
	if miss := Why(s, nil, "shop", "v2", "nope", 0); !strings.Contains(miss.Note, "not found") {
		t.Errorf("miss: %s", miss.Note)
	}
}
