//go:build treesitter

// intent_git.go is the thin git-blame adapter behind query.IntentProvider
// (architecture ext §8A.6 / ADR-017): given a symbol's file span it blames the
// lines, takes the most recent commit that touched them, and parses the
// PR/ticket linkage from that commit's message (query.ParseIntentMessage, pure).
// go-git is pure Go but rides the treesitter tag with the rest of the indexer
// (ADR-018). Every failure path degrades to (_, false) so brief simply omits the
// intent section rather than surfacing wrong provenance.
package processing

import (
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"

	"github.com/vishwak02/reponite/internal/query"
)

// GitIntent resolves change provenance from a repo's git history.
type GitIntent struct{ dir string }

// NewGitIntent returns a provider blaming against the repo at dir.
func NewGitIntent(dir string) *GitIntent { return &GitIntent{dir: dir} }

var _ query.IntentProvider = (*GitIntent)(nil)

// IntentFor blames path's [start,end] lines at HEAD and returns the linkage of
// the newest commit among them. path is repo-relative (as the indexer records).
func (g *GitIntent) IntentFor(repo, ref, qid, path string, start, end int) (query.IntentRecord, bool) {
	if path == "" || start <= 0 || end < start {
		return query.IntentRecord{}, false
	}
	r, err := git.PlainOpen(g.dir)
	if err != nil {
		return query.IntentRecord{}, false
	}
	head, err := r.Head()
	if err != nil {
		return query.IntentRecord{}, false
	}
	commit, err := r.CommitObject(head.Hash())
	if err != nil {
		return query.IntentRecord{}, false
	}
	blame, err := git.Blame(commit, path)
	if err != nil || len(blame.Lines) == 0 {
		return query.IntentRecord{}, false
	}

	hi := end
	if hi > len(blame.Lines) {
		hi = len(blame.Lines)
	}
	var newestHash plumbing.Hash
	var newest time.Time
	for i := start - 1; i < hi; i++ {
		ln := blame.Lines[i]
		if ln == nil {
			continue
		}
		if newestHash.IsZero() || ln.Date.After(newest) {
			newest, newestHash = ln.Date, ln.Hash
		}
	}
	if newestHash.IsZero() {
		return query.IntentRecord{}, false
	}
	mc, err := r.CommitObject(newestHash)
	if err != nil {
		return query.IntentRecord{}, false
	}
	return query.ParseIntentMessage(newestHash.String(), mc.Message), true
}
