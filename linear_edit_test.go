package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
)

// linearEditServer answers the three kinds of request the Linear editor makes
// and records the issueUpdate input it receives.
type linearEditServer struct {
	updates    []map[string]any // variables of each issueUpdate
	lookups    int
	lookupVars map[string]any // variables of the last label lookup
	failWrite  bool
}

func (s *linearEditServer) handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		switch {
		case strings.Contains(req.Query, "issueUpdate"):
			s.updates = append(s.updates, req.Variables)
			ok := "true"
			if s.failWrite {
				ok = "false"
			}
			io.WriteString(w, `{"data":{"issueUpdate":{"success":`+ok+`}}}`)
		case strings.Contains(req.Query, "issueLabels"):
			s.lookups++
			s.lookupVars = req.Variables
			io.WriteString(w, `{"data":{
			  "issue":{"id":"uuid-1","team":{"id":"team-1"},
			    "labels":{"nodes":[{"id":"l-bug","name":"bug"},{"id":"l-keep","name":"keep"}]}},
			  "issueLabels":{"nodes":[
			    {"id":"l-ready","name":"Symphony-Ready","team":{"id":"team-1"}},
			    {"id":"l-other","name":"elsewhere","team":{"id":"team-2"}},
			    {"id":"l-ws","name":"workspace-wide","team":null}]}}}`)
		case strings.Contains(req.Query, "states"):
			io.WriteString(w, `{"data":{"issue":{"id":"uuid-1",
			  "state":{"id":"s-prog"},
			  "team":{"states":{"nodes":[
			    {"id":"s-done","name":"Done","type":"completed","position":3},
			    {"id":"s-todo","name":"Todo","type":"unstarted","position":1},
			    {"id":"s-prog","name":"In Progress","type":"started","position":2},
			    {"id":"s-back","name":"Backlog","type":"backlog","position":0}]}}}}}`)
		default:
			t.Errorf("unexpected query: %s", req.Query)
		}
	}
}

func TestLinearTransitionsListsTheTeamsOtherStates(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	got, err := c.Transitions(context.Background(), "ENG-12")
	if err != nil {
		t.Fatalf("Transitions() error = %v", err)
	}

	var names []string
	for _, tr := range got {
		names = append(names, tr.To)
	}
	// In workflow order, without the state the issue is already in.
	if want := []string{"Backlog", "Todo", "Done"}; !slices.Equal(names, want) {
		t.Fatalf("transitions to %v, want %v", names, want)
	}
	if got[0].ID != "s-back" || got[0].ToCategory != "To Do" || got[2].ToCategory != "Done" {
		t.Errorf("transitions = %+v", got)
	}
	if len(got[0].Required) != 0 {
		t.Errorf("a Linear move needs no extra fields, got %+v", got[0].Required)
	}
}

func TestLinearApplyTransitionMovesTheIssue(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	if err := c.ApplyTransition(context.Background(), "ENG-12", "s-done", nil); err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if len(srv.updates) != 1 || srv.updates[0]["stateId"] != "s-done" || srv.updates[0]["id"] != "ENG-12" {
		t.Errorf("updates = %+v, want one moving ENG-12 to s-done", srv.updates)
	}
}

func TestLinearApplyTransitionReportsAFailedUpdate(t *testing.T) {
	srv := &linearEditServer{failWrite: true}
	c := testLinearClient(t, srv.handler(t))

	if err := c.ApplyTransition(context.Background(), "ENG-12", "s-done", nil); err == nil {
		t.Fatal("ApplyTransition() should error when Linear reports success: false")
	}
}

func TestLinearUpdateLabelsAddsAndRemoves(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	// Label names match without regard to case, like the rest of the dashboard.
	err := c.UpdateLabels(context.Background(), "ENG-12", []string{"symphony-ready", "workspace-wide"}, []string{"BUG"})
	if err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	if len(srv.updates) != 1 {
		t.Fatalf("got %d updates, want 1", len(srv.updates))
	}
	ids, _ := srv.updates[0]["labelIds"].([]any)
	var got []string
	for _, id := range ids {
		got = append(got, id.(string))
	}
	slices.Sort(got)
	if want := []string{"l-keep", "l-ready", "l-ws"}; !slices.Equal(got, want) {
		t.Errorf("labelIds = %v, want %v", got, want)
	}
	if srv.updates[0]["id"] != "uuid-1" {
		t.Errorf("updated %v, want the issue's own id", srv.updates[0]["id"])
	}
}

func TestLinearUpdateLabelsNamesALabelThatDoesNotExist(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	// "elsewhere" exists, but only on another team.
	err := c.UpdateLabels(context.Background(), "ENG-12", []string{"elsewhere"}, nil)
	if err == nil || !strings.Contains(err.Error(), `"elsewhere"`) {
		t.Fatalf("UpdateLabels() error = %v, want it to name the missing label", err)
	}
	if len(srv.updates) != 0 {
		t.Errorf("sent %+v, want nothing written", srv.updates)
	}
}

func TestLinearUpdateLabelsWithNothingToDoSendsNothing(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	if err := c.UpdateLabels(context.Background(), "ENG-12", nil, nil); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	if srv.lookups != 0 || len(srv.updates) != 0 {
		t.Errorf("made %d lookups and %d updates, want none", srv.lookups, len(srv.updates))
	}
}

// The labels to add are looked up by name, so a workspace with more labels than
// one page holds still finds them, and a missing one is really missing.
func TestLinearUpdateLabelsLooksUpOnlyTheLabelsToAdd(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	if err := c.UpdateLabels(context.Background(), "ENG-12", []string{"symphony-ready"}, nil); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	got, _ := json.Marshal(srv.lookupVars["names"])
	if want := `{"or":[{"name":{"eqIgnoreCase":"symphony-ready"}}]}`; string(got) != want {
		t.Errorf("label filter = %s, want %s", got, want)
	}
	if srv.lookupVars["lookup"] != true {
		t.Errorf("lookup = %v, want true when adding", srv.lookupVars["lookup"])
	}
}

func TestLinearUpdateLabelsThatChangesNothingWritesNothing(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	if err := c.UpdateLabels(context.Background(), "ENG-12", nil, []string{"never-there"}); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	if len(srv.updates) != 0 {
		t.Errorf("sent %+v, want nothing written", srv.updates)
	}
	if srv.lookupVars["lookup"] != false {
		t.Errorf("lookup = %v, want false: there is nothing to add", srv.lookupVars["lookup"])
	}
}
