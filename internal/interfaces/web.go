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
	"io/fs"
	"net/http"
	"strconv"

	"github.com/vishwak02/reponite/internal/query"
)

// DBStater optionally exposes the physical index database behind a store — its
// file path and per-table row counts — for the dashboard's index/database view.
// The SQLite store implements it; the in-memory store does not.
type DBStater interface {
	DBStats() (path string, tables map[string]int64)
}

// WebHandler answers dashboard + API requests against a Store, scoped to a repo.
// Intent is optional provenance for the brief endpoint (nil = omitted).
// RepoStores optionally maps each repo to its concrete backing store so the
// Overview view can surface per-repo database facts (path + table counts); it is
// nil in tests over a single in-memory store.
type WebHandler struct {
	Store      query.Store
	Repo       string
	Intent     query.IntentProvider
	RepoStores map[string]query.Store
	// ParseSymbols parses proposed file content for /api/verify_edit (injected by
	// the tree-sitter serve command; nil otherwise).
	ParseSymbols func(path, content string) []query.EditedSymbol
	// Ranker overrides the semantic rung for /api/investigate (ADR-020);
	// injected by the neural build when configured, nil = term-idf default.
	Ranker query.SemanticRanker
}

// Routes returns the handler's mux (dashboard at /, JSON under /api/*).
func (h *WebHandler) Routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/", h.index)
	mux.Handle("/assets/", immutable(http.FileServer(http.FS(appDist))))
	mux.HandleFunc("/api/repos", h.apiRepos)
	mux.HandleFunc("/api/overview", h.apiOverview)
	mux.HandleFunc("/api/refs", h.apiRefs)
	mux.HandleFunc("/api/search", h.apiSearch)
	mux.HandleFunc("/api/context", h.apiContext)
	mux.HandleFunc("/api/brief", h.apiBrief)
	mux.HandleFunc("/api/compat", h.apiCompat)
	mux.HandleFunc("/api/diff", h.apiDiff)
	mux.HandleFunc("/api/rootcause", h.apiRootcause)
	mux.HandleFunc("/api/ximpact", h.apiXImpact)
	mux.HandleFunc("/api/blast_radius", h.apiBlastRadius)
	mux.HandleFunc("/api/investigate", h.apiInvestigate)
	mux.HandleFunc("/api/usages", h.apiUsages)
	mux.HandleFunc("/api/topics", h.apiTopics)
	mux.HandleFunc("/api/verify_edit", h.apiVerifyEdit)
	mux.HandleFunc("/api/grep", h.apiGrep)
	mux.HandleFunc("/api/graph", h.apiGraph)
	mux.HandleFunc("/api/links", h.apiLinks)
	mux.HandleFunc("/api/coverage", h.apiCoverage)
	mux.HandleFunc("/api/fleetmap", h.apiFleetMap)
	mux.HandleFunc("/api/file", h.apiFile)
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

// apiOverview returns the index summary for every repo — per-ref logical stats
// plus, where the backing store is a real database, its file path and per-table
// row counts (the dashboard's Overview/database view).
func (h *WebHandler) apiOverview(w http.ResponseWriter, r *http.Request) {
	body, err := OverviewJSON(query.Overview(h.Store), h.dbStatsFor)
	writeJSON(w, body, err)
}

// dbStatsFor returns a repo's database path + table counts if its backing store
// is a DBStater, else ("", nil). Falls back to the primary Store when no
// per-repo store map is set (single-repo serve).
func (h *WebHandler) dbStatsFor(repo string) (string, map[string]int64) {
	var st query.Store = h.Store
	if h.RepoStores != nil {
		st = h.RepoStores[repo]
	}
	if ds, ok := st.(DBStater); ok {
		return ds.DBStats()
	}
	return "", nil
}

// index serves the dashboard app. It routes in the URL fragment, so every
// page is this one document.
func (h *WebHandler) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	b, err := fs.ReadFile(appDist, "index.html")
	if err != nil {
		http.Error(w, "dashboard not built (run npm run build in web/)", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Write(b)
}

// immutable marks the app's content-hashed assets cacheable for good.
func immutable(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		next.ServeHTTP(w, r)
	})
}

func (h *WebHandler) apiRefs(w http.ResponseWriter, r *http.Request) {
	repo := h.repoOr(r)
	refs := h.Store.Refs(repo)
	// a pinned view reads HEAD as the repo's pinned ref: offer it first
	if p, ok := h.Store.(query.RefPinner); ok && p.RefFor(repo, "HEAD") != "HEAD" {
		refs = append([]string{"HEAD"}, refs...)
	}
	body, err := RefsJSON(repo, refs)
	writeJSON(w, body, err)
}

func (h *WebHandler) apiSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo, ref := h.repoOr(r), h.refOr(r)
	hits := query.SearchName(h.Store, repo, ref, q.Get("q"), q.Get("tests") == "true")
	if len(hits) == 0 && q.Get("q") != "" {
		body, err := SuggestJSON("symbol", q.Get("q"), query.Suggest(h.Store, query.FleetRepo, ref, q.Get("q"), 6))
		writeJSON(w, body, err)
		return
	}
	body, err := SearchJSON(hits)
	writeJSON(w, body, err)
}

