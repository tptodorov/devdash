package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
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
	wantKinds := []string{stopTicket, stopChildren, stopPR, stopPR, stopSymphony}
	if len(rich.stops) != len(wantKinds) {
		t.Fatalf("PROJ-455 has %d stops, want %d: %+v", len(rich.stops), len(wantKinds), rich.stops)
	}
	for i, want := range wantKinds {
		if rich.stops[i].kind != want {
			t.Errorf("stop %d = %q, want %q", i, rich.stops[i].kind, want)
		}
	}

	// Each stop opens something distinct.
	if got := rich.stops[0].url; got != "https://jira.test/browse/PROJ-455" {
		t.Errorf("ticket stop opens %q", got)
	}
	if got := rich.stops[1].url; !strings.Contains(got, "/issues/?jql=") || !strings.Contains(got, "PROJ-455") {
		t.Errorf("children stop opens %q, want a JIRA search for its children", got)
	}
	if rich.stops[2].url != "https://gh.test/1088" || rich.stops[3].url != "https://gh.test/1091" {
		t.Errorf("PR stops open %q and %q", rich.stops[2].url, rich.stops[3].url)
	}
	if got := rich.stops[4].url; got != "http://127.0.0.1:10000" {
		t.Errorf("Symphony stop opens %q", got)
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

	for i, wantKind := range []string{stopTicket, stopChildren, stopPR, stopPR, stopSymphony} {
		a.col = i
		stop, ok := a.activeStop()
		if !ok {
			t.Fatalf("no active stop at col %d", i)
		}
		if stop.kind != wantKind {
			t.Errorf("col %d is %q, want %q", i, stop.kind, wantKind)
		}
	}

	// An out-of-range column is clamped rather than panicking.
	a.col = 99
	if stop, ok := a.activeStop(); !ok || stop.kind != stopSymphony {
		t.Errorf("clamped stop = %+v ok=%v", stop, ok)
	}
}

// The active column is painted a shade brighter than the rest of the row.
func TestActiveColumnIsPaintedBrighter(t *testing.T) {
	a := columnApp()
	lay := a.layout()
	a.cursor = 0

	for _, col := range []int{0, 1, 2, 4} {
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
	a.col = 3
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
