// intent.go holds the pure half of intent linkage (architecture ext §8A.6 /
// ADR-017): extracting PR numbers and ticket ids from a commit message. This is
// the "why" behind a change, needed by brief and root-cause — and it needs only
// the *linkage* half of intent, no LLM. The git-blame lookup that finds the
// changing commit lives in a thin treesitter-tagged adapter; this parsing is
// pure and unit-tested in-sandbox (ADR-018).
package query

import (
	"regexp"
	"strings"
)

var (
	// GitHub PR/issue references: "#123", "(#123)", ".../pull/123".
	rePRRef = regexp.MustCompile(`(?:#|/pull/|/issues/)(\d+)`)
	// Jira-style ticket ids: "PROJ-123" (2-10 leading upper/alnum chars).
	reTicketRef = regexp.MustCompile(`\b[A-Z][A-Z0-9]{1,9}-\d+\b`)
)

// ParseIntentMessage extracts linkage (PR numbers, ticket ids) and the commit
// subject (first line) from a commit message. No LLM — pure regex over metadata.
func ParseIntentMessage(commit, msg string) IntentRecord {
	rec := IntentRecord{Commit: commit}
	seen := map[string]bool{}
	for _, m := range rePRRef.FindAllStringSubmatch(msg, -1) {
		if !seen["#"+m[1]] {
			seen["#"+m[1]] = true
			rec.PRs = append(rec.PRs, m[1])
		}
	}
	for _, m := range reTicketRef.FindAllString(msg, -1) {
		if !seen[m] {
			seen[m] = true
			rec.Tickets = append(rec.Tickets, m)
		}
	}
	if i := strings.IndexByte(msg, '\n'); i >= 0 {
		rec.Summary = strings.TrimSpace(msg[:i])
	} else {
		rec.Summary = strings.TrimSpace(msg)
	}
	return rec
}
