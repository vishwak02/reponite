package cxx

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/vishwak02/reponite/internal/precise"
)

// manifest is a unit's cache entry: everything its result depends on beyond
// the key (the unit's own source, its compile flags, the compiler). The
// result holds only while every part is unchanged.
type manifest struct {
	// Includes is every repo file the unit read (path -> content id), and
	// every submodule file (libMark/path -> content id).
	Includes map[string]string
	// External is every other file it read (absolute path -> size, times
	// and inode): system, ROS and generated headers.
	External map[string]string
	// Probe is the key of the repo and submodule files named like each file
	// it read or did not find: a new header of such a name may be found first
	// (or at all) next time.
	Probe  string
	Names  []string // the names Probe covers
	Missed []string // of them, the includes not found
	// Roots is the key of the include directories each file it read is
	// under (a header found through a directory that stops being one is not
	// found); Dirs the order of those holding a name with several files.
	Roots string
	Dirs  string
	// Probes are the names it looked for outside the repo's walked files —
	// includes not found, __has_include, and each outside header (which an
	// earlier include directory could come to hold) — and Looked the key of
	// where each could appear and whether it is there.
	Probes []Probe
	Looked string
	Result string
}

// manifests are the cache entries of one unit key, the most recent first:
// refs that differ only in a header the unit reads keep one each.
type manifests struct {
	List []manifest
}

const maxManifests = 8

// validator checks manifests against one ref's files.
type validator struct {
	in      Input
	cc      *compileCtx
	byBase  map[string][]string // file name -> repo paths and libMark/paths of that name
	ids     map[string]string   // repo files outside in.Files, by content
	stats   map[string]string   // files outside the repo, by size, times and inode
	there   map[string]bool     // outside paths that exist
	absDirs map[string][]string // unit -> its include directories outside the repo, in order
	hasIncl map[string][]string // repo file -> what its __has_include names
}

func newValidator(in Input, cc *compileCtx) *validator {
	v := &validator{in: in, cc: cc, byBase: map[string][]string{}, ids: map[string]string{}, stats: map[string]string{},
		there: map[string]bool{}, absDirs: map[string][]string{}, hasIncl: map[string][]string{}}
	for _, m := range []map[string]string{in.Files, in.Extra} {
		for p := range m {
			v.byBase[path.Base(p)] = append(v.byBase[path.Base(p)], p)
		}
	}
	for p := range in.Libs {
		v.byBase[path.Base(p)] = append(v.byBase[path.Base(p)], libMark+"/"+p)
	}
	for _, ps := range v.byBase {
		sort.Strings(ps)
	}
	return v
}

// id is a repo or submodule file's content id, "" when it is not there.
func (v *validator) id(p string) string {
	if rel, ok := strings.CutPrefix(p, libMark+"/"); ok {
		return v.in.Libs[rel]
	}
	if id, ok := v.in.Files[p]; ok {
		return id
	}
	if id, ok := v.in.Extra[p]; ok {
		return id
	}
	// a file the walk does not hold (an excluded directory's header, an odd
	// extension): its content, read once per run
	if id, ok := v.ids[p]; ok {
		return id
	}
	id := ""
	if v.in.Read != nil {
		if b, err := v.in.Read(p); err == nil {
			id = blobID(b)
		}
	}
	v.ids[p] = id
	return id
}

// stat is an outside file's identity — size, modification and change times,
// inode — "" when it is gone. A copy that keeps the modification time (cp
// -p, tar, docker cp) still changes the change time.
func (v *validator) stat(p string) string {
	if s, ok := v.stats[p]; ok {
		return s
	}
	s := ""
	if fi, err := os.Stat(p); err == nil {
		s = statID(fi)
	}
	v.stats[p] = s
	return s
}

// exists reports whether a candidate path (rootMark/, libMark/ or absolute)
// names a file.
func (v *validator) exists(c string) bool {
	switch {
	case strings.HasPrefix(c, rootMark+"/"):
		return v.id(c[len(rootMark)+1:]) != ""
	case strings.HasPrefix(c, libMark+"/"):
		_, ok := v.in.Libs[c[len(libMark)+1:]]
		return ok
	case strings.HasPrefix(c, "/"):
		there, ok := v.there[c]
		if !ok {
			fi, err := os.Stat(c)
			there = err == nil && !fi.IsDir()
			v.there[c] = there
		}
		return there
	}
	return false
}

