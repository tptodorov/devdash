package main

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
	"github.com/muesli/termenv"
)

const (
	// bgEscape is the SGR sequence for the dark-theme selection background, and
	// colEscape the contrasting one marking the column left/right is on.
	bgEscape  = "48;5;238"
	colEscape = "48;5;24"
	// boldEscape is how lipgloss opens a bold run.
	boldEscape = "\x1b[1;"
)

// countSGR reports how many styled runs in the line carry the given attribute,
// which is how these tests check the highlight reaches every part of a row.
func countSGR(line, attr string) int {
	return strings.Count(line, attr)
}

// lipgloss drops all colour when its output is not a terminal, which is exactly
// the case under go test. Pin a profile so the styling these tests are about is
// actually emitted.
func TestMain(m *testing.M) {
	lipgloss.SetColorProfile(termenv.ANSI256)
	lipgloss.SetHasDarkBackground(true)
	os.Exit(m.Run())
}

func selectionApp(width int) *app {
	a := &app{
		width:      width,
		hyperlinks: true,
		tickets: []Ticket{
			{
				Key: "PROJ-1", Summary: "A ticket with two pull requests",
				Status: "In Progress", Category: "In Progress", Type: "Task",
				URL: "https://jira/browse/PROJ-1",
			},
			{
				Key: "PROJ-2", Summary: "A ticket with none",
				Status: "In Progress", Category: "In Progress", Type: "Task",
				URL: "https://jira/browse/PROJ-2",
			},
		},
		prs: []PullRequest{
			{Repo: "platform", Number: 1, Title: "PROJ-1: first", URL: "https://gh/1", Review: "APPROVED"},
			{Repo: "platform", Number: 2, Title: "PROJ-1: second", URL: "https://gh/2"},
			{Repo: "solo", Number: 9, Title: "no ticket here", URL: "https://gh/9"},
		},
	}
	a.settle()
	return a
}

// The highlight must cover the whole row — ticket, connector and pull request —
// which means every rendered row line is exactly the terminal width.
func TestSelectedRowIsHighlightedEndToEnd(t *testing.T) {
	for _, width := range []int{80, 100, 118, 160} {
		a := selectionApp(width)
		lay := a.layout()

		a.cursor = 0
		body, rowLine := a.buildBody(lay)

		selected := body[rowLine[0]]
		if !strings.Contains(selected, bgEscape) {
			t.Errorf("width %d: selected row has no background: %q", width, selected)
		}
		if got := lipgloss.Width(selected); got != lay.width {
			t.Errorf("width %d: selected row is %d wide, want exactly %d", width, got, lay.width)
		}

		// Every styled run on the row is bold and painted, not just the ticket:
		// the PR reference at the far end must be emphasised too.
		bold := countSGR(selected, boldEscape)
		// Either background counts as painted: the active column uses the
		// brighter one.
		painted := countSGR(selected, bgEscape) + countSGR(selected, colEscape)
		if bold < 4 {
			t.Errorf("width %d: only %d bold runs on the selected row: %q", width, bold, selected)
		}
		if painted < bold {
			t.Errorf("width %d: %d bold runs but only %d painted", width, bold, painted)
		}
		if !strings.Contains(selected, "platform #1") {
			t.Fatalf("width %d: expected the PR on the selected row: %q", width, selected)
		}
		// The PR reference sits after the summary, so a bold run must open there.
		if idx := strings.Index(selected, "platform #1"); idx > 0 {
			if !strings.Contains(selected[:idx], boldEscape) {
				t.Errorf("width %d: PR reference is not bold: %q", width, selected)
			}
		}

		// The row below is a different ticket and must stay unpainted.
		other := body[rowLine[1]]
		if strings.Contains(other, bgEscape) {
			t.Errorf("width %d: unselected row is highlighted: %q", width, other)
		}
		if got := lipgloss.Width(other); got > lay.width {
			t.Errorf("width %d: unselected row is %d wide, over %d", width, got, lay.width)
		}
	}
}

