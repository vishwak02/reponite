// web_cache.go is what every /api/* answer passes through on its way to the
// browser: the method check (read-only API: GET, plus POST for verify_edit),
// a conditional-request check (ETag / If-None-Match → 304 without running the
// query), a memo of recent answers by ETag, gzip, and single-line JSON. None of it touches the formatters or
// the query layer, so the CLI and MCP answers are unchanged: the JSON is
// compacted after it is rendered, here, and only for the web.
package interfaces

import (
	"bytes"
	"compress/gzip"
	"container/list"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Bounds on the size parameters a page can ask for, so one request cannot
// make the server build an unbounded answer. The dashboard asks for at most
// 500 graph nodes and pages grep 100 matches at a time.
const (
	maxGraphNodes  = 1000
	maxGrepMatches = 1000
	maxBudget      = 64000
	// gzipMin is the smallest answer worth compressing.
	gzipMin = 1024
)

// bootID changes on every start, so a restarted or rebuilt server never
// confirms an answer an earlier one gave.
var bootID = strconv.FormatInt(time.Now().UnixNano(), 36)

// apiKind is what a route allows.
type apiKind int

const (
	apiGet       apiKind = iota // GET/HEAD; the answer depends only on the index (ETag)
	apiGetLive                  // GET/HEAD; reads something besides the index (no ETag)
	apiGetOrPost                // also POST (verify_edit's form body)
)

// api wraps one /api/* handler.
func (h *WebHandler) api(kind apiKind, fn http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet || r.Method == http.MethodHead:
		case r.Method == http.MethodPost && kind == apiGetOrPost:
			r.Body = http.MaxBytesReader(w, r.Body, 16<<20)
		default:
			allow := "GET, HEAD"
			if kind == apiGetOrPost {
				allow += ", POST"
			}
			w.Header().Set("Allow", allow)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if gone(r) {
			return
		}
		w.Header().Set("Vary", "Accept-Encoding")
		tag := ""
		if kind == apiGet {
			if tag = h.etag(r); tag != "" {
				w.Header().Set("ETag", tag)
				w.Header().Set("Cache-Control", "no-cache")
				if etagMatches(r.Header.Get("If-None-Match"), tag) {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}
		aw := &apiWriter{w: w, gzip: acceptsEncoding(r.Header.Get("Accept-Encoding"), "gzip")}
		defer aw.close()
		if tag == "" {
			fn(aw, r)
			return
		}
		// the ETag names the answer exactly, so an answer already computed
		// for it (another tab, a cleared browser cache) is written again
		if a, ok := h.answers.get(tag); ok {
			w.Header().Set("Content-Type", a.ctype)
			if aw.gzip && len(a.body) >= gzipMin {
				// compressed once per answer, not once per request
				w.Header().Set("Content-Encoding", "gzip")
				aw.Write(a.gzipped())
				return
			}
			aw.Write(a.body)
			return
		}
		aw.keep = true
		fn(aw, r)
		if aw.code == http.StatusOK && !aw.overflow && !gone(r) {
			h.answers.put(tag, w.Header().Get("Content-Type"), aw.kept)
		}
	})
}

// answerMemo keeps recent API answers by ETag, within a byte budget: a
// repeated fleet-wide question (the Overview's table counts, a search, the
// topic graph) costs a map lookup instead of the query.
func newAnswerMemo(budget int) *answerMemo {
	return &answerMemo{budget: budget, byTag: map[string]*list.Element{}, order: list.New()}
}

// maxKept is the largest answer memoized.
const maxKept = 8 << 20

type answerMemo struct {
	mu     sync.Mutex
	budget int
	used   int
	byTag  map[string]*list.Element
	order  *list.List // most recently used first
}

type answer struct {
	tag, ctype string
	body       []byte
	gzOnce     sync.Once
	gz         []byte
}

func (a *answer) gzipped() []byte {
	a.gzOnce.Do(func() {
		var b bytes.Buffer
		zw := gzipPool.Get().(*gzip.Writer)
		zw.Reset(&b)
		zw.Write(a.body)
		zw.Close()
		zw.Reset(io.Discard)
		gzipPool.Put(zw)
		a.gz = b.Bytes()
	})
	return a.gz
}

func (m *answerMemo) get(tag string) (*answer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.byTag[tag]
	if !ok {
		return nil, false
	}
	m.order.MoveToFront(e)
	return e.Value.(*answer), true
}

func (m *answerMemo) put(tag, ctype string, body []byte) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.byTag[tag]; ok || len(body) > m.budget {
		return
	}
	m.byTag[tag] = m.order.PushFront(&answer{tag: tag, ctype: ctype, body: body})
	m.used += len(body)
	for m.used > m.budget {
		old := m.order.Back()
		a := old.Value.(*answer)
		m.order.Remove(old)
		delete(m.byTag, a.tag)
		m.used -= len(a.body)
	}
}

// gone reports whether the client has stopped waiting for the answer.
func gone(r *http.Request) bool { return r.Context().Err() != nil }

