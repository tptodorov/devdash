package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/mattn/go-runewidth"
)

// columnApp mirrors the demo row that exercises every stop: a task with
// children, two pull requests and a Symphony session.
func columnApp() *app {
	a := &app{
		width:       118,
		symphonyURL: "http://127.0.0.1:10000",
		tickets: []Ticket{
			{
				Key: "PROJ-455", Summary: "Add a shared database registry",
				Status: "In Progress", Category: "In Progress", Type: "Task",
				ChildCount: 4, Symphony: SymphonyRunning,
				URL: "https://jira.test/browse/PROJ-455",
			},
			{
				Key: "PROJ-460", Summary: "No children, no PR, no Symphony",
				Status: "In Progress", Category: "In Progress", Type: "Task",
				URL: "https://jira.test/browse/PROJ-460",
			},
		},
		prs: []PullRequest{
			{Repo: "platform", Number: 1088, Title: "PROJ-455: first", URL: "https://gh.test/1088", State: "MERGED"},
			{Repo: "platform", Number: 1091, Title: "PROJ-455: second", URL: "https://gh.test/1091", State: "OPEN"},
			{Repo: "sandbox", Number: 5, Title: "no ticket", URL: "https://gh.test/5", State: "OPEN"},
		},
	}
	a.settle()
	return a
}

// Stops exist only where the column does, so left/right never lands somewhere
// that would do nothing.
func TestRowStops(t *testing.T) {
	a := columnApp()

	rich := a.sel[0]
	if rich.ticketKey != "PROJ-455" {
		t.Fatalf("expected PROJ-455 first, got %q", rich.ticketKey)
	}
	wantKinds := []string{stopSymphony, stopRelation, stopTicket, stopPR, stopPR}
	if len(rich.stops) != len(wantKinds) {
		t.Fatalf("PROJ-455 has %d stops, want %d: %+v", len(rich.stops), len(wantKinds), rich.stops)
	}
	for i, want := range wantKinds {
		if rich.stops[i].kind != want {
			t.Errorf("stop %d = %q, want %q", i, rich.stops[i].kind, want)
		}
	}

	// Each stop opens something distinct. The order is the order the columns are
	// drawn, left to right, so left/right walks the row the way it reads.
	if got := rich.stops[0].url; got != "http://127.0.0.1:10000" {
		t.Errorf("Symphony stop opens %q", got)
	}
	if got := rich.stops[1].url; !strings.Contains(got, "/issues/?jql=") || !strings.Contains(got, "PROJ-455") {
		t.Errorf("relation stop opens %q, want a JIRA search for its children", got)
	}
	if got := rich.stops[2].url; got != "https://jira.test/browse/PROJ-455" {
		t.Errorf("ticket stop opens %q", got)
	}
	if rich.stops[3].url != "https://gh.test/1088" || rich.stops[4].url != "https://gh.test/1091" {
		t.Errorf("PR stops open %q and %q", rich.stops[3].url, rich.stops[4].url)
	}

	// A bare ticket has only the one stop.
	if bare := a.sel[1]; len(bare.stops) != 1 || bare.stops[0].kind != stopTicket {
		t.Errorf("PROJ-460 stops = %+v, want just the ticket", bare.stops)
	}
	// A pull request with no ticket is its own single stop.
	if orphan := a.sel[len(a.sel)-1]; len(orphan.stops) != 1 || orphan.stops[0].kind != stopPR {
		t.Errorf("orphan stops = %+v, want just the PR", orphan.stops)
	}
}

// A ticket's later pull requests are on their own lines, which carry no ticket
// of their own. The branch connector sits immediately left of the pull request
// cell, and the cell starts in the same column as the first PR's, which is what
// lets a column of pull request states be read straight down the page.
func TestContinuationLinesAreConnectedToTheirTicket(t *testing.T) {
	a := columnApp()
	a.cursor = -1 // unhighlighted, so the columns are easy to measure
	lay := a.layout()

	body, rowLine := a.buildBody(lay)
	ticketLine, continuation := body[rowLine[0]], body[rowLine[0]+1]

	if !strings.Contains(continuation, "#1091") {
		t.Fatalf("expected the second PR on the continuation line, got %q", continuation)
	}

	branch := connectorColumn(t, continuation, prBranch)
	if want := lay.prColumn() - 2; branch != want {
		t.Errorf("branch connector is at column %d, want %d", branch, want)
	}

	// The reference sits two columns into the cell, after the state glyph.
	first := connectorColumn(t, ticketLine, "platform #1088")
	second := connectorColumn(t, continuation, "platform #1091")
	if first != second {
		t.Errorf("first PR reference is at column %d but the second is at %d; "+
			"they must line up", first, second)
	}
	if want := lay.prColumn() + 2; first != want {
		t.Errorf("PR reference is at column %d, want %d", first, want)
	}
}

