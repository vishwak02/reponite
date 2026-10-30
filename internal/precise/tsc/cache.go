package tsc

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/precise"
)

// validator answers, for one ref, what a project's manifest recorded: a
// repo file's content id (from the ref's files), an outside file's identity
// (size, times, inode), whether a path exists, what a directory lists.
// Paths are keyed as the worker keys them: "R:<rel>" under the root, else
// absolute; a repo path inside a node_modules directory is read from the
// repo's directory on disk.
type validator struct {
	in    Input
	dirs  map[string]bool // the repo's directories ("" the root), from its files
	stats map[string]string
	ids   map[string]string // repo files outside Files and Extra, by content
}

func newValidator(in Input) *validator {
	v := &validator{in: in, dirs: map[string]bool{"": true}, stats: map[string]string{}, ids: map[string]string{}}
	for _, m := range []map[string]string{in.Files, in.Extra} {
		for p := range m {
			for d := path.Dir(p); d != "." && !v.dirs[d]; d = path.Dir(d) {
				v.dirs[d] = true
			}
		}
	}
	return v
}

// disk is where a key's file is on disk, "" for a repo file read from the
// ref (one not inside node_modules).
func (v *validator) disk(key string) string {
	rel, ok := strings.CutPrefix(key, "R:")
	if !ok {
		return key
	}
	for _, seg := range strings.Split(rel, "/") {
		if seg == "node_modules" {
			if v.in.Dir == "" {
				return string(os.PathSeparator) + "\x00" // nowhere
			}
			return filepath.Join(v.in.Dir, filepath.FromSlash(rel))
		}
	}
	return ""
}

func (v *validator) stat(p string) os.FileInfo {
	fi, err := os.Stat(p)
	if err != nil {
		return nil
	}
	return fi
}

// id is a read file's identity now, "" when it is gone.
func (v *validator) id(key string) string {
	d := v.disk(key)
	if d == "" {
		rel := key[2:]
		if id, ok := v.in.Files[rel]; ok {
			return id
		}
		if id, ok := v.in.Extra[rel]; ok {
			return id
		}
		// a repo file of another kind the compiler read (a JSON module):
		// its content, read once per run
		if id, ok := v.ids[rel]; ok {
			return id
		}
		id := ""
		if v.in.Read != nil {
			if b, err := v.in.Read(rel); err == nil {
				id = precise.BlobID(b)
			}
		}
		v.ids[rel] = id
		return id
	}
	if s, ok := v.stats[d]; ok {
		return s
	}
	s := ""
	if fi := v.stat(d); fi != nil && !fi.IsDir() {
		s = precise.StatID(fi)
	}
	v.stats[d] = s
	return s
}

// probe is whether "f:<key>" (a file) or "d:<key>" (a directory) exists now.
func (v *validator) probe(k string) bool {
	if len(k) < 2 {
		return false
	}
	isDir, key := k[0] == 'd', k[2:]
	d := v.disk(key)
	if d == "" {
		rel := key[2:]
		if isDir {
			return v.dirs[rel]
		}
		_, a := v.in.Files[rel]
		_, b := v.in.Extra[rel]
		return a || b
	}
	fi := v.stat(d)
	return fi != nil && fi.IsDir() == isDir
}

// list is a directory's subdirectories now, newline-joined and sorted.
func (v *validator) list(key string) string {
	d := v.disk(key)
	var out []string
	if d == "" {
		rel := key[2:]
		pre := ""
		if rel != "" {
			pre = rel + "/"
		}
		for x := range v.dirs {
			if x != "" && strings.HasPrefix(x, pre) && !strings.Contains(x[len(pre):], "/") {
				out = append(out, x[len(pre):])
			}
		}
	} else if es, err := os.ReadDir(d); err == nil {
		for _, e := range es {
			if e.IsDir() {
				out = append(out, e.Name())
			} else if e.Type()&os.ModeSymlink != 0 {
				if fi, err := os.Stat(filepath.Join(d, e.Name())); err == nil && fi.IsDir() {
					out = append(out, e.Name())
				}
			}
		}
	}
	sort.Strings(out)
	return strings.Join(out, "\n")
}

// valid reports whether everything a manifest recorded still holds.
func (v *validator) valid(m manifest) bool {
	for k, id := range m.Reads {
		if v.id(k) != id {
			return false
		}
	}
	for k, there := range m.Probes {
		if v.probe(k) != there {
			return false
		}
	}
	for k, l := range m.Lists {
		if v.list(k) != l {
			return false
		}
	}
	return true
}

// record makes the manifest of a worker's reply; false when some file it
// read cannot be identified (then the result is not cached).
func (v *validator) record(rep *reply) (manifest, bool) {
	m := manifest{Reads: map[string]string{}, Probes: rep.Probes, Lists: rep.Lists}
	if m.Probes == nil {
		m.Probes = map[string]bool{}
	}
	if m.Lists == nil {
		m.Lists = map[string]string{}
	}
	known := true
	for _, k := range rep.Reads {
		id := v.id(k)
		if id == "" {
			known = false
		}
		m.Reads[k] = id
	}
	return m, known
}

// digest is the key of a result: the project's key and every part of its
// manifest.
func (m manifest) digest(key string) string {
	parts := []string{"result", key}
	for _, k := range sortedKeys(m.Reads) {
		parts = append(parts, k, m.Reads[k])
	}
	probes := make([]string, 0, len(m.Probes))
	for k := range m.Probes {
		probes = append(probes, k)
	}
	sort.Strings(probes)
	for _, k := range probes {
		b := "0"
		if m.Probes[k] {
			b = "1"
		}
		parts = append(parts, k, b)
	}
	for _, k := range sortedKeys(m.Lists) {
		parts = append(parts, k, m.Lists[k])
	}
	return precise.Key(parts...)
}

func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
