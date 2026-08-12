package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testGitHub(t *testing.T, h http.HandlerFunc) *githubClient {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &githubClient{token: "tok", http: srv.Client(), api: srv.URL}
}

const prNodesFixture = `{"data":{"a":{"nodes":[
 {"number":1099,"title":"PROJ-17552: manual-only","url":"https://gh/platform/pull/1099",
  "isDraft":true,"reviewDecision":"REVIEW_REQUIRED","headRefName":"PROJ-17552",
  "repository":{"name":"platform","isArchived":false},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":"SUCCESS"}}}]}},
 {"number":188,"title":"PROJ-10821 layout","url":"https://gh/old/pull/188",
  "isDraft":false,"reviewDecision":null,"headRefName":"monorepo",
  "repository":{"name":"legacy-service","isArchived":true},
  "commits":{"nodes":[{"commit":{"statusCheckRollup":null}}]}},
 {}
]}}}`

func TestPullRequestsParsesAndFiltersArchived(t *testing.T) {
	var gotQuery string
	c := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer tok" {
			t.Errorf("authorization = %q", got)
		}
		var body struct {
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotQuery, _ = body.Variables["a"].(string)
		io.WriteString(w, prNodesFixture)
	})
	prs, err := c.PullRequests(context.Background(), "author:@me is:pr is:open", false)
	if err != nil {
		t.Fatalf("PullRequests() error = %v", err)
	}
	if gotQuery != "author:@me is:pr is:open" {
		t.Errorf("search query = %q", gotQuery)
	}

	// The archived repo's PR is dropped, and the empty node is skipped.
	if len(prs) != 1 {
		t.Fatalf("got %d PRs, want 1: %+v", len(prs), prs)
	}
	got := prs[0]
	if got.Repo != "platform" || got.Number != 1099 || !got.Draft || got.CI != "SUCCESS" {
		t.Errorf("PR = %+v", got)
	}
	if got.Branch != "PROJ-17552" {
		t.Errorf("branch = %q", got.Branch)
	}

	// With archived included, both real PRs survive and one is flagged.
	all, err := c.PullRequests(context.Background(), "q", true)
	if err != nil {
		t.Fatalf("PullRequests(include) error = %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("got %d PRs, want 2", len(all))
	}
	if !all[1].Archived {
		t.Errorf("second PR should be flagged archived: %+v", all[1])
	}
}

// GitHub has no "open or merged" qualifier, so the merged list is a second
// search derived from the first.
func TestMergedPRQuery(t *testing.T) {
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	window := 30 * 24 * time.Hour

	tests := []struct {
		name string
		open string
		want string
	}{
		{
			name: "swaps is:open for is:merged and bounds the window",
			open: "author:@me is:pr is:open",
			want: "author:@me is:pr is:merged merged:>=2026-07-13",
		},
		{
			// The repo scope has to survive onto the merged search too, or a
			// scoped dashboard would pull in merges from every repository.
			name: "keeps the repository scope",
			open: "author:@me is:pr is:open repo:acme/platform",
			want: "author:@me is:pr is:merged repo:acme/platform merged:>=2026-07-13",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mergedPRQuery(tc.open, now, window); got != tc.want {
				t.Errorf("mergedPRQuery() = %q, want %q", got, tc.want)
			}
		})
	}
}

// Both searches travel in one request, and their results are combined.
func TestOpenAndMergedPRs(t *testing.T) {
	var gotA, gotB string
	c := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		gotA, _ = body.Variables["a"].(string)
		gotB, _ = body.Variables["b"].(string)

		if !strings.Contains(body.Query, "a: search") || !strings.Contains(body.Query, "b: search") {
			t.Errorf("expected two aliased searches in one document, got %q", body.Query)
		}
		io.WriteString(w, `{"data":{
      "a":{"nodes":[{"number":1099,"url":"https://gh/1099","state":"OPEN","isDraft":true,
            "title":"PROJ-17552: open one","repository":{"name":"platform"},"commits":{"nodes":[]}}]},
      "b":{"nodes":[
            {"number":1105,"url":"https://gh/1105","state":"MERGED",
             "title":"PROJ-17506: merged one","repository":{"name":"platform"},"commits":{"nodes":[]}},
            {"number":1099,"url":"https://gh/1099","state":"OPEN",
             "title":"PROJ-17552: duplicate","repository":{"name":"platform"},"commits":{"nodes":[]}}]}}}`)
	})

	prs, err := c.OpenAndMergedPRs(context.Background(), "open-q", "merged-q", false)
	if err != nil {
		t.Fatalf("OpenAndMergedPRs() error = %v", err)
	}
	if gotA != "open-q" || gotB != "merged-q" {
		t.Errorf("queries = %q / %q, want open-q / merged-q", gotA, gotB)
	}

	// Two distinct PRs, with the repeated one collected only once.
	if len(prs) != 2 {
		t.Fatalf("got %d PRs, want 2 (the duplicate deduped): %+v", len(prs), prs)
	}
	byNumber := map[int]PullRequest{}
	for _, pr := range prs {
		byNumber[pr.Number] = pr
	}
	if got := byNumber[1105].State; got != "MERGED" {
		t.Errorf("#1105 state = %q, want MERGED", got)
	}
	if !byNumber[1099].Draft {
		t.Errorf("#1099 should still be a draft: %+v", byNumber[1099])
	}
}

func TestGraphQLSurfacesErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{
			name:   "bad credentials",
			status: http.StatusUnauthorized,
			body:   `{"message":"Bad credentials"}`,
			want:   "rejected the token",
		},
		{
			name:   "graphql level error",
			status: http.StatusOK,
			body:   `{"errors":[{"message":"Field 'nope' doesn't exist"}]}`,
			want:   "Field 'nope' doesn't exist",
		},
		{
			name:   "server error",
			status: http.StatusBadGateway,
			body:   `upstream unavailable`,
			want:   "returned 502",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := testGitHub(t, func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				io.WriteString(w, tc.body)
			})
			_, err := c.PullRequests(context.Background(), "q", false)
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to contain %q", err, tc.want)
			}
		})
	}
}

// A renamed repository answers 301; the client must end up with the new name,
// because GitHub search will not match the old one.
func TestCanonicalRepoFollowsRename(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/acme/oldname", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/repositories/951742708", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/repositories/951742708", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"full_name":"acme/platform"}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &githubClient{token: "tok", http: srv.Client(), api: srv.URL}
	got, err := c.CanonicalRepo(context.Background(), "acme", "oldname")
	if err != nil {
		t.Fatalf("CanonicalRepo() error = %v", err)
	}
	if got != "acme/platform" {
		t.Errorf("CanonicalRepo() = %q, want acme/platform", got)
	}
}

func TestCanonicalRepoMissing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := &githubClient{token: "tok", http: srv.Client(), api: srv.URL}
	if _, err := c.CanonicalRepo(context.Background(), "o", "gone"); err == nil {
		t.Error("expected an error for a repository that does not exist")
	}
}

func TestNewGitHubClientRequiresAToken(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	if _, err := newGitHubClient(http.DefaultClient); err == nil {
		t.Fatal("expected an error with no token in the environment")
	}

	t.Setenv("GH_TOKEN", "  from-gh-token  ")
	c, err := newGitHubClient(http.DefaultClient)
	if err != nil {
		t.Fatalf("newGitHubClient() error = %v", err)
	}
	if c.token != "from-gh-token" {
		t.Errorf("token = %q, want it trimmed and taken from GH_TOKEN", c.token)
	}
}
