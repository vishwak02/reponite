// Package fleet is the persistent cross-run repo registry: the list of repos
// this machine has indexed, so `reponite serve` / `reponite mcp` can mount the
// whole fleet without being handed every directory on every invocation
// (architecture ext §8B.7's deferred "global.db").
//
// It stores METADATA only — repo name, directory, module path, last-index time
// — never symbols or content. That is deliberately a small JSON file rather
// than a database: there is nothing to query, and JSON keeps the registry pure
// stdlib, unit-tested in the core, and hand-inspectable/editable (ADR-018).
// Per-repo index.db files remain the only content store.
//
// Honesty rules (invariant 5) are the interesting part: a registered repo whose
// index has since been deleted or moved is REPORTED as stale, never silently
// skipped — an agent must not read "3 repos mounted" when the fourth vanished.
package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Entry is one registered repo. Dir is the canonical key: one directory is one
// repo, and re-registering the same directory updates it in place.
type Entry struct {
	Repo      string `json:"repo"`
	Dir       string `json:"dir"` // absolute
	Module    string `json:"module,omitempty"`
	IndexedAt string `json:"indexed_at,omitempty"` // RFC3339
}

// Registry is the machine's known fleet, sorted by repo for stable output.
type Registry struct {
	Repos []Entry `json:"repos"`
}

// EnvPath overrides the registry location (tests, or a per-project fleet).
const EnvPath = "REPONITE_FLEET"

// DefaultPath is $REPONITE_FLEET, else $XDG_CONFIG_HOME/reponite/fleet.json,
// else ~/.config/reponite/fleet.json. Returns "" when no home is discoverable
// (the caller then runs registry-less rather than guessing a path).
func DefaultPath() string {
	if p := os.Getenv(EnvPath); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "reponite", "fleet.json")
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, ".config", "reponite", "fleet.json")
}

// Load reads the registry at path. A missing file is an EMPTY registry, not an
// error — "no fleet registered yet" is a normal state. A corrupt file IS an
// error: silently starting empty would hide a fleet the user believes exists.
func Load(path string) (Registry, error) {
	var reg Registry
	if path == "" {
		return reg, nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return reg, nil
	}
	if err != nil {
		return reg, err
	}
	if len(data) == 0 {
		return reg, nil
	}
	if err := json.Unmarshal(data, &reg); err != nil {
		return Registry{}, err
	}
	sortEntries(reg.Repos)
	return reg, nil
}

// Save writes the registry to path, creating parent directories. The write is
// atomic (temp file + rename) so an interrupted save can never truncate an
// existing fleet — the same "a ref is real only when written last" discipline
// invariant 4 applies to manifests.
func Save(path string, reg Registry) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	sortEntries(reg.Repos)
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Add upserts an entry, keyed by directory, and returns the updated registry.
// Re-indexing a repo refreshes its module path and timestamp rather than
// duplicating it.
func (r Registry) Add(e Entry) Registry {
	for i := range r.Repos {
		if sameDir(r.Repos[i].Dir, e.Dir) {
			// Keep a previously-detected module if this pass found none, so a
			// partial reindex doesn't erase known identity.
			if e.Module == "" {
				e.Module = r.Repos[i].Module
			}
			r.Repos[i] = e
			sortEntries(r.Repos)
			return r
		}
	}
	r.Repos = append(r.Repos, e)
	sortEntries(r.Repos)
	return r
}

// Remove drops the entry matching dirOrRepo (by directory or repo name) and
// reports whether anything was removed.
func (r Registry) Remove(dirOrRepo string) (Registry, bool) {
	abs := absOr(dirOrRepo)
	out := r.Repos[:0:0]
	removed := false
	for _, e := range r.Repos {
		if sameDir(e.Dir, abs) || e.Repo == dirOrRepo {
			removed = true
			continue
		}
		out = append(out, e)
	}
	r.Repos = out
	return r, removed
}

// Live splits the registry into entries whose index database still exists and
// entries that are stale (directory or index gone). Callers mount `live` and
// must surface `stale` — a vanished repo is reported, never silently dropped.
func (r Registry) Live(indexRelPath string) (live, stale []Entry) {
	for _, e := range r.Repos {
		if _, err := os.Stat(filepath.Join(e.Dir, indexRelPath)); err == nil {
			live = append(live, e)
		} else {
			stale = append(stale, e)
		}
	}
	return live, stale
}

// Dirs is the directory list of the given entries, for mounting.
func Dirs(entries []Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Dir)
	}
	return out
}

// NewEntry builds an entry for dir (absolutized) stamped with the current time.
func NewEntry(repo, dir, module string) Entry {
	return Entry{Repo: repo, Dir: absOr(dir), Module: module, IndexedAt: time.Now().UTC().Format(time.RFC3339)}
}

func absOr(dir string) string {
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// sameDir compares directories after cleaning, so "/a/b" and "/a/b/" match.
func sameDir(a, b string) bool { return filepath.Clean(a) == filepath.Clean(b) }

func sortEntries(es []Entry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Repo != es[j].Repo {
			return es[i].Repo < es[j].Repo
		}
		return es[i].Dir < es[j].Dir
	})
}
