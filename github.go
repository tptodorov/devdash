package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

const defaultGitHubAPI = "https://api.github.com"

type githubClient struct {
	token string
	http  *http.Client
	// api is the API root, a field rather than a constant so tests can point the
	// client at a local server.
	api string
}

func newGitHubClient(hc *http.Client) (*githubClient, error) {
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		token = os.Getenv("GH_TOKEN")
	}
	if token == "" {
		return nil, fmt.Errorf("set GITHUB_TOKEN (or GH_TOKEN) to a token with repo scope")
	}
	return &githubClient{
		token: strings.TrimSpace(token),
		http:  hc,
		api:   defaultGitHubAPI,
	}, nil
}

func (c *githubClient) root() string {
	if c.api == "" {
		return defaultGitHubAPI
	}
	return strings.TrimRight(c.api, "/")
}

func (c *githubClient) do(ctx context.Context, method, endpoint string, body []byte) ([]byte, int, error) {
	var rdr *bytes.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}

	var req *http.Request
	var err error
	if rdr != nil {
		req, err = http.NewRequestWithContext(ctx, method, endpoint, rdr)
	} else {
		req, err = http.NewRequestWithContext(ctx, method, endpoint, nil)
	}
	if err != nil {
		return nil, 0, err
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("github request failed: %w", err)
	}
	defer resp.Body.Close()

	out, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

// graphql runs a query and decodes the whole envelope into out.
func (c *githubClient) graphql(ctx context.Context, query string, vars map[string]any, out any) error {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": vars})
	if err != nil {
		return err
	}

	body, status, err := c.do(ctx, http.MethodPost, c.root()+"/graphql", payload)
	if err != nil {
		return err
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("github rejected the token (%d) — check GITHUB_TOKEN", status)
	default:
		return fmt.Errorf("github graphql returned %d: %s", status, firstLine(body))
	}
	return json.Unmarshal(body, out)
}

// prFields is everything a row needs about a pull request: its state, the review
// decision, the approval count and the CI rollup of its head commit.
const prFields = `
fragment prFields on PullRequest {
  number
  title
  url
  isDraft
  state
  reviewDecision
  reviews(states: APPROVED) { totalCount }
  headRefName
  repository { name isArchived }
  commits(last: 1) {
    nodes { commit { statusCheckRollup { state } } }
  }
}`

const onePRSearch = prFields + `
query($a: String!) {
  a: search(query: $a, type: ISSUE, first: 100) { nodes { ...prFields } }
}`

// twoPRSearches fetches both lists in a single request. GitHub's search has no
// qualifier for "open or merged" — is:open and is:merged are disjoint and
// -is:unmerged excludes open entirely — so two searches are unavoidable.
const twoPRSearches = prFields + `
query($a: String!, $b: String!) {
  a: search(query: $a, type: ISSUE, first: 100) { nodes { ...prFields } }
  b: search(query: $b, type: ISSUE, first: 100) { nodes { ...prFields } }
}`

type prNode struct {
	Number         int    `json:"number"`
	Title          string `json:"title"`
	URL            string `json:"url"`
	IsDraft        bool   `json:"isDraft"`
	State          string `json:"state"`
	ReviewDecision string `json:"reviewDecision"`
	Reviews        struct {
		TotalCount int `json:"totalCount"`
	} `json:"reviews"`
	HeadRefName string `json:"headRefName"`
	Repository  struct {
		Name       string `json:"name"`
		IsArchived bool   `json:"isArchived"`
	} `json:"repository"`
	Commits struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *struct {
					State string `json:"state"`
				} `json:"statusCheckRollup"`
			} `json:"commit"`
		} `json:"nodes"`
	} `json:"commits"`
}

