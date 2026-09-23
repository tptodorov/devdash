package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeTracker is a Tracker whose Tickets() result is fixed, so tests can
// exercise the multi-tracker merge without any network.
type fakeTracker struct {
	name    string
	tickets []Ticket
	err     error
}

func (f fakeTracker) Name() string { return f.name }
func (f fakeTracker) Tickets(context.Context, string) ([]Ticket, error) {
	return f.tickets, f.err
}

func TestFetchTicketsMergesEveryTracker(t *testing.T) {
	trackers := []trackerSource{
		{tracker: fakeTracker{name: "JIRA", tickets: []Ticket{{Key: "PROJ-1", Source: "JIRA"}}}},
		{tracker: fakeTracker{name: "Linear", tickets: []Ticket{{Key: "ENG-1", Source: "Linear"}}}},
	}

	msg := fetchTickets(context.Background(), trackers, nil, nil)
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	if len(msg.tickets) != 2 {
		t.Fatalf("got %d tickets, want 2", len(msg.tickets))
	}
}

func TestFetchTicketsPartialFailureIsAWarningNotAnError(t *testing.T) {
	trackers := []trackerSource{
		{tracker: fakeTracker{name: "JIRA", err: errors.New("boom")}},
		{tracker: fakeTracker{name: "Linear", tickets: []Ticket{{Key: "ENG-1", Source: "Linear"}}}},
	}

	msg := fetchTickets(context.Background(), trackers, nil, nil)
	if msg.err != nil {
		t.Fatalf("err = %v, want nil: one working tracker should still show its tickets", msg.err)
	}
	if msg.warn == nil || !strings.Contains(msg.warn.Error(), "JIRA: boom") {
		t.Errorf("warn = %v, want it to mention %q", msg.warn, "JIRA: boom")
	}
	if len(msg.tickets) != 1 || msg.tickets[0].Key != "ENG-1" {
		t.Fatalf("tickets = %+v, want just the Linear ticket that succeeded", msg.tickets)
	}
}

func TestFetchTicketsErrorsWhenEveryTrackerFails(t *testing.T) {
	trackers := []trackerSource{
		{tracker: fakeTracker{name: "JIRA", err: errors.New("boom")}},
		{tracker: fakeTracker{name: "Linear", err: errors.New("bang")}},
	}

	msg := fetchTickets(context.Background(), trackers, nil, nil)
	if msg.err == nil {
		t.Fatal("want an error when every configured tracker fails")
	}
	if !strings.Contains(msg.err.Error(), "JIRA: boom") || !strings.Contains(msg.err.Error(), "Linear: bang") {
		t.Errorf("err = %v, want it to name both failures", msg.err)
	}
}

// A JIRA_* group that is only partially set is a configuration mistake that
// must keep surfacing on every refresh, not just the one where it was first
// discovered: once a working tracker's ticketsMsg lands, the unconditional
// `a.trackerErr = msg.err` in Update must not erase it, because fetchTickets
// folds the boot-time config error back into every result it produces.
func TestTrackerCfgErrSurvivesRepeatedRefreshes(t *testing.T) {
	a := newAppForTest()
	a.trackerCfgErr = errors.New("JIRA: missing environment variables: JIRA_USERNAME, JIRA_API_TOKEN")
	a.trackers = []trackerSource{
		{tracker: fakeTracker{name: "Linear", tickets: []Ticket{{Key: "ENG-1", Source: "Linear"}}}},
	}

	for i := range 3 {
		msg := fetchTickets(context.Background(), a.trackers, a.jira, a.trackerCfgErr)
		a.Update(msg)
		if a.trackerErr != nil {
			t.Fatalf("refresh %d: trackerErr = %v, want nil (Linear succeeded)", i, a.trackerErr)
		}
		if a.trackerWarn == nil || !strings.Contains(a.trackerWarn.Error(), "JIRA_USERNAME") {
			t.Fatalf("refresh %d: trackerWarn = %v, want the JIRA config mistake still surfaced", i, a.trackerWarn)
		}
	}
}

