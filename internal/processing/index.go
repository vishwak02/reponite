// index.go is the indexer core: given a repo ref's parsed files, it computes the
// three hashes for every symbol, resolves callees to confidence-tagged CALLS
// edges (resolve.go), runs the behavior-hash pass over the whole-ref graph, and
// writes the resulting records + files to a Store. It is PURE (backend-agnostic),
// so it is unit-tested in-sandbox against storage.Mem (ADR-018); the thin
// treesitter-tagged layer (ParseGo + ExtractGo + IndexFiles) supplies real files.
package processing

import (
	"strings"

	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Indexer is the write surface the indexer needs; satisfied by storage.Mem and
// the SQLite adapter (storage/sqlite).
type Indexer interface {
	// ClearRef drops a ref's existing symbols/files/external-refs so a reindex
	// replaces rather than accumulates (vanished rows must not linger).
	ClearRef(repo, ref string) error
	Put(repo, ref, name string, rec storage.SymbolRecord) error
	PutFile(repo, ref string, f query.File) error
	// PutExternalRefs records a ref's cross-repo dependency edges (§8B).
	PutExternalRefs(repo, ref string, refs []query.ExternalRef) error
	// SetModulePath records repo's module/package identity (idempotent per repo).
	SetModulePath(repo, modulePath string) error
}

var _ Indexer = (*storage.Mem)(nil)

// ParsedFile is a file's extracted symbols plus optional line spans (for grep
// fusion), produced by the tree-sitter layer.
type ParsedFile struct {
	Path    string
	Content string
	Lang    string // language name (lang.go); empty defaults to "go" for back-compat
	Symbols []Symbol
	Spans   []query.SymbolSpan
	// Imports are the file's external import bindings (imports.go), used to
	// resolve qualified calls to (module, name) external references (§8B).
	Imports []ImportBinding
}

// IndexFiles indexes all files of one repo ref with name-based edge resolution.
// Symbols are keyed by a package-qualified id (pkg.name, pkg = the file's
// directory) so distinct definitions sharing a bare name (e.g. storage.Mem.Put
// vs sqlite.Store.Put) are distinct nodes and never conflated (correctness).
func IndexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile) error {
	return indexFiles(w, repo, ref, normVer, files, nil)
}

// indexFiles is IndexFiles with an optional precise edge map (callerQID -> base
// callee name -> type-checker-proven callee QID) that upgrades matching edges to
// go-types confidence; nil precise means pure name-based resolution.
func indexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile, precise map[string]map[string]string) error {
	type computed struct {
		sym        Symbol
		pkg        string
		lang       string
		symbolHash content.Hash
		sigHash    content.Hash
	}
	var order []string              // qualified ids, first-seen order
	byQID := map[string]computed{}  // qid -> facts
	byBase := map[string][]string{} // bare name -> defining qids (for edge resolution)
	var extRefs []query.ExternalRef // cross-repo dependency edges (§8B)
	for _, f := range files {
		pkg := pkgOf(f.Path)
		lang := f.Lang
		if lang == "" {
			lang = "go" // back-compat: pure callers/tests predate the Lang field
		}
		byLocal := importsByLocal(f.Imports)
		for _, s := range f.Symbols {
			// Methods qualify by receiver too (pkg.Recv.name) so same-named methods
			// on different types are distinct; edge resolution still keys off the
			// bare name (byBase).
			local := s.Name
			if s.Recv != "" {
				local = s.Recv + "." + s.Name
			}
			qid := qualify(pkg, local)
			id := content.SymbolIdentity{Repo: repo, Lang: lang, Kind: s.Kind, Signature: s.Signature, CanonBody: s.CanonBody}
			if _, dup := byQID[qid]; !dup {
				order = append(order, qid)
				byBase[s.Name] = append(byBase[s.Name], qid)
			}
			byQID[qid] = computed{sym: s, pkg: pkg, lang: lang, symbolHash: content.SymbolHash(normVer, id), sigHash: content.SignatureHash(normVer, id)}
			if len(byLocal) > 0 {
				extRefs = append(extRefs, resolveExternalRefs(qid, s.QualifiedCalls, byLocal)...)
			}
		}
	}

	nodeSet := make(map[string]bool, len(order))
	for _, qid := range order {
		nodeSet[qid] = true
	}

	nodes := make([]Node, 0, len(order))
	var edges []Edge
	resolved := make(map[string][]query.Callee, len(order))
	for _, qid := range order {
		c := byQID[qid]
		nodes = append(nodes, Node{ID: qid, SymbolHash: c.symbolHash})
		callees := resolveEdges(c.pkg, c.sym.Callees, nodeSet, byBase, precise[qid])
		resolved[qid] = callees
		for _, ce := range callees {
			edges = append(edges, Edge{From: qid, To: ce.Name, Confidence: ce.Confidence})
		}
	}
	beh := ComputeBehavior(nodes, edges, normVer)

	// Replace, don't accumulate: drop the ref's prior records before rewriting.
	if err := w.ClearRef(repo, ref); err != nil {
		return err
	}
	for _, qid := range order {
		c := byQID[qid]
		directConf := 1.0 // min confidence over this symbol's own direct edges
		for _, ce := range resolved[qid] {
			if ce.Confidence < directConf {
				directConf = ce.Confidence
			}
		}
		if err := w.Put(repo, ref, qid, storage.SymbolRecord{
			Lang:          c.lang,
			SymbolHash:    c.symbolHash,
			SignatureHash: c.sigHash,
			BehaviorHash:  beh.BehaviorHash[qid],
			BehaviorConf:  beh.BehaviorConf[qid],
			DirectConf:    directConf,
			Callees:       resolved[qid],
		}); err != nil {
			return err
		}
	}
	for _, f := range files {
		if err := w.PutFile(repo, ref, query.File{Path: f.Path, Content: f.Content, Symbols: f.Spans}); err != nil {
			return err
		}
	}
	captureTargetSignatures(w, repo, extRefs)
	if err := w.PutExternalRefs(repo, ref, extRefs); err != nil {
		return err
	}
	return nil
}