// outsideDirs is a unit's include directories outside the repo, in search
// order (the repo's and its submodules' are covered by Probe).
func (v *validator) outsideDirs(tu string) []string {
	if d, ok := v.absDirs[tu]; ok {
		return d
	}
	var out []string
	args := v.cc.full(tu)
	for i := 0; i < len(args); i++ {
		val := ""
		if includeDirFlags[args[i]] && i+1 < len(args) {
			val = args[i+1]
			i++
		} else if f := joinedPath(args[i]); f != "" && includeDirFlags[f] {
			val = args[i][len(f):]
		}
		if strings.HasPrefix(val, "/") {
			out = append(out, path.Clean(val))
		}
	}
	v.absDirs[tu] = out
	return out
}

// looked is the key of where each probe could appear and whether it does.
func (v *validator) looked(tu string, probes []Probe) string {
	if len(probes) == 0 {
		return ""
	}
	dirs := v.outsideDirs(tu)
	parts := []string{"looked"}
	for _, p := range probes {
		upto := len(dirs)
		if p.Before != "" {
			upto = -1
			for i, d := range dirs {
				if d == p.Before {
					upto = i
					break
				}
			}
			if upto < 0 {
				parts = append(parts, p.Spelling, p.Before, "gone")
				continue
			}
		}
		var bits []byte
		if p.From != "" {
			bits = append(bits, bit(v.exists(path.Join(p.From, p.Spelling))))
		}
		for _, d := range dirs[:upto] {
			bits = append(bits, bit(v.exists(path.Join(d, p.Spelling))))
		}
		parts = append(parts, p.Spelling, p.From, p.Before, string(bits))
	}
	return precise.Key(parts...)
}

func bit(b bool) byte {
	if b {
		return '1'
	}
	return '0'
}

func (v *validator) probe(names []string) string {
	parts := []string{"probe"}
	for _, n := range names {
		parts = append(parts, n)
		parts = append(parts, v.byBase[n]...)
		parts = append(parts, "")
	}
	return precise.Key(parts...)
}

// roots is the key of the include directories each file is under.
func (v *validator) roots(files []string) string {
	if v.cc.roots == nil {
		return ""
	}
	parts := []string{"roots"}
	for _, p := range files {
		parts = append(parts, p)
		parts = append(parts, v.cc.roots(p)...)
		parts = append(parts, "")
	}
	return precise.Key(parts...)
}

// dirs is the key of the order of tu's include directories that hold any of
// paths (repo paths, libMark/paths), "" when the order is the build's own.
func (v *validator) dirs(tu string, paths []string) string {
	if len(paths) == 0 || v.cc.dirs == nil {
		return ""
	}
	above := map[string]bool{} // every directory above one of paths
	for _, p := range paths {
		for d := path.Dir(markRepo(p)); d != "." && d != "/" && !above[d]; d = path.Dir(d) {
			above[d] = true
		}
	}
	held := []string{"dirs"}
	for _, d := range v.cc.dirs(tu) {
		if above[d] {
			held = append(held, d)
		}
	}
	return precise.Key(held...)
}

// markRepo is a path as include directories name it: rootMark/ before a
// repo path (a submodule's has libMark/ already).
func markRepo(p string) string {
	if strings.HasPrefix(p, libMark+"/") {
		return p
	}
	return rootMark + "/" + p
}

// ambiguous is every file named like another the unit read or missed.
func (v *validator) ambiguous(names []string, missing map[string]bool) []string {
	var out []string
	for _, n := range names {
		if c := v.byBase[n]; len(c) > 1 || missing[n] && len(c) > 0 {
			out = append(out, c...)
		}
	}
	return out
}

func (v *validator) valid(m manifest, tu string) bool {
	if m.Result == "" {
		return false
	}
	for p, id := range m.Includes {
		if v.id(p) != id {
			return false
		}
	}
	for p, s := range m.External {
		if v.stat(p) != s {
			return false
		}
	}
	if v.probe(m.Names) != m.Probe {
		return false
	}
	if m.Roots != "" && v.roots(sortedKeys(m.Includes)) != m.Roots {
		return false
	}
	if m.Dirs != "" {
		missed := map[string]bool{}
		for _, n := range m.Missed {
			missed[n] = true
		}
		if v.dirs(tu, v.ambiguous(m.Names, missed)) != m.Dirs {
			return false
		}
	}
	return v.looked(tu, m.Probes) == m.Looked
}

