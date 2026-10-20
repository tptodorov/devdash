package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
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
	// labelOps records each label mutation as +id or -id; labelWrites counts
	// the requests that carried them.
	labelOps    []string
	labelWrites int
	failRemove  bool // removals fail, as Linear reports a per-field error
}

// linearLabelOpRE reads one aliased label mutation out of a request.
var linearLabelOpRE = regexp.MustCompile(`(\w+): issue(Add|Remove)Label\(id: \$id, labelId: \$(\w+)\)`)

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
		case strings.Contains(req.Query, "issueAddLabel") || strings.Contains(req.Query, "issueRemoveLabel"):
			s.labelWrites++
			data, errs := []string{}, []string{}
			for _, m := range linearLabelOpRE.FindAllStringSubmatch(req.Query, -1) {
				alias, op, id := m[1], m[2], req.Variables[m[3]].(string)
				if op == "Remove" && s.failRemove {
					data = append(data, fmt.Sprintf(`%q:null`, alias))
					errs = append(errs, fmt.Sprintf(`{"message":"Label is locked","path":[%q]}`, alias))
					continue
				}
				sign := map[string]string{"Add": "+", "Remove": "-"}[op]
				s.labelOps = append(s.labelOps, sign+id)
				data = append(data, fmt.Sprintf(`%q:{"success":true}`, alias))
			}
			resp := `{"data":{` + strings.Join(data, ",") + `}`
			if len(errs) > 0 {
				resp += `,"errors":[` + strings.Join(errs, ",") + `]`
			}
			io.WriteString(w, resp+"}")
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

	if err := c.ApplyTransition(context.Background(), "ENG-12", Transition{ID: "s-done"}); err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if len(srv.updates) != 1 || srv.updates[0]["stateId"] != "s-done" || srv.updates[0]["id"] != "ENG-12" {
		t.Errorf("updates = %+v, want one moving ENG-12 to s-done", srv.updates)
	}
}

func TestLinearApplyTransitionReportsAFailedUpdate(t *testing.T) {
	srv := &linearEditServer{failWrite: true}
	c := testLinearClient(t, srv.handler(t))

	if err := c.ApplyTransition(context.Background(), "ENG-12", Transition{ID: "s-done"}); err == nil {
		t.Fatal("ApplyTransition() should error when Linear reports success: false")
	}
}

// Labels are added and removed one by one rather than by replacing the set, so
// a label someone else added in the meantime is never lost.
func TestLinearUpdateLabelsAddsAndRemovesEachLabel(t *testing.T) {
	srv := &linearEditServer{}
	c := testLinearClient(t, srv.handler(t))

	// Names match without regard to case; "keep" is already there.
	err := c.UpdateLabels(context.Background(), "ENG-12", []string{"symphony-ready", "workspace-wide", "KEEP"}, []string{"BUG"})
	if err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	got := slices.Sorted(slices.Values(srv.labelOps))
	if want := []string{"+l-ready", "+l-ws", "-l-bug"}; !slices.Equal(got, want) {
		t.Errorf("label ops = %v, want %v", got, want)
	}
	if srv.labelWrites != 1 || len(srv.updates) != 0 {
		t.Errorf("made %d label requests and %d set-replacing updates, want 1 and 0", srv.labelWrites, len(srv.updates))
	}
}

// Linear applies the mutations of one request one by one, so a failure can
// leave part of the change applied. The error must say exactly what was.
func TestLinearUpdateLabelsSaysWhatAPartialFailureApplied(t *testing.T) {
	srv := &linearEditServer{failRemove: true}
	c := testLinearClient(t, srv.handler(t))

	err := c.UpdateLabels(context.Background(), "ENG-12", []string{"symphony-ready"}, []string{"bug"})
	if err == nil {
		t.Fatal("UpdateLabels() reported success though a removal failed")
	}
	// Labels are named as Linear spells them.
	for _, want := range []string{`added "Symphony-Ready"`, `could not remove "bug"`, "Label is locked", "ENG-12"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
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
	if len(srv.updates) != 0 || srv.labelWrites != 0 {
		t.Errorf("wrote %d updates and %d label requests, want nothing written", len(srv.updates), srv.labelWrites)
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
	if len(srv.updates) != 0 || srv.labelWrites != 0 {
		t.Errorf("wrote %d updates and %d label requests, want nothing written", len(srv.updates), srv.labelWrites)
	}
	if srv.lookupVars["lookup"] != false {
		t.Errorf("lookup = %v, want false: there is nothing to add", srv.lookupVars["lookup"])
	}
}
