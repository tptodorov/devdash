package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// defaultIssuesQuery narrows a repository's open issues to the ones assigned
// to the current user, mirroring the "assigned to me, not done" default of the
// other trackers. It is appended to the repository scope as GitHub search
// qualifiers; an empty query shows every open issue in the repository.
const defaultIssuesQuery = "assignee:@me"

const (
	issuePageSize = 100
	// maxIssuePages bounds the fetch at GitHub search's own ceiling of 1000
	// results, so a longer list is reported instead of silently cut short.
	maxIssuePages = 10
)

// issueStatus is the Status and status category of an open issue: In Progress
// while a pull request that closes it is open, otherwise just Open.
func issueStatus(inProgress bool) (status, category string) {
	if inProgress {
		return "In Progress", "In Progress"
	}
	return "Open", "To Do"
}

// checkIssueScope refuses a query that would leave the repository's open
// issues: another repository, closed issues, or pull requests.
func checkIssueScope(query string) error {
	for _, term := range strings.Fields(query) {
		key, val, _ := strings.Cut(strings.ToLower(term), ":")
		switch key {
		case "repo", "org", "user", "state", "type":
		case "is":
			if !slices.Contains([]string{"closed", "merged", "unmerged", "pr", "draft"}, val) {
				continue
			}
		default:
			continue
		}
		return fmt.Errorf("%q would leave the repository's open issues; the query only adds to that scope", term)
	}
	return nil
}

// githubIssues is the Tracker for the issues of one GitHub repository. A
// repository is assumed to use a single ticketing system, so it never looks
// beyond the repository it was built for and a bare #123 is a unique key.
type githubIssues struct {
	gh          *githubClient
	owner, name string

	mu       sync.Mutex
	resolved string // canonical owner/name, empty until first looked up
}

func newGitHubIssues(gh *githubClient, owner, name string) *githubIssues {
	return &githubIssues{gh: gh, owner: owner, name: name}
}

// Name identifies this tracker in status and error messages.
func (g *githubIssues) Name() string { return "GitHub" }

// canonical returns the repository's full name, following a rename once:
// search does not, so a stale remote would otherwise match nothing.
func (g *githubIssues) canonical(ctx context.Context) string {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.resolved == "" {
		if full, err := g.gh.CanonicalRepo(ctx, g.owner, g.name); err == nil {
			g.resolved = full
		}
	}
	return cmp.Or(g.resolved, g.owner+"/"+g.name)
}

var issuesSearch = fmt.Sprintf(`
query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: %d, after: $after) {
    nodes {
      ... on Issue {
        number
        title
        url
        issueType { name }
        labels(first: 20) { nodes { name } }
        parent { number }
        subIssues { totalCount }
        closedByPullRequestsReferences(first: 10) { nodes { url state } }
      }
    }
    pageInfo { hasNextPage endCursor }
  }
}`, issuePageSize)

