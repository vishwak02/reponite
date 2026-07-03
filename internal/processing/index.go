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

// IndexFiles indexes all files of one repo ref.
func IndexFiles(w Indexer, repo, ref string, normVer int, files []ParsedFile) error {
	type computed struct {
		sym        Symbol
		symbolHash content.Hash
		sigHash    content.Hash
	}
	var order []string
	byName := map[string]computed{}
	for _, f := range files {
		for _, s := range f.Symbols {
			id := content.SymbolIdentity{Repo: repo, Lang: "go", Kind: s.Kind, Signature: s.Signature, CanonBody: s.CanonBody}
			if _, dup := byName[s.Name]; !dup {
				order = append(order, s.Name)
			}
			byName[s.Name] = computed{sym: s, symbolHash: content.SymbolHash(normVer, id), sigHash: content.SignatureHash(normVer, id)}
		}
	}

	// Resolve every callee name against the set of symbols indexed in this ref,
	// so each edge records how it resolved and gets an honest confidence
	// (resolve.go) instead of one blanket constant.
	indexed := make(map[string]bool, len(order))
	for _, name := range order {
		indexed[name] = true
	}

	nodes := make([]Node, 0, len(order))
	var edges []Edge
	resolved := make(map[string][]ResolvedCallee, len(order))
	for _, name := range order {
		c := byName[name]
		nodes = append(nodes, Node{ID: name, SymbolHash: c.symbolHash})
		rcs := Resolve(c.sym.Callees, indexed)
		resolved[name] = rcs
		for _, rc := range rcs {
			edges = append(edges, Edge{From: name, To: rc.Name, Confidence: rc.Confidence})
		}
	}
	beh := ComputeBehavior(nodes, edges, normVer)

	for _, name := range order {
		c := byName[name]
		callees := make([]query.Callee, 0, len(resolved[name]))
		for _, rc := range resolved[name] {
			callees = append(callees, query.Callee{Name: rc.Name, ResolutionMethod: rc.Method, Confidence: rc.Confidence})
		}
		if err := w.Put(repo, ref, name, storage.SymbolRecord{
			SymbolHash:    c.symbolHash,
			SignatureHash: c.sigHash,
			BehaviorHash:  beh.BehaviorHash[name],
			BehaviorConf:  beh.BehaviorConf[name],
			Callees:       callees,
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