// The pull request title is what fills the row out to the terminal's edge, in a
// column of its own so it lines up across rows and continuation lines alike.
func TestPRTitlesFillTheRow(t *testing.T) {
	a := columnApp()
	a.cursor = -1
	lay := a.layout()
	if lay.title == 0 {
		t.Fatalf("no room for a title at width %d; the case is untested", a.width)
	}

	body, rowLine := a.buildBody(lay)
	ticketLine, continuation := body[rowLine[0]], body[rowLine[0]+1]

	// The key the title conventionally opens with is already on the row, so it
	// is dropped rather than spending the widest column on a repeat.
	for _, line := range []string{ticketLine, continuation} {
		if strings.Contains(ansi.Strip(line), "PROJ-455:") {
			t.Errorf("title repeats the ticket key: %q", ansi.Strip(line))
		}
	}
	first := titleColumn(t, ticketLine, "first")
	second := titleColumn(t, continuation, "second")
	if first != second {
		t.Errorf("titles start at columns %d and %d; they must line up", first, second)
	}
	if want := lay.prColumn() + lay.pr + 2; first != want {
		t.Errorf("title starts at column %d, want %d", first, want)
	}
}

func TestPRTitleDropsTheTicketKey(t *testing.T) {
	tests := []struct {
		pr   PullRequest
		want string
	}{
		{PullRequest{Ticket: "PROJ-455", Title: "PROJ-455: registry groundwork"}, "registry groundwork"},
		{PullRequest{Ticket: "PROJ-455", Title: "PROJ-455 wire the registry in"}, "wire the registry in"},
		{PullRequest{Ticket: "PROJ-455", Title: "proj-455 — lower case"}, "lower case"},
		// Nothing to drop: no ticket, the key elsewhere, or the key is all there is.
		{PullRequest{Title: "Monorepo template"}, "Monorepo template"},
		{PullRequest{Ticket: "PROJ-455", Title: "fix the PROJ-455 registry"}, "fix the PROJ-455 registry"},
		{PullRequest{Ticket: "PROJ-455", Title: "PROJ-455"}, "PROJ-455"},
	}
	for _, tc := range tests {
		if got := prTitle(tc.pr); got != tc.want {
			t.Errorf("prTitle(%q) = %q, want %q", tc.pr.Title, got, tc.want)
		}
	}
}

// titleColumn reports the display column a PR title starts at, found by a word
// from the title itself.
func titleColumn(t *testing.T, line, word string) int {
	t.Helper()
	plain := ansi.Strip(line)
	i := strings.Index(plain, word)
	if i < 0 {
		t.Fatalf("no title containing %q in %q", word, plain)
	}
	return runewidth.StringWidth(plain[:i])
}

// connectorColumn reports the display column a connector glyph sits at, with any
// styling and hyperlinks stripped out.
func connectorColumn(t *testing.T, line, glyph string) int {
	t.Helper()
	plain := ansi.Strip(line)
	i := strings.Index(plain, glyph)
	if i < 0 {
		t.Fatalf("no %q connector in %q", glyph, plain)
	}
	return runewidth.StringWidth(plain[:i])
}

func TestChildrenSearchURL(t *testing.T) {
	tests := []struct {
		browse, key, want string
	}{
		{
			"https://your-org.atlassian.net/browse/PROJ-455", "PROJ-455",
			"https://your-org.atlassian.net/issues/?jql=parent+%3D+PROJ-455",
		},
		// Nothing sensible to build from a URL that is not a browse link.
		{"https://example.test/PROJ-1", "PROJ-1", ""},
		{"", "PROJ-1", ""},
	}
	for _, tc := range tests {
		if got := childrenSearchURL(tc.browse, tc.key); got != tc.want {
			t.Errorf("childrenSearchURL(%q) = %q, want %q", tc.browse, got, tc.want)
		}
	}
}