type issuesResponse struct {
	Data struct {
		Search struct {
			Nodes []struct {
				Number    int    `json:"number"`
				Title     string `json:"title"`
				URL       string `json:"url"`
				IssueType *struct {
					Name string `json:"name"`
				} `json:"issueType"`
				Labels struct {
					Nodes []struct {
						Name string `json:"name"`
					} `json:"nodes"`
				} `json:"labels"`
				Parent *struct {
					Number int `json:"number"`
				} `json:"parent"`
				SubIssues struct {
					TotalCount int `json:"totalCount"`
				} `json:"subIssues"`
				ClosedBy struct {
					Nodes []struct {
						URL   string `json:"url"`
						State string `json:"state"`
					} `json:"nodes"`
				} `json:"closedByPullRequestsReferences"`
			} `json:"nodes"`
			PageInfo struct {
				HasNextPage bool   `json:"hasNextPage"`
				EndCursor   string `json:"endCursor"`
			} `json:"pageInfo"`
		} `json:"search"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// Tickets returns the repository's open issues matching query, which is added
// to the repository scope as GitHub search qualifiers. An open issue with an
// open pull request that closes it is In Progress; any other is To Do.
func (g *githubIssues) Tickets(ctx context.Context, query string) ([]Ticket, error) {
	if err := checkIssueScope(query); err != nil {
		return nil, err
	}
	search := strings.TrimSpace("repo:" + g.canonical(ctx) + " is:issue is:open " + query)

	var out []Ticket
	after := ""
	for range maxIssuePages {
		vars := map[string]any{"q": search}
		if after != "" {
			vars["after"] = after
		}
		var parsed issuesResponse
		if err := g.gh.graphql(ctx, issuesSearch, vars, &parsed); err != nil {
			return nil, err
		}
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("github api: %s", parsed.Errors[0].Message)
		}

		for _, n := range parsed.Data.Search.Nodes {
			if n.Number == 0 {
				continue // not an Issue node
			}
			t := Ticket{
				Key:        "#" + strconv.Itoa(n.Number),
				Summary:    strings.TrimSpace(n.Title),
				URL:        n.URL,
				IsSubtask:  n.Parent != nil,
				ChildCount: n.SubIssues.TotalCount,
				Source:     g.Name(),
			}
			if n.IssueType != nil {
				t.Type = n.IssueType.Name
			}
			for _, l := range n.Labels.Nodes {
				t.Labels = append(t.Labels, l.Name)
			}
			inProgress := false
			for _, pr := range n.ClosedBy.Nodes {
				t.LinkedPRs = append(t.LinkedPRs, pr.URL)
				inProgress = inProgress || pr.State == "OPEN"
			}
			t.Status, t.Category = issueStatus(inProgress)
			out = append(out, t)
		}

		info := parsed.Data.Search.PageInfo
		if !info.HasNextPage {
			return out, nil
		}
		after = info.EndCursor
	}
	return nil, fmt.Errorf("more than %d issues match; narrow -github-query", maxIssuePages*issuePageSize)
}

// issueNumber reads N out of a ticket key of the form #N.
func issueNumber(key string) (int, error) {
	digits, ok := strings.CutPrefix(key, "#")
	n, err := strconv.Atoi(digits)
	if !ok || err != nil || n <= 0 {
		return 0, fmt.Errorf("%q is not a GitHub issue number", key)
	}
	return n, nil
}

// issueURL is the REST endpoint of an issue, or of a path below it.
func (g *githubIssues) issueURL(ctx context.Context, key, suffix string) (string, error) {
	n, err := issueNumber(key)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s/repos/%s/issues/%d%s", g.gh.root(), g.canonical(ctx), n, suffix), nil
}

// write sends one REST request that changes an issue. okStatus names any
// status besides 2xx that counts as success.
func (g *githubIssues) write(ctx context.Context, method, endpoint string, payload any, okStatus ...int) error {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	out, status, err := g.gh.do(ctx, method, endpoint, body)
	if err != nil {
		return err
	}
	switch {
	case status >= 200 && status < 300, slices.Contains(okStatus, status):
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("github refused the change (%d) — the token needs write access to issues", status)
	default:
		return fmt.Errorf("github returned %d: %s", status, firstLine(out))
	}
}

// issue reads an issue, so a write is only ever attempted against one that
// exists, is open, and is an issue rather than a pull request (GitHub numbers
// both together, and its issues endpoint answers for both).
func (g *githubIssues) issue(ctx context.Context, key string) (labels []string, err error) {
	endpoint, err := g.issueURL(ctx, key, "")
	if err != nil {
		return nil, err
	}
	body, status, err := g.gh.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, fmt.Errorf("%s does not exist in %s", key, g.canonical(ctx))
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return nil, fmt.Errorf("github refused to read %s (%d) — check GITHUB_TOKEN", key, status)
	case status != http.StatusOK:
		return nil, fmt.Errorf("reading %s: github returned %d: %s", key, status, firstLine(body))
	}
	var issue struct {
		State       string    `json:"state"`
		PullRequest *struct{} `json:"pull_request"`
		Labels      []struct {
			Name string `json:"name"`
		} `json:"labels"`
	}
	if err := json.Unmarshal(body, &issue); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", key, err)
	}
	if issue.PullRequest != nil {
		return nil, fmt.Errorf("%s is a pull request, not an issue", key)
	}
	if issue.State != "open" {
		return nil, fmt.Errorf("%s is %s", key, issue.State)
	}
	for _, l := range issue.Labels {
		labels = append(labels, l.Name)
	}
	return labels, nil
}

// Transitions offers closing the issue, as completed or as not planned. Only
// open issues are shown and GitHub has no other workflow, so those are the only
// moves; an issue's In Progress is derived from its pull requests, not set.
func (g *githubIssues) Transitions(ctx context.Context, key string) ([]Transition, error) {
	if _, err := g.issue(ctx, key); err != nil {
		return nil, err
	}
	return []Transition{
		{ID: "completed", Name: "Close", To: "Closed", ToCategory: "Done"},
		{ID: "not_planned", Name: "Close as not planned", To: "Closed (not planned)", ToCategory: "Done"},
	}, nil
}

// ApplyTransition closes the issue with the state reason the transition names.
func (g *githubIssues) ApplyTransition(ctx context.Context, key string, tr Transition) error {
	endpoint, err := g.issueURL(ctx, key, "")
	if err != nil {
		return err
	}
	return g.write(ctx, http.MethodPatch, endpoint, map[string]string{"state": "closed", "state_reason": tr.ID})
}

// UpdateLabels sets the issue's labels to its current ones minus remove plus
// add, in one request, so the change applies whole or not at all. Names match
// without regard to case, as GitHub's do: a label already carried in another
// case is kept as it is. A label the repository lacks is created.
func (g *githubIssues) UpdateLabels(ctx context.Context, key string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	current, err := g.issue(ctx, key)
	if err != nil {
		return err
	}

	labels := []string{}
	for _, l := range current {
		if !containsFold(remove, l) {
			labels = append(labels, l)
		}
	}
	for _, l := range add {
		if !containsFold(labels, l) {
			labels = append(labels, l)
		}
	}
	if slices.Equal(labels, current) {
		return nil
	}

	endpoint, err := g.issueURL(ctx, key, "/labels")
	if err != nil {
		return err
	}
	return g.write(ctx, http.MethodPut, endpoint, map[string][]string{"labels": labels})
}