// apiVerifyEdit reports what breaks if the posted content replaces path.
func (h *WebHandler) apiVerifyEdit(w http.ResponseWriter, r *http.Request) {
	if h.ParseSymbols == nil {
		http.Error(w, "verify_edit needs the tree-sitter build", http.StatusNotImplemented)
		return
	}
	q := r.URL.Query()
	repo, ref := h.repoOr(r), h.refOr(r)
	path := q.Get("path")
	newContent := r.FormValue("content") // body param (POST) or query
	var oldContent string
	for _, f := range h.Store.Files(repo, ref) {
		if f.Path == path {
			oldContent = f.Content
			break
		}
	}
	old := h.ParseSymbols(path, oldContent)
	nw := h.ParseSymbols(path, newContent)
	body, err := VerifyEditJSON(query.VerifyEdit(h.Store, repo, ref, path, old, nw))
	writeJSON(w, body, err)
}

// apiGrep runs a lexical/regex search (?pattern=&fixed=&limit=&offset=),
// fleet-wide unless ?repo= scopes it — same paging semantics as CLI/MCP.
func (h *WebHandler) apiGrep(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	res, err := query.GrepRepo(h.Store, repo, h.refOr(r), q.Get("pattern"),
		query.GrepOptions{Fixed: q.Get("fixed") == "true", Limit: limit, Offset: offset})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	body, err := GrepJSON(res)
	writeJSON(w, body, err)
}

// apiUsages returns every call site of a symbol, call-graph-confirmed.
func (h *WebHandler) apiUsages(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	body, err := UsagesJSON(query.Usages(h.Store, repo, h.refOr(r), q.Get("symbol")))
	writeJSON(w, body, err)
}

// apiTopics returns the ROS communication graph (fleet-wide), or one topic's
// producers and consumers when `topic` is set.
func (h *WebHandler) apiTopics(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	if topic := q.Get("topic"); topic != "" {
		body, err := TopicsJSON(query.Topic(h.Store, repo, h.refOr(r), topic))
		writeJSON(w, body, err)
		return
	}
	body, err := TopicsJSON(query.CommGraph(h.Store, repo, h.refOr(r)))
	writeJSON(w, body, err)
}

// apiInvestigate answers a natural-language question with a cited dossier.
func (h *WebHandler) apiInvestigate(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	budget, _ := strconv.Atoi(q.Get("budget"))
	body, err := InvestigateJSON(query.InvestigateWith(h.Store, repo, h.refOr(r), q.Get("q"), budget, h.Ranker))
	writeJSON(w, body, err)
}

// apiBlastRadius returns the pre-edit impact dossier for a symbol (§2 macro).
func (h *WebHandler) apiBlastRadius(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo, ref := h.repoOr(r), h.refOr(r)
	sym := q.Get("symbol")
	if len(query.ResolveSymbol(h.Store, repo, ref, sym)) == 0 {
		body, err := SuggestJSON("symbol", sym, query.Suggest(h.Store, query.FleetRepo, ref, sym, 6))
		writeJSON(w, body, err)
		return
	}
	body, err := BlastRadiusJSON(query.BlastRadius(h.Store, repo, ref, sym))
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

// apiGraph returns a symbol's call-graph neighborhood (nodes + edges) for the
// graph view: ?symbol=&repo=&depth=1..4&limit=&tests=true.
func (h *WebHandler) apiGraph(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	depth, _ := strconv.Atoi(q.Get("depth"))
	limit, _ := strconv.Atoi(q.Get("limit"))
	body, err := snakeJSON(query.CallGraph(h.Store, h.repoOr(r), h.refOr(r), q.Get("symbol"), depth, limit, q.Get("tests") == "true", q.Get("external") == "true"))
	writeJSON(w, body, err)
}

// apiLinks returns the data references to and from a file or symbol, or
// with no target every reference in the repo.
func (h *WebHandler) apiLinks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	target := q.Get("target")
	res := query.LinksFor(h.Store, repo, h.refOr(r), target)
	if target == "" {
		// no target: every link of the repo, for the dashboard's file graph
		res.RefersTo = query.Links(h.Store, repo, h.refOr(r))
	}
	body, err := snakeJSON(res)
	writeJSON(w, body, err)
}

// apiCoverage returns what each repo's index stored and skipped.
func (h *WebHandler) apiCoverage(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		repo = query.FleetRepo
	}
	body, err := snakeJSON(query.Coverage(h.Store, repo, h.refOr(r)))
	writeJSON(w, body, err)
}

// apiFile returns one stored file's content (the dashboard's file view and
// the starting text of a Verify edit).
func (h *WebHandler) apiFile(w http.ResponseWriter, r *http.Request) {
	repo, ref, path := h.repoOr(r), h.refOr(r), r.URL.Query().Get("path")
	files := h.Store.Files
	if pr, ok := h.Store.(query.PathFilesReader); ok {
		files = func(repo, ref string) []query.File {
			return pr.FilesMatching(repo, ref, func(p string) bool { return p == path })
		}
	}
	for _, f := range files(repo, ref) {
		if f.Path == path {
			body, err := snakeJSON(struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}{f.Path, f.Content})
			writeJSON(w, body, err)
			return
		}
	}
	http.Error(w, "no stored file "+path+" in "+repo+" at "+ref, http.StatusNotFound)
}

// apiFleetMap returns every repo with the cross-repo links between them.
func (h *WebHandler) apiFleetMap(w http.ResponseWriter, r *http.Request) {
	body, err := FleetMapJSON(query.FleetMap(h.Store, h.refOr(r), 1<<20))
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
