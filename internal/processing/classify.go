// classify.go decides, for every tracked file, what the index keeps of it —
// one decision shared by the git-tree walker and the working-tree walker, so
// the two can never disagree about a file.
//
// Every file ends in exactly one place: parsed as source, stored as a
// structured or plain content file, or skipped with a reason. Nothing is
// dropped silently: a skip is recorded (and `coverage` reports it), so a
// blind spot is visible instead of looking like an absence.
//
// Text the indexer has no handler for is still stored and searchable: its
// structure is recognized by content when the extension says nothing
// (JSON-shaped files such as ALICA plans, XML such as Tiled maps or BPMN, INI),
// and plain text otherwise. Binary content, Git LFS placeholders and bulk
// data tables are skipped with that reason.
package processing

import (
	"bytes"
	"path"
	"strings"
	"unicode/utf8"
)

// fileInput is one tracked file offered by a walker.
type fileInput struct {
	path string // repo-relative, slash-separated
	size int64
	read func() (string, error)
	// load re-reads the content for storage when a parsed file's text is not
	// kept in memory; nil keeps the text read here.
	load func() (string, error)
}

// classified is what the index keeps of a file.
type classified struct {
	pf       *ParsedFile                      // a file stored as is (content, data, launch, …)
	job      func() (ParsedFile, bool, error) // a source file to parse on the pool
	manifest []byte                           // a module manifest's bytes (§8B.2)
	skip     *SkippedFile
	lang     string // the source language, for a job
}

// noiseDotDirs are dot-directories that hold tool state, caches and
// environments, never the project's own files.
var noiseDotDirs = map[string]bool{
	".git": true, ".svn": true, ".hg": true, ".reponite": true, ".idea": true, ".vscode": true,
	".venv": true, ".tox": true, ".nox": true, ".cache": true, ".mypy_cache": true, ".pytest_cache": true,
	".ruff_cache": true, ".gradle": true, ".next": true, ".nuxt": true, ".svelte-kit": true,
	".terraform": true, ".yarn": true, ".pnpm-store": true, ".npm": true, ".eggs": true,
	".ipynb_checkpoints": true, ".history": true, ".angular": true, ".parcel-cache": true,
	".turbo": true, ".serverless": true, ".dart_tool": true, ".pub-cache": true, ".stack-work": true,
	".bundle": true, ".sass-cache": true, ".vs": true, ".fleet": true, ".DS_Store": true,
}

// inNoiseDir reports a path under a tool/cache dot-directory (or that file itself).
func inNoiseDir(p string) bool {
	for _, seg := range strings.Split(p, "/") {
		if noiseDotDirs[seg] {
			return true
		}
	}
	return false
}

// binaryExts are formats that are never text; their blobs are not read.
var binaryExts = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true, ".bmp": true,
	".tif": true, ".tiff": true, ".psd": true, ".ai": true, ".heic": true, ".avif": true,
	".pdf": true, ".zip": true, ".gz": true, ".tgz": true, ".xz": true, ".bz2": true, ".7z": true,
	".tar": true, ".rar": true, ".zst": true, ".jar": true, ".war": true, ".whl": true, ".egg": true,
	".so": true, ".a": true, ".o": true, ".dylib": true, ".dll": true, ".exe": true, ".bin": true,
	".class": true, ".pyc": true, ".wasm": true, ".trt": true, ".onnx": true, ".pt": true, ".pth": true,
	".pb": true, ".tflite": true, ".h5": true, ".ckpt": true, ".safetensors": true, ".npy": true,
	".npz": true, ".pkl": true, ".glb": true, ".stl": true, ".dae": true, ".fbx": true, ".blend": true,
	".mp4": true, ".mov": true, ".avi": true, ".mkv": true, ".webm": true, ".wav": true, ".mp3": true,
	".ogg": true, ".flac": true, ".ttf": true, ".otf": true, ".woff": true, ".woff2": true, ".eot": true,
	".xlsx": true, ".xls": true, ".docx": true, ".doc": true, ".pptx": true, ".ppt": true, ".odt": true,
	".db": true, ".sqlite": true, ".sqlite3": true, ".bag": true, ".mcap": true, ".db3": true,
	".pgm": true, ".ppm": true, ".pcd": true, ".ply": true, ".mllayx": true, ".keystore": true,
	".jks": true, ".p12": true, ".der": true, ".dmg": true, ".iso": true, ".img": true, ".deb": true,
	".rpm": true, ".apk": true, ".ipa": true,
}

// maxDataTableBytes bounds a stored CSV/TSV: a small lookup table is worth
// searching, a bulk fixture is not; a larger one is skipped with its reason.
const maxDataTableBytes = 32 << 10

// looksBinary reports content that is not text: a NUL in the first 8 KiB or
// invalid UTF-8 there.
func looksBinary(head []byte) bool {
	if len(head) > 8192 {
		head = head[:8192]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return true
	}
	// a multi-byte rune cut at the window's end is not invalid
	for len(head) > 0 && !utf8.Valid(head) {
		r, _ := utf8.DecodeLastRune(head)
		if r != utf8.RuneError || len(head) < 4 {
			break
		}
		head = head[:len(head)-1]
		if utf8.Valid(head) {
			return false
		}
	}
	return !utf8.Valid(head)
}

const lfsPointer = "version https://git-lfs.github.com/spec/v1"

// sniffLang recognizes a text file's structure from its content.
func sniffLang(content string) string {
	t := strings.TrimLeft(content, " \t\r\n\ufeff")
	switch {
	case strings.HasPrefix(t, "{") || strings.HasPrefix(t, "["):
		return "json"
	case strings.HasPrefix(t, "<?xml") || len(t) > 1 && t[0] == '<' && (t[1] >= 'a' && t[1] <= 'z' || t[1] >= 'A' && t[1] <= 'Z'):
		return "xml"
	}
	return "text"
}

// textFile stores a file the indexer has no specific handler for: a language
// without a bundled grammar gets heuristic symbols and calls, anything else
// is stored as text with the structure its content shows.
func textFile(in fileInput) classified {
	p := in.path
	ext := strings.ToLower(path.Ext(p))
	if HeuristicLang(p) != "" && in.size <= DefaultMaxSourceBytes {
		if src, err := in.read(); err == nil && !looksBinary([]byte(src)) {
			pf := heuristicFile(p, src, 1)
			return classified{pf: &pf}
		}
	}
	skip := func(why string) classified { return classified{skip: &SkippedFile{Path: p, Reason: why, Quiet: true}} }
	if binaryExts[ext] {
		return skip("binary format")
	}
	if ext == ".lock" || strings.HasSuffix(p, ".sum") {
		return skip("lockfile (machine-written dependency pins)")
	}
	limit := int64(maxContentFileBytes)
	if ext == ".csv" || ext == ".tsv" {
		limit = maxDataTableBytes
	}
	if in.size > limit {
		if ext == ".csv" || ext == ".tsv" {
			return skip("large data table")
		}
		return skip("large non-source text")
	}
	src, err := in.read()
	if err != nil {
		return skip("unreadable")
	}
	if strings.HasPrefix(src, lfsPointer) {
		return skip("Git LFS pointer (the content is not in git)")
	}
	if looksBinary([]byte(src)) {
		return skip("binary content")
	}
	lang := sniffLang(src)
	if ext == ".csv" || ext == ".tsv" {
		lang = "csv"
	}
	syms, spans := sectionSymbolsAs(p, src, lang)
	return classified{pf: &ParsedFile{Path: p, Content: src, Lang: lang, IsTest: IsTestPath(p), Symbols: syms, Spans: spans}}
}
