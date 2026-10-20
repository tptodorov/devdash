package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLiveGitHubIssuesEditing runs the GitHub Editor against the real API on a
// throwaway issue in the repository in the working directory. It writes, so
// it only runs when asked:
//
//	DEVDASH_LIVE_GITHUB_WRITES=1 GITHUB_TOKEN=$(gh auth token) go test -run TestLiveGitHubIssuesEditing -v
//
// It opens an issue, edits its labels, schedules and unschedules it for
// Symphony, and closes it as not planned. Every effect is read back through
// the REST API, not through the code under test. Labels the test created are
// deleted from the repository afterwards; the closed issue remains.
func TestLiveGitHubIssuesEditing(t *testing.T) {
	if os.Getenv("DEVDASH_LIVE_GITHUB_WRITES") != "1" {
		t.Skip("set DEVDASH_LIVE_GITHUB_WRITES=1 to run this against real GitHub; it creates and closes an issue")
	}
	tr := liveGitHubIssues(t)
	// Not t.Context(): that is cancelled before cleanups run, and the cleanups
	// need it to close the issue and delete the labels. Registered first, the
	// cancel runs after every other cleanup.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	t.Cleanup(cancel)
	api := liveGitHubAPI{t: t, ctx: ctx, gh: tr.gh, repo: tr.canonical(ctx)}

	stamp := time.Now().UTC().Format("20060102-150405")
	ready := "devdash-live-" + stamp    // stands in for Symphony's required label
	second := "Devdash-Second-" + stamp // mixed case on purpose
	theirs := "devdash-theirs-" + stamp // added behind the editor's back
	for _, l := range []string{ready, second, theirs} {
		api.deleteLabelAfterwards(l)
	}

	n := api.createIssue("devdash live editing test " + stamp + " (safe to close)")
	key := fmt.Sprintf("#%d", n)
	t.Cleanup(func() { api.closeIfOpen(n) })
	t.Logf("testing on %s %s", api.repo, key)

	t.Run("transitions offer closing an open issue", func(t *testing.T) {
		trs, err := tr.Transitions(ctx, key)
		if err != nil {
			t.Fatalf("Transitions: %v", err)
		}
		var ids []string
		for _, x := range trs {
			ids = append(ids, x.ID)
		}
		if !slices.Equal(ids, []string{"completed", "not_planned"}) {
			t.Errorf("transition ids = %v", ids)
		}
	})

	t.Run("a number naming no issue is refused", func(t *testing.T) {
		if _, err := tr.Transitions(ctx, "#99999999"); err == nil {
			t.Error("Transitions on a missing issue succeeded")
		}
		if err := tr.UpdateLabels(ctx, "#99999999", []string{ready}, nil); err == nil {
			t.Error("UpdateLabels on a missing issue succeeded")
		}
	})

	t.Run("labels are added, including ones the repository lacks", func(t *testing.T) {
		if err := tr.UpdateLabels(ctx, key, []string{ready, second}, nil); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(n, ready, second)
	})

	t.Run("a label carried in another case is not added twice", func(t *testing.T) {
		if err := tr.UpdateLabels(ctx, key, []string{strings.ToUpper(ready)}, nil); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(n, ready, second)
	})

	t.Run("removal ignores case and keeps a label someone else added", func(t *testing.T) {
		api.addLabel(n, theirs) // as another person would, before our update
		if err := tr.UpdateLabels(ctx, key, nil, []string{strings.ToLower(second), "never-there-" + stamp}); err != nil {
			t.Fatalf("UpdateLabels: %v", err)
		}
		api.wantLabels(n, ready, theirs)
	})

	t.Run("Symphony scheduling toggles only the labels", func(t *testing.T) {
		var cfg symphonyConfig
		cfg.Tracker.Kind = "github"
		cfg.Tracker.Provider.ProjectKey = api.repo
		cfg.Tracker.RequiredLabels = []string{ready}

		ticket := api.ticket(n)
		plan := planToggle(cfg, ticket)
		if plan.refusal != "" || !plan.unschedule {
			t.Fatalf("labelled open issue: plan = %+v, want an unschedule", plan)
		}
		if err := applySchedule(ctx, tr, plan); err != nil {
			t.Fatalf("unschedule: %v", err)
		}
		api.wantLabels(n, theirs)

		plan = planToggle(cfg, api.ticket(n))
		if plan.refusal != "" || plan.unschedule || plan.toStatus != "" {
			t.Fatalf("unlabelled open issue: plan = %+v, want only the label added", plan)
		}
		if err := applySchedule(ctx, tr, plan); err != nil {
			t.Fatalf("schedule: %v", err)
		}
		api.wantLabels(n, ready, theirs)
		if state, _ := api.state(n); state != "open" {
			t.Errorf("scheduling changed the state to %q", state)
		}
	})

	t.Run("closing as not planned closes it and ends further moves", func(t *testing.T) {
		if err := tr.ApplyTransition(ctx, key, Transition{ID: "not_planned"}); err != nil {
			t.Fatalf("ApplyTransition: %v", err)
		}
		if state, reason := api.state(n); state != "closed" || reason != "not_planned" {
			t.Errorf("state = %q (%q), want closed (not_planned)", state, reason)
		}
		if _, err := tr.Transitions(ctx, key); err == nil || !strings.Contains(err.Error(), "closed") {
			t.Errorf("Transitions on a closed issue: err = %v, want it to say closed", err)
		}
	})
}