// A ticket's second and later PRs are part of the same row, so they highlight
// with it.
func TestContinuationLinesHighlightWithTheirTicket(t *testing.T) {
	a := selectionApp(118)
	lay := a.layout()
	a.cursor = 0

	body, rowLine := a.buildBody(lay)
	continuation := body[rowLine[0]+1]

	if !strings.Contains(continuation, "#2") {
		t.Fatalf("expected the second PR on the line below, got %q", continuation)
	}
	if !strings.Contains(continuation, bgEscape) {
		t.Errorf("continuation line is not highlighted: %q", continuation)
	}
	if got := lipgloss.Width(continuation); got != lay.width {
		t.Errorf("continuation line is %d wide, want %d", got, lay.width)
	}
}

func TestSelectedOrphanRowIsHighlightedEndToEnd(t *testing.T) {
	a := selectionApp(118)
	lay := a.layout()

	// The orphan PR is the last selectable row.
	a.cursor = len(a.sel) - 1
	body, rowLine := a.buildBody(lay)

	row := body[rowLine[a.cursor]]
	if !strings.Contains(row, "solo #9") {
		t.Fatalf("expected the orphan PR row, got %q", row)
	}
	if !strings.Contains(row, bgEscape) {
		t.Errorf("selected orphan row is not highlighted: %q", row)
	}
	if got := lipgloss.Width(row); got != lay.width {
		t.Errorf("selected orphan row is %d wide, want %d", got, lay.width)
	}
}

// Nothing outside the selected row may be painted, and no row may overflow.
func TestNoRowOverflowsWithSelection(t *testing.T) {
	for _, width := range []int{80, 118} {
		a := selectionApp(width)
		lay := a.layout()
		for cursor := range a.sel {
			a.cursor = cursor
			body, _ := a.buildBody(lay)
			painted := 0
			for i, line := range body {
				if got := lipgloss.Width(line); got > lay.width {
					t.Errorf("width %d cursor %d: line %d is %d wide", width, cursor, i, got)
				}
				if strings.Contains(line, bgEscape) {
					painted++
				}
			}
			if painted == 0 {
				t.Errorf("width %d cursor %d: nothing highlighted", width, cursor)
			}
		}
	}
}

// The CI and review badges are the readable form of GitHub's own status values.
// Each is one glyph in a fixed slot, so they line up down the page, and each is
// blank when there is nothing to say.
func TestPRBadges(t *testing.T) {
	tests := []struct {
		name    string
		pr      PullRequest
		want    string
		notWant string
	}{
		{name: "checks passing", pr: PullRequest{CI: "SUCCESS"}, want: iconCheckPass},
		{name: "checks failing", pr: PullRequest{CI: "FAILURE"}, want: iconCheckFail},
		{name: "checks errored counts as failing", pr: PullRequest{CI: "ERROR"}, want: iconCheckFail},
		{name: "checks running", pr: PullRequest{CI: "PENDING"}, want: iconCheckRun},
		{name: "checks expected counts as running", pr: PullRequest{CI: "EXPECTED"}, want: iconCheckRun},
		{name: "no checks reported", pr: PullRequest{}, notWant: iconCheckPass},

		{name: "approved", pr: PullRequest{Review: "APPROVED"}, want: iconApproved},
		{name: "changes requested", pr: PullRequest{Review: "CHANGES_REQUESTED"}, want: iconChanges},
		{
			// Awaiting review is what a pull request is supposed to be, so the
			// slot stays empty: only a deviation from that earns ink.
			name: "awaiting review draws nothing",
			pr:   PullRequest{Review: "REVIEW_REQUIRED"}, notWant: iconApproved,
		},
		{name: "no review reported", pr: PullRequest{}, notWant: iconChanges},
		{
			// GitHub reports no decision when the base branch requires none, so
			// the approval count is what keeps a real approval visible. This is
			// real: platform #1103 had one approval and no decision.
			name: "approved with no decision reported",
			pr:   PullRequest{Approvals: 1}, want: iconApproved,
		},
		{
			name: "several approvals show the count",
			pr:   PullRequest{Review: "APPROVED", Approvals: 3}, want: iconApproved + "3",
		},
		{
			name: "changes requested outranks an approval",
			pr:   PullRequest{Review: "CHANGES_REQUESTED", Approvals: 1}, want: iconChanges,
		},
		// The old rev✓ / rev± / rev? vocabulary spent five columns a row on what
		// one glyph and a colour now carry.
		{name: "the rev prefix is gone", pr: PullRequest{Review: "APPROVED"}, notWant: "rev"},
		// The state is carried by colour and glyph shape, never spelled out.
		{name: "draft is not a word", pr: PullRequest{Draft: true}, notWant: "draft"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			pr.Repo, pr.Number = "r", 1

			var plain strings.Builder
			for _, s := range prSegs(pr, 34, true, 0) {
				plain.WriteString(s.text)
			}
			got := plain.String()

			if tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("prSegs = %q, want it to contain %q", got, tc.want)
			}
			if tc.notWant != "" && strings.Contains(got, tc.notWant) {
				t.Errorf("prSegs = %q, want %q absent", got, tc.notWant)
			}
		})
	}
}

