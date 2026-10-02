package main

import (
	"context"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// These tests run the GitHub issues tracker against the real GitHub GraphQL
// API for the repository in the working directory. They need a token and
// skip without one:
//
//	GITHUB_TOKEN=$(gh auth token) go test -run TestLiveGitHubIssues -v
//
// They assert invariants that hold for whatever issues the repository has, so
// they do not depend on any particular issue existing.

var issueKeyRE = regexp.MustCompile(`^#\d+$`)

func liveGitHubIssues(t *testing.T) *githubIssues {
	t.Helper()
	gh, err := newGitHubClient(&http.Client{Timeout: 30 * time.Second})
	if err != nil {
		t.Skip("set GITHUB_TOKEN to run this against real GitHub")
	}
	wd, _ := os.Getwd()
	owner, name, err := localRepo(wd)
	if err != nil {
		t.Skipf("not in a GitHub repository: %v", err)
	}
	return newGitHubIssues(gh, owner, name)
}

func TestLiveGitHubIssuesTicketsAreWellFormed(t *testing.T) {
	tr := liveGitHubIssues(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	got, err := tr.Tickets(ctx, "")
	if err != nil {
		t.Fatalf("Tickets() error = %v", err)
	}
	repo := tr.canonical(ctx)
	t.Logf("%d open issues in %s", len(got), repo)

	for _, tk := range got {
		if !issueKeyRE.MatchString(tk.Key) {
			t.Errorf("key %q, want #<number>", tk.Key)
		}
		if tk.Source != "GitHub" {
			t.Errorf("%s source = %q, want GitHub", tk.Key, tk.Source)
		}
		if !strings.Contains(strings.ToLower(tk.URL), "/"+strings.ToLower(repo)+"/issues/") {
			t.Errorf("%s url %q is not an issue of %s", tk.Key, tk.URL, repo)
		}
		if tk.Category != "To Do" && tk.Category != "In Progress" {
			t.Errorf("%s category = %q, want To Do or In Progress", tk.Key, tk.Category)
		}
	}
}

func TestLiveGitHubIssuesAssignedIsASubsetOfAll(t *testing.T) {
	tr := liveGitHubIssues(t)
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()

	mine, err := tr.Tickets(ctx, defaultIssuesQuery)
	if err != nil {
		t.Fatalf("assigned Tickets() error = %v", err)
	}
	all, err := tr.Tickets(ctx, "")
	if err != nil {
		t.Fatalf("all Tickets() error = %v", err)
	}

	have := map[string]bool{}
	for _, tk := range all {
		have[tk.Key] = true
	}
	for _, tk := range mine {
		if !have[tk.Key] {
			t.Errorf("assigned issue %s missing from the unfiltered list", tk.Key)
		}
	}
}
