package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// DefaultJQL selects every issue assigned to the current user that has not
// reached the Done status category.
const DefaultJQL = `assignee = currentUser() AND statusCategory != Done ORDER BY updated DESC`

type jiraClient struct {
	baseURL string
	user    string
	token   string
	http    *http.Client
}

// Name identifies this tracker in status and error messages.
func (c *jiraClient) Name() string { return "JIRA" }

func newJIRAClient(hc *http.Client) (*jiraClient, error) {
	c := &jiraClient{
		baseURL: strings.TrimRight(os.Getenv("JIRA_URL"), "/"),
		user:    os.Getenv("JIRA_USERNAME"),
		token:   os.Getenv("JIRA_API_TOKEN"),
		http:    hc,
	}
	var missing []string
	if c.baseURL == "" {
		missing = append(missing, "JIRA_URL")
	}
	if c.user == "" {
		missing = append(missing, "JIRA_USERNAME")
	}
	if c.token == "" {
		missing = append(missing, "JIRA_API_TOKEN")
	}
	if len(missing) > 0 {
		return nil, fmt.Errorf("missing environment variables: %s", strings.Join(missing, ", "))
	}
	return c, nil
}

// get performs an authenticated GET and returns the body and status code.
func (c *jiraClient) get(ctx context.Context, path string, q url.Values) ([]byte, int, error) {
	endpoint := c.baseURL + path
	if len(q) > 0 {
		endpoint += "?" + q.Encode()
	}
	return c.do(ctx, http.MethodGet, endpoint, nil)
}

// post performs an authenticated POST with a JSON body.
func (c *jiraClient) post(ctx context.Context, path string, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	return c.do(ctx, http.MethodPost, c.baseURL+path, body)
}

// put performs an authenticated PUT with a JSON body.
func (c *jiraClient) put(ctx context.Context, path string, payload any) ([]byte, int, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, 0, err
	}
	return c.do(ctx, http.MethodPut, c.baseURL+path, body)
}

