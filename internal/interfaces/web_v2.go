// web_v2.go is the dashboard's second set of read-only endpoints (/api/v2/*):
// the question-shaped analyses the CLI and MCP already answer — explain,
// context, impls, tests, why, errors, logsite, semsearch, investigate and
// config. Each handler calls the same internal/query function and the same
// *JSON formatter as the MCP tool of that name, with the same defaults (a
// discovery question is fleet-wide unless a repo is named; a symbol question
// answers "did you mean" when nothing resolves), so the web, the CLI and MCP
// give one answer. Every route goes through web_cache.go (method check, ETag,
// memo, gzip, compact JSON); size parameters are bounded.
package interfaces

import (
	"fmt"
	"net/http"

	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Bounds on the v2 size parameters (the CLI's defaults are far below them).
const (
	maxImpls    = 500
	maxLogSites = 200
	maxSemHits  = 200
	maxTestHops = 8
)

// apiRoute is one /api/* route and what it allows.
type apiRoute struct {
	path string
	kind apiKind
	fn   http.HandlerFunc
}

// v2Routes lists the /api/v2/* routes. why reads the repos' git history,
// which moves without the index changing, so it carries no ETag; semsearch
// and investigate carry none when a ranker other than the index's own
// (an embeddings endpoint) orders the answer.
func (h *WebHandler) v2Routes() []apiRoute {
	ranked := apiGet
	if h.Ranker != nil {
		ranked = apiGetLive
	}
	return []apiRoute{
		{"/api/v2/explain", apiGet, h.v2Explain},
		{"/api/v2/context", apiGet, h.v2Context},
		{"/api/v2/impls", apiGet, h.v2Impls},
		{"/api/v2/tests", apiGet, h.v2Tests},
		{"/api/v2/why", apiGetLive, h.v2Why},
		{"/api/v2/errors", apiGet, h.v2Errors},
		{"/api/v2/logsite", apiGet, h.v2LogSite},
		{"/api/v2/semsearch", ranked, h.v2SemSearch},
		{"/api/v2/investigate", ranked, h.v2Investigate},
		{"/api/v2/config", apiGet, h.v2Config},
	}
}

// discoverRepo is the repo a discovery question reads: the named one, else
// the whole fleet (the MCP tools' default).
func discoverRepo(r *http.Request) string {
	if v := r.URL.Query().Get("repo"); v != "" {
		return v
	}
	return query.FleetRepo
}

// fleetNote appends what a fleet-wide answer read to its note, so an empty
// answer never stands in for "not indexed at that ref" (as MCP does).
func (h *WebHandler) fleetNote(repo, ref, note string) string {
	if repo != query.FleetRepo {
		return note
	}
	read, missing := storage.Coverage(h.Store, ref)
	switch {
	case len(read) == 0:
		return note + " — " + fmt.Sprintf("NOTHING READ: no repo is indexed at the requested ref (%v)", missing)
	case len(missing) > 0:
		return note + " — " + fmt.Sprintf("read %v; not indexed at the requested ref, so not searched: %v", read, missing)
	}
	return note + " — " + fmt.Sprintf("read %v", read)
}

// notFound answers a symbol question nothing resolves with fleet-wide "did
// you mean" suggestions.
func (h *WebHandler) notFound(w http.ResponseWriter, ref, symbol string) {
	body, err := SuggestJSON("symbol", symbol, query.Suggest(h.Store, query.FleetRepo, ref, symbol, 6))
	h.writeJSON(w, body, err)
}

// symbolRepo is the repo a symbol question answers for: the named repo, else
// the default one when it defines the symbol, else the single fleet repo that
// does (several = the default).
func (h *WebHandler) symbolRepo(r *http.Request, ref, symbol string) string {
	if v := r.URL.Query().Get("repo"); v != "" {
		return v
	}
	if len(query.ResolveSymbol(h.Store, h.Repo, ref, symbol)) > 0 {
		return h.Repo
	}
	var found []string
	for _, repo := range h.Store.Repos() {
		if len(query.ResolveSymbol(h.Store, repo, ref, symbol)) > 0 {
			found = append(found, repo)
		}
	}
	if len(found) == 1 {
		return found[0]
	}
	return h.Repo
}

// noTests hides test code from one answer when ?no_tests=true.
func (h *WebHandler) noTests(r *http.Request) query.Store {
	if r.URL.Query().Get("no_tests") == "true" {
		return &storage.NoTests{Inner: h.Store}
	}
	return h.Store
}

// v2Explain: what a symbol, file, directory or "METHOD /route" is and does
// (?target=&repo=&budget=&no_tests=).
func (h *WebHandler) v2Explain(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	target := q.Get("target")
	if target == "" {
		http.Error(w, `explain needs a target: a symbol, path[:line], directory or "METHOD /route"`, http.StatusBadRequest)
		return
	}
	res := query.Explain(h.noTests(r), h.refOr(r), target, query.ExplainOptions{Repo: q.Get("repo"), Prefer: h.Repo, Budget: budgetParam(r)})
	body, err := ExplainJSON(res)
	h.writeJSON(w, body, err)
}

// v2Context: a symbol's callers and callees, with calls that cross into
// another repo (HTTP routes, topics) dispatched to their handlers there
// (?symbol=&repo=&tests=).
func (h *WebHandler) v2Context(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo, ref, sym := h.repoOr(r), h.refOr(r), q.Get("symbol")
	if len(query.ResolveSymbol(h.Store, repo, ref, sym)) == 0 {
		h.notFound(w, ref, sym)
		return
	}
	tests := q.Get("tests") == "true"
	res := query.Context(h.Store, repo, ref, sym, tests)
	if gone(r) {
		return
	}
	body, err := ContextJSON(query.WithCrossRepoDispatch(h.Store, repo, ref, res, tests))
	h.writeJSON(w, body, err)
}

// v2Impls: the implementations of an interface, base class or method
// (?base=&repo=&limit=).
func (h *WebHandler) v2Impls(w http.ResponseWriter, r *http.Request) {
	repo, ref := discoverRepo(r), h.refOr(r)
	res := query.Impls(h.Store, repo, ref, r.URL.Query().Get("base"), intParam(r, "limit", maxImpls))
	res.Note = h.fleetNote(repo, ref, res.Note)
	body, err := ImplsJSON(res)
	h.writeJSON(w, body, err)
}

// v2Tests: the tests that reach a symbol, each with the command that runs it
// (?symbol=&repo=&depth=&budget=).
func (h *WebHandler) v2Tests(w http.ResponseWriter, r *http.Request) {
	ref, sym := h.refOr(r), r.URL.Query().Get("symbol")
	repo := h.symbolRepo(r, ref, sym)
	if len(query.ResolveSymbol(h.Store, repo, ref, sym)) == 0 {
		h.notFound(w, ref, sym)
		return
	}
	body, err := TestsJSON(query.Tests(h.Store, repo, ref, sym, intParam(r, "depth", maxTestHops), budgetParam(r)))
	h.writeJSON(w, body, err)
}

// v2Why: the commits that made a symbol what it is, plus its history across
// the indexed refs (?symbol=&repo=&budget=).
func (h *WebHandler) v2Why(w http.ResponseWriter, r *http.Request) {
	ref, sym := h.refOr(r), r.URL.Query().Get("symbol")
	repo := h.symbolRepo(r, ref, sym)
	if len(query.ResolveSymbol(h.Store, repo, ref, sym)) == 0 {
		h.notFound(w, ref, sym)
		return
	}
	body, err := WhyJSON(query.Why(h.Store, h.History, repo, ref, sym, budgetParam(r)))
	h.writeJSON(w, body, err)
}

// v2Errors: where an error message, type or status comes from and who
// handles it; with no q, the error catalogue (?q=&repo=&budget=&no_tests=).
func (h *WebHandler) v2Errors(w http.ResponseWriter, r *http.Request) {
	repo, ref := discoverRepo(r), h.refOr(r)
	res := query.Errors(h.noTests(r), repo, ref, r.URL.Query().Get("q"), budgetParam(r))
	res.Note = h.fleetNote(repo, ref, res.Note)
	body, err := ErrorsJSON(res)
	h.writeJSON(w, body, err)
}

// v2LogSite: the statements that print a pasted log line (?line=&repo=&limit=).
func (h *WebHandler) v2LogSite(w http.ResponseWriter, r *http.Request) {
	repo, ref := discoverRepo(r), h.refOr(r)
	res := query.LogSites(h.Store, repo, ref, r.URL.Query().Get("line"), intParam(r, "limit", maxLogSites))
	res.Note = h.fleetNote(repo, ref, res.Note)
	body, err := LogSitesJSON(res)
	h.writeJSON(w, body, err)
}

// v2SemSearch: symbols ranked by relevance to a plain-language query
// (?q=&repo=&limit=).
func (h *WebHandler) v2SemSearch(w http.ResponseWriter, r *http.Request) {
	body, err := SemanticJSON(query.SemanticSearch(h.Store, discoverRepo(r), h.refOr(r), r.URL.Query().Get("q"), intParam(r, "limit", maxSemHits), h.Ranker))
	h.writeJSON(w, body, err)
}

// v2Investigate: a cited dossier answering a plain-language question
// (?q=&repo=&budget=).
func (h *WebHandler) v2Investigate(w http.ResponseWriter, r *http.Request) {
	body, err := InvestigateJSON(query.InvestigateWith(h.Store, discoverRepo(r), h.refOr(r), r.URL.Query().Get("q"), budgetParam(r), h.Ranker))
	h.writeJSON(w, body, err)
}

// v2Config: where a configuration key is defined, rendered and read
// (?key=&repo=&kind=&budget=).
func (h *WebHandler) v2Config(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo, ref := discoverRepo(r), h.refOr(r)
	res := query.Config(h.Store, repo, ref, q.Get("key"), query.ConfigOptions{Budget: budgetParam(r), Kind: q.Get("kind")})
	res.Note = h.fleetNote(repo, ref, res.Note)
	body, err := ConfigJSON(res)
	h.writeJSON(w, body, err)
}
