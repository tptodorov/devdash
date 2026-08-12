package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// transitionsFixture mirrors the shape the real MOD workflow returns, including
// a transition whose name differs from its destination and one that demands a
// resolution.
const transitionsFixture = `{"transitions":[
 {"id":"21","name":"In Progress","to":{"name":"In Progress","statusCategory":{"name":"In Progress"}},"fields":{}},
 {"id":"31","name":"Done","to":{"name":"Awaiting Verification","statusCategory":{"name":"In Progress"}},"fields":{}},
 {"id":"41","name":"Closed","to":{"name":"Closed","statusCategory":{"name":"Done"}},"fields":{
    "resolution":{"required":true,"name":"Resolution","schema":{"type":"resolution"},
      "allowedValues":[{"id":"10000","name":"Done"},{"id":"10001","name":"Won't Do"}]},
    "fixVersions":{"required":false,"name":"Fix versions","schema":{"type":"array"}}}},
 {"id":"51","name":"Needs Comment","to":{"name":"Reviewed","statusCategory":{"name":"In Progress"}},"fields":{
    "comment":{"required":true,"name":"Comment","schema":{"type":"string"},"allowedValues":[]}}}
]}`

func testClient(t *testing.T, h http.HandlerFunc) (*jiraClient, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &jiraClient{baseURL: srv.URL, user: "u", token: "tok", http: srv.Client()}, srv
}

func TestTransitionsParsing(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Path; got != "/rest/api/3/issue/PROJ-1/transitions" {
			t.Errorf("path = %q", got)
		}
		if got := r.URL.Query().Get("expand"); got != "transitions.fields" {
			t.Errorf("expand = %q, want transitions.fields", got)
		}
		if u, p, _ := r.BasicAuth(); u != "u" || p != "tok" {
			t.Errorf("basic auth = %q/%q", u, p)
		}
		io.WriteString(w, transitionsFixture)
	})

	got, err := c.Transitions(context.Background(), "PROJ-1")
	if err != nil {
		t.Fatalf("Transitions() error = %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("got %d transitions, want 4", len(got))
	}

	// The destination, not the transition label, is what the user acts on.
	if got[1].Name != "Done" || got[1].To != "Awaiting Verification" {
		t.Errorf("transition 31 = %q -> %q, want Done -> Awaiting Verification", got[1].Name, got[1].To)
	}

	// A required field with allowed values is offered as a second step.
	closed := got[2]
	if !closed.NeedsInput() {
		t.Error("Closed should need input for resolution")
	}
	if len(closed.Blocked()) != 0 {
		t.Errorf("Closed should not be blocked, got %v", closed.Blocked())
	}
	if len(closed.Required) != 1 || closed.Required[0].Key != "resolution" {
		t.Fatalf("Closed required = %+v, want just resolution", closed.Required)
	}
	if len(closed.Required[0].Allowed) != 2 || closed.Required[0].Allowed[0].ID != "10000" {
		t.Errorf("resolution options = %+v", closed.Required[0].Allowed)
	}

	// A required field with no selectable values cannot be satisfied here.
	if blocked := got[3].Blocked(); len(blocked) != 1 || blocked[0] != "Comment" {
		t.Errorf("Needs Comment blocked = %v, want [Comment]", blocked)
	}
}

func TestApplyTransitionSendsFields(t *testing.T) {
	var body map[string]any
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("content-type = %q", ct)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	})

	fields := map[string]any{"resolution": map[string]string{"id": "10000"}}
	if err := c.ApplyTransition(context.Background(), "PROJ-1", "41", fields); err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}

	tr, ok := body["transition"].(map[string]any)
	if !ok || tr["id"] != "41" {
		t.Errorf("transition in body = %v, want id 41", body["transition"])
	}
	sent, ok := body["fields"].(map[string]any)
	if !ok {
		t.Fatalf("fields missing from body: %v", body)
	}
	res, ok := sent["resolution"].(map[string]any)
	if !ok || res["id"] != "10000" {
		t.Errorf("resolution = %v, want id 10000", sent["resolution"])
	}
}

func TestApplyTransitionOmitsEmptyFields(t *testing.T) {
	var body map[string]any
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := c.ApplyTransition(context.Background(), "PROJ-1", "21", nil); err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	if _, present := body["fields"]; present {
		t.Errorf("fields should be omitted when empty, got %v", body)
	}
}

func TestApplyTransitionSurfacesJIRAError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "field error",
			status: http.StatusBadRequest,
			body:   `{"errorMessages":[],"errors":{"resolution":"Resolution is required."}}`,
			want:   "resolution: Resolution is required.",
		},
		{
			name:   "global message",
			status: http.StatusBadRequest,
			body:   `{"errorMessages":["Transition is not valid for this issue."],"errors":{}}`,
			want:   "Transition is not valid for this issue.",
		},
		{
			name:   "permission denied",
			status: http.StatusForbidden,
			body:   `{}`,
			want:   "not allowed to move PROJ-1",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			err := c.ApplyTransition(context.Background(), "PROJ-1", "41", nil)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// An expired token makes JIRA answer the search with 200 and no issues, so the
// client must confirm identity rather than report an empty backlog.
func TestTicketsDetectsUnauthenticatedEmptyResult(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/jql"):
			io.WriteString(w, `{"issues":[],"isLast":true}`)
		case strings.HasSuffix(r.URL.Path, "/myself"):
			w.WriteHeader(http.StatusUnauthorized)
		}
	})

	_, err := c.Tickets(context.Background(), DefaultJQL)
	if err == nil {
		t.Fatal("expected an authentication error, got nil")
	}
	if !strings.Contains(err.Error(), "authentication failed") {
		t.Errorf("error = %q, want an authentication failure", err)
	}
}

func TestTicketsEmptyButAuthenticatedIsNotAnError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/search/jql"):
			io.WriteString(w, `{"issues":[],"isLast":true}`)
		case strings.HasSuffix(r.URL.Path, "/myself"):
			io.WriteString(w, `{"accountId":"abc123"}`)
		}
	})

	got, err := c.Tickets(context.Background(), DefaultJQL)
	if err != nil {
		t.Fatalf("Tickets() error = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d tickets, want 0", len(got))
	}
}
