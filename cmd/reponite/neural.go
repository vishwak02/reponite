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
	r, _ := semanticRankerWhy()
	return r
}

// semanticRankerWhy also says why there is no neural ranker, so a keyword
// ranking is never mistaken for a semantic one.
func semanticRankerWhy() (query.SemanticRanker, string) {
	if r, ok := semantic.FromEnv(); ok {
		return r, ""
	}
	return nil, "no embedding model is configured (REPONITE_EMBED_ENDPOINT and REPONITE_EMBED_MODEL are unset); to rank by meaning, point them at a LOCAL OpenAI-compatible embeddings server — symbol bodies are sent to that endpoint"
}