func (c *jiraClient) do(ctx context.Context, method, endpoint string, body []byte) ([]byte, int, error) {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, rdr)
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(c.user, c.token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("jira request failed: %w", err)
	}
	defer resp.Body.Close()

	out, err := readAllLimited(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

type jiraSearchResponse struct {
	NextPageToken string `json:"nextPageToken"`
	Issues        []struct {
		Key    string `json:"key"`
		Fields struct {
			Summary string `json:"summary"`
			Status  struct {
				Name           string `json:"name"`
				StatusCategory struct {
					Name string `json:"name"`
				} `json:"statusCategory"`
			} `json:"status"`
			IssueType struct {
				Name    string `json:"name"`
				Subtask bool   `json:"subtask"`
			} `json:"issuetype"`
			Labels []string `json:"labels"`
		} `json:"fields"`
	} `json:"issues"`
}

// Tickets runs the JQL query and returns the matching issues, following
// pagination until the result set is exhausted.
func (c *jiraClient) Tickets(ctx context.Context, jql string) ([]Ticket, error) {
	var out []Ticket
	token := ""
	for page := 0; page < 10; page++ {
		q := url.Values{}
		q.Set("jql", jql)
		q.Set("maxResults", "100")
		q.Set("fields", "key,summary,status,issuetype,labels")
		if token != "" {
			q.Set("nextPageToken", token)
		}

		body, status, err := c.get(ctx, "/rest/api/3/search/jql", q)
		if err != nil {
			return nil, err
		}
		if status != http.StatusOK {
			return nil, fmt.Errorf("jira search returned %d: %s", status, firstLine(body))
		}

		var parsed jiraSearchResponse
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("decoding jira response: %w", err)
		}
		for _, issue := range parsed.Issues {
			out = append(out, Ticket{
				Key:       issue.Key,
				Summary:   strings.TrimSpace(issue.Fields.Summary),
				Status:    issue.Fields.Status.Name,
				Category:  issue.Fields.Status.StatusCategory.Name,
				Type:      issue.Fields.IssueType.Name,
				Labels:    issue.Fields.Labels,
				IsSubtask: issue.Fields.IssueType.Subtask,
				URL:       c.baseURL + "/browse/" + issue.Key,
				Source:    c.Name(),
			})
		}
		if parsed.NextPageToken == "" {
			break
		}
		token = parsed.NextPageToken
	}

	// The search endpoint answers an unauthenticated request with 200 and an
	// empty result set, so an expired token is indistinguishable from an empty
	// backlog. Confirm the identity before reporting "nothing assigned".
	if len(out) == 0 {
		if err := c.verifyAuth(ctx); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// childBatch caps how many keys go into one JQL "parent in (...)" clause.
const childBatch = 50

// ChildCounts reports how many issues name each candidate as their parent,
// counting children assigned to anyone. The subtasks field on an issue cannot
// answer this: it stays empty for epic and initiative children, so the
// relationship has to be queried from the child side.
func (c *jiraClient) ChildCounts(ctx context.Context, candidates []string) (map[string]int, error) {
	counts := map[string]int{}
	for start := 0; start < len(candidates); start += childBatch {
		batch := candidates[start:min(start+childBatch, len(candidates))]

		jql := "parent in (" + strings.Join(batch, ",") + ")"
		token := ""
		for page := 0; page < 10; page++ {
			q := url.Values{}
			q.Set("jql", jql)
			q.Set("maxResults", "100")
			q.Set("fields", "parent")
			if token != "" {
				q.Set("nextPageToken", token)
			}

			body, status, err := c.get(ctx, "/rest/api/3/search/jql", q)
			if err != nil {
				return nil, err
			}
			if status != http.StatusOK {
				return nil, fmt.Errorf("counting children returned %d: %s", status, firstLine(body))
			}

			var parsed struct {
				NextPageToken string `json:"nextPageToken"`
				Issues        []struct {
					Fields struct {
						Parent struct {
							Key string `json:"key"`
						} `json:"parent"`
					} `json:"fields"`
				} `json:"issues"`
			}
			if err := json.Unmarshal(body, &parsed); err != nil {
				return nil, fmt.Errorf("decoding children: %w", err)
			}
			for _, issue := range parsed.Issues {
				if key := issue.Fields.Parent.Key; key != "" {
					counts[strings.ToUpper(key)]++
				}
			}
			if parsed.NextPageToken == "" {
				break
			}
			token = parsed.NextPageToken
		}
	}
	return counts, nil
}

// UpdateLabels adds and removes labels on an issue without touching any other
// field, in a single request so the two never half-apply.
func (c *jiraClient) UpdateLabels(ctx context.Context, key string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	ops := make([]map[string]string, 0, len(add)+len(remove))
	for _, l := range add {
		ops = append(ops, map[string]string{"add": l})
	}
	for _, l := range remove {
		ops = append(ops, map[string]string{"remove": l})
	}
	payload := map[string]any{"update": map[string]any{"labels": ops}}

	body, status, err := c.put(ctx, "/rest/api/3/issue/"+url.PathEscape(key), payload)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusNoContent || status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("not allowed to relabel %s (%d)", key, status)
	default:
		return fmt.Errorf("%s: %s", key, jiraErrorMessage(body, status))
	}
}

// verifyAuth checks the credentials actually identify a user.
func (c *jiraClient) verifyAuth(ctx context.Context) error {
	body, status, err := c.get(ctx, "/rest/api/3/myself", nil)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("authentication failed (%d) — check JIRA_USERNAME and JIRA_API_TOKEN", status)
	case status != http.StatusOK:
		return fmt.Errorf("jira myself returned %d: %s", status, firstLine(body))
	}

	var me struct {
		AccountID string `json:"accountId"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		return fmt.Errorf("decoding jira identity: %w", err)
	}
	if me.AccountID == "" {
		return fmt.Errorf("jira did not identify the credentials — check JIRA_USERNAME and JIRA_API_TOKEN")
	}
	return nil
}
