package main

import (
	"context"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLiveLinearEditing runs the Linear Editor against the real API on a
// throwaway issue, assigned to the key's own user in the first team (or the
// team DEVDASH_LIVE_LINEAR_TEAM names). It writes, so it only runs when asked:
//
//	DEVDASH_LIVE_LINEAR_WRITES=1 LINEAR_API_KEY=... go test -run TestLiveLinearEditing -v
//
// It creates labels and an issue, edits the labels, schedules and unschedules
// it for Symphony, and walks it through every workflow state of the team.
// Every effect is read back with GraphQL queries of its own, not through the
// code under test. The issue and labels are deleted afterwards.
func TestLiveLinearEditing(t *testing.T) {
	if os.Getenv("DEVDASH_LIVE_LINEAR_WRITES") != "1" {
		t.Skip("set DEVDASH_LIVE_LINEAR_WRITES=1 to run this against real Linear; it creates and deletes an issue")
	}
	c, err := newLinearClient(&http.Client{Timeout: 30 * time.Second})
	if err != nil {
		t.Skip("set LINEAR_API_KEY to run this against real Linear")
	}
	// Not t.Context(): that is cancelled before cleanups run, and the cleanups
	// need it. Registered first, the cancel runs after every other cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	t.Cleanup(cancel)
	api := liveLinearAPI{t: t, ctx: ctx, c: c}

	team, viewer := api.team(os.Getenv("DEVDASH_LIVE_LINEAR_TEAM")), api.viewer()
	stamp := time.Now().UTC().Format("20060102-150405")
	ready := api.createLabel(team.ID, "devdash-live-"+stamp)
	second := api.createLabel(team.ID, "Devdash-Second-"+stamp) // mixed case on purpose
	theirs := api.createLabel(team.ID, "devdash-theirs-"+stamp)

	issueID, key := api.createIssue(team.ID, viewer, "devdash live editing test "+stamp+" (safe to delete)")
	t.Logf("testing on %s", key)

	t.Run("the new issue is one of the viewer's tickets", func(t *testing.T) {
		if _, found := liveLinearTicket(t, c, key); !found {
			t.Errorf("%s is not among Tickets", key)
		}
	})

	t.Run("a key naming no issue is refused", func(t *testing.T) {
		missing := team.Key + "-99999999"
		if _, err := c.Transitions(ctx, missing); err == nil {
			t.Error("Transitions on a missing issue succeeded")
		}
		if err := c.UpdateLabels(ctx, missing, []string{ready.name}, nil); err == nil {
			t.Error("UpdateLabels on a missing issue succeeded")
		}
	})

	t.Run("labels are added", func(t *testing.T) {
		if err := c.UpdateLabels(ctx, key, []string{ready.name, second.name}, nil); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(issueID, ready.name, second.name)
	})

	t.Run("a label carried in another case is not added twice", func(t *testing.T) {
		if err := c.UpdateLabels(ctx, key, []string{strings.ToUpper(ready.name)}, nil); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(issueID, ready.name, second.name)
	})

	t.Run("removal ignores case and keeps a label someone else added", func(t *testing.T) {
		api.addLabel(issueID, theirs.id) // as another person would, before our update
		if err := c.UpdateLabels(ctx, key, nil, []string{strings.ToLower(second.name), "never-there-" + stamp}); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(issueID, ready.name, theirs.name)
	})

	t.Run("a label that does not exist is named and nothing is written", func(t *testing.T) {
		missing := "devdash-missing-" + stamp
		err := c.UpdateLabels(ctx, key, []string{missing}, []string{ready.name})
		if err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("err = %v, want it to name %q", err, missing)
		}
		api.wantLabels(issueID, ready.name, theirs.name)
	})

	t.Run("Symphony scheduling sets the label and moves to the first active state", func(t *testing.T) {
		var cfg symphonyConfig
		cfg.Tracker.Kind = "linear"
		cfg.Tracker.Provider.ProjectKey = team.Key
		cfg.Tracker.RequiredLabels = []string{ready.name}
		cfg.Tracker.ActiveStates = []string{team.stateOfType("started").Name}

		tk, _ := liveLinearTicket(t, c, key)
		plan := planToggle(cfg, tk)
		if plan.refusal != "" || plan.unschedule || plan.toStatus != cfg.Tracker.ActiveStates[0] {
			t.Fatalf("labelled issue outside the active states: plan = %+v, want a move to %s", plan, cfg.Tracker.ActiveStates[0])
		}
		if err := applySchedule(ctx, c, plan); err != nil {
			t.Fatalf("schedule: %v", err)
		}
		if got := api.state(issueID).Name; got != cfg.Tracker.ActiveStates[0] {
			t.Errorf("state = %q, want %q", got, cfg.Tracker.ActiveStates[0])
		}

		tk, _ = liveLinearTicket(t, c, key)
		plan = planToggle(cfg, tk)
		if plan.refusal != "" || !plan.unschedule {
			t.Fatalf("scheduled issue: plan = %+v, want an unschedule", plan)
		}
		if err := applySchedule(ctx, c, plan); err != nil {
			t.Fatalf("unschedule: %v", err)
		}
		api.wantLabels(issueID, theirs.name)
	})

	t.Run("every workflow state but duplicate is reachable and is what Tickets then reports", func(t *testing.T) {
		for _, st := range team.States {
			if api.state(issueID).ID == st.ID {
				continue
			}
			trs, err := c.Transitions(ctx, key)
			if err != nil {
				t.Fatalf("Transitions: %v", err)
			}
			i := slices.IndexFunc(trs, func(tr Transition) bool { return tr.ID == st.ID })
			if st.Type == "duplicate" {
				// Linear refuses a move there without the issue it duplicates.
				if i >= 0 {
					t.Errorf("%s (duplicate) is offered, but Linear refuses it without a duplicate relation", st.Name)
				}
				continue
			}
			if i < 0 {
				t.Errorf("%s (%s) is not offered", st.Name, st.Type)
				continue
			}
			tr := trs[i]
			if want := liveLinearCategory(st.Type); tr.ToCategory != want {
				t.Errorf("%s (%s) has category %q, want %q", st.Name, st.Type, tr.ToCategory, want)
			}
			if err := c.ApplyTransition(ctx, key, tr); err != nil {
				t.Errorf("moving to %s: %v", st.Name, err)
				continue
			}
			if got := api.state(issueID); got.ID != st.ID {
				t.Errorf("after moving to %s the state is %s", st.Name, got.Name)
			}
			switch tk, found := liveLinearTicket(t, c, key); {
			case found && tk.Status != st.Name:
				t.Errorf("after moving to %s, Tickets reports %q", st.Name, tk.Status)
			case found && tr.ToCategory == "Done":
				t.Errorf("%s (%s) is finished but still among the default tickets", st.Name, st.Type)
			case !found && tr.ToCategory != "Done":
				t.Errorf("after moving to %s (%s) the ticket left the default scope", st.Name, st.Type)
			}
		}
	})
}