// etag identifies an answer: the request, the server process, the index's
// data version, and the asked repo's ref with the commit and content it was
// indexed from. "" when the handler cannot tell when its data changes.
func (h *WebHandler) etag(r *http.Request) string {
	if h.DataVersion == nil {
		return ""
	}
	v := h.DataVersion()
	if v == "" {
		return ""
	}
	repo, ref := h.repoOr(r), h.refOr(r)
	sum := sha256.New()
	fmt.Fprintf(sum, "%s\x00%s\x00%s\x00%s\x00%s\x00%s\x00%s", bootID, v, r.URL.Path, r.URL.RawQuery, repo, ref, h.refIdentity(v, repo, ref))
	return `W/"` + hex.EncodeToString(sum.Sum(nil)[:12]) + `"`
}

// refIdentity is the commit and manifest hash repo's ref was indexed from,
// memoized per data version (reading a manifest walks every blob of the ref).
func (h *WebHandler) refIdentity(version, repo, ref string) string {
	h.idMu.Lock()
	defer h.idMu.Unlock()
	if h.idVersion != version || h.ids == nil {
		h.idVersion, h.ids = version, map[[2]string]string{}
	}
	k := [2]string{repo, ref}
	if id, ok := h.ids[k]; ok {
		return id
	}
	id := "-"
	if man, ok := h.Store.Manifest(repo, ref); ok {
		id = man.Commit + "/" + string(man.Hash())
	}
	h.ids[k] = id
	return id
}

// etagMatches applies If-None-Match's weak comparison (RFC 9110 §13.1.2).
func etagMatches(header, tag string) bool {
	if header == "" {
		return false
	}
	want := strings.TrimPrefix(tag, "W/")
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimSpace(t)
		if t == "*" || strings.TrimPrefix(t, "W/") == want {
			return true
		}
	}
	return false
}

// FileVersion is a data version for stores kept in files: the size and
// modification time of each database and its write-ahead log. Any commit —
// this process's or a re-index running beside it — changes one of them.
func FileVersion(paths ...string) string {
	sum := sha256.New()
	for _, p := range paths {
		for _, f := range []string{p, p + "-wal"} {
			if st, err := os.Stat(f); err == nil {
				fmt.Fprintf(sum, "%s %d %d\n", f, st.Size(), st.ModTime().UnixNano())
			} else {
				fmt.Fprintf(sum, "%s -\n", f)
			}
		}
	}
	return hex.EncodeToString(sum.Sum(nil)[:12])
}

// writeJSON writes a rendered answer, as one line when the handler is
// Compact (the formatters indent for the CLI; the browser does not read it).
func (h *WebHandler) writeJSON(w http.ResponseWriter, body string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if h.Compact {
		var b bytes.Buffer
		if json.Compact(&b, []byte(body)) == nil {
			w.Write(b.Bytes())
			return
		}
	}
	io.WriteString(w, body)
}

var gzipPool = sync.Pool{New: func() any {
	zw, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
	return zw
}}

// apiWriter gzips an answer when the client accepts it and the answer is
// big enough to gain, and drops the ETag from anything but a 200 (an error
// must never be confirmed as current). The decision is made on the first
// write; the API writes each answer in one.
type apiWriter struct {
	w       http.ResponseWriter
	gzip    bool
	code    int
	started bool
	zw      *gzip.Writer
	// keep collects the uncompressed answer for the memo, up to maxKept
	keep     bool
	kept     []byte
	overflow bool
}

func (a *apiWriter) Header() http.Header { return a.w.Header() }

func (a *apiWriter) WriteHeader(code int) {
	if a.code == 0 {
		a.code = code
	}
}

func (a *apiWriter) start(n int) {
	a.started = true
	if a.code == 0 {
		a.code = http.StatusOK
	}
	h := a.w.Header()
	if a.code != http.StatusOK {
		h.Del("ETag")
		h.Del("Cache-Control")
	}
	if a.gzip && n >= gzipMin && h.Get("Content-Encoding") == "" {
		h.Set("Content-Encoding", "gzip")
		h.Del("Content-Length")
		a.zw = gzipPool.Get().(*gzip.Writer)
		a.zw.Reset(a.w)
	}
	a.w.WriteHeader(a.code)
}

func (a *apiWriter) Write(p []byte) (int, error) {
	if !a.started {
		a.start(len(p))
	}
	if a.keep && !a.overflow {
		if len(a.kept)+len(p) > maxKept {
			a.overflow, a.kept = true, nil
		} else {
			a.kept = append(a.kept, p...)
		}
	}
	if a.zw != nil {
		return a.zw.Write(p)
	}
	return a.w.Write(p)
}

func (a *apiWriter) close() {
	if !a.started {
		if a.code == 0 {
			return // the client left before an answer was written
		}
		a.start(0)
	}
	if a.zw != nil {
		a.zw.Close()
		a.zw.Reset(io.Discard)
		gzipPool.Put(a.zw)
		a.zw = nil
	}
}

// intParam reads a size parameter bounded by max; a negative value (the
// CLI's "all") is max too.
func intParam(r *http.Request, key string, max int) int {
	v, _ := strconv.Atoi(r.URL.Query().Get(key))
	if v < 0 || v > max {
		return max
	}
	return v
}

// budgetParam reads a token budget bounded by maxBudget (<= 0 = the query's
// default).
func budgetParam(r *http.Request) int {
	v, _ := strconv.Atoi(r.URL.Query().Get("budget"))
	if v > maxBudget {
		return maxBudget
	}
	return v
}
