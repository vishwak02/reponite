package fleet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRegistryRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "fleet.json")

	// A missing registry is an empty one, not an error ("no fleet yet").
	reg, err := Load(path)
	if err != nil || len(reg.Repos) != 0 {
		t.Fatalf("missing registry must load empty: %+v %v", reg, err)
	}

	reg = reg.Add(NewEntry("api", "/srv/api", "github.com/acme/api"))
	reg = reg.Add(NewEntry("web", "/srv/web", ""))
	if err := Save(path, reg); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Repos) != 2 || got.Repos[0].Repo != "api" || got.Repos[1].Repo != "web" {
		t.Fatalf("round-trip must preserve entries, sorted: %+v", got.Repos)
	}
	if got.Repos[0].Module != "github.com/acme/api" || got.Repos[0].IndexedAt == "" {
		t.Fatalf("entry fields lost: %+v", got.Repos[0])
	}
}

// Re-registering a directory updates in place (a reindex must not duplicate),
// and a pass that detects no module keeps the previously known identity.
func TestRegistryAddUpsertsByDir(t *testing.T) {
	reg := Registry{}.Add(NewEntry("api", "/srv/api", "github.com/acme/api"))
	reg = reg.Add(NewEntry("api", "/srv/api/", "")) // trailing slash = same dir
	if len(reg.Repos) != 1 {
		t.Fatalf("re-registering the same dir must upsert, got %+v", reg.Repos)
	}
	if reg.Repos[0].Module != "github.com/acme/api" {
		t.Fatalf("a module-less reindex must not erase known identity: %+v", reg.Repos[0])
	}
}

func TestRegistryRemove(t *testing.T) {
	reg := Registry{}.Add(NewEntry("api", "/srv/api", "")).Add(NewEntry("web", "/srv/web", ""))
	reg, ok := reg.Remove("/srv/api")
	if !ok || len(reg.Repos) != 1 || reg.Repos[0].Repo != "web" {
		t.Fatalf("remove by dir: ok=%v repos=%+v", ok, reg.Repos)
	}
	reg, ok = reg.Remove("web") // by repo name
	if !ok || len(reg.Repos) != 0 {
		t.Fatalf("remove by repo name: ok=%v repos=%+v", ok, reg.Repos)
	}
	if _, ok := reg.Remove("nope"); ok {
		t.Fatal("removing an unknown entry must report false")
	}
}

// The honesty rule: a registered repo whose index has vanished is reported as
// stale, never silently dropped from the mount.
func TestRegistryLiveSeparatesStale(t *testing.T) {
	base := t.TempDir()
	liveDir := filepath.Join(base, "alive")
	if err := os.MkdirAll(filepath.Join(liveDir, ".reponite"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(liveDir, ".reponite", "index.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := Registry{}.
		Add(NewEntry("alive", liveDir, "")).
		Add(NewEntry("ghost", filepath.Join(base, "ghost"), ""))

	live, stale := reg.Live(".reponite/index.db")
	if len(live) != 1 || live[0].Repo != "alive" {
		t.Fatalf("live = %+v", live)
	}
	if len(stale) != 1 || stale[0].Repo != "ghost" {
		t.Fatalf("a vanished index must be reported stale, got %+v", stale)
	}
	if d := Dirs(live); len(d) != 1 || d[0] != liveDir {
		t.Fatalf("Dirs(live) = %v", d)
	}
}

// A corrupt registry errors instead of silently starting empty — a user whose
// fleet file got mangled must not be told they have no repos.
func TestRegistryCorruptErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a corrupt registry must surface an error, not read as empty")
	}
}

func TestDefaultPathHonorsEnv(t *testing.T) {
	t.Setenv(EnvPath, "/custom/fleet.json")
	if got := DefaultPath(); got != "/custom/fleet.json" {
		t.Fatalf("REPONITE_FLEET override ignored, got %q", got)
	}
	t.Setenv(EnvPath, "")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if got := DefaultPath(); got != filepath.Join("/xdg", "reponite", "fleet.json") {
		t.Fatalf("XDG path = %q", got)
	}
}

// Save must be atomic: an existing fleet is never left truncated.
func TestSaveAtomicLeavesNoTemp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.json")
	reg := Registry{}.Add(NewEntry("api", "/srv/api", ""))
	if err := Save(path, reg); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temp file must be renamed away, not left behind")
	}
}