var reHasInclude = regexp.MustCompile(`__has_include(?:_next)?\s*\(\s*[<"]([^>"]+)[>"]`)

// hasIncludes is what a repo file's __has_include tests name.
func (v *validator) hasIncludes(rel string) []string {
	if out, ok := v.hasIncl[rel]; ok {
		return out
	}
	var out []string
	if v.in.Read != nil {
		if b, err := v.in.Read(rel); err == nil && strings.Contains(string(b), "__has_include") {
			for _, m := range reHasInclude.FindAllStringSubmatch(string(b), -1) {
				out = append(out, m[1])
			}
		}
	}
	v.hasIncl[rel] = out
	return out
}

// marked is an absolute path the compiler reported as include directories
// name it: under the sources (root) rootMark/, under the submodules (libs)
// libMark/, else as it is.
func marked(p, root, libs string) string {
	p = filepath.ToSlash(p)
	switch {
	case p == root:
		return rootMark
	case strings.HasPrefix(p, root+"/"):
		return rootMark + "/" + p[len(root)+1:]
	case libs != "" && strings.HasPrefix(p, libs+"/"):
		return libMark + "/" + p[len(libs)+1:]
	}
	return p
}

// record builds the manifest of a unit just compiled (root its sources'
// directory, libs its submodules'); ok false when some file it read cannot
// be identified, or an include it did not find is in the tree (a copy that
// left it out): the result is then not cached.
func (v *validator) record(r Result, tu, root, libs string) (manifest, bool) {
	m := manifest{Includes: map[string]string{}, External: map[string]string{}}
	names := map[string]bool{}
	for _, inc := range r.Includes {
		id := v.id(inc)
		if id == "" {
			return m, false
		}
		m.Includes[inc] = id
		names[path.Base(inc)] = true
		for _, s := range v.hasIncludes(inc) {
			m.Probes = append(m.Probes, Probe{Spelling: s, From: path.Dir(rootMark + "/" + inc)})
			names[path.Base(s)] = true
		}
	}
	dirs := v.outsideDirs(tu)
	for _, o := range r.Others {
		names[filepath.Base(o)] = true
		if rel, ok := strings.CutPrefix(o, libs+"/"); ok && libs != "" && len(v.in.Libs) > 0 {
			rel = filepath.ToSlash(rel)
			id := v.in.Libs[rel]
			if id == "" {
				return m, false
			}
			m.Includes[libMark+"/"+rel] = id
			continue
		}
		if strings.HasPrefix(o, root+"/") {
			continue // the materialized tree's own (reported as I)
		}
		s := v.stat(o)
		if s == "" {
			return m, false
		}
		m.External[o] = s
		// found in an include directory: none before it may come to hold it
		for _, d := range dirs {
			if strings.HasPrefix(o, d+"/") {
				m.Probes = append(m.Probes, Probe{Spelling: o[len(d)+1:], Before: d})
				break
			}
		}
	}
	missing := map[string]bool{}
	for _, x := range r.Missing {
		missing[path.Base(x.Spelling)] = true
		names[path.Base(x.Spelling)] = true
		p := Probe{Spelling: x.Spelling}
		if x.From != "" {
			p.From = path.Dir(marked(x.From, root, libs))
			if strings.HasPrefix(p.From, rootMark) && v.exists(path.Join(p.From, x.Spelling)) {
				return m, false // the tree holds it, the compiler's copy did not
			}
		}
		m.Probes = append(m.Probes, p)
	}
	for n := range names {
		m.Names = append(m.Names, n)
	}
	sort.Strings(m.Names)
	for n := range missing {
		m.Missed = append(m.Missed, n)
	}
	sort.Strings(m.Missed)
	m.Probe = v.probe(m.Names)
	if v.cc.method(tu) == MethodSynth {
		// a build's own command holds its include directories (in the key)
		m.Roots = v.roots(sortedKeys(m.Includes))
		m.Dirs = v.dirs(tu, v.ambiguous(m.Names, missing))
	}
	m.Looked = v.looked(tu, m.Probes)
	return m, true
}

