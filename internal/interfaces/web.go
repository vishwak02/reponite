// web.go serves reponite's read model as a small HTTP dashboard + JSON API
// (roadmap 3.3): indexed refs at a glance, structural search, a symbol brief,
// a ref-to-ref diff, and cross-repo impact. It is a thin adapter over the same
// query coordinators the CLI/MCP use, so the handler logic is pure over a Store
// and unit-tested in-sandbox with httptest + storage.Mem (ADR-018). The only
// build-tagged piece is the `reponite serve` command that opens the SQLite store
// and calls http.ListenAndServe.
package interfaces

import (
	"io"
	"net/http"
	"strconv"

	"github.com/vishwak02/reponite/internal/query"
)

// WebHandler answers dashboard + API requests against a Store, scoped to a repo.
// Intent is optional provenance for the brief endpoint (nil = omitted).
type WebHandler struct {
	Store  query.Store
	Repo   string
	Intent query.IntentProvider
}

// Routes returns the handler's mux (dashboard at /, JSON under /api/*).
func (h *WebHandler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.index)
	mux.HandleFunc("/style.css", asset("text/css; charset=utf-8", dashboardCSS))
	mux.HandleFunc("/app.js", asset("application/javascript; charset=utf-8", dashboardJS))
	mux.HandleFunc("/api/repos", h.apiRepos)
	mux.HandleFunc("/api/refs", h.apiRefs)
	mux.HandleFunc("/api/search", h.apiSearch)
	mux.HandleFunc("/api/context", h.apiContext)
	mux.HandleFunc("/api/brief", h.apiBrief)
	mux.HandleFunc("/api/compat", h.apiCompat)
	mux.HandleFunc("/api/diff", h.apiDiff)
	mux.HandleFunc("/api/rootcause", h.apiRootcause)
	mux.HandleFunc("/api/ximpact", h.apiXImpact)
	return mux
}

func (h *WebHandler) refOr(r *http.Request) string {
	if v := r.URL.Query().Get("ref"); v != "" {
		return v
	}
	return "HEAD"
}

// repoOr returns the ?repo= query param (team/fleet view) or the handler's
// default repo. Lets one server serve every repo in a MultiStore.
func (h *WebHandler) repoOr(r *http.Request) string {
	if v := r.URL.Query().Get("repo"); v != "" {
		return v
	}
	return h.Repo
}

// apiRepos lists every repo in the store (the team-server landing data).
func (h *WebHandler) apiRepos(w http.ResponseWriter, r *http.Request) {
	body, err := ReposJSON(h.Store.Repos())
	writeJSON(w, body, err)
}

func (h *WebHandler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, dashboardHTML)
}

// asset serves an embedded static file (the dashboard's CSS/JS) with a fixed
// content type.
func asset(contentType, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		io.WriteString(w, body)
	}
}

func (h *WebHandler) apiRefs(w http.ResponseWriter, r *http.Request) {
	repo := h.repoOr(r)
	body, err := RefsJSON(repo, h.Store.Refs(repo))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, err := SearchJSON(query.SearchName(h.Store, h.repoOr(r), h.refOr(r), q.Get("q"), q.Get("tests") == "true"))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiContext(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, err := ContextJSON(query.Context(h.Store, h.repoOr(r), h.refOr(r), q.Get("symbol"), q.Get("tests") == "true"))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiBrief(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	budget, _ := strconv.Atoi(q.Get("budget"))
	body, err := BriefJSON(query.Brief(h.Store, h.repoOr(r), h.refOr(r), q.Get("symbol"), budget, h.Intent))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiCompat(w http.ResponseWriter, r *http.Request) {
	repo, ref := h.repoOr(r), h.refOr(r)
	var targets []query.RepoRef
	for _, rf := range h.Store.Refs(repo) {
		if rf != ref {
			targets = append(targets, query.RepoRef{Repo: repo, Ref: rf})
		}
	}
	rep, err := query.CompatSymbol(h.Store, query.RepoRef{Repo: repo, Ref: ref}, r.URL.Query().Get("symbol"), targets)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := CompatJSON(rep)
	writeJSON(w, body, err)
}

func (h *WebHandler) apiDiff(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	min, _ := strconv.ParseFloat(q.Get("confidence_min"), 64)
	opt := query.DiffOptions{ChangedOnly: q.Get("changed_only") == "true", Package: q.Get("package"), MinConfidence: min}
	body, err := DiffJSON(query.DiffRefsBy(h.Store, h.repoOr(r), q.Get("from"), q.Get("to"), opt))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiRootcause(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, err := RootCauseJSON(query.RootCauseBy(h.Store, h.repoOr(r), q.Get("symbol"), q.Get("from"), q.Get("to")))
	writeJSON(w, body, err)
}

func (h *WebHandler) apiXImpact(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	body, err := XImpactJSON(query.XImpact(h.Store, q.Get("symbol"), q.Get("ref")))
	writeJSON(w, body, err)
}

func writeJSON(w http.ResponseWriter, body string, err error) {
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, body)
}