// captureTargetSignatures stamps each external ref with the target's signature
// hash AS SEEN NOW (§8B.3 per-caller skew: comparing this captured contract to
// the target's future signature answers "does this caller still expect the old
// shape"). Resolvable only when the target's repo lives in the SAME store being
// written (shared/fleet store, monorepo) — capture-early, query-later (§8B.6).
// Per-repo stores, an unindexed target, or an ambiguous name leave it "" —
// unknown is reported, never guessed (invariant 5).
func captureTargetSignatures(w Indexer, callerRepo string, extRefs []query.ExternalRef) {
	s, ok := w.(query.Store)
	if !ok || len(extRefs) == 0 {
		return
	}
	repoByModule := map[string]string{}
	for _, r := range s.Repos() {
		if r == callerRepo {
			continue
		}
		if m := s.ModulePath(r); m != "" {
			repoByModule[m] = r
		}
	}
	if len(repoByModule) == 0 {
		return
	}
	type key struct{ module, name string }
	cache := map[key]string{}
	for i := range extRefs {
		k := key{extRefs[i].Module, extRefs[i].Name}
		sig, seen := cache[k]
		if !seen {
			sig = targetSignature(s, repoByModule[k.module], k.name)
			cache[k] = sig
		}
		extRefs[i].TargetSignatureHash = sig
	}
}

// targetSignature returns the signature hash of the UNIQUE symbol named name in
// repo's preferred ref (HEAD if indexed, else the lexically-newest ref), or ""
// when the repo is unknown, the name is absent, or several symbols share it.
func targetSignature(s query.Store, repo, name string) string {
	if repo == "" {
		return ""
	}
	ref := preferredRef(s.Refs(repo))
	if ref == "" {
		return ""
	}
	sig, count := "", 0
	for qid, facts := range s.SymbolsAt(repo, ref) {
		if baseName(qid) == name {
			sig = string(facts.SignatureHash)
			count++
		}
	}
	if count != 1 {
		return "" // ambiguous or absent: unknown, never guessed
	}
	return sig
}

func preferredRef(refs []string) string {
	best := ""
	for _, r := range refs {
		if r == "HEAD" {
			return r
		}
		if r > best {
			best = r
		}
	}
	return best
}

// baseName mirrors query.baseName for target matching (bare name of a qid).
func baseName(qid string) string {
	if i := strings.LastIndex(qid, "."); i >= 0 {
		return qid[i+1:]
	}
	return qid
}
