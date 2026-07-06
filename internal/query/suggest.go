// suggest.go turns a miss into a hint. When an agent asks for a symbol that
// isn't indexed, returning [] forces it to guess again; instead we return the
// nearest indexed names (fleet-wide), so the tool self-heals: "Symbol Picking
// not found — did you mean DynamicPicking in rr_io_amr?" (§ agent-optimized UX).
// Pure over the Store, ranked by substring containment then edit distance.
package query

import (
	"sort"
	"strings"
)

// Suggest returns up to limit indexed symbols whose bare name is closest to q,
// across repo (FleetRepo "*" = every repo) at ref. Exact matches are excluded —
// if q resolved exactly the caller would not be asking for suggestions.
func Suggest(s Store, repo, ref, q string, limit int) []SearchHit {
	if limit <= 0 {
		limit = 5
	}
	ql := strings.ToLower(q)
	type scored struct {
		hit   SearchHit
		score int // lower is closer
	}
	var cands []scored
	for _, rp := range reposFor(s, repo) {
		for name := range s.SymbolsAt(rp, ref) {
			base := baseName(name)
			if base == q {
				continue // exact base match isn't a "did you mean"
			}
			bl := strings.ToLower(base)
			var sc int
			switch {
			case bl == ql:
				sc = 1 // same name, different case/qualifier
			case strings.Contains(bl, ql) || strings.Contains(ql, bl):
				sc = 2 + abs(len(bl)-len(ql)) // substring either way
			default:
				d := levenshtein(ql, bl)
				if d > 3 && d*3 > len(ql)+len(bl) { // too far to be a plausible typo
					continue
				}
				sc = 10 + d
			}
			cands = append(cands, scored{SearchHit{Repo: rp, Name: name, Ref: ref}, sc})
		}
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].score != cands[j].score {
			return cands[i].score < cands[j].score
		}
		return cands[i].hit.Name < cands[j].hit.Name
	})
	out := make([]SearchHit, 0, limit)
	for _, c := range cands {
		if len(out) >= limit {
			break
		}
		out = append(out, c.hit)
	}
	return out
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// levenshtein is the classic edit distance (two-row DP), for typo-close names.
func levenshtein(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	prev := make([]int, len(rb)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ra); i++ {
		cur := make([]int, len(rb)+1)
		cur[0] = i
		for j := 1; j <= len(rb); j++ {
			cost := 1
			if ra[i-1] == rb[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(rb)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}