// liveLinearCategory is the status category each Linear state type belongs
// in, written out independently of the code under test.
func liveLinearCategory(stateType string) string {
	switch stateType {
	case "completed", "canceled", "duplicate":
		return "Done"
	case "started":
		return "In Progress"
	default:
		return "To Do"
	}
}

func liveLinearTicket(t *testing.T, c *linearClient, key string) (Ticket, bool) {
	t.Helper()
	tickets, err := c.Tickets(context.Background(), "")
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	for _, tk := range tickets {
		if tk.Key == key {
			return tk, true
		}
	}
	return Ticket{}, false
}

// liveLinearAPI arranges and reads state with GraphQL of its own, independently
// of the code under test.
type liveLinearAPI struct {
	t   *testing.T
	ctx context.Context
	c   *linearClient
}

type liveLinearState struct{ ID, Name, Type string }

type liveLinearTeam struct {
	ID, Key string
	States  []liveLinearState
}

func (tm liveLinearTeam) stateOfType(typ string) liveLinearState {
	for _, s := range tm.States {
		if s.Type == typ {
			return s
		}
	}
	return liveLinearState{}
}

type liveLinearLabel struct{ id, name string }

func (a liveLinearAPI) run(query string, vars map[string]any, out any) {
	a.t.Helper()
	if err := a.c.run(a.ctx, query, vars, out); err != nil {
		a.t.Fatalf("linear: %v", err)
	}
}

