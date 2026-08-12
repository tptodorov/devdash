package main

import (
	"slices"
	"testing"
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

// typeCode is derived from the type name alone, with nothing hard-coded, so it
// has to behave for issue types this tool has never seen.
func TestTypeCode(t *testing.T) {
	tests := []struct {
		issueType string
		want      string
	}{
		// Single words reduce to one initial.
		{"Task", "T"},
		{"Epic", "E"},
		{"Story", "S"},
		{"Bug", "B"},
		{"Initiative", "I"},
		// Hyphens, spaces, slashes and underscores all separate words.
		{"Sub-task", "ST"},
		{"New Feature", "NF"},
		{"Change Request", "CR"},
		{"Technical Debt", "TD"},
		{"Bug/Defect", "BD"},
		{"service_request", "SR"},
		// Case is normalised upward.
		{"subtask", "S"},
		{"SUB-TASK", "ST"},
		{"sub-task", "ST"},
		{" epic ", "E"},
		// Digits count as word characters.
		{"L3 Escalation", "LE"},
		{"2nd Line Support", "2LS"},
		// Long names are capped so one type cannot widen every row.
		{"Really Very Extremely Long Type Name", "RVEL"},
		// Degenerate input still yields something printable.
		{"", "·"},
		{"---", "·"},
	}
	for _, tc := range tests {
		if got := typeCode(tc.issueType); got != tc.want {
			t.Errorf("typeCode(%q) = %q, want %q", tc.issueType, got, tc.want)
		}
	}
}

func TestTypeCodeNeverExceedsColumnCap(t *testing.T) {
	types := []string{
		"Task", "Sub-task", "A B C D E F G", "Extremely-Long-Hyphenated-Type-Name-Here", "",
	}
	for _, issueType := range types {
		if got := typeCode(issueType); len([]rune(got)) > maxTypeCode {
			t.Errorf("typeCode(%q) = %q, which is %d runes, over the %d cap",
				issueType, got, len([]rune(got)), maxTypeCode)
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
	tickets := []Ticket{{Key: "proj-17453", Type: "Task"}, {Key: "PROJ-17455", Type: "Task"}}
	applyChildCounts(tickets, map[string]int{"PROJ-17453": 4})

	if tickets[0].ChildCount != 4 {
		t.Errorf("ChildCount = %d, want 4", tickets[0].ChildCount)
	}
	if tickets[1].ChildCount != 0 {
		t.Errorf("ChildCount = %d, want 0", tickets[1].ChildCount)
	}
}

// The child column collapses entirely when nothing has children, and widens to
// fit the largest count when something does.
func TestMeasureSizesColumnsToData(t *testing.T) {
	tests := []struct {
		name       string
		tickets    []Ticket
		wantTypeW  int
		wantChildW int
		wantKeyW   int
	}{
		{
			name:       "no children collapses the column",
			tickets:    []Ticket{{Key: "M-1", Type: "Task"}, {Key: "M-2", Type: "Bug"}},
			wantTypeW:  1,
			wantChildW: 0,
			wantKeyW:   minKeyW,
		},
		{
			name: "widest type and widest count win",
			tickets: []Ticket{
				{Key: "M-1", Type: "Task", ChildCount: 4},
				{Key: "M-2", Type: "Sub-task"},
				{Key: "M-3", Type: "Epic", ChildCount: 11},
			},
			wantTypeW:  2,
			wantChildW: 2,
			wantKeyW:   minKeyW,
		},
		{
			name:       "three-digit counts widen the column",
			tickets:    []Ticket{{Key: "M-1", Type: "New Feature Request", ChildCount: 100}},
			wantTypeW:  3,
			wantChildW: 3,
			wantKeyW:   minKeyW,
		},
		{
			// A long project key must widen the column, or every row after it
			// shifts right and the summary column stops lining up.
			name: "long project keys widen the key column",
			tickets: []Ticket{
				{Key: "OPS-264", Type: "Standard Change"},
				{Key: "LONGPROJ-308", Type: "Child Task", ChildCount: 7},
			},
			wantTypeW:  2,
			wantChildW: 1,
			wantKeyW:   len("LONGPROJ-308"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := &app{tickets: tc.tickets}
			a.settle()
			got := a.measure()
			want := widths{typeW: tc.wantTypeW, childW: tc.wantChildW, keyW: tc.wantKeyW}
			if got != want {
				t.Errorf("measure() = %+v, want %+v", got, want)
			}
		})
	}
}

// The layout must never ask for more columns than the terminal has, or rows
// wrap and the alignment collapses.
func TestComputeLayoutFitsWidth(t *testing.T) {
	for _, width := range []int{20, 40, 60, 72, 80, 100, 120, 200} {
		for _, typeW := range []int{1, 2, 4} {
			for _, childW := range []int{0, 1, 4} {
				lay := computeLayout(width, widths{typeW: typeW, childW: childW, keyW: minKeyW})
				// prefix + summary + gap + connector + gap + pr
				total := lay.prefix() + lay.summary + 5 + lay.pr
				if total > lay.width {
					t.Errorf("width %d type %d child %d: needs %d columns, have %d",
						width, typeW, childW, total, lay.width)
				}
				if lay.summary < 10 || lay.pr < 18 {
					t.Errorf("width %d type %d child %d: degenerate summary=%d pr=%d",
						width, typeW, childW, lay.summary, lay.pr)
				}
				if lay.prColumn() >= lay.width {
					t.Errorf("width %d type %d child %d: PR column starts at %d, off screen",
						width, typeW, childW, lay.prColumn())
				}
			}
		}
	}
}
