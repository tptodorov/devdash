package main

import (
	"cmp"
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// defaultIssuesQuery narrows a repository's open issues to the ones assigned
// to the current user, mirroring the "assigned to me, not done" default of the
// other trackers. It is appended to the repository scope as GitHub search
// qualifiers; an empty query shows every open issue in the repository.
const defaultIssuesQuery = "assignee:@me"

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

// repo returns the repository's full name, following a rename once: search
// does not, so a stale remote would otherwise match nothing.
func (g *githubIssues) repo() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return cmp.Or(g.resolved, g.owner+"/"+g.name)
}

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

const issuesSearch = `
query($q: String!, $after: String) {
  search(query: $q, type: ISSUE, first: 100, after: $after) {
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
}`

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
	search := strings.TrimSpace("repo:" + g.canonical(ctx) + " is:issue is:open " + query)

	var out []Ticket
	after := ""
	for range 10 {
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
				Status:     "Open",
				Category:   "To Do",
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
			for _, pr := range n.ClosedBy.Nodes {
				t.LinkedPRs = append(t.LinkedPRs, pr.URL)
				if pr.State == "OPEN" {
					t.Status, t.Category = "In Progress", "In Progress"
				}
			}
			out = append(out, t)
		}

		info := parsed.Data.Search.PageInfo
		if !info.HasNextPage {
			break
		}
		after = info.EndCursor
	}
	return out, nil
}