// liveGitHubAPI reads and arranges state through GitHub's REST API directly,
// independently of the code under test.
type liveGitHubAPI struct {
	t    *testing.T
	ctx  context.Context
	gh   *githubClient
	repo string
}

func (a liveGitHubAPI) call(method, path string, payload any, out any, ok ...int) int {
	a.t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	resp, status, err := a.gh.do(a.ctx, method, a.gh.root()+"/repos/"+a.repo+path, body)
	if err != nil {
		a.t.Fatalf("%s %s: %v", method, path, err)
	}
	if status >= 300 && !slices.Contains(ok, status) {
		a.t.Fatalf("%s %s: %d %s", method, path, status, firstLine(resp))
	}
	if out != nil {
		if err := json.Unmarshal(resp, out); err != nil {
			a.t.Fatalf("decoding %s %s: %v", method, path, err)
		}
	}
	return status
}

func (a liveGitHubAPI) createIssue(title string) int {
	var issue struct{ Number int }
	a.call(http.MethodPost, "/issues", map[string]string{"title": title,
		"body": "Created by devdash's TestLiveGitHubIssuesEditing; it closes itself."}, &issue)
	return issue.Number
}

func (a liveGitHubAPI) closeIfOpen(n int) {
	if state, _ := a.state(n); state == "open" {
		a.call(http.MethodPatch, fmt.Sprintf("/issues/%d", n),
			map[string]string{"state": "closed", "state_reason": "not_planned"}, nil)
	}
}

func (a liveGitHubAPI) state(n int) (state, reason string) {
	var issue struct {
		State       string `json:"state"`
		StateReason string `json:"state_reason"`
	}
	a.call(http.MethodGet, fmt.Sprintf("/issues/%d", n), nil, &issue)
	return issue.State, issue.StateReason
}

func (a liveGitHubAPI) labels(n int) []string {
	var labels []struct{ Name string }
	a.call(http.MethodGet, fmt.Sprintf("/issues/%d/labels", n), nil, &labels)
	var out []string
	for _, l := range labels {
		out = append(out, l.Name)
	}
	slices.Sort(out)
	return out
}

func (a liveGitHubAPI) wantLabels(n int, want ...string) {
	a.t.Helper()
	want = slices.Sorted(slices.Values(want))
	if got := a.labels(n); !slices.Equal(got, want) {
		a.t.Errorf("labels = %v, want %v", got, want)
	}
}

func (a liveGitHubAPI) addLabel(n int, label string) {
	a.call(http.MethodPost, fmt.Sprintf("/issues/%d/labels", n), map[string][]string{"labels": {label}}, nil)
}

// ticket builds the Ticket the dashboard would show, from the REST API rather
// than search, which can lag behind a just-created issue.
func (a liveGitHubAPI) ticket(n int) Ticket {
	var issue struct {
		HTMLURL string `json:"html_url"`
	}
	a.call(http.MethodGet, fmt.Sprintf("/issues/%d", n), nil, &issue)
	return Ticket{Key: fmt.Sprintf("#%d", n), URL: issue.HTMLURL, Source: "GitHub", Status: "Open",
		Category: "To Do", Labels: a.labels(n)}
}

// deleteLabelAfterwards removes a repository label at the end of the test, but
// only one the test created: a label that already existed is left alone.
func (a liveGitHubAPI) deleteLabelAfterwards(name string) {
	path := "/labels/" + url.PathEscape(name)
	if a.call(http.MethodGet, path, nil, nil, http.StatusNotFound) != http.StatusNotFound {
		a.t.Fatalf("label %q already exists in %s; refusing to touch it", name, a.repo)
	}
	a.t.Cleanup(func() { a.call(http.MethodDelete, path, nil, nil, http.StatusNotFound) })
}