// The trackers loop must run every tracker concurrently rather than one after
// another, since they all share a single fixed deadline (25s in production);
// sequential fetches would double worst-case latency as more trackers are
// added.
func TestFetchTicketsRunsTrackersConcurrently(t *testing.T) {
	const delay = 150 * time.Millisecond
	slow := func(name string) trackerSource {
		return trackerSource{tracker: slowTracker{name: name, delay: delay}}
	}
	trackers := []trackerSource{slow("JIRA"), slow("Linear")}

	start := time.Now()
	msg := fetchTickets(context.Background(), trackers, nil, nil)
	elapsed := time.Since(start)

	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	if elapsed >= 2*delay {
		t.Errorf("fetchTickets took %v for two %v trackers, want them run concurrently (well under %v)", elapsed, delay, 2*delay)
	}
}

type slowTracker struct {
	name  string
	delay time.Duration
}

func (s slowTracker) Name() string { return s.name }
func (s slowTracker) Tickets(ctx context.Context, _ string) ([]Ticket, error) {
	select {
	case <-time.After(s.delay):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return []Ticket{{Key: s.name + "-1", Source: s.name}}, nil
}

func TestFetchTicketsWithNoTrackersReturnsTheConfigError(t *testing.T) {
	cfgErr := errors.New("no ticket tracker configured")

	msg := fetchTickets(context.Background(), nil, nil, cfgErr)
	if !errors.Is(msg.err, cfgErr) {
		t.Errorf("err = %v, want %v", msg.err, cfgErr)
	}
}

// Child counts are a JIRA-only enrichment: JIRA's parent/child hierarchy has
// no equivalent for other trackers, so asking about it must never leak a
// non-JIRA ticket's key into the JQL sent to JIRA.
func TestFetchTicketsOnlyAsksJIRAAboutJIRATicketChildren(t *testing.T) {
	var childQueries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jql := r.URL.Query().Get("jql")
		if strings.Contains(jql, "parent in") {
			childQueries = append(childQueries, jql)
			io.WriteString(w, `{"issues":[]}`)
			return
		}
		io.WriteString(w, `{"issues":[
			{"key":"PROJ-1","fields":{"summary":"s","status":{"name":"Open","statusCategory":{"name":"To Do"}},"issuetype":{"name":"Task","subtask":false},"labels":[]}}
		]}`)
	}))
	t.Cleanup(srv.Close)
	jira := &jiraClient{baseURL: srv.URL, user: "u", token: "t", http: srv.Client()}

	trackers := []trackerSource{
		{tracker: jira, query: "project = PROJ"},
		{tracker: fakeTracker{name: "Linear", tickets: []Ticket{{Key: "ENG-1", Source: "Linear"}}}},
	}

	msg := fetchTickets(context.Background(), trackers, jira, nil)
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	if len(childQueries) != 1 {
		t.Fatalf("made %d child-count queries, want 1", len(childQueries))
	}
	if !strings.Contains(childQueries[0], "PROJ-1") {
		t.Errorf("child-count query = %q, want it to include PROJ-1", childQueries[0])
	}
	if strings.Contains(childQueries[0], "ENG-1") {
		t.Errorf("child-count query = %q, should never mention the Linear ticket", childQueries[0])
	}
}

// planSchedule's conditions (status and labels) carry no tracker component,
// so a JIRA and a Linear ticket sharing a key (plausible during a JIRA-to-
// Linear migration that keeps the same short code) can both satisfy them.
// applySymphony's source scoping must hold at this fetchTickets integration
// point, not just in its own unit tests, since a regression here would
// silently show a Linear ticket as queued for work Symphony can never
// actually perform on it.
func TestFetchTicketsScopesSymphonyToJIRAAcrossATrackerKeyCollision(t *testing.T) {
	t.Chdir(writeWorkflow(t, `---
tracker:
  provider:
    project_key: ENG
  required_labels:
    - symphony-ready
  active_states:
    - To Do
---
`))

	eligible := Ticket{Key: "ENG-1", Status: "To Do", Labels: []string{"symphony-ready"}}
	jiraTicket, linearTicket := eligible, eligible
	jiraTicket.Source, linearTicket.Source = "JIRA", "Linear"

	trackers := []trackerSource{
		{tracker: fakeTracker{name: "JIRA", tickets: []Ticket{jiraTicket}}},
		{tracker: fakeTracker{name: "Linear", tickets: []Ticket{linearTicket}}},
	}

	msg := fetchTickets(context.Background(), trackers, nil, nil)
	if msg.err != nil {
		t.Fatalf("err = %v", msg.err)
	}
	if len(msg.tickets) != 2 {
		t.Fatalf("got %d tickets, want 2", len(msg.tickets))
	}
	for _, tk := range msg.tickets {
		switch tk.Source {
		case "JIRA":
			if tk.Symphony != SymphonyScheduled {
				t.Errorf("JIRA ENG-1 Symphony = %q, want %q", tk.Symphony, SymphonyScheduled)
			}
		case "Linear":
			if tk.Symphony != "" {
				t.Errorf("Linear ENG-1 Symphony = %q, want empty: Symphony can never act on a non-JIRA ticket", tk.Symphony)
			}
		}
	}
}

