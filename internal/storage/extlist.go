// extlist.go implements query.ExternalRefLister — a ref's captured cross-repo
// references, listed — for the in-memory store and the composing views, so
// the fleet map counts import-resolved links between repos in one pass.
package storage

import "github.com/vishwak02/reponite/internal/query"

var (
	_ query.ExternalRefLister = (*Mem)(nil)
	_ query.ExternalRefLister = (*MultiStore)(nil)
	_ query.ExternalRefLister = (*Pinned)(nil)
	_ query.ExternalRefLister = (*NoTests)(nil)
)

// ExternalRefsFrom lists the cross-repo references captured at repo@ref.
func (m *Mem) ExternalRefsFrom(repo, ref string) []query.ExternalRefHit {
	var out []query.ExternalRefHit
	for _, r := range m.extrefs[refKey{repo, ref}] {
		out = append(out, query.ExternalRefHit{
			Repo: repo, Ref: ref, Caller: r.From, Module: r.Module, Name: r.Name,
			ResolutionMethod: r.ResolutionMethod, Confidence: r.Confidence,
			TargetSymbol: r.TargetSymbol, TargetSignatureHash: r.TargetSignatureHash,
		})
	}
	sortHits(out)
	return out
}

// ExternalRefsFrom routes to the store owning repo.
func (m *MultiStore) ExternalRefsFrom(repo, ref string) []query.ExternalRefHit {
	if l, ok := m.owner[repo].(query.ExternalRefLister); ok {
		return l.ExternalRefsFrom(repo, ref)
	}
	return nil
}

// ExternalRefsFrom reads repo at its pinned ref.
func (p *Pinned) ExternalRefsFrom(repo, ref string) []query.ExternalRefHit {
	if l, ok := p.Inner.(query.ExternalRefLister); ok {
		return l.ExternalRefsFrom(repo, p.at(repo, ref))
	}
	return nil
}

// ExternalRefsFrom drops references made by test code.
func (n *NoTests) ExternalRefsFrom(repo, ref string) []query.ExternalRefHit {
	if l, ok := n.Inner.(query.ExternalRefLister); ok {
		return n.keep(l.ExternalRefsFrom(repo, ref))
	}
	return nil
}
