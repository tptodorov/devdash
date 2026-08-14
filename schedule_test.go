package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testConfig mirrors this repository's WORKFLOW.md tracker block.
func testConfig() symphonyConfig {
	var cfg symphonyConfig
	cfg.Tracker.Provider.ProjectKey = "PROJ"
	cfg.Tracker.RequiredLabels = []string{"symphony-ready"}
	cfg.Tracker.ActiveStates = []string{"To Do", "In Progress"}
	cfg.Tracker.TerminalStates = []string{"Done", "Closed", "Cancelled", "Duplicate"}
	return cfg
}

func TestPlanSchedule(t *testing.T) {
	tests := []struct {
		name        string
		ticket      Ticket
		wantLabels  []string
		wantStatus  string
		wantRefusal string
		wantNoop    bool
	}{
		{
			// Already labelled and already active: Symphony will pick it up as is.
			name:     "already eligible",
			ticket:   Ticket{Key: "PROJ-1", Status: "In Progress", Labels: []string{"symphony-ready"}},
			wantNoop: true,
		},
		{
			name:       "active but unlabelled needs only the label",
			ticket:     Ticket{Key: "PROJ-2", Status: "To Do"},
			wantLabels: []string{"symphony-ready"},
		},
		{
			// Labelled but parked in a state Symphony ignores.
			name:       "labelled but not active needs the move",
			ticket:     Ticket{Key: "PROJ-3", Status: "Awaiting CR", Labels: []string{"symphony-ready"}},
			wantStatus: "To Do",
		},
		{
			name:       "neither labelled nor active needs both",
			ticket:     Ticket{Key: "PROJ-4", Status: "Awaiting CR"},
			wantLabels: []string{"symphony-ready"},
			wantStatus: "To Do",
		},
		{
			// Other labels are left alone.
			name:       "keeps existing labels",
			ticket:     Ticket{Key: "PROJ-5", Status: "To Do", Labels: []string{"tech-debt"}},
			wantLabels: []string{"symphony-ready"},
		},
		{
			name:       "label match ignores case",
			ticket:     Ticket{Key: "PROJ-6", Status: "to do", Labels: []string{"Symphony-Ready"}},
			wantNoop:   true,
			wantLabels: nil,
		},
		{
			// Restarting finished work is a bigger decision than a keystroke.
			name:        "terminal state is refused",
			ticket:      Ticket{Key: "PROJ-7", Status: "Done", Labels: []string{"symphony-ready"}},
			wantRefusal: "finished",
		},
		{
			name:        "another project is refused",
			ticket:      Ticket{Key: "TEAM-8", Status: "To Do"},
			wantRefusal: "only watches project PROJ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := planSchedule(testConfig(), tc.ticket)

			if tc.wantRefusal != "" {
				if !strings.Contains(plan.refusal, tc.wantRefusal) {
					t.Fatalf("refusal = %q, want it to mention %q", plan.refusal, tc.wantRefusal)
				}
				// A refused plan must not also propose changes.
				if len(plan.addLabels) > 0 || plan.toStatus != "" {
					t.Errorf("refused plan still proposes changes: %+v", plan)
				}
				return
			}
			if plan.refusal != "" {
				t.Fatalf("unexpected refusal: %q", plan.refusal)
			}
			if got := plan.nothingToDo(); got != tc.wantNoop {
				t.Errorf("nothingToDo() = %v, want %v (plan %+v)", got, tc.wantNoop, plan)
			}
			if strings.Join(plan.addLabels, ",") != strings.Join(tc.wantLabels, ",") {
				t.Errorf("addLabels = %v, want %v", plan.addLabels, tc.wantLabels)
			}
			if plan.toStatus != tc.wantStatus {
				t.Errorf("toStatus = %q, want %q", plan.toStatus, tc.wantStatus)
			}
		})
	}
}

// With no active_states configured there is no state to aim for, so refuse rather
// than invent one.
func TestPlanScheduleWithoutActiveStates(t *testing.T) {
	cfg := testConfig()
	cfg.Tracker.ActiveStates = nil

	plan := planSchedule(cfg, Ticket{Key: "PROJ-1", Status: "Awaiting CR"})
	if !strings.Contains(plan.refusal, "no active_states") {
		t.Errorf("refusal = %q, want it to mention the missing configuration", plan.refusal)
	}
}

