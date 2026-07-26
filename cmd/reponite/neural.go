//go:build neural

package main

import (
	"github.com/vishwak02/reponite/internal/query"
	"github.com/vishwak02/reponite/internal/semantic"
)

// semanticRanker returns the neural ranker when REPONITE_EMBED_ENDPOINT +
// REPONITE_EMBED_MODEL are set, else nil (the pure term-idf default ranks).
// Build-tag bridge (ADR-018): only the `neural` build carries a network path.
func semanticRanker() query.SemanticRanker {
	if r, ok := semantic.FromEnv(); ok {
		return r
	}
	return nil
}