// The cell leads with the state glyph: <state> repo #n <checks> <review>. Colour
// carries the state, and the glyph's shape carries only whether it is a draft.
func TestPRStateIconLeadsTheCell(t *testing.T) {
	tests := []struct {
		name string
		pr   PullRequest
		want string
	}{
		{"open", PullRequest{State: "OPEN"}, iconPR},
		{"draft", PullRequest{State: "OPEN", Draft: true}, iconPRDraft},
		{"merged", PullRequest{State: "MERGED"}, iconPR},
		{"closed", PullRequest{State: "CLOSED"}, iconPR},
		// A draft that was closed is still a draft in shape; the colour says closed.
		{"closed draft stays hollow", PullRequest{State: "CLOSED", Draft: true}, iconPRDraft},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			pr.Repo, pr.Number, pr.CI = "repo", 1, "SUCCESS"

			var plain strings.Builder
			for _, s := range prSegs(pr, 34, true, 0) {
				plain.WriteString(s.text)
			}
			got := plain.String()

			if !strings.HasPrefix(got, tc.want+" ") {
				t.Errorf("prSegs = %q, want it to start with %q", got, tc.want)
			}
			// The check glyph trails the reference, not the other way round.
			if strings.LastIndex(got, iconCheckPass) < strings.Index(got, "repo #1") {
				t.Errorf("check glyph should follow the reference: %q", got)
			}
		})
	}
}

// Archived goes after the badge slots, so a PR in an archived repository cannot
// push the checks and review glyphs out of the column they share with every
// other row.
func TestArchivedFollowsTheBadgeSlots(t *testing.T) {
	pr := PullRequest{Repo: "repo", Number: 1, State: "OPEN", CI: "SUCCESS", Archived: true}

	var plain strings.Builder
	for _, s := range prSegs(pr, 14, true, 0) {
		plain.WriteString(s.text)
	}
	got := plain.String()

	if !strings.Contains(got, "archived") {
		t.Fatalf("prSegs = %q, want it to mention archived", got)
	}
	if strings.Index(got, "archived") < strings.LastIndex(got, iconCheckPass) {
		t.Errorf("archived precedes the check slot, shifting it: %q", got)
	}
}

