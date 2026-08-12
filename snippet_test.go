package main

import (
	"strings"
	"testing"
)

func TestSnippet(t *testing.T) {
	tests := []struct {
		name string
		row  selRow
		want string
	}{
		{
			name: "ticket with one PR",
			row: selRow{
				label:     "PROJ-17552",
				ticketKey: "PROJ-17552",
				summary:   "Mark the CI wait helper as manual-only",
				ticketURL: "https://your-org.atlassian.net/browse/PROJ-17552",
				prURLs:    []string{"https://github.com/acme/platform/pull/1099"},
			},
			want: "PROJ-17552 — Mark the CI wait helper as manual-only\n" +
				"https://your-org.atlassian.net/browse/PROJ-17552\n" +
				"https://github.com/acme/platform/pull/1099",
		},
		{
			name: "ticket with no PR omits the PR line entirely",
			row: selRow{
				label:     "PROJ-17453",
				ticketKey: "PROJ-17453",
				summary:   "Add a shared database registry",
				ticketURL: "https://your-org.atlassian.net/browse/PROJ-17453",
			},
			want: "PROJ-17453 — Add a shared database registry\n" +
				"https://your-org.atlassian.net/browse/PROJ-17453",
		},
		{
			name: "every PR is listed, not just the first",
			row: selRow{
				label:     "PROJ-200",
				ticketKey: "PROJ-200",
				summary:   "Two pull requests",
				ticketURL: "https://jira/browse/PROJ-200",
				prURLs: []string{
					"https://github.com/o/r/pull/2",
					"https://github.com/o/r/pull/9",
				},
			},
			want: "PROJ-200 — Two pull requests\n" +
				"https://jira/browse/PROJ-200\n" +
				"https://github.com/o/r/pull/2\n" +
				"https://github.com/o/r/pull/9",
		},
		{
			name: "PR with no ticket uses the repo and number as its label",
			row: selRow{
				label:   "sandbox #5",
				summary: "feat: Monorepo template with clean architecture",
				prURLs:  []string{"https://github.com/acme/sandbox/pull/5"},
			},
			want: "sandbox #5 — feat: Monorepo template with clean architecture\n" +
				"https://github.com/acme/sandbox/pull/5",
		},
		{
			name: "a missing summary does not leave a dangling dash",
			row: selRow{
				label:     "PROJ-1",
				ticketKey: "PROJ-1",
				ticketURL: "https://jira/browse/PROJ-1",
			},
			want: "PROJ-1\nhttps://jira/browse/PROJ-1",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.row.snippet()
			if got != tc.want {
				t.Errorf("snippet() =\n%q\nwant\n%q", got, tc.want)
			}
			// A trailing newline would paste as an extra blank line.
			if strings.HasSuffix(got, "\n") {
				t.Error("snippet() should not end in a newline")
			}
		})
	}
}

// The snippet is built from the same rows the cursor uses, so it has to survive
// the real correlation path rather than a hand-built struct.
func TestSnippetFromSettledRows(t *testing.T) {
	a := &app{
		tickets: []Ticket{
			{
				Key: "PROJ-1", Summary: "Live work", Status: "In Progress",
				Category: "In Progress", Type: "Task",
				URL: "https://jira/browse/PROJ-1",
			},
		},
		prs: []PullRequest{
			{Repo: "platform", Number: 10, Title: "PROJ-1: the change", URL: "https://gh/platform/pull/10"},
			{Repo: "solo", Number: 3, Title: "unrelated work", URL: "https://gh/solo/pull/3"},
		},
	}
	a.settle()

	if len(a.sel) != 2 {
		t.Fatalf("got %d selectable rows, want 2", len(a.sel))
	}

	wantTicket := "PROJ-1 — Live work\nhttps://jira/browse/PROJ-1\nhttps://gh/platform/pull/10"
	if got := a.sel[0].snippet(); got != wantTicket {
		t.Errorf("ticket row snippet =\n%q\nwant\n%q", got, wantTicket)
	}

	wantOrphan := "solo #3 — unrelated work\nhttps://gh/solo/pull/3"
	if got := a.sel[1].snippet(); got != wantOrphan {
		t.Errorf("orphan row snippet =\n%q\nwant\n%q", got, wantOrphan)
	}
}

func TestOpenAndPRTargets(t *testing.T) {
	ticket := selRow{ticketURL: "https://jira/browse/PROJ-1", prURLs: []string{"https://gh/pull/1"}}
	if got := ticket.openTarget(); got != "https://jira/browse/PROJ-1" {
		t.Errorf("openTarget() = %q, want the ticket", got)
	}
	if got := ticket.prTarget(); got != "https://gh/pull/1" {
		t.Errorf("prTarget() = %q, want the PR", got)
	}

	// On a row that is only a pull request, enter should still open something.
	orphan := selRow{prURLs: []string{"https://gh/pull/5"}}
	if got := orphan.openTarget(); got != "https://gh/pull/5" {
		t.Errorf("openTarget() = %q, want the PR as a fallback", got)
	}

	bare := selRow{}
	if got := bare.openTarget(); got != "" {
		t.Errorf("openTarget() = %q, want empty", got)
	}
	if got := bare.prTarget(); got != "" {
		t.Errorf("prTarget() = %q, want empty", got)
	}
}