// The status-change and Symphony-scheduling keys are JIRA workflows, so they
// must refuse a ticket sourced from another tracker even while JIRA itself is
// configured.
func TestOpenPickerRefusesNonJIRATicket(t *testing.T) {
	a := newAppForTest()
	a.jira = &jiraClient{}
	a.tickets = []Ticket{{Key: "ENG-1", Summary: "not a JIRA ticket", Status: "Todo", Category: "To Do", Source: "Linear"}}
	a.settle()
	a.cursor = 0

	cmd := a.openPicker()
	if cmd != nil {
		t.Error("openPicker() should not start a fetch for a non-JIRA ticket")
	}
	if !strings.Contains(a.flash, "only supported for JIRA tickets") {
		t.Errorf("flash = %q, want it to explain JIRA-only support", a.flash)
	}
}

func TestScheduleForSymphonyRefusesNonJIRATicket(t *testing.T) {
	a := newAppForTest()
	a.jira = &jiraClient{}
	a.tickets = []Ticket{{Key: "ENG-1", Summary: "not a JIRA ticket", Status: "Todo", Category: "To Do", Source: "Linear"}}
	a.settle()
	a.cursor = 0

	cmd := a.scheduleForSymphony()
	if cmd != nil {
		t.Error("scheduleForSymphony() should not start a fetch for a non-JIRA ticket")
	}
	if !strings.Contains(a.flash, "only supported for JIRA tickets") {
		t.Errorf("flash = %q, want it to explain JIRA-only support", a.flash)
	}
}

// ticketByKey scans every tracker's tickets by key alone, so a JIRA and a
// Linear ticket sharing a key (plausible during a JIRA-to-Linear migration
// that keeps the same short code) must not let the cursor sitting on the
// Linear row resolve to the JIRA ticket that happens to share its key.
func TestOpenPickerRefusesNonJIRATicketAcrossATrackerKeyCollision(t *testing.T) {
	a := newAppForTest()
	a.jira = &jiraClient{}
	// The JIRA ticket's group must sort ahead of the Linear ticket's group, so
	// a key-only scan meets the JIRA ticket first — the exact ordering that
	// let the bug hide behind a green test before.
	a.tickets = []Ticket{
		{Key: "ENG-1", Summary: "jira", Status: "In Progress", Category: "In Progress", Source: "JIRA"},
		{Key: "ENG-1", Summary: "linear", Status: "To Do", Category: "To Do", Source: "Linear"},
	}
	a.settle()
	a.cursor = rowIndexByStatus(t, a, "To Do")

	cmd := a.openPicker()
	if cmd != nil {
		t.Error("openPicker() should not start a fetch for the Linear row, even though a JIRA ticket shares its key")
	}
	if !strings.Contains(a.flash, "only supported for JIRA tickets") {
		t.Errorf("flash = %q, want it to explain JIRA-only support", a.flash)
	}
}

func TestScheduleForSymphonyRefusesNonJIRATicketAcrossATrackerKeyCollision(t *testing.T) {
	a := newAppForTest()
	a.jira = &jiraClient{}
	a.tickets = []Ticket{
		{Key: "ENG-1", Summary: "jira", Status: "In Progress", Category: "In Progress", Source: "JIRA"},
		{Key: "ENG-1", Summary: "linear", Status: "To Do", Category: "To Do", Source: "Linear"},
	}
	a.settle()
	a.cursor = rowIndexByStatus(t, a, "To Do")

	cmd := a.scheduleForSymphony()
	if cmd != nil {
		t.Error("scheduleForSymphony() should not start a fetch for the Linear row, even though a JIRA ticket shares its key")
	}
	if !strings.Contains(a.flash, "only supported for JIRA tickets") {
		t.Errorf("flash = %q, want it to explain JIRA-only support", a.flash)
	}
}

