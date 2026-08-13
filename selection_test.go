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
	// bgEscape is the SGR sequence for the dark-theme selection background.
	bgEscape = "48;5;238"
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
		bold, painted := countSGR(selected, boldEscape), countSGR(selected, bgEscape)
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
func TestPRBadges(t *testing.T) {
	tests := []struct {
		name    string
		pr      PullRequest
		want    string
		notWant string
	}{
		{name: "checks passing", pr: PullRequest{CI: "SUCCESS"}, want: nerdCheckIcons.success},
		{name: "checks failing", pr: PullRequest{CI: "FAILURE"}, want: nerdCheckIcons.failure},
		{name: "checks errored counts as failing", pr: PullRequest{CI: "ERROR"}, want: nerdCheckIcons.failure},
		{name: "checks running", pr: PullRequest{CI: "PENDING"}, want: nerdCheckIcons.pending},
		{name: "checks expected counts as running", pr: PullRequest{CI: "EXPECTED"}, want: nerdCheckIcons.pending},
		{name: "no checks reported", pr: PullRequest{}, notWant: nerdCheckIcons.success},

		{name: "approved", pr: PullRequest{Review: "APPROVED"}, want: "rev✓"},
		{name: "changes requested", pr: PullRequest{Review: "CHANGES_REQUESTED"}, want: "rev±"},
		{name: "awaiting review", pr: PullRequest{Review: "REVIEW_REQUIRED"}, want: "rev?"},
		{name: "no review reported", pr: PullRequest{}, notWant: "rev"},
		{
			// GitHub reports no decision when the base branch requires none, so
			// the approval count is what keeps a real approval visible. This is
			// real: platform #1103 had one approval and no decision.
			name: "approved with no decision reported",
			pr:   PullRequest{Approvals: 1}, want: "rev✓",
		},
		{
			name: "several approvals show the count",
			pr:   PullRequest{Review: "APPROVED", Approvals: 3}, want: "rev✓3",
		},
		{
			name: "changes requested outranks an approval",
			pr:   PullRequest{Review: "CHANGES_REQUESTED", Approvals: 1}, want: "rev±",
		},
		// The state is carried by colour, never spelled out.
		{name: "draft is not a word", pr: PullRequest{Draft: true}, notWant: "draft"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			pr.Repo, pr.Number = "r", 1

			var plain strings.Builder
			for _, s := range prSegs(pr, 34) {
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

// The cell leads with the state icon, as workmux does: <state> repo #n <checks>.
func TestPRStateIconLeadsTheCell(t *testing.T) {
	tests := []struct {
		name string
		pr   PullRequest
		want string
	}{
		{"open", PullRequest{State: "OPEN"}, nerdPRIcons.open},
		{"draft", PullRequest{State: "OPEN", Draft: true}, nerdPRIcons.draft},
		{"merged", PullRequest{State: "MERGED"}, nerdPRIcons.merged},
		{"closed", PullRequest{State: "CLOSED"}, nerdPRIcons.closed},
		{"closed draft reads as closed", PullRequest{State: "CLOSED", Draft: true}, nerdPRIcons.closed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pr := tc.pr
			pr.Repo, pr.Number, pr.CI = "repo", 1, "SUCCESS"

			var plain strings.Builder
			for _, s := range prSegs(pr, 34) {
				plain.WriteString(s.text)
			}
			got := plain.String()

			if !strings.HasPrefix(got, tc.want+" ") {
				t.Errorf("prSegs = %q, want it to start with %q", got, tc.want)
			}
			// The check icon trails the reference, not the other way round.
			if strings.Index(got, nerdCheckIcons.success) < strings.Index(got, "repo #1") {
				t.Errorf("check icon should follow the reference: %q", got)
			}
		})
	}

	// Every icon must be one display column, or every row shifts.
	for name, g := range map[string]string{
		"open": nerdPRIcons.open, "draft": nerdPRIcons.draft,
		"merged": nerdPRIcons.merged, "closed": nerdPRIcons.closed,
		"success": nerdCheckIcons.success, "failure": nerdCheckIcons.failure,
		"pending": nerdCheckIcons.pending,
		"fb-open": fallbackPRIcons.open, "fb-draft": fallbackPRIcons.draft,
		"fb-merged": fallbackPRIcons.merged, "fb-closed": fallbackPRIcons.closed,
		"fb-success": fallbackCheckIcons.success, "fb-failure": fallbackCheckIcons.failure,
		"fb-pending": fallbackCheckIcons.pending,
	} {
		if w := runewidth.StringWidth(g); w != 1 {
			t.Errorf("%s icon %q is %d columns, want 1", name, g, w)
		}
	}
}

// With nerd fonts off, the plain-Unicode set is used instead. Terminals without
// a patched font would otherwise draw every icon as a blank box.
func TestFallbackIconsWhenNerdFontDisabled(t *testing.T) {
	useNerdFont = false
	t.Cleanup(func() { useNerdFont = true })

	var plain strings.Builder
	for _, s := range prSegs(PullRequest{Repo: "repo", Number: 1, State: "MERGED", CI: "FAILURE"}, 34) {
		plain.WriteString(s.text)
	}
	got := plain.String()

	if !strings.Contains(got, fallbackPRIcons.merged) {
		t.Errorf("prSegs = %q, want the fallback merged icon %q", got, fallbackPRIcons.merged)
	}
	if !strings.Contains(got, fallbackCheckIcons.failure) {
		t.Errorf("prSegs = %q, want the fallback failure icon %q", got, fallbackCheckIcons.failure)
	}
	// No private-use codepoints may survive with nerd fonts off.
	for _, r := range got {
		if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 {
			t.Errorf("prSegs = %q, contains private-use rune U+%04X", got, r)
		}
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
			rendered := segsRender(prSegs(pr, 34), false)

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
// consider wide would silently shift the PR column on every row.

// The block is always three characters, whatever the PR's state.

// The old words must be gone, and the block must be far shorter than they were.

// "devdash help -no-nerd-font" must describe the plain set, not the glyphs it is
// being asked to avoid. Help runs before flag.Parse, so this is easy to get wrong.
func TestHelpHonoursNoNerdFontFlag(t *testing.T) {
	t.Cleanup(func() { useNerdFont = true })

	useNerdFont = true
	applyHelpFlags([]string{"-no-nerd-font"})
	if useNerdFont {
		t.Error("applyHelpFlags did not turn nerd fonts off")
	}

	useNerdFont = true
	applyHelpFlags([]string{"--no-nerd-font"})
	if useNerdFont {
		t.Error("applyHelpFlags ignored the double-dash form")
	}

	useNerdFont = true
	applyHelpFlags([]string{"-once", "-all-repos"})
	if !useNerdFont {
		t.Error("applyHelpFlags turned nerd fonts off for an unrelated flag")
	}
}
