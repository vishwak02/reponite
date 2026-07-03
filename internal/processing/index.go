// index.go is the indexer core: given a repo ref's parsed files, it computes the
// three hashes for every symbol, resolves callees to confidence-tagged CALLS
// edges (resolve.go), runs the behavior-hash pass over the whole-ref graph, and
// writes the resulting records + files to a Store. It is PURE (backend-agnostic),
// so it is unit-tested in-sandbox against storage.Mem (ADR-018); the thin
// treesitter-tagged layer (ParseGo + ExtractGo + IndexFiles) supplies real files.
package processing

import (
	"github.com/vishwak02/reponite/internal/content"
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/storage"
)

// Indexer is the write surface the indexer needs; satisfied by storage.Mem and
// the SQLite adapter (storage/sqlite).
type Indexer interface {
	Put(repo, ref, name string, rec storage.SymbolRecord) error
	PutFile(repo, ref string, f query.File) error
}

var _ Indexer = (*storage.Mem)(nil)

// ParsedFile is a file's extracted symbols plus optional line spans (for grep
// fusion), produced by the tree-sitter layer.
type ParsedFile struct {
	Path    string
	Content string
	Symbols []Symbol
	Spans   []query.SymbolSpan
}

// IndexFiles indexes all files of one repo ref. Symbols are keyed by a
// package-qualified id (pkg.name, pkg = the file's directory) so distinct
// definitions that share a bare name (e.g. storage.Mem.Put vs sqlite.Store.Put)
// are distinct nodes and never conflated (correctness). Callee edges are then
// resolved against those ids (resolve.go).
func IndexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile) error {
	type computed struct {
		sym        Symbol
		pkg        string
		symbolHash content.Hash
		sigHash    content.Hash
	}
	var order []string              // qualified ids, first-seen order
	byQID := map[string]computed{}  // qid -> facts
	byBase := map[string][]string{} // bare name -> defining qids (for edge resolution)
	for _, f := range files {
		pkg := pkgOf(f.Path)
		for _, s := range f.Symbols {
			qid := qualify(pkg, s.Name)
			id := content.SymbolIdentity{Repo: repo, Lang: "go", Kind: s.Kind, Signature: s.Signature, CanonBody: s.CanonBody}
			if _, dup := byQID[qid]; !dup {
				order = append(order, qid)
				byBase[s.Name] = append(byBase[s.Name], qid)
			}
			byQID[qid] = computed{sym: s, pkg: pkg, symbolHash: content.SymbolHash(normVer, id), sigHash: content.SignatureHash(normVer, id)}
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
		callees := resolveEdges(c.pkg, c.sym.Callees, nodeSet, byBase)
		resolved[qid] = callees
		for _, ce := range callees {
			edges = append(edges, Edge{From: qid, To: ce.Name, Confidence: ce.Confidence})
		}
	}
	beh := ComputeBehavior(nodes, edges, normVer)

	for _, qid := range order {
		c := byQID[qid]
		if err := w.Put(repo, ref, qid, storage.SymbolRecord{
			SymbolHash:    c.symbolHash,
			SignatureHash: c.sigHash,
			BehaviorHash:  beh.BehaviorHash[qid],
			BehaviorConf:  beh.BehaviorConf[qid],
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
	return nil
}