// The PR state is conveyed by the colour of its reference, so each state must
// render a distinct foreground. Verified against the real states of platform PRs:
// #1105 open, #1099 an open draft, #1071 a closed draft, #1083 merged.
func TestPRStateColours(t *testing.T) {
	tests := []struct {
		name string
		pr   PullRequest
		want string // ANSI 256 foreground code
	}{
		{name: "open", pr: PullRequest{State: "OPEN"}, want: "252"},
		{name: "open draft", pr: PullRequest{State: "OPEN", Draft: true}, want: "239"},
		{name: "merged", pr: PullRequest{State: "MERGED"}, want: "141"},
		{name: "closed", pr: PullRequest{State: "CLOSED"}, want: "203"},
		// Closed and merged outrank draft: a draft that was closed is closed.
		{name: "closed draft is closed", pr: PullRequest{State: "CLOSED", Draft: true}, want: "203"},
		{name: "merged draft is merged", pr: PullRequest{State: "MERGED", Draft: true}, want: "141"},
		// An unset state is treated as open rather than mislabelled.
		{name: "unknown state", pr: PullRequest{}, want: "252"},
	}

	seen := map[string][]string{}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			pr.Repo, pr.Number = "repo", 1
			rendered := segsRender(prSegs(pr, 34, true, 0), false)

			if !strings.Contains(rendered, "38;5;"+tc.want+"m") {
				t.Errorf("%s rendered %q, want foreground %s", tc.name, rendered, tc.want)
			}
			// The state must not be spelled out; colour carries it now.
			if strings.Contains(rendered, "draft") {
				t.Errorf("%s still prints the word draft: %q", tc.name, rendered)
			}
			seen[tc.want] = append(seen[tc.want], tc.name)
		})
	}

	// Open, draft, merged and closed must not collapse onto one colour.
	if len(seen) != 4 {
		t.Errorf("expected 4 distinct state colours, got %d: %v", len(seen), seen)
	}
}

// Every indicator must be exactly one display column. A glyph the width tables
// consider wide would silently shift the PR column on every row, and since the
// whole design is now plain Unicode there is no Nerd Font fallback to hide it.
func TestEveryIndicatorIsOneColumn(t *testing.T) {
	for name, g := range map[string]string{
		"pr":         iconPR,
		"pr draft":   iconPRDraft,
		"check pass": iconCheckPass,
		"check fail": iconCheckFail,
		"check run":  iconCheckRun,
		"approved":   iconApproved,
		"changes":    iconChanges,
		"symphony":   iconSymphony,
		"alert":      iconAlert,
		"child":      iconChild,
		"subtask":    iconSubtask,
		"branch":     prBranch,
		"cursor":     selBar,
	} {
		if w := runewidth.StringWidth(g); w != 1 {
			t.Errorf("%s indicator %q is %d columns, want 1", name, g, w)
		}
	}
}

// Nothing devdash draws may need a patched font: a private-use codepoint would
// render as a blank box for anyone without one.
func TestNoPrivateUseCodepoints(t *testing.T) {
	var plain strings.Builder
	for _, pr := range []PullRequest{
		{Repo: "repo", Number: 1, State: "OPEN", CI: "SUCCESS", Review: "APPROVED"},
		{Repo: "repo", Number: 2, State: "OPEN", Draft: true, CI: "FAILURE"},
		{Repo: "repo", Number: 3, State: "MERGED", CI: "PENDING", Review: "CHANGES_REQUESTED"},
		{Repo: "repo", Number: 4, State: "CLOSED", Archived: true},
	} {
		for _, s := range prSegs(pr, 34, true, 0) {
			plain.WriteString(s.text)
		}
	}
	for _, state := range []string{
		SymphonyScheduled, SymphonyRunning, SymphonyRetrying, SymphonyBlocked,
	} {
		marker, _ := symphonyMarker(state)
		plain.WriteString(marker)
	}
	for _, a := range []attention{attnNone, attnMerge, attnChanges, attnAlert} {
		marker, _ := a.marker()
		plain.WriteString(marker)
	}

	for _, r := range plain.String() {
		if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 {
			t.Errorf("indicator set contains private-use rune U+%04X", r)
		}
	}
}
