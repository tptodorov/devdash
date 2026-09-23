package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testLinearClient(t *testing.T, h http.HandlerFunc) *linearClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &linearClient{apiKey: "test-key", endpoint: srv.URL, http: srv.Client()}
}

const linearPage1 = `{"data":{"viewer":{"assignedIssues":{"nodes":[
 {"identifier":"ENG-12","title":"Fix the flaky retry test","url":"https://linear.app/acme/issue/ENG-12",
  "state":{"name":"In Progress","type":"started"},"labels":{"nodes":[{"name":"bug"}]},"parent":null},
 {"identifier":"ENG-9","title":"Sub-issue of ENG-1","url":"https://linear.app/acme/issue/ENG-9",
  "state":{"name":"Todo","type":"unstarted"},"labels":{"nodes":[]},"parent":{"id":"parent-1"}}
],"pageInfo":{"hasNextPage":true,"endCursor":"cursor-1"}}}}}`

const linearPage2 = `{"data":{"viewer":{"assignedIssues":{"nodes":[
 {"identifier":"ENG-3","title":"Done already","url":"https://linear.app/acme/issue/ENG-3",
  "state":{"name":"Done","type":"completed"},"labels":{"nodes":[]},"parent":null}
],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`

func TestLinearTicketsParsesAndPaginates(t *testing.T) {
	var requests []map[string]any
	c := testLinearClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "test-key" {
			t.Errorf("Authorization = %q, want the raw API key", got)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		requests = append(requests, req)

		if len(requests) == 1 {
			io.WriteString(w, linearPage1)
			return
		}
		io.WriteString(w, linearPage2)
	})

	got, err := c.Tickets(context.Background(), "")
	if err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}
	if len(requests) != 2 {
		t.Fatalf("made %d requests, want 2 (one per page)", len(requests))
	}

	// The second request carries the cursor from the first page's response.
	vars := requests[1]["variables"].(map[string]any)
	if vars["after"] != "cursor-1" {
		t.Errorf("second request after = %v, want cursor-1", vars["after"])
	}

	if len(got) != 3 {
		t.Fatalf("got %d tickets, want 3", len(got))
	}

	first := got[0]
	if first.Key != "ENG-12" || first.Summary != "Fix the flaky retry test" {
		t.Errorf("first ticket = %+v", first)
	}
	if first.Status != "In Progress" || first.Category != "In Progress" {
		t.Errorf("first ticket status/category = %q/%q, want In Progress/In Progress", first.Status, first.Category)
	}
	if first.URL != "https://linear.app/acme/issue/ENG-12" {
		t.Errorf("first ticket URL = %q", first.URL)
	}
	if len(first.Labels) != 1 || first.Labels[0] != "bug" {
		t.Errorf("first ticket labels = %v, want [bug]", first.Labels)
	}
	if first.IsSubtask {
		t.Error("first ticket has no parent, should not be a subtask")
	}
	if first.Source != "Linear" {
		t.Errorf("first ticket source = %q, want Linear", first.Source)
	}

	second := got[1]
	if !second.IsSubtask {
		t.Error("second ticket has a parent, should be a subtask")
	}
	if second.Category != "To Do" {
		t.Errorf("second ticket category = %q, want To Do", second.Category)
	}

	third := got[2]
	if third.Category != "Done" {
		t.Errorf("third ticket category = %q, want Done", third.Category)
	}
}

func TestLinearTicketsUsesDefaultFilterWhenQueryEmpty(t *testing.T) {
	var gotFilter map[string]any
	c := testLinearClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		gotFilter = req["variables"].(map[string]any)["filter"].(map[string]any)
		io.WriteString(w, linearPage2)
	})

	if _, err := c.Tickets(context.Background(), ""); err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}

	var wantFilter map[string]any
	if err := json.Unmarshal([]byte(DefaultLinearFilter), &wantFilter); err != nil {
		t.Fatalf("parsing DefaultLinearFilter: %v", err)
	}
	wantJSON, _ := json.Marshal(wantFilter)
	gotJSON, _ := json.Marshal(gotFilter)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("filter sent = %s, want %s", gotJSON, wantJSON)
	}
}

func TestLinearTicketsSendsCustomFilter(t *testing.T) {
	const custom = `{"team":{"key":{"eq":"ENG"}}}`
	var gotFilter map[string]any
	c := testLinearClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decoding request: %v", err)
		}
		gotFilter = req["variables"].(map[string]any)["filter"].(map[string]any)
		io.WriteString(w, linearPage2)
	})

	if _, err := c.Tickets(context.Background(), custom); err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}

	if gotFilter["team"] == nil {
		t.Errorf("filter sent = %v, want the custom team filter", gotFilter)
	}
}

func TestLinearTicketsRejectsInvalidFilterJSON(t *testing.T) {
	c := &linearClient{apiKey: "k", endpoint: "http://unused.test", http: http.DefaultClient}
	if _, err := c.Tickets(context.Background(), "not json"); err == nil {
		t.Fatal("Tickets() with invalid filter JSON should error")
	}
}

func TestLinearTicketsReportsHTTPErrors(t *testing.T) {
	c := testLinearClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, "unauthorized")
	})

	_, err := c.Tickets(context.Background(), "")
	if err == nil {
		t.Fatal("Tickets() should error on a non-200 response")
	}
}

func TestLinearTicketsReportsGraphQLErrors(t *testing.T) {
	c := testLinearClient(t, func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"errors":[{"message":"Authentication required"}]}`)
	})

	_, err := c.Tickets(context.Background(), "")
	if err == nil {
		t.Fatal("Tickets() should error when the response carries GraphQL errors")
	}
}

func TestNewLinearClientRequiresAPIKey(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	if _, err := newLinearClient(http.DefaultClient); err == nil {
		t.Fatal("newLinearClient() without LINEAR_API_KEY should error")
	}

	t.Setenv("LINEAR_API_KEY", "secret")
	c, err := newLinearClient(http.DefaultClient)
	if err != nil {
		t.Fatalf("newLinearClient() error = %v", err)
	}
	if c.apiKey != "secret" {
		t.Errorf("apiKey = %q, want secret", c.apiKey)
	}
}