func TestColumnNavigation(t *testing.T) {
	a := columnApp()
	a.cursor, a.col = 0, 0

	// Right walks forward and stops at the end rather than wrapping.
	for want := 1; want <= 4; want++ {
		a.moveColumn(1)
		if a.col != want {
			t.Fatalf("after %d rights col = %d, want %d", want, a.col, want)
		}
	}
	a.moveColumn(1)
	if a.col != 4 {
		t.Errorf("col = %d, want it to stay on the last stop", a.col)
	}

	// Left walks back and stops at the first.
	for i := 0; i < 10; i++ {
		a.moveColumn(-1)
	}
	if a.col != 0 {
		t.Errorf("col = %d, want 0", a.col)
	}

	// Moving to another row starts again on the ticket.
	a.col = 3
	a.move(1)
	if a.col != 0 {
		t.Errorf("col = %d after changing row, want 0", a.col)
	}

	// A row with one stop cannot move off it.
	a.moveColumn(1)
	if a.col != 0 {
		t.Errorf("col = %d on a single-stop row, want 0", a.col)
	}
}

// enter acts on the column, so activeStop has to follow left/right.
func TestActiveStopFollowsTheColumn(t *testing.T) {
	a := columnApp()
	a.cursor, a.col = 0, 0

	for i, wantKind := range []string{stopSymphony, stopRelation, stopTicket, stopPR, stopPR} {
		a.col = i
		stop, ok := a.activeStop()
		if !ok {
			t.Fatalf("no active stop at col %d", i)
		}
		if stop.kind != wantKind {
			t.Errorf("col %d is %q, want %q", i, stop.kind, wantKind)
		}
	}

	// An out-of-range column is clamped rather than panicking. The last stop is
	// now the row's final pull request, since the PRs are drawn rightmost.
	a.col = 99
	if stop, ok := a.activeStop(); !ok || stop.kind != stopPR {
		t.Errorf("clamped stop = %+v ok=%v", stop, ok)
	}
}

// The active column is painted in a contrasting colour, not the row's grey.
func TestActiveColumnIsPainted(t *testing.T) {
	a := columnApp()
	lay := a.layout()
	a.cursor = 0

	// Symphony, relation, ticket and the first PR are all on the ticket line.
	for _, col := range []int{0, 1, 2, 3} {
		a.col = col
		body, rowLine := a.buildBody(lay)
		row := body[rowLine[0]]

		if !strings.Contains(row, colEscape) {
			t.Errorf("col %d: no active-column background on the row", col)
		}
		if !strings.Contains(row, bgEscape) {
			t.Errorf("col %d: the rest of the row lost its highlight", col)
		}
	}

	// The second PR lives on the continuation line, so selecting it must paint
	// that line and not the ticket line.
	a.col = 4
	body, rowLine := a.buildBody(lay)
	ticketLine, continuation := body[rowLine[0]], body[rowLine[0]+1]
	if !strings.Contains(continuation, "#1091") {
		t.Fatalf("expected the second PR on the continuation line: %q", continuation)
	}
	if !strings.Contains(continuation, colEscape) {
		t.Error("selecting the second PR did not paint the continuation line")
	}
	if strings.Contains(ticketLine, colEscape) {
		t.Error("the ticket line should carry no active column when a later PR is selected")
	}
}

// Nothing may overflow once a column is highlighted.
func TestColumnHighlightDoesNotOverflow(t *testing.T) {
	for _, width := range []int{80, 118, 160} {
		a := columnApp()
		a.width = width
		lay := a.layout()
		for row := range a.sel {
			a.cursor = row
			for col := range a.sel[row].stops {
				a.col = col
				body, _ := a.buildBody(lay)
				for i, line := range body {
					if got := lipgloss.Width(line); got > lay.width {
						t.Errorf("width %d row %d col %d: line %d is %d wide",
							width, row, col, i, got)
					}
				}
			}
		}
	}
}