// settle()'s cursor-restore logic keys row identity by id alone (the same
// disambiguation gap ticketByKey had): if two colliding tickets ever produced
// the same id, a refresh that reorders groups could silently move the cursor
// from one tracker's ticket onto the other tracker's same-keyed ticket.
func TestSettleCursorRestoreStaysOnTheSameTrackersTicketAcrossAKeyCollision(t *testing.T) {
	a := newAppForTest()
	jira := Ticket{Key: "ENG-1", Summary: "jira", Source: "JIRA"}
	linear := Ticket{Key: "ENG-1", Summary: "linear", Source: "Linear"}

	// First settle: Linear's group sorts ahead of JIRA's, and the cursor
	// lands on the Linear row.
	linear.Status, linear.Category = "In Progress", "In Progress"
	jira.Status, jira.Category = "To Do", "To Do"
	a.tickets = []Ticket{jira, linear}
	a.settle()
	a.cursor = rowIndexByStatus(t, a, "In Progress")

	// Second settle: a status change now sorts JIRA's group ahead of
	// Linear's — the exact kind of reorder settle()'s own comment says the
	// id-based restore exists to survive.
	jira.Status, jira.Category = "In Progress", "In Progress"
	linear.Status, linear.Category = "To Do", "To Do"
	a.tickets = []Ticket{jira, linear}
	a.settle()

	row, ok := a.current()
	if !ok || row.source != "Linear" {
		t.Fatalf("cursor restored onto source %q, want it to stay on the Linear ticket it was on before the refresh", row.source)
	}
}

// rowIndexByStatus finds the selRow whose status matches, since two rows
// sharing a key (a cross-tracker collision) are otherwise indistinguishable
// by label alone.
func rowIndexByStatus(t *testing.T, a *app, status string) int {
	t.Helper()
	for i, s := range a.sel {
		if s.status == status {
			return i
		}
	}
	t.Fatalf("no row with status %q in %+v", status, a.sel)
	return -1
}

func TestNewAppActivatesTrackersFromEnvironment(t *testing.T) {
	for _, key := range []string{"JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN", "LINEAR_API_KEY"} {
		t.Setenv(key, "")
	}

	t.Run("neither configured reports a config error", func(t *testing.T) {
		a := newApp("", "", "", 0, true, false)
		if len(a.trackers) != 0 {
			t.Errorf("trackers = %+v, want none", a.trackers)
		}
		if a.trackerErr == nil {
			t.Error("want a config error when no tracker is configured")
		}
	})

	t.Run("JIRA alone activates just JIRA", func(t *testing.T) {
		t.Setenv("JIRA_URL", "https://example.atlassian.net")
		t.Setenv("JIRA_USERNAME", "me@example.com")
		t.Setenv("JIRA_API_TOKEN", "tok")

		a := newApp("", "", "", 0, true, false)
		if len(a.trackers) != 1 || a.trackers[0].tracker.Name() != "JIRA" {
			t.Errorf("trackers = %+v, want just JIRA", a.trackers)
		}
		if a.trackerErr != nil {
			t.Errorf("trackerErr = %v, want nil", a.trackerErr)
		}
	})

	t.Run("a partial JIRA group is a configuration error, not a silent opt-out", func(t *testing.T) {
		t.Setenv("JIRA_URL", "https://example.atlassian.net")
		t.Setenv("JIRA_USERNAME", "")
		t.Setenv("JIRA_API_TOKEN", "")

		a := newApp("", "", "", 0, true, false)
		if len(a.trackers) != 0 {
			t.Errorf("trackers = %+v, want none", a.trackers)
		}
		if a.trackerErr == nil {
			t.Error("want an error explaining the incomplete JIRA_* group")
		}
	})

	t.Run("both configured activates both", func(t *testing.T) {
		t.Setenv("JIRA_URL", "https://example.atlassian.net")
		t.Setenv("JIRA_USERNAME", "me@example.com")
		t.Setenv("JIRA_API_TOKEN", "tok")
		t.Setenv("LINEAR_API_KEY", "linear-tok")

		a := newApp("", "", "", 0, true, false)
		if len(a.trackers) != 2 {
			t.Fatalf("trackers = %+v, want both JIRA and Linear", a.trackers)
		}
		if a.trackerErr != nil {
			t.Errorf("trackerErr = %v, want nil", a.trackerErr)
		}
	})
}