// A project that configures Symphony differently must be honoured without code
// changes, since every condition is read from the file.
func TestPlanScheduleFollowsAnotherProjectsConfig(t *testing.T) {
	var cfg symphonyConfig
	cfg.Tracker.Provider.ProjectKey = "OPS"
	cfg.Tracker.RequiredLabels = []string{"agent-ok", "triaged"}
	cfg.Tracker.ActiveStates = []string{"Ready", "Doing"}
	cfg.Tracker.TerminalStates = []string{"Shipped"}

	plan := planSchedule(cfg, Ticket{Key: "OPS-9", Status: "Backlog", Labels: []string{"triaged"}})
	if plan.refusal != "" {
		t.Fatalf("unexpected refusal: %q", plan.refusal)
	}
	if want := []string{"agent-ok"}; strings.Join(plan.addLabels, ",") != strings.Join(want, ",") {
		t.Errorf("addLabels = %v, want %v", plan.addLabels, want)
	}
	if plan.toStatus != "Ready" {
		t.Errorf("toStatus = %q, want Ready, the first active state", plan.toStatus)
	}
	if got := plan.summary(); !strings.Contains(got, "agent-ok") || !strings.Contains(got, "Ready") {
		t.Errorf("summary = %q, want it to name both changes", got)
	}
}

func TestPlanSummary(t *testing.T) {
	tests := []struct {
		plan schedulePlan
		want string
	}{
		{schedulePlan{addLabels: []string{"symphony-ready"}}, "labelled symphony-ready"},
		{schedulePlan{toStatus: "To Do"}, "moved to To Do"},
		{
			schedulePlan{addLabels: []string{"symphony-ready"}, toStatus: "To Do"},
			"labelled symphony-ready and moved to To Do",
		},
		{schedulePlan{}, ""},
	}
	for _, tc := range tests {
		if got := tc.plan.summary(); got != tc.want {
			t.Errorf("summary() = %q, want %q", got, tc.want)
		}
	}
}

