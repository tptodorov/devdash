package main

import (
	"fmt"
	"slices"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTicketKeyFor(t *testing.T) {
	tests := []struct {
		name string
		pr   PullRequest
		want string
	}{
		{
			name: "key in title",
			pr:   PullRequest{Title: "PROJ-17506: Migrate the live stores", Branch: "some-branch"},
			want: "PROJ-17506",
		},
		{
			name: "title wins over branch",
			pr:   PullRequest{Title: "PROJ-17552: fix", Branch: "PROJ-9999-other"},
			want: "PROJ-17552",
		},
		{
			name: "falls back to branch",
			pr:   PullRequest{Title: "fix the thing", Branch: "PROJ-17538-database-identifiers"},
			want: "PROJ-17538",
		},
		{
			name: "bare branch key",
			pr:   PullRequest{Title: "chore: tidy", Branch: "PROJ-17506"},
			want: "PROJ-17506",
		},
		{
			name: "project with digits",
			pr:   PullRequest{Title: "TEAM-205732 platform work"},
			want: "TEAM-205732",
		},
		{
			name: "no key anywhere",
			pr:   PullRequest{Title: "fix postgres error", Branch: "fix-u0000"},
			want: "",
		},
		{
			name: "lowercase is not a key",
			pr:   PullRequest{Title: "proj-123: nope", Branch: "topic"},
			want: "",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ticketKeyFor(tc.pr); got != tc.want {
				t.Errorf("ticketKeyFor() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildCorrelatesAndGroups(t *testing.T) {
	tickets := []Ticket{
		{Key: "PROJ-100", Status: "Backlog", Category: "To Do"},
		{Key: "PROJ-200", Status: "Awaiting CR", Category: "In Progress"},
		{Key: "PROJ-300", Status: "In Progress", Category: "In Progress"},
		{Key: "PROJ-400", Status: "To Do", Category: "To Do"},
	}
	prs := []PullRequest{
		{Repo: "platform", Number: 9, Title: "PROJ-200: second pr"},
		{Repo: "platform", Number: 2, Title: "PROJ-200: first pr"},
		{Repo: "platform", Number: 3, Title: "no key here", Branch: "PROJ-300-work"},
		{Repo: "other", Number: 7, Title: "PROJ-999: not an active ticket"},
		{Repo: "toolbox", Number: 5, Title: "fix a bug", Branch: "fix-u0000"},
	}

	groups, orphans := build(tickets, prs)

	// Groups are ordered most-urgent first: In Progress category before To Do,
	// and within it review before active development.
	wantOrder := []string{"Awaiting CR", "In Progress", "To Do", "Backlog"}
	if len(groups) != len(wantOrder) {
		t.Fatalf("got %d groups, want %d", len(groups), len(wantOrder))
	}
	for i, want := range wantOrder {
		if groups[i].Status != want {
			t.Errorf("group %d = %q, want %q", i, groups[i].Status, want)
		}
	}

	byKey := map[string]Ticket{}
	for _, g := range groups {
		for _, tk := range g.Tickets {
			byKey[tk.Key] = tk
		}
	}

	// Two PRs attach to PROJ-200 and are ordered by number, not input order.
	got200 := byKey["PROJ-200"].PRs
	if len(got200) != 2 {
		t.Fatalf("PROJ-200 got %d PRs, want 2", len(got200))
	}
	if got200[0].Number != 2 || got200[1].Number != 9 {
		t.Errorf("PROJ-200 PR order = %d,%d, want 2,9", got200[0].Number, got200[1].Number)
	}

	// A PR whose key only appears in the branch still links.
	if got := byKey["PROJ-300"].PRs; len(got) != 1 || got[0].Number != 3 {
		t.Errorf("PROJ-300 PRs = %+v, want just #3", got)
	}

	if got := byKey["PROJ-400"].PRs; len(got) != 0 {
		t.Errorf("PROJ-400 should have no PRs, got %+v", got)
	}

	// PRs with an unknown key or no key at all stay visible as orphans.
	if len(orphans) != 2 {
		t.Fatalf("got %d orphans, want 2", len(orphans))
	}
	if orphans[0].Repo != "other" || orphans[1].Repo != "toolbox" {
		t.Errorf("orphans = %q,%q, want other,toolbox", orphans[0].Repo, orphans[1].Repo)
	}
}

// A key is only guaranteed unique within its own tracker: a JIRA project and
// a Linear team can plausibly issue the same short code (e.g. during a
// migration), so a PR matching that key must attach to every ticket sharing
// it rather than silently overwriting one tracker's ticket with the other's.
func TestBuildAttachesPRToEveryTicketSharingAKeyAcrossTrackers(t *testing.T) {
	tickets := []Ticket{
		{Key: "ENG-1", Source: "JIRA", Status: "To Do", Category: "To Do"},
		{Key: "ENG-1", Source: "Linear", Status: "To Do", Category: "To Do"},
	}
	prs := []PullRequest{{Repo: "platform", Number: 1, Title: "ENG-1: shared code"}}

	groups, orphans := build(tickets, prs)
	if len(orphans) != 0 {
		t.Fatalf("got %d orphans, want 0", len(orphans))
	}

	var jiraPRs, linearPRs int
	for _, g := range groups {
		for _, tk := range g.Tickets {
			switch tk.Source {
			case "JIRA":
				jiraPRs = len(tk.PRs)
			case "Linear":
				linearPRs = len(tk.PRs)
			}
		}
	}
	if jiraPRs != 1 {
		t.Errorf("JIRA ticket got %d PRs, want 1 (collision must not drop it)", jiraPRs)
	}
	if linearPRs != 1 {
		t.Errorf("Linear ticket got %d PRs, want 1 (collision must not drop it)", linearPRs)
	}
}

func TestBuildTreatsTicketKeyCaseInsensitively(t *testing.T) {
	tickets := []Ticket{{Key: "proj-42", Status: "To Do", Category: "To Do"}}
	prs := []PullRequest{{Repo: "platform", Number: 1, Title: "PROJ-42: work"}}

	groups, orphans := build(tickets, prs)
	if len(orphans) != 0 {
		t.Fatalf("got %d orphans, want 0", len(orphans))
	}
	if got := groups[0].Tickets[0].PRs; len(got) != 1 {
		t.Errorf("expected the PR to link despite key case, got %+v", got)
	}
}

func TestDropArchived(t *testing.T) {
	prs := []PullRequest{
		{Repo: "platform", Number: 1},
		{Repo: "legacy-service", Number: 188, Archived: true},
		{Repo: "sandbox", Number: 5},
		{Repo: "toolbox", Number: 5, Archived: true},
	}

	got := dropArchived(prs, false)
	if len(got) != 2 {
		t.Fatalf("got %d PRs, want 2", len(got))
	}
	for _, pr := range got {
		if pr.Archived {
			t.Errorf("%s #%d from an archived repo survived the filter", pr.Repo, pr.Number)
		}
	}
	if got[0].Repo != "platform" || got[1].Repo != "sandbox" {
		t.Errorf("kept %q,%q, want platform,sandbox", got[0].Repo, got[1].Repo)
	}

	if all := dropArchived(prs, true); len(all) != 4 {
		t.Errorf("with include=true got %d PRs, want all 4", len(all))
	}
}

// Merged PRs are fetched so an active ticket still shows the PR that closed its
// work — PROJ-17506 kept #1105 after it merged. One matching no active ticket is
// finished work and would bury the open PRs that still need attention.
func TestMergedPRsOnlyShowOnActiveTickets(t *testing.T) {
	tickets := []Ticket{
		{Key: "PROJ-17506", Status: "In Review", Category: "In Progress"},
	}
	prs := []PullRequest{
		{Repo: "platform", Number: 1105, Title: "PROJ-17506: merged, on an active ticket", State: "MERGED"},
		{Repo: "platform", Number: 1070, Title: "PROJ-17454: merged, ticket already done", State: "MERGED"},
		{Repo: "platform", Number: 1099, Title: "PROJ-99999: open, no active ticket", State: "OPEN"},
		{Repo: "solo", Number: 3, Title: "open with no key at all", State: "OPEN"},
	}

	groups, orphans := build(tickets, prs)

	// The merged PR stays attached to its ticket.
	attached := groups[0].Tickets[0].PRs
	if len(attached) != 1 || attached[0].Number != 1105 {
		t.Errorf("PROJ-17506 PRs = %+v, want just #1105", attached)
	}

	// Open PRs with no ticket remain visible; the stray merged one does not.
	var numbers []int
	for _, pr := range orphans {
		numbers = append(numbers, pr.Number)
	}
	if !slices.Equal(numbers, []int{1099, 3}) && !slices.Equal(numbers, []int{3, 1099}) {
		t.Errorf("orphans = %v, want the two open PRs only", numbers)
	}
	for _, pr := range orphans {
		if pr.State == "MERGED" {
			t.Errorf("merged PR #%d with no active ticket should not be listed", pr.Number)
		}
	}
}

// The header count must match the rows, or dropping merged PRs makes it lie.
func TestPRCountMatchesWhatIsShown(t *testing.T) {
	a := &app{
		tickets: []Ticket{{Key: "PROJ-1", Status: "To Do", Category: "To Do", Type: "Task"}},
		prs: []PullRequest{
			{Repo: "platform", Number: 1, Title: "PROJ-1: attached", State: "OPEN"},
			{Repo: "platform", Number: 2, Title: "PROJ-1: also attached", State: "MERGED"},
			{Repo: "platform", Number: 3, Title: "open orphan", State: "OPEN"},
			// Dropped: merged with no active ticket.
			{Repo: "platform", Number: 4, Title: "PROJ-999: stale merge", State: "MERGED"},
			{Repo: "platform", Number: 5, Title: "PROJ-998: another", State: "MERGED"},
		},
	}
	a.settle()

	if len(a.prs) != 5 {
		t.Fatalf("setup: fetched %d PRs, want 5", len(a.prs))
	}
	if got := a.prCount(); got != 3 {
		t.Errorf("prCount() = %d, want 3 (2 attached + 1 orphan), not the 5 fetched", got)
	}
}

// An archived-repo PR must not reach the correlation step, or it would attach
// itself to a live ticket and look actionable.
func TestArchivedPRsAreNotCorrelated(t *testing.T) {
	tickets := []Ticket{{Key: "PROJ-1", Status: "In Progress", Category: "In Progress"}}
	prs := []PullRequest{
		{Repo: "old", Number: 7, Title: "PROJ-1: superseded", Archived: true},
		{Repo: "platform", Number: 8, Title: "PROJ-1: current"},
	}

	groups, orphans := build(tickets, dropArchived(prs, false))
	got := groups[0].Tickets[0].PRs
	if len(got) != 1 || got[0].Repo != "platform" {
		t.Errorf("PROJ-1 PRs = %+v, want only platform #8", got)
	}
	if len(orphans) != 0 {
		t.Errorf("orphans = %+v, want none", orphans)
	}
}

// The relation column replaces the old type and children columns. +N and the
// sub-task glyph are mutually exclusive — JIRA forbids sub-tasks of sub-tasks —
// so one column carries both, and neither needs a legend to be read.
func TestRelationText(t *testing.T) {
	tests := []struct {
		name   string
		ticket Ticket
		want   string
	}{
		{"children are counted", Ticket{ChildCount: 4}, iconChild + "4"},
		{"a three-digit count still renders", Ticket{ChildCount: 128}, iconChild + "128"},
		{"a sub-task points at its parent", Ticket{IsSubtask: true}, iconSubtask},
		{"an ordinary ticket says nothing", Ticket{}, ""},
		// A parent that is somehow also flagged a sub-task shows the count: it is
		// the actionable half, and there is nowhere to open a parent from anyway.
		{"children win over the sub-task flag",
			Ticket{ChildCount: 2, IsSubtask: true}, iconChild + "2"},
		// A zero count is not a childless ticket saying "0", it is silence.
		{"zero children is blank, not a zero", Ticket{ChildCount: 0}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := relationText(tc.ticket); got != tc.want {
				t.Errorf("relationText() = %q, want %q", got, tc.want)
			}
		})
	}
}

// One verbose ticket must not widen the column for every other row.
func TestRelationTextFitsTheColumnCap(t *testing.T) {
	for _, ticket := range []Ticket{
		{ChildCount: 1}, {ChildCount: 999}, {IsSubtask: true}, {},
	} {
		if got := relationText(ticket); runewidth.StringWidth(got) > maxRelW {
			t.Errorf("relationText(%+v) = %q, %d columns, over the %d cap",
				ticket, got, runewidth.StringWidth(got), maxRelW)
		}
	}
}

// JIRA forbids sub-tasks of sub-tasks, so those lookups are skipped. Everything
// else may have children, including epics and initiatives.
func TestChildCandidatesSkipsSubtasksOnly(t *testing.T) {
	tickets := []Ticket{
		{Key: "PROJ-1", Type: "Task"},
		{Key: "PROJ-2", Type: "Epic"},
		{Key: "PROJ-3", Type: "Sub-task", IsSubtask: true},
		{Key: "TEAM-4", Type: "Initiative"},
		{Key: "PROJ-5", Type: "Story"},
		{Key: "PROJ-6", Type: "Underlying work item", IsSubtask: true}, // oddly named sub-task
	}

	got := childCandidates(tickets)
	want := []string{"PROJ-1", "PROJ-2", "TEAM-4", "PROJ-5"}
	if !slices.Equal(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
}

func TestApplyChildCountsIsCaseInsensitive(t *testing.T) {
	tickets := []Ticket{{Key: "proj-17453", Source: "JIRA", Type: "Task"}, {Key: "PROJ-17455", Source: "JIRA", Type: "Task"}}
	applyChildCounts(tickets, map[string]int{"PROJ-17453": 4}, "JIRA")

	if tickets[0].ChildCount != 4 {
		t.Errorf("ChildCount = %d, want 4", tickets[0].ChildCount)
	}
	if tickets[1].ChildCount != 0 {
		t.Errorf("ChildCount = %d, want 0", tickets[1].ChildCount)
	}
}

// The relation column collapses entirely when no ticket has children and none is
// a sub-task, and widens to fit the largest count when something does.
func TestMeasureSizesColumnsToData(t *testing.T) {
	tests := []struct {
		name         string
		tickets      []Ticket
		wantRelW     int
		wantKeyW     int
		wantSummaryW int
	}{
		{
			name:     "no relations collapses the column",
			tickets:  []Ticket{{Key: "M-1", Type: "Task"}, {Key: "M-2", Type: "Bug"}},
			wantRelW: 0,
			wantKeyW: minKeyW,
		},
		{
			name: "the widest count wins",
			tickets: []Ticket{
				{Key: "M-1", ChildCount: 4},
				{Key: "M-2", IsSubtask: true},
				{Key: "M-3", ChildCount: 11},
			},
			wantRelW: len("+11"),
			wantKeyW: minKeyW,
		},
		{
			name:     "three-digit counts widen the column",
			tickets:  []Ticket{{Key: "M-1", ChildCount: 100}},
			wantRelW: len("+100"),
			wantKeyW: minKeyW,
		},
		{
			// A sub-task glyph on its own is one column, so a view of nothing but
			// sub-tasks does not pay for a count column it never uses.
			name:     "sub-tasks alone need one column",
			tickets:  []Ticket{{Key: "M-1", IsSubtask: true}, {Key: "M-2", IsSubtask: true}},
			wantRelW: 1,
			wantKeyW: minKeyW,
		},
		{
			// A long project key must widen the column, or every row after it
			// shifts right and the summary column stops lining up.
			name: "long project keys widen the key column",
			tickets: []Ticket{
				{Key: "OPS-264"},
				{Key: "LONGPROJ-308", ChildCount: 7},
			},
			wantRelW: len("+7"),
			wantKeyW: len("LONGPROJ-308"),
		},
		{
			// The summary column is sized from the data, so a view of short
			// summaries does not leave a stretch of empty column.
			name: "the longest summary sizes the summary column",
			tickets: []Ticket{
				{Key: "M-1", Summary: "short"},
				{Key: "M-2", Summary: "a longer summary"},
			},
			wantRelW:     0,
			wantKeyW:     minKeyW,
			wantSummaryW: len("a longer summary"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{tickets: tc.tickets}
			a.settle()
			got := a.measure()
			want := widths{
				relW: tc.wantRelW, keyW: tc.wantKeyW, summaryW: tc.wantSummaryW,
			}
			if got != want {
				t.Errorf("measure() = %+v, want %+v", got, want)
			}
		})
	}
}

// The pull request columns are measured the same way as the rest: from the data,
// so a row's reference is never a column short of what it has to draw.
func TestMeasureSizesPRColumnsToData(t *testing.T) {
	a := &app{
		tickets: []Ticket{{Key: "PROJ-1", Status: "To Do", Category: "To Do", Type: "Task"}},
		prs: []PullRequest{
			{Repo: "app", Number: 7, Title: "PROJ-1: short", State: "OPEN"},
			// The widest reference: a long repo name and a big number.
			{Repo: "platform-controller", Number: 10488, Title: "PROJ-1: a considerably longer title",
				State: "OPEN", CI: "SUCCESS", Review: "APPROVED", Approvals: 3},
		},
	}
	a.settle()
	got := a.measure()

	widest := a.groups[0].Tickets[0].PRs[1]
	if want := runewidth.StringWidth(prLabel(widest)); got.refW != want {
		t.Errorf("refW = %d, want %d, the widest reference on screen", got.refW, want)
	}
	// Measured after the ticket key is dropped, which is what gets drawn.
	if want := len("a considerably longer title"); got.titleW != want {
		t.Errorf("titleW = %d, want %d, the longest title without its key", got.titleW, want)
	}
	if got.archived {
		t.Error("archived = true with no archived repository among the PRs")
	}
}

// An orphan pull request puts its title in the summary column, so it has to size
// that column too or the longest orphan title is the one that gets truncated.
func TestMeasureSizesTheSummaryFromOrphanTitles(t *testing.T) {
	const title = "a template repository with a notably long descriptive title"
	a := &app{
		tickets: []Ticket{{Key: "PROJ-1", Status: "To Do", Category: "To Do", Summary: "short"}},
		prs:     []PullRequest{{Repo: "sandbox", Number: 5, Title: title, State: "OPEN"}},
	}
	a.settle()
	if got := a.measure(); got.summaryW != len(title) {
		t.Errorf("summaryW = %d, want %d, the orphan's title", got.summaryW, len(title))
	}
}

// No layout may ask for more columns than the terminal has, or rows wrap and the
// alignment of every row goes with them. When there is more content than room,
// the columns must add up to the width exactly rather than stopping short.
func TestComputeLayoutFitsWidth(t *testing.T) {
	for _, width := range []int{20, 40, 60, 72, 80, 100, 120, 200} {
		for _, relW := range []int{0, 1, 2, 4} {
			for _, keyW := range []int{minKeyW, maxKeyW} {
				for _, symW := range []int{0, 1} {
					for _, archived := range []bool{false, true} {
						for _, data := range []widths{
							{},                                     // nothing loaded
							{summaryW: 12, refW: 14, titleW: 8},    // everything short
							{summaryW: 200, refW: 60, titleW: 200}, // everything oversized
							{summaryW: 200, refW: 14, titleW: 40},  // a long summary
							{summaryW: 12, refW: 14, titleW: 200},  // a long title
						} {
							data.relW, data.keyW, data.symW = relW, keyW, symW
							data.archived = archived
							lay := computeLayout(width, data)
							where := fmt.Sprintf("width %d rel %d key %d sym %d arch %v data %+v",
								width, relW, keyW, symW, archived, data)

							// prefix + summary + gap + pr + gap + title + archived
							total := lay.prefix() + 2 + lay.summary + lay.pr +
								lay.titleBlock() + lay.archivedBlock()
							if total > lay.width-1 {
								t.Errorf("%s: columns total %d, over %d",
									where, total, lay.width-1)
							}
							// Oversized content has to be spent to the last column.
							if data.summaryW > lay.width && total != lay.width-1 {
								t.Errorf("%s: columns total %d, want the full %d",
									where, total, lay.width-1)
							}
							if lay.summary < minSummaryW || lay.ref < minRefW {
								t.Errorf("%s: degenerate summary=%d ref=%d",
									where, lay.summary, lay.ref)
							}
							if lay.prColumn() >= lay.width {
								t.Errorf("%s: PR column starts at %d, off screen",
									where, lay.prColumn())
							}
						}
					}
				}
			}
		}
	}
}

// The summary is the column that flexes: it grows with the terminal up to the
// longest summary loaded, and shrinks when the terminal cannot hold that.
func TestSummaryColumnTakesTheSpareRoom(t *testing.T) {
	const data = 120 // a summary longer than any of these terminals leave room for

	narrow := computeLayout(80, widths{relW: 2, keyW: minKeyW, summaryW: data, refW: 14})
	wide := computeLayout(200, widths{relW: 2, keyW: minKeyW, summaryW: data, refW: 14})
	if narrow.summary >= wide.summary {
		t.Errorf("summary did not grow with the terminal: %d at 80, %d at 200",
			narrow.summary, wide.summary)
	}
	if wide.summary != data {
		t.Errorf("summary = %d on a wide terminal, want the longest summary %d",
			wide.summary, data)
	}

	// Never wider than its content, however much room there is.
	short := computeLayout(200, widths{relW: 2, keyW: minKeyW, summaryW: 15, refW: 14})
	if short.summary != 15 {
		t.Errorf("summary = %d, want the 15 its content needs", short.summary)
	}
}

// The reference and title columns fit their content: the reference so references
// line up, the title capped, since it only describes a PR the row already names.
func TestPRColumnsFitTheirContent(t *testing.T) {
	tests := []struct {
		name               string
		refW, titleW       int
		wantRef, wantTitle int
	}{
		{"both short", 14, 18, 14, 18},
		{"a long title is capped", 14, 200, 14, maxTitleW},
		{"a wide reference is capped", 200, 18, maxRefW, 18},
		{"a narrow reference has a floor", 4, 18, minRefW, 18},
		{"no titles, no column", 14, 0, 14, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lay := computeLayout(200, widths{
				relW: 2, keyW: minKeyW, summaryW: 40, refW: tc.refW, titleW: tc.titleW,
			})
			if lay.ref != tc.wantRef {
				t.Errorf("ref = %d, want %d", lay.ref, tc.wantRef)
			}
			if lay.title != tc.wantTitle {
				t.Errorf("title = %d, want %d", lay.title, tc.wantTitle)
			}
			// The cell is the reference plus the fixed badge tail, always.
			if want := prCellWidth(lay.ref); lay.pr != want {
				t.Errorf("pr = %d, want %d", lay.pr, want)
			}
		})
	}

	// A terminal with no room to spare drops the title rather than the summary.
	tight := computeLayout(72, widths{relW: 2, keyW: minKeyW, summaryW: 200, refW: 14, titleW: 30})
	if tight.title != 0 {
		t.Errorf("title = %d on a tight terminal, want it collapsed", tight.title)
	}
}