type prSearchResponse struct {
	Data struct {
		A struct{ Nodes []prNode } `json:"a"`
		B struct{ Nodes []prNode } `json:"b"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func toPullRequests(nodes ...[]prNode) []PullRequest {
	var out []PullRequest
	seen := map[string]bool{}

	for _, group := range nodes {
		for _, n := range group {
			if n.Number == 0 || seen[n.URL] {
				continue // a non-PullRequest node, or already collected
			}
			seen[n.URL] = true

			ci := ""
			if len(n.Commits.Nodes) > 0 {
				if rollup := n.Commits.Nodes[0].Commit.StatusCheckRollup; rollup != nil {
					ci = rollup.State
				}
			}
			out = append(out, PullRequest{
				Repo:      n.Repository.Name,
				Number:    n.Number,
				Title:     strings.TrimSpace(n.Title),
				URL:       n.URL,
				Branch:    n.HeadRefName,
				Draft:     n.IsDraft,
				State:     n.State,
				Archived:  n.Repository.IsArchived,
				Review:    n.ReviewDecision,
				Approvals: n.Reviews.TotalCount,
				CI:        ci,
			})
		}
	}
	return out
}

// PullRequests runs one search. Used when an explicit query is given, which is
// then honoured exactly as written.
func (c *githubClient) PullRequests(ctx context.Context, searchQuery string, includeArchived bool) ([]PullRequest, error) {
	var parsed prSearchResponse
	if err := c.graphql(ctx, onePRSearch, map[string]any{"a": searchQuery}, &parsed); err != nil {
		return nil, err
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("github api: %s", parsed.Errors[0].Message)
	}
	return dropArchived(toPullRequests(parsed.Data.A.Nodes), includeArchived), nil
}

// OpenAndMergedPRs returns open pull requests plus recently merged ones, in one
// request. Closed-without-merging PRs are excluded by construction: they are
// abandoned work, not something a dashboard of live work should carry.
func (c *githubClient) OpenAndMergedPRs(ctx context.Context, openQuery, mergedQuery string, includeArchived bool) ([]PullRequest, error) {
	var parsed prSearchResponse
	err := c.graphql(ctx, twoPRSearches,
		map[string]any{"a": openQuery, "b": mergedQuery}, &parsed)
	if err != nil {
		return nil, err
	}
	if len(parsed.Errors) > 0 {
		return nil, fmt.Errorf("github api: %s", parsed.Errors[0].Message)
	}
	return dropArchived(toPullRequests(parsed.Data.A.Nodes, parsed.Data.B.Nodes), includeArchived), nil
}

// CanonicalRepo resolves owner/name to the repository's current full name.
//
// This matters because a renamed repository keeps its old URL in the local git
// remote, and GitHub's search does not follow renames: searching
// repo:acme/oldname matches nothing even though that remote is live.
// The REST endpoint answers 301 for a renamed repo and Go follows it, so this
// returns the name search will actually match.
func (c *githubClient) CanonicalRepo(ctx context.Context, owner, name string) (string, error) {
	endpoint := c.root() + "/repos/" + url.PathEscape(owner) + "/" + url.PathEscape(name)

	body, status, err := c.do(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", err
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("looking up %s/%s returned %d", owner, name, status)
	}

	var repo struct {
		FullName string `json:"full_name"`
	}
	if err := json.Unmarshal(body, &repo); err != nil {
		return "", err
	}
	if repo.FullName == "" {
		return "", fmt.Errorf("github did not name %s/%s", owner, name)
	}
	return repo.FullName, nil
}

// prSearchQuery decides which GitHub search to run and what scope to show in the
// header. An explicit query always wins; otherwise the current repository
// narrows the search unless that is turned off.
func prSearchQuery(explicit, repo string, allRepos bool) (query, scope string) {
	if explicit != "" {
		return explicit, ""
	}
	if allRepos || repo == "" {
		return defaultPRQuery, ""
	}
	return defaultPRQuery + " repo:" + repo, repo
}

// mergedPRQuery mirrors a query onto recently merged pull requests. A ticket
// that is still open with an already-merged PR merged it recently, so a window
// keeps this from dragging in years of finished work — there are 403 merged PRs
// in this account and only a handful matter.
func mergedPRQuery(openQuery string, now time.Time, window time.Duration) string {
	since := now.Add(-window).UTC().Format("2006-01-02")
	q := strings.ReplaceAll(openQuery, "is:open", "is:merged")
	return q + " merged:>=" + since
}
