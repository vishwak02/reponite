//go:build !neural

package main

import "github.com/vishwak02/reponite/internal/query"

// semanticRanker without the neural tag: always the pure term-idf default.
func semanticRanker() query.SemanticRanker { return nil }

func semanticRankerWhy() (query.SemanticRanker, string) {
	return nil, "this build has no neural ranker (built without the neural tag)"
}
