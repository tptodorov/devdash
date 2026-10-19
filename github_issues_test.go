package main

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

// issuesServer serves GitHub's REST and GraphQL endpoints for one repository.
// It records every GraphQL search string and replies with pages in order.
type issuesServer struct {
	searches []string
	afters   []string
	pages    []string
	rest     []string // "METHOD /path body" for each REST write
	// issue is what reading the issue returns; an open issue labelled
	// "needs triage" and "keep" when empty.
	issue string
}

func (s *issuesServer) client(t *testing.T) *githubIssues {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/app", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"full_name":"Acme/App"}`)
	})
	mux.HandleFunc("/graphql", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		s.searches = append(s.searches, req.Variables["q"].(string))
		after, _ := req.Variables["after"].(string)
		s.afters = append(s.afters, after)
		page := s.pages[min(len(s.searches), len(s.pages))-1]
		io.WriteString(w, page)
	})
	mux.HandleFunc("/repos/Acme/App/issues/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			io.WriteString(w, cmp.Or(s.issue, `{"state":"open","labels":[{"name":"needs triage"},{"name":"keep"}]}`))
			return
		}
		b, _ := io.ReadAll(r.Body)
		s.rest = append(s.rest, strings.TrimSpace(r.Method+" "+r.URL.EscapedPath()+" "+string(b)))
		w.WriteHeader(http.StatusOK)
		io.WriteString(w, `{}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	gh := &githubClient{token: "tok", http: srv.Client(), api: srv.URL}
	return newGitHubIssues(gh, "acme", "app")
}

const issuesPage = `{"data":{"search":{"nodes":[
 {"number":1,"title":"Plain issue","url":"https://github.com/Acme/App/issues/1",
  "issueType":null,"labels":{"nodes":[]},"parent":null,"subIssues":{"totalCount":0},
  "closedByPullRequestsReferences":{"nodes":[]}},
 {"number":2,"title":" Typed and labelled ","url":"https://github.com/Acme/App/issues/2",
  "issueType":{"name":"Bug"},"labels":{"nodes":[{"name":"p1"},{"name":"api"}]},
  "parent":null,"subIssues":{"totalCount":3},
  "closedByPullRequestsReferences":{"nodes":[]}},
 {"number":3,"title":"Sub-issue","url":"https://github.com/Acme/App/issues/3",
  "issueType":null,"labels":{"nodes":[]},"parent":{"number":2},"subIssues":{"totalCount":0},
  "closedByPullRequestsReferences":{"nodes":[]}},
 {"number":4,"title":"Being fixed","url":"https://github.com/Acme/App/issues/4",
  "issueType":null,"labels":{"nodes":[]},"parent":null,"subIssues":{"totalCount":0},
  "closedByPullRequestsReferences":{"nodes":[
    {"url":"https://github.com/Acme/App/pull/40","state":"OPEN"},
    {"url":"https://github.com/Acme/App/pull/39","state":"MERGED"}]}},
 {"number":5,"title":"Only a merged fix","url":"https://github.com/Acme/App/issues/5",
  "issueType":null,"labels":{"nodes":[]},"parent":null,"subIssues":{"totalCount":0},
  "closedByPullRequestsReferences":{"nodes":[{"url":"https://github.com/Acme/App/pull/50","state":"MERGED"}]}},
 {}
],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}`

func TestGitHubIssuesTicketsMapsIssues(t *testing.T) {
	srv := &issuesServer{pages: []string{issuesPage}}
	tr := srv.client(t)

	got, err := tr.Tickets(context.Background(), defaultIssuesQuery)
	if err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}

	// The search is scoped to the repository under its canonical name, open
	// issues only, with the caller's qualifiers appended.
	if want := "repo:Acme/App is:issue is:open assignee:@me"; len(srv.searches) != 1 || srv.searches[0] != want {
		t.Errorf("searches = %q, want [%q]", srv.searches, want)
	}

	if len(got) != 5 {
		t.Fatalf("got %d tickets, want 5 (the empty node is not an issue): %+v", len(got), got)
	}
	byKey := map[string]Ticket{}
	for _, tk := range got {
		byKey[tk.Key] = tk
		if tk.Source != "GitHub" {
			t.Errorf("%s source = %q, want GitHub", tk.Key, tk.Source)
		}
	}

	plain := byKey["#1"]
	if plain.Summary != "Plain issue" || plain.Status != "Open" || plain.Category != "To Do" ||
		plain.URL != "https://github.com/Acme/App/issues/1" || plain.Type != "" || plain.IsSubtask {
		t.Errorf("#1 = %+v", plain)
	}

	typed := byKey["#2"]
	if typed.Summary != "Typed and labelled" || typed.Type != "Bug" ||
		!slices.Equal(typed.Labels, []string{"p1", "api"}) || typed.ChildCount != 3 {
		t.Errorf("#2 = %+v", typed)
	}

	if !byKey["#3"].IsSubtask {
		t.Error("#3 has a parent and should be a sub-task")
	}

	fixing := byKey["#4"]
	if fixing.Status != "In Progress" || fixing.Category != "In Progress" {
		t.Errorf("#4 with an open closing PR = %q/%q, want In Progress", fixing.Status, fixing.Category)
	}
	if !slices.Equal(fixing.LinkedPRs, []string{
		"https://github.com/Acme/App/pull/40", "https://github.com/Acme/App/pull/39"}) {
		t.Errorf("#4 linked PRs = %v", fixing.LinkedPRs)
	}

	if merged := byKey["#5"]; merged.Category != "To Do" {
		t.Errorf("#5 with only a merged closing PR = %q, want To Do", merged.Category)
	}
}

func TestGitHubIssuesEmptyQueryShowsEveryOpenIssue(t *testing.T) {
	srv := &issuesServer{pages: []string{issuesPage}}
	tr := srv.client(t)

	if _, err := tr.Tickets(context.Background(), ""); err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}
	if want := "repo:Acme/App is:issue is:open"; srv.searches[0] != want {
		t.Errorf("search = %q, want %q", srv.searches[0], want)
	}
}

func TestGitHubIssuesPaginates(t *testing.T) {
	srv := &issuesServer{pages: []string{
		`{"data":{"search":{"nodes":[{"number":1,"title":"a","url":"u1","labels":{"nodes":[]},
		   "closedByPullRequestsReferences":{"nodes":[]}}],
		   "pageInfo":{"hasNextPage":true,"endCursor":"c1"}}}}`,
		`{"data":{"search":{"nodes":[{"number":2,"title":"b","url":"u2","labels":{"nodes":[]},
		   "closedByPullRequestsReferences":{"nodes":[]}}],
		   "pageInfo":{"hasNextPage":false,"endCursor":""}}}}`,
	}}
	tr := srv.client(t)

	got, err := tr.Tickets(context.Background(), "")
	if err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}
	if len(got) != 2 || got[0].Key != "#1" || got[1].Key != "#2" {
		t.Errorf("tickets = %+v, want #1 and #2", got)
	}
	if !slices.Equal(srv.afters, []string{"", "c1"}) {
		t.Errorf("cursors = %q, want the first page's cursor on the second request", srv.afters)
	}
}

// GitHub search never returns more than 1000 results, so running out of pages
// while more remain must be said rather than showing a silently short list.
func TestGitHubIssuesSaysWhenResultsAreTruncated(t *testing.T) {
	more := `{"data":{"search":{"nodes":[{"number":1,"title":"a","url":"u","labels":{"nodes":[]},
	   "closedByPullRequestsReferences":{"nodes":[]}}],
	   "pageInfo":{"hasNextPage":true,"endCursor":"c"}}}}`
	srv := &issuesServer{pages: []string{more}}
	tr := srv.client(t)

	_, err := tr.Tickets(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "narrow") {
		t.Fatalf("Tickets() error = %v, want one asking to narrow the query", err)
	}
}

// The repository is the tracker's scope. A query that names another one, or
// asks for closed issues or pull requests, would break that, so it is refused
// before anything is sent.
func TestGitHubIssuesRefusesQueriesThatLeaveTheScope(t *testing.T) {
	for _, q := range []string{
		"repo:other/thing", "org:acme", "user:me", "is:closed", "is:pr", "state:closed",
		"type:pr", "assignee:@me repo:other/thing",
	} {
		t.Run(q, func(t *testing.T) {
			srv := &issuesServer{pages: []string{issuesPage}}
			tr := srv.client(t)

			_, err := tr.Tickets(context.Background(), q)
			if err == nil || !strings.Contains(err.Error(), "scope") {
				t.Fatalf("Tickets(%q) error = %v, want a scope error", q, err)
			}
			if len(srv.searches) != 0 {
				t.Errorf("sent %q, want nothing sent", srv.searches)
			}
		})
	}
}

func TestGitHubIssuesQualifiersThatNarrowAreAllowed(t *testing.T) {
	srv := &issuesServer{pages: []string{issuesPage}}
	tr := srv.client(t)

	if _, err := tr.Tickets(context.Background(), "label:bug -label:wontfix assignee:@me sort:updated-desc"); err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}
}

func TestGitHubIssuesReportsARejectedToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		io.WriteString(w, `{"full_name":"acme/app"}`)
	}))
	defer srv.Close()
	tr := newGitHubIssues(&githubClient{token: "bad", http: srv.Client(), api: srv.URL}, "acme", "app")

	_, err := tr.Tickets(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "rejected the token") {
		t.Fatalf("Tickets() error = %v, want a rejected-token error", err)
	}
}

func TestGitHubIssuesTransitionsOffersClosingIt(t *testing.T) {
	tr := (&issuesServer{}).client(t)

	got, err := tr.Transitions(context.Background(), "#12")
	if err != nil {
		t.Fatalf("Transitions() error = %v", err)
	}
	var ids []string
	for _, tr := range got {
		ids = append(ids, tr.ID)
		if tr.ToCategory != "Done" || len(tr.Required) != 0 {
			t.Errorf("%s = %+v, want a move to Done needing no fields", tr.ID, tr)
		}
	}
	if want := []string{"completed", "not_planned"}; !slices.Equal(ids, want) {
		t.Errorf("transition ids = %v, want %v", ids, want)
	}
}

func TestGitHubIssuesApplyTransitionClosesTheIssue(t *testing.T) {
	srv := &issuesServer{}
	tr := srv.client(t)

	if err := tr.ApplyTransition(context.Background(), "#12", "not_planned", nil); err != nil {
		t.Fatalf("ApplyTransition() error = %v", err)
	}
	want := `PATCH /repos/Acme/App/issues/12 {"state":"closed","state_reason":"not_planned"}`
	if len(srv.rest) != 1 || srv.rest[0] != want {
		t.Errorf("requests = %q, want [%s]", srv.rest, want)
	}
}

func TestGitHubIssuesApplyTransitionRejectsAKeyThatIsNotAnIssueNumber(t *testing.T) {
	srv := &issuesServer{}
	tr := srv.client(t)

	if err := tr.ApplyTransition(context.Background(), "ENG-1", "completed", nil); err == nil {
		t.Fatal("ApplyTransition() should refuse a key that is not #<number>")
	}
	if len(srv.rest) != 0 {
		t.Errorf("sent %q, want nothing", srv.rest)
	}
}

func TestGitHubIssuesTransitionsRefusesAPullRequestOrAClosedIssue(t *testing.T) {
	for issue, want := range map[string]string{
		`{"state":"open","pull_request":{"url":"x"},"labels":[]}`: "pull request",
		`{"state":"closed","labels":[]}`:                          "closed",
	} {
		tr := (&issuesServer{issue: issue}).client(t)
		if _, err := tr.Transitions(context.Background(), "#12"); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Transitions() on %s error = %v, want it to say %q", issue, err, want)
		}
	}
}

func TestGitHubIssuesUpdateLabelsSetsTheWholeSetInOneRequest(t *testing.T) {
	srv := &issuesServer{}
	tr := srv.client(t)

	// "NEEDS TRIAGE" names the issue's "needs triage"; "Keep" is already there.
	if err := tr.UpdateLabels(context.Background(), "#12", []string{"symphony-ready", "Keep"}, []string{"NEEDS TRIAGE"}); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	want := []string{`PUT /repos/Acme/App/issues/12/labels {"labels":["keep","symphony-ready"]}`}
	if !slices.Equal(srv.rest, want) {
		t.Errorf("requests = %q, want %q", srv.rest, want)
	}
}

func TestGitHubIssuesUpdateLabelsThatChangesNothingWritesNothing(t *testing.T) {
	srv := &issuesServer{}
	tr := srv.client(t)

	if err := tr.UpdateLabels(context.Background(), "#12", []string{"keep"}, []string{"gone"}); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	if len(srv.rest) != 0 {
		t.Errorf("sent %q, want nothing", srv.rest)
	}
}

func TestGitHubIssuesUpdateLabelsWithNothingToDoSendsNothing(t *testing.T) {
	srv := &issuesServer{}
	tr := srv.client(t)

	if err := tr.UpdateLabels(context.Background(), "#12", nil, nil); err != nil {
		t.Fatalf("UpdateLabels() error = %v", err)
	}
	if len(srv.rest) != 0 {
		t.Errorf("sent %q, want nothing", srv.rest)
	}
}