// blobID is git's id of a file's content, as Input.Files holds.
func blobID(content []byte) string {
	h := sha1.New()
	fmt.Fprintf(h, "blob %d\x00", len(content))
	h.Write(content)
	return hex.EncodeToString(h.Sum(nil))
}

var reNotFound = regexp.MustCompile(`^'([^']+)' file not found`)

// definedNames adds every name src seems to define — an identifier, its
// parenthesized parameters, then a body ({, or a constructor's :) — to into.
// A unit not read this run may define a function another unit's call names.
func definedNames(src string, into map[string]bool) {
	n := len(src)
	ident := func(c byte) bool {
		return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
	}
	space := func(p int) int {
		for p < n && (src[p] == ' ' || src[p] == '\t' || src[p] == '\n' || src[p] == '\r') {
			p++
		}
		return p
	}
	for i := 0; i < n; i++ {
		if !ident(src[i]) || i > 0 && ident(src[i-1]) || src[i] >= '0' && src[i] <= '9' {
			continue
		}
		j := i
		for j < n && ident(src[j]) {
			j++
		}
		name := src[i:j]
		k := space(j)
		if k >= n || src[k] != '(' {
			i = j - 1
			continue
		}
		depth, m := 0, k
		for ; m < n; m++ {
			if src[m] == '(' {
				depth++
			} else if src[m] == ')' {
				if depth--; depth == 0 {
					break
				}
			}
		}
		if m >= n {
			return
		}
		p := m + 1
		for {
			p = space(p)
			if p < n && ident(src[p]) {
				q := p
				for q < n && ident(src[q]) {
					q++
				}
				if w := src[p:q]; w == "const" || w == "noexcept" || w == "override" || w == "final" || w == "volatile" || w == "mutable" {
					p = q
					continue
				}
			} else if p < n && src[p] == '&' {
				p++
				continue
			} else if p+1 < n && src[p] == '-' && src[p+1] == '>' {
				for p < n && src[p] != '{' && src[p] != ';' {
					p++
				}
			}
			break
		}
		if p < n && (src[p] == '{' || src[p] == ':' && !(p+1 < n && src[p+1] == ':')) {
			// Cls::f( is Cls's f; a bare f( a free function or one inside a
			// class body
			if b := i; b >= 3 && src[b-1] == ':' && src[b-2] == ':' {
				q := b - 2
				for q > 0 && ident(src[q-1]) {
					q--
				}
				if q < b-2 {
					into[src[q:b-2]+"::"+name] = true
					i = j - 1
					continue
				}
			}
			into[name] = true
		}
		i = j - 1
	}
	macroArgs(src, into)
}

// macroArgs adds the names a file-scope macro call is given (DEF(helper),
// whose expansion may define helper) to into.
func macroArgs(src string, into map[string]bool) {
	depth := 0
	for i := 0; i < len(src); i++ {
		switch c := src[i]; {
		case c == '{':
			depth++
		case c == '}':
			if depth > 0 {
				depth--
			}
		case depth == 0 && c >= 'A' && c <= 'Z' && (i == 0 || !isIdent(src[i-1])):
			j := i
			upper := true
			for j < len(src) && isIdent(src[j]) {
				if src[j] >= 'a' && src[j] <= 'z' {
					upper = false
				}
				j++
			}
			if upper && j < len(src) && src[j] == '(' {
				if end := strings.IndexByte(src[j:], ')'); end > 0 {
					for _, id := range reIdent.FindAllString(src[j+1:j+end], -1) {
						into[id] = true
					}
				}
			}
			i = j - 1
		}
	}
}

func isIdent(c byte) bool {
	return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// usrClass is the class a USR's function belongs to ("" a free function):
// c:@N@nav@S@Map@F@floor# -> Map.
func usrClass(usr string) string {
	f := strings.LastIndex(usr, "@F@")
	if t := strings.LastIndex(usr, "@FT@"); t > f {
		f = t
	}
	if f < 0 {
		return ""
	}
	pre := usr[:f]
	best := -1
	for _, k := range []string{"@S@", "@C@", "@U@", "@ST>"} {
		if i := strings.LastIndex(pre, k); i > best {
			best = i + len(k)
		}
	}
	if best < 0 {
		return ""
	}
	name := pre[best:]
	if i := strings.IndexAny(name, "@>#"); i >= 0 {
		name = name[:i]
	}
	return name
}
