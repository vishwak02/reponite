// coverage.go answers "what did the index see of this repo?": per file kind,
// how many tracked files were stored and how many were skipped and why. An
// answer that comes back empty is only meaningful if the files it would have
// come from were read; this is how to know.
package query

import (
	"path"
	"sort"
	"strings"
)

// CoverageKind is one file kind (extension, or the name of an extension-less file).
type CoverageKind struct {
	Kind    string         `json:"kind"`
	Stored  int            `json:"stored"`
	Skipped map[string]int `json:"skipped,omitempty"` // reason -> files
}

// CoverageRepo is one repo's coverage at a ref.
type CoverageRepo struct {
	Repo    string `json:"repo"`
	Ref     string `json:"ref"`
	Stored  int    `json:"stored"`
	Skipped int    `json:"skipped"`
	// Generated counts stored files a code generator writes (ranked after hand-written code).
	Generated int            `json:"generated,omitempty"`
	Reasons   map[string]int `json:"reasons,omitempty"`
	Kinds     []CoverageKind `json:"kinds"`
	// Examples are a few skipped paths per reason, to see what a reason covers.
	Examples map[string][]string `json:"examples,omitempty"`
	Recorded bool                `json:"skips_recorded"` // false: indexed before skips were recorded
}

// CoverageResult is coverage for every repo in scope.
type CoverageResult struct {
	Repos []CoverageRepo `json:"repos"`
	Note  string         `json:"note"`
	Meta  Meta           `json:"_meta"`
}

func coverageKind(p string) string {
	base := path.Base(p)
	if strings.HasSuffix(p, "/") {
		return "(directory)"
	}
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return strings.ToLower(base[i:])
	}
	return "(no extension)"
}

// Coverage reports what the index of repo (or every repo) stored at ref.
func Coverage(s Store, repo, ref string) CoverageResult {
	res := CoverageResult{Meta: NewMeta(s, repo, ref)}
	sr, canSkip := s.(SkipReader)
	for _, rp := range reposFor(s, repo) {
		eff := EffectiveRef(s, rp, ref)
		c := CoverageRepo{Repo: rp, Ref: eff, Reasons: map[string]int{}, Examples: map[string][]string{}}
		kinds := map[string]*CoverageKind{}
		kind := func(k string) *CoverageKind {
			if kinds[k] == nil {
				kinds[k] = &CoverageKind{Kind: k, Skipped: map[string]int{}}
			}
			return kinds[k]
		}
		// paths only: the filter sees every stored path and keeps none
		filesMatching(s, rp, eff, func(p string) bool {
			kind(coverageKind(p)).Stored++
			c.Stored++
			if IsGeneratedPath(p) {
				c.Generated++
			}
			return false
		})
		if canSkip {
			sk := sr.Skipped(rp, eff)
			c.Recorded = len(sk) > 0 || c.Stored > 0
			// skips are recorded from ruleset 11 on; an older ref has none to show
			if iv, ok := s.(IndexVersioner); ok {
				if v, ok := iv.IndexVersion(rp, eff); ok && v < 11 {
					c.Recorded = false
				}
			}
			for _, x := range sk {
				reason := x.Reason
				kind(coverageKind(x.Path)).Skipped[reason]++
				c.Skipped++
				c.Reasons[reason]++
				if len(c.Examples[reason]) < 3 {
					c.Examples[reason] = append(c.Examples[reason], x.Path)
				}
			}
		}
		for _, k := range kinds {
			if len(k.Skipped) == 0 {
				k.Skipped = nil
			}
			c.Kinds = append(c.Kinds, *k)
		}
		sort.Slice(c.Kinds, func(i, j int) bool {
			ti := c.Kinds[i].Stored + sumCounts(c.Kinds[i].Skipped)
			tj := c.Kinds[j].Stored + sumCounts(c.Kinds[j].Skipped)
			if ti != tj {
				return ti > tj
			}
			return c.Kinds[i].Kind < c.Kinds[j].Kind
		})
		res.Repos = append(res.Repos, c)
	}
	res.Note = "stored = files the index keeps (searchable; parsed or with sections where the format is known). " +
		"skipped = tracked files it does not keep, with the reason; a directory entry stands for everything under it. " +
		"A repo indexed before skips were recorded shows skips_recorded false — re-index it to see them."
	return res
}

func sumCounts(m map[string]int) int {
	n := 0
	for _, v := range m {
		n += v
	}
	return n
}
