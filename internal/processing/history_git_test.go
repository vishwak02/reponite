package processing

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// gitRepo builds a throwaway repository; each step writes files and commits.
type gitRepo struct {
	t   *testing.T
	dir string
	n   int
}

func newGitRepo(t *testing.T) *gitRepo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	g := &gitRepo{t: t, dir: t.TempDir()}
	g.run("init", "-q", "-b", "main")
	return g
}

func (g *gitRepo) run(args ...string) string {
	g.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", g.dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=Ada", "GIT_AUTHOR_EMAIL=ada@example.com",
		"GIT_COMMITTER_NAME=Ada", "GIT_COMMITTER_EMAIL=ada@example.com", "GIT_CONFIG_NOSYSTEM=1", "HOME="+g.dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		g.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// commit writes files (path -> content; "" deletes) and commits with msg at
// a fixed, increasing date so ordering is deterministic.
func (g *gitRepo) commit(msg string, files map[string]string) string {
	g.t.Helper()
	for p, c := range files {
		full := filepath.Join(g.dir, p)
		if c == "" {
			os.Remove(full)
			continue
		}
		os.MkdirAll(filepath.Dir(full), 0o755)
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			g.t.Fatal(err)
		}
	}
	g.run("add", "-A")
	g.n++
	date := "2026-01-0" + string(rune('0'+g.n)) + "T10:00:00Z"
	os.Setenv("GIT_AUTHOR_DATE", date)
	os.Setenv("GIT_COMMITTER_DATE", date)
	defer os.Unsetenv("GIT_AUTHOR_DATE")
	defer os.Unsetenv("GIT_COMMITTER_DATE")
	g.run("commit", "-q", "-m", msg)
	return g.run("rev-parse", "HEAD")
}

func TestGitHistoryWhyEndToEnd(t *testing.T) {
	g := newGitRepo(t)
	v1 := "import os\n\n\ndef add_item(cart, sku):\n    cart.append(sku)\n    return cart\n"
	c1 := g.commit("feat: cart (#12)", map[string]string{"shop/cart.py": v1})
	g.run("tag", "v1")
	v2 := "import os\n\n\ndef add_item(cart, sku):\n    if sku not in cart:\n        cart.append(sku)\n    return cart\n"
	c2 := g.commit("fix: dedupe skus\n\nDuplicate lines confused the packer.\n\nSigned-off-by: Ada <ada@example.com>", map[string]string{"shop/cart.py": v2})
	v3 := "import os\n\n\ndef add_item(cart, sku, qty=1):\n    if sku not in cart:\n        cart.append(sku)\n    return cart\n"
	c3 := g.commit("feat: quantities SHOP-7", map[string]string{"shop/cart.py": v3})
	g.commit("docs: readme", map[string]string{"README.md": "shop\n"})
	tip := g.run("rev-parse", "HEAD")
	g.run("tag", "v2")

	m := storage.NewMem()
	put := func(ref, commit, src, sig string, start, end int) {
		m.Put("shop", ref, "shop.add_item", storage.SymbolRecord{Lang: "python", SignatureHash: content.Hash(sig), BehaviorHash: content.Hash(src)})
		m.PutFile("shop", ref, query.File{Path: "shop/cart.py", Content: src, Symbols: []query.SymbolSpan{{Name: "add_item", StartLine: start, EndLine: end}}})
		m.PutManifest("shop", ref, content.Manifest{Ref: ref, Commit: commit})
	}
	put("v1", c1, v1, "sig1", 4, 6)
	put("v2", tip, v3, "sig2", 4, 7)

	hp := NewGitHistory(map[string]string{"shop": g.dir})
	r := query.Why(m, hp, "shop", "v2", "add_item", 0)
	if len(r.Commits) != 3 {
		t.Fatalf("want the 3 commits touching add_item, got %d: %+v (%s)", len(r.Commits), r.Commits, r.Note)
	}
	want := []struct {
		sha, kind, first string
	}{{c3, "signature", "v2"}, {c2, "body", "v2"}, {c1, "introduced", "v1"}}
	for i, w := range want {
		c := r.Commits[i]
		if c.Commit != w.sha || string(c.Kind) != w.kind || c.FirstIn != w.first {
			t.Errorf("commit %d = %s/%s/%s, want %s/%s/%s", i, c.Commit[:7], c.Kind, c.FirstIn, w.sha[:7], w.kind, w.first)
		}
	}
	if r.Commits[0].Tickets[0] != "SHOP-7" || r.Commits[2].PRs[0] != "12" || r.Commits[1].Author != "Ada" {
		t.Errorf("linkage/author: %+v", r.Commits)
	}
	if r.Commits[1].Body != "Duplicate lines confused the packer." {
		t.Errorf("body excerpt %q", r.Commits[1].Body)
	}
	if r.Commits[2].Date != "2026-01-01" {
		t.Errorf("date %q", r.Commits[2].Date)
	}
	if len(r.Compat) != 2 || r.Compat[0].Ref != "v1" || r.Compat[1].Verdict != "shape_changed" {
		t.Errorf("compat history %+v", r.Compat)
	}

	// The object store is read only: no checkout moved, nothing staged.
	if st := g.run("status", "--porcelain"); st != "" {
		t.Errorf("working tree touched: %q", st)
	}
}

func TestGitHistoryPickaxeAndTimes(t *testing.T) {
	g := newGitRepo(t)
	c1 := g.commit("add", map[string]string{"a.go": "package a\n\nfunc Charge() {}\n"})
	c2 := g.commit("rename", map[string]string{"a.go": "package a\n\nfunc Bill() {}\n"})
	hp := NewGitHistory(map[string]string{"r": g.dir})
	cs, err := hp.Pickaxe("r", c2, "Charge", []string{"a.go"}, 10)
	if err != nil || len(cs) != 2 {
		t.Fatalf("pickaxe: %v %+v", err, cs)
	}
	times, err := hp.CommitTimes("r", []string{c1, c2, "0000000000000000000000000000000000000000"})
	if err != nil || times[c1] == 0 || times[c2] <= times[c1] {
		t.Errorf("times: %v %v", err, times)
	}
	in, err := hp.Contains("r", []string{c1, c2}, map[string]string{"old": c1, "new": c2})
	if err != nil || len(in[c1]) != 2 || len(in[c2]) != 1 || in[c2][0] != "new" {
		t.Errorf("contains: %v %v", err, in)
	}
	if _, err := hp.LineHistory("r", query.LineRange{Commit: c2, Path: "missing.go", Start: 1, End: 2}, 5); err == nil {
		t.Error("a missing path must error (Why falls back to pickaxe)")
	}
	if NewGitHistory(nil).Has("r") {
		t.Error("unknown repo must have no history")
	}
}
