// Package query is the read path: ref resolution, staleness detection, query
// decomposition, result merging (RRF), the Compatibility Oracle, and the
// agent-facing reads — brief, rootcause, ximpact (architecture §8, §10; ext
// §8A–§8C).
//
// The Oracle (compat.go), diff (diff.go), and rootcause (rootcause.go) are pure
// comparisons over ref_history/edges and are unit-tested in-sandbox against
// fake stores. Planned files: refresolver.go staledetect.go decomposer.go
// struct.go semantic.go intentsearch.go merger.go coordinator.go diff.go
// compat.go rootcause.go brief.go ximpact.go.
package query
