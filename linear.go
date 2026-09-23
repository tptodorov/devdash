package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// DefaultLinearFilter selects every issue assigned to the current user whose
// workflow state has not reached the completed or canceled category,
// mirroring DefaultJQL's "not Done" scope for JIRA. It is a Linear
// IssueFilter, encoded as the JSON Linear's GraphQL API expects for the
// assignedIssues(filter:) argument.
const DefaultLinearFilter = `{"state":{"type":{"nin":["completed","canceled"]}}}`

const linearEndpoint = "https://api.linear.app/graphql"

type linearClient struct {
	apiKey   string
	endpoint string
	http     *http.Client
}

// Name identifies this tracker in status and error messages.
func (c *linearClient) Name() string { return "Linear" }

func newLinearClient(hc *http.Client) (*linearClient, error) {
	key := os.Getenv("LINEAR_API_KEY")
	if key == "" {
		return nil, fmt.Errorf("missing environment variables: LINEAR_API_KEY")
	}
	return &linearClient{apiKey: key, endpoint: linearEndpoint, http: hc}, nil
}

type linearGraphQLRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type linearGraphQLError struct {
	Message string `json:"message"`
}

const linearAssignedIssuesQuery = `
query Assigned($filter: IssueFilter, $after: String) {
  viewer {
    assignedIssues(filter: $filter, first: 100, after: $after) {
      nodes {
        identifier
        title
        url
        state { name type }
        labels { nodes { name } }
        parent { id }
      }
      pageInfo { hasNextPage endCursor }
    }
  }
}`

type linearIssuesResponse struct {
	Data struct {
		Viewer struct {
			AssignedIssues struct {
				Nodes []struct {
					Identifier string `json:"identifier"`
					Title      string `json:"title"`
					URL        string `json:"url"`
					State      struct {
						Name string `json:"name"`
						Type string `json:"type"`
					} `json:"state"`
					Labels struct {
						Nodes []struct {
							Name string `json:"name"`
						} `json:"nodes"`
					} `json:"labels"`
					Parent *struct {
						ID string `json:"id"`
					} `json:"parent"`
				} `json:"nodes"`
				PageInfo struct {
					HasNextPage bool   `json:"hasNextPage"`
					EndCursor   string `json:"endCursor"`
				} `json:"pageInfo"`
			} `json:"assignedIssues"`
		} `json:"viewer"`
	} `json:"data"`
	Errors []linearGraphQLError `json:"errors"`
}

// Tickets runs a Linear issue filter against the issues assigned to the
// authenticated user, following pagination until the result set is
// exhausted. query is a Linear IssueFilter encoded as JSON; an empty query
// falls back to DefaultLinearFilter.
func (c *linearClient) Tickets(ctx context.Context, query string) ([]Ticket, error) {
	filterJSON := query
	if filterJSON == "" {
		filterJSON = DefaultLinearFilter
	}
	var filter map[string]any
	if err := json.Unmarshal([]byte(filterJSON), &filter); err != nil {
		return nil, fmt.Errorf("invalid linear filter: %w", err)
	}

	var out []Ticket
	after := ""
	for page := 0; page < 10; page++ {
		vars := map[string]any{"filter": filter}
		if after != "" {
			vars["after"] = after
		}

		body, status, err := c.do(ctx, linearAssignedIssuesQuery, vars)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("linear search returned %d: %s", status, firstLine(body))
		}

		var parsed linearIssuesResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("decoding linear response: %w", err)
		}
		if len(parsed.Errors) > 0 {
			return nil, fmt.Errorf("linear: %s", parsed.Errors[0].Message)
		}

		for _, issue := range parsed.Data.Viewer.AssignedIssues.Nodes {
			labels := make([]string, 0, len(issue.Labels.Nodes))
			for _, l := range issue.Labels.Nodes {
				labels = append(labels, l.Name)
			}
			out = append(out, Ticket{
				Key:       issue.Identifier,
				Summary:   strings.TrimSpace(issue.Title),
				Status:    issue.State.Name,
				Category:  linearCategory(issue.State.Type),
				URL:       issue.URL,
				Labels:    labels,
				IsSubtask: issue.Parent != nil,
				Source:    c.Name(),
			})
		}

		info := parsed.Data.Viewer.AssignedIssues.PageInfo
		if !info.HasNextPage {
			break
		}
		after = info.EndCursor
	}
	return out, nil
}

// linearCategory maps a Linear workflow state type onto the same three
// buckets JIRA's statusCategory uses, so both trackers sort and group
// together.
func linearCategory(stateType string) string {
	switch stateType {
	case "completed", "canceled":
		return "Done"
	case "started":
		return "In Progress"
	default: // triage, backlog, unstarted
		return "To Do"
	}
}

func (c *linearClient) do(ctx context.Context, query string, variables map[string]any) ([]byte, int, error) {
	payload, err := json.Marshal(linearGraphQLRequest{Query: query, Variables: variables})
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", c.apiKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("linear request failed: %w", err)
	}
	defer resp.Body.Close()

	out, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}