// The conditions must come from the real file's shape, not from a struct built in
// a test.
func TestReadSymphonyConfigFromWorkflowFile(t *testing.T) {
	dir := t.TempDir()
	content := `---
tracker:
  kind: jira
  provider:
    base_url: $JIRA_URL
    project_key: PROJ
  required_labels:
    - symphony-ready
  active_states:
    - To Do
    - In Progress
  terminal_states:
    - Done
    - Closed
hooks:
  after_create: |
    set -eu
    echo "shell text that must not confuse the parser"
    active_states: not config
server:
  port: 10000
---

# Prose below the front matter
`
	if err := os.WriteFile(filepath.Join(dir, workflowFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := readSymphonyConfig(dir)
	if err != nil {
		t.Fatalf("readSymphonyConfig() error = %v", err)
	}
	if cfg.Tracker.Provider.ProjectKey != "PROJ" {
		t.Errorf("project_key = %q", cfg.Tracker.Provider.ProjectKey)
	}
	if strings.Join(cfg.Tracker.RequiredLabels, ",") != "symphony-ready" {
		t.Errorf("required_labels = %v", cfg.Tracker.RequiredLabels)
	}
	if strings.Join(cfg.Tracker.ActiveStates, ",") != "To Do,In Progress" {
		t.Errorf("active_states = %v", cfg.Tracker.ActiveStates)
	}
	if strings.Join(cfg.Tracker.TerminalStates, ",") != "Done,Closed" {
		t.Errorf("terminal_states = %v", cfg.Tracker.TerminalStates)
	}
	if cfg.Server.Port != 10000 {
		t.Errorf("server.port = %d", cfg.Server.Port)
	}

	// A plan built from the real file's conditions behaves as expected.
	plan := planSchedule(cfg, Ticket{Key: "PROJ-1", Status: "Awaiting CR"})
	if plan.toStatus != "To Do" || len(plan.addLabels) != 1 {
		t.Errorf("plan from file = %+v", plan)
	}
}

// S is a toggle: a ticket Symphony would already pick up comes back out of the
// queue, anything else goes in.
func TestPlanToggle(t *testing.T) {
	cfg := testConfig()

	tests := []struct {
		name         string
		ticket       Ticket
		wantUnsched  bool
		wantRemove   []string
		wantAdd      []string
		wantToStatus string
		wantRefusal  string
	}{
		{
			// Eligible, so the toggle takes it out.
			name:        "eligible ticket is unscheduled",
			ticket:      Ticket{Key: "PROJ-1", Status: "In Progress", Labels: []string{"symphony-ready"}},
			wantUnsched: true,
			wantRemove:  []string{"symphony-ready"},
		},
		{
			// Only the label is missing, so it goes in.
			name:    "unlabelled active ticket is scheduled",
			ticket:  Ticket{Key: "PROJ-2", Status: "To Do"},
			wantAdd: []string{"symphony-ready"},
		},
		{
			// Labelled but parked: not eligible, so the toggle schedules it
			// rather than stripping the label it already has.
			name:         "labelled but inactive ticket is scheduled, not unscheduled",
			ticket:       Ticket{Key: "PROJ-3", Status: "Awaiting CR", Labels: []string{"symphony-ready"}},
			wantToStatus: "To Do",
		},
		{
			name:        "another project is refused either way",
			ticket:      Ticket{Key: "TEAM-4", Status: "To Do", Labels: []string{"symphony-ready"}},
			wantRefusal: "only watches project PROJ",
		},
		{
			name:        "terminal ticket is still refused",
			ticket:      Ticket{Key: "PROJ-5", Status: "Done", Labels: []string{"symphony-ready"}},
			wantRefusal: "finished",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan := planToggle(cfg, tc.ticket)

			if tc.wantRefusal != "" {
				if !strings.Contains(plan.refusal, tc.wantRefusal) {
					t.Fatalf("refusal = %q, want it to mention %q", plan.refusal, tc.wantRefusal)
				}
				return
			}
			if plan.refusal != "" {
				t.Fatalf("unexpected refusal: %q", plan.refusal)
			}
			if plan.unschedule != tc.wantUnsched {
				t.Errorf("unschedule = %v, want %v", plan.unschedule, tc.wantUnsched)
			}
			if strings.Join(plan.removeLabels, ",") != strings.Join(tc.wantRemove, ",") {
				t.Errorf("removeLabels = %v, want %v", plan.removeLabels, tc.wantRemove)
			}
			if strings.Join(plan.addLabels, ",") != strings.Join(tc.wantAdd, ",") {
				t.Errorf("addLabels = %v, want %v", plan.addLabels, tc.wantAdd)
			}
			if plan.toStatus != tc.wantToStatus {
				t.Errorf("toStatus = %q, want %q", plan.toStatus, tc.wantToStatus)
			}
			// Unscheduling never moves the ticket: undoing the status change would
			// mean guessing where it came from.
			if plan.unschedule && plan.toStatus != "" {
				t.Errorf("unscheduling should not move the ticket, got %q", plan.toStatus)
			}
		})
	}
}

// Toggling twice returns the ticket to where it started, which is what makes it a
// toggle rather than two unrelated commands.
func TestToggleRoundTrips(t *testing.T) {
	cfg := testConfig()
	ticket := Ticket{Key: "PROJ-1", Status: "To Do"} // active, unlabelled

	out := planToggle(cfg, ticket)
	if out.unschedule || strings.Join(out.addLabels, ",") != "symphony-ready" {
		t.Fatalf("first toggle = %+v, want it to add the label", out)
	}
	// Apply it by hand: the label is now present.
	ticket.Labels = append(ticket.Labels, out.addLabels...)
	if !scheduled(cfg, ticket) {
		t.Fatal("ticket should be scheduled after the first toggle")
	}

	back := planToggle(cfg, ticket)
	if !back.unschedule || strings.Join(back.removeLabels, ",") != "symphony-ready" {
		t.Fatalf("second toggle = %+v, want it to remove the label", back)
	}
	// And applying that leaves the original ticket.
	ticket.Labels = nil
	if scheduled(cfg, ticket) {
		t.Error("ticket should not be scheduled after toggling back")
	}
}

// Unrelated labels must survive a toggle in both directions.
func TestToggleLeavesOtherLabelsAlone(t *testing.T) {
	cfg := testConfig()
	ticket := Ticket{
		Key: "PROJ-1", Status: "To Do",
		Labels: []string{"tech-debt", "symphony-ready", "ram"},
	}

	plan := planToggle(cfg, ticket)
	if !plan.unschedule {
		t.Fatal("expected an unschedule")
	}
	if strings.Join(plan.removeLabels, ",") != "symphony-ready" {
		t.Errorf("removeLabels = %v, want only the required label", plan.removeLabels)
	}
}

func TestScheduledPredicate(t *testing.T) {
	cfg := testConfig()
	tests := []struct {
		ticket Ticket
		want   bool
	}{
		{Ticket{Status: "To Do", Labels: []string{"symphony-ready"}}, true},
		{Ticket{Status: "In Progress", Labels: []string{"symphony-ready"}}, true},
		{Ticket{Status: "to do", Labels: []string{"Symphony-Ready"}}, true}, // case-insensitive
		{Ticket{Status: "To Do"}, false},                                    // no label
		{Ticket{Status: "Awaiting CR", Labels: []string{"symphony-ready"}}, false},
		{Ticket{Status: "Done", Labels: []string{"symphony-ready"}}, false},
	}
	for _, tc := range tests {
		if got := scheduled(cfg, tc.ticket); got != tc.want {
			t.Errorf("scheduled(%q, %v) = %v, want %v",
				tc.ticket.Status, tc.ticket.Labels, got, tc.want)
		}
	}
}

// Only an agent actually running the ticket blocks the toggle, since a keystroke
// cannot interrupt a turn in progress.
func TestCannotUnscheduleWhileAnAgentIsRunning(t *testing.T) {
	cfg := testConfig()
	base := Ticket{Key: "PROJ-1", Status: "In Progress", Labels: []string{"symphony-ready"}}

	ticket := base
	ticket.Symphony = SymphonyRunning

	plan := planToggle(cfg, ticket)
	if plan.refusal == "" {
		t.Fatalf("expected a refusal while running, got %+v", plan)
	}
	if !strings.Contains(plan.refusal, ticket.Key) {
		t.Errorf("refusal = %q, want it to name the ticket", plan.refusal)
	}
	// Nothing may be proposed alongside the refusal.
	if len(plan.removeLabels) > 0 || len(plan.addLabels) > 0 || plan.toStatus != "" {
		t.Errorf("refused plan still proposes changes: %+v", plan)
	}
}

// Stuck work is the case the toggle exists for: a ticket that is blocked or
// failing to start has to come back out so it can be fixed and put back.
func TestCanUnscheduleStuckWork(t *testing.T) {
	cfg := testConfig()
	base := Ticket{Key: "PROJ-1", Status: "In Progress", Labels: []string{"symphony-ready"}}

	for _, state := range []string{
		SymphonyBlocked, SymphonyRetrying, SymphonyScheduled, "",
	} {
		name := state
		if name == "" {
			name = "no session"
		}
		t.Run(name, func(t *testing.T) {
			ticket := base
			ticket.Symphony = state

			plan := planToggle(cfg, ticket)
			if plan.refusal != "" {
				t.Fatalf("unexpected refusal: %q", plan.refusal)
			}
			if !plan.unschedule {
				t.Fatalf("plan = %+v, want an unschedule", plan)
			}
			if strings.Join(plan.removeLabels, ",") != "symphony-ready" {
				t.Errorf("plan = %+v, want it to remove the label", plan)
			}
			// The status is left alone: Symphony drops the claim on the label
			// going, and guessing where the ticket came from is not this key's job.
			if plan.toStatus != "" {
				t.Errorf("plan = %+v, want the status untouched", plan)
			}
		})
	}
}

func TestSymphonyIsWorkingOnIt(t *testing.T) {
	tests := map[string]bool{
		SymphonyRunning:   true,
		SymphonyBlocked:   false,
		SymphonyRetrying:  false,
		SymphonyScheduled: false,
		"":                false,
	}
	for state, want := range tests {
		if got := symphonyIsWorkingOnIt(Ticket{Symphony: state}); got != want {
			t.Errorf("symphonyIsWorkingOnIt(%q) = %v, want %v", state, got, want)
		}
	}
}
