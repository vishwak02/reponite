// verify.go implements reponite_verify_edit — the read/write feedback loop
// (blueprint §3). Before an agent saves a change, it asks "does this break
// anything?" and gets the answer BEFORE a compiler runs: reponite compares the
// proposed symbols of a file against the currently-indexed ones and, for every
// symbol whose signature changed or that was removed, lists the exact call sites
// (fleet-wide) that would break. Pure over the Store — the caller supplies the
// parsed old/new symbol facts (the tree-sitter parse is a thin tagged shim), so
// the diff + blast-radius logic is tested in-sandbox (ADR-018).
package query

import (
	"sort"

	"github.com/vishwak02/reponite/internal/content"
)

// EditedSymbol is one symbol of a file, reduced to what an edit-diff needs: its
// receiver-qualified identity and its body-independent signature hash.
type EditedSymbol struct {
	Name          string
	Recv          string
	SignatureHash content.Hash
}

func (e EditedSymbol) key() string { return e.Recv + "\x00" + e.Name }

// EditImpact is one changed/removed symbol and the call sites it would break.
type EditImpact struct {
	Symbol string     // the symbol's (receiver-qualified) name
	Kind   ChangeKind // shape_changed | removed
	Breaks []Usage    // confirmed call sites across the fleet that rely on it
}

// VerifyResult is the pre-commit safety report for a proposed file edit.
type VerifyResult struct {
	Path    string
	Added   []string     // symbols the edit introduces (no impact)
	Removed []string     // symbols the edit deletes
	Changed []string     // symbols whose signature the edit changes
	Impacts []EditImpact // per breaking change (removed / shape-changed), what breaks
	Safe    bool         // true when nothing with confirmed callers breaks
	Note    string
	Meta    Meta
}

// VerifyEdit diffs the proposed symbols of path against its old (indexed)
// symbols and, for each removed or signature-changed symbol, gathers the
// confirmed call sites across the fleet that would break. old and new are the
// file's symbols before/after the edit (same extraction scheme, so signature
// hashes are comparable). Safe is true iff no breaking change has a confirmed
// caller.
func VerifyEdit(s Store, repo, ref, path string, old, new []EditedSymbol) VerifyResult {
	res := VerifyResult{Path: path, Safe: true, Meta: Meta{Repo: repo, Ref: ref}}
	oldByKey := map[string]EditedSymbol{}
	for _, o := range old {
		oldByKey[o.key()] = o
	}
	newByKey := map[string]EditedSymbol{}
	for _, n := range new {
		newByKey[n.key()] = n
	}

	// Cache Usages per symbol name so a file touching a name once isn't scanned twice.
	usagesOf := func(name string) []Usage {
		var breaks []Usage
		for _, u := range Usages(s, FleetRepo, ref, name).Usages {
			if u.Confirmed {
				breaks = append(breaks, u)
			}
		}
		return breaks
	}

	for _, o := range old {
		n, present := newByKey[o.key()]
		switch {
		case !present:
			res.Removed = append(res.Removed, o.Name)
			if b := usagesOf(o.Name); len(b) > 0 {
				res.Impacts = append(res.Impacts, EditImpact{Symbol: o.Name, Kind: ChangeRemoved, Breaks: b})
				res.Safe = false
			}
		case o.SignatureHash != n.SignatureHash:
			res.Changed = append(res.Changed, o.Name)
			if b := usagesOf(o.Name); len(b) > 0 {
				res.Impacts = append(res.Impacts, EditImpact{Symbol: o.Name, Kind: ChangeShape, Breaks: b})
				res.Safe = false
			}
		}
	}
	for _, n := range new {
		if _, present := oldByKey[n.key()]; !present {
			res.Added = append(res.Added, n.Name)
		}
	}
	sort.Strings(res.Added)
	sort.Strings(res.Removed)
	sort.Strings(res.Changed)
	sort.Slice(res.Impacts, func(i, j int) bool { return res.Impacts[i].Symbol < res.Impacts[j].Symbol })

	if res.Safe {
		res.Note = "no confirmed callers break — signature changes/removals have no in-graph call sites (RPC/HTTP/dynamic still invisible)"
	} else {
		res.Note = "breaking: a changed/removed symbol has confirmed call sites; fix the sites in impacts[].breaks before committing"
	}
	return res
}