func (a liveLinearAPI) viewer() string {
	var res struct{ Viewer struct{ ID string } }
	a.run(`{ viewer { id } }`, nil, &res)
	return res.Viewer.ID
}

func (a liveLinearAPI) team(key string) liveLinearTeam {
	var res struct {
		Teams struct {
			Nodes []struct {
				ID, Key string
				States  struct{ Nodes []liveLinearState }
			}
		}
	}
	a.run(`{ teams { nodes { id key states { nodes { id name type } } } } }`, nil, &res)
	for _, tm := range res.Teams.Nodes {
		if key == "" || strings.EqualFold(tm.Key, key) {
			return liveLinearTeam{ID: tm.ID, Key: tm.Key, States: tm.States.Nodes}
		}
	}
	a.t.Fatalf("no Linear team %q", key)
	return liveLinearTeam{}
}

func (a liveLinearAPI) createLabel(teamID, name string) liveLinearLabel {
	var res struct {
		IssueLabelCreate struct{ IssueLabel struct{ ID string } }
	}
	a.run(`mutation($name: String!, $team: String!) {
	  issueLabelCreate(input: { name: $name, teamId: $team }) { issueLabel { id } } }`,
		map[string]any{"name": name, "team": teamID}, &res)
	id := res.IssueLabelCreate.IssueLabel.ID
	a.t.Cleanup(func() {
		a.run(`mutation($id: String!) { issueLabelDelete(id: $id) { success } }`, map[string]any{"id": id}, &struct{}{})
	})
	return liveLinearLabel{id: id, name: name}
}

func (a liveLinearAPI) createIssue(teamID, assignee, title string) (id, key string) {
	var res struct {
		IssueCreate struct {
			Issue struct{ ID, Identifier string }
		}
	}
	a.run(`mutation($team: String!, $assignee: String!, $title: String!) {
	  issueCreate(input: { teamId: $team, assigneeId: $assignee, title: $title,
	    description: "Created by devdash's TestLiveLinearEditing; it deletes itself." }) {
	    issue { id identifier } } }`,
		map[string]any{"team": teamID, "assignee": assignee, "title": title}, &res)
	id = res.IssueCreate.Issue.ID
	a.t.Cleanup(func() {
		a.run(`mutation($id: String!) { issueDelete(id: $id) { success } }`, map[string]any{"id": id}, &struct{}{})
	})
	return id, res.IssueCreate.Issue.Identifier
}

func (a liveLinearAPI) addLabel(issueID, labelID string) {
	a.run(`mutation($id: String!, $label: String!) { issueAddLabel(id: $id, labelId: $label) { success } }`,
		map[string]any{"id": issueID, "label": labelID}, &struct{}{})
}

func (a liveLinearAPI) state(issueID string) liveLinearState {
	var res struct {
		Issue struct{ State liveLinearState }
	}
	a.run(`query($id: String!) { issue(id: $id) { state { id name type } } }`, map[string]any{"id": issueID}, &res)
	return res.Issue.State
}

func (a liveLinearAPI) wantLabels(issueID string, want ...string) {
	a.t.Helper()
	var res struct {
		Issue struct {
			Labels struct{ Nodes []struct{ Name string } }
		}
	}
	a.run(`query($id: String!) { issue(id: $id) { labels { nodes { name } } } }`, map[string]any{"id": issueID}, &res)
	var got []string
	for _, l := range res.Issue.Labels.Nodes {
		got = append(got, l.Name)
	}
	slices.Sort(got)
	want = slices.Sorted(slices.Values(want))
	if !slices.Equal(got, want) {
		a.t.Errorf("labels = %v, want %v", got, want)
	}
}
