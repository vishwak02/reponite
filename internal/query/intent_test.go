package query_test

import (
	"testing"

	"github.com/vishwak02/reponite/internal/query"
)

func TestParseIntentMessage(t *testing.T) {
	rec := query.ParseIntentMessage("abc123",
		"Fix charge rounding (#42)\n\nRefs JIRA-100 and BILLING-7; see also #42 and github.com/x/y/pull/99")
	if rec.Commit != "abc123" {
		t.Fatalf("commit = %q", rec.Commit)
	}
	if rec.Summary != "Fix charge rounding (#42)" {
		t.Fatalf("summary = %q", rec.Summary)
	}
	// PRs deduped (#42 appears twice); /pull/99 also captured.
	if len(rec.PRs) != 2 || rec.PRs[0] != "42" || rec.PRs[1] != "99" {
		t.Fatalf("prs = %v", rec.PRs)
	}
	if len(rec.Tickets) != 2 || rec.Tickets[0] != "JIRA-100" || rec.Tickets[1] != "BILLING-7" {
		t.Fatalf("tickets = %v", rec.Tickets)
	}
}

func TestParseIntentMessageNoLinkage(t *testing.T) {
	rec := query.ParseIntentMessage("h", "tidy imports")
	if rec.Summary != "tidy imports" || len(rec.PRs) != 0 || len(rec.Tickets) != 0 {
		t.Fatalf("unexpected linkage: %+v", rec)
	}
}
