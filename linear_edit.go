package main

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
)

// run sends one GraphQL request and decodes the response's data into out.
func (c *linearClient) run(ctx context.Context, query string, vars map[string]any, out any) error {
	body, status, err := c.do(ctx, query, vars)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("linear returned %d: %s", status, firstLine(body))
	}
	var env struct {
		Data   json.RawMessage      `json:"data"`
		Errors []linearGraphQLError `json:"errors"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return fmt.Errorf("decoding linear response: %w", err)
	}
	if len(env.Errors) > 0 {
		return fmt.Errorf("linear: %s", env.Errors[0].Message)
	}
	return json.Unmarshal(env.Data, out)
}

// linearState is one workflow state of a team.
type linearState struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Position float64 `json:"position"`
}

const linearStatesQuery = `
query States($id: String!) {
  issue(id: $id) {
    id
    state { id }
    team { states { nodes { id name type position } } }
  }
}`

// Transitions lists the workflow states of the issue's team other than the one
// it is in. Linear lets an issue move to any state, so unlike JIRA the set
// does not depend on where it is, and no move needs extra fields.
func (c *linearClient) Transitions(ctx context.Context, key string) ([]Transition, error) {
	var res struct {
		Issue struct {
			State struct {
				ID string `json:"id"`
			} `json:"state"`
			Team struct {
				States struct {
					Nodes []linearState `json:"nodes"`
				} `json:"states"`
			} `json:"team"`
		} `json:"issue"`
	}
	if err := c.run(ctx, linearStatesQuery, map[string]any{"id": key}, &res); err != nil {
		return nil, err
	}

	states := res.Issue.Team.States.Nodes
	slices.SortStableFunc(states, func(a, b linearState) int { return cmp.Compare(a.Position, b.Position) })

	var out []Transition
	for _, s := range states {
		if s.ID == res.Issue.State.ID {
			continue
		}
		out = append(out, Transition{
			ID: s.ID, Name: s.Name, To: s.Name, ToCategory: linearCategory(s.Type),
		})
	}
	return out, nil
}

const linearUpdateMutation = `
mutation Update($id: String!, $stateId: String, $labelIds: [String!]) {
  issueUpdate(id: $id, input: { stateId: $stateId, labelIds: $labelIds }) { success }
}`

// update sends an issueUpdate. Only the variables present are set, because a
// null variable leaves its input field out.
func (c *linearClient) update(ctx context.Context, vars map[string]any) error {
	var res struct {
		IssueUpdate struct {
			Success bool `json:"success"`
		} `json:"issueUpdate"`
	}
	if err := c.run(ctx, linearUpdateMutation, vars, &res); err != nil {
		return err
	}
	if !res.IssueUpdate.Success {
		return fmt.Errorf("linear did not apply the change to %v", vars["id"])
	}
	return nil
}

// ApplyTransition moves an issue to the workflow state with the given id.
func (c *linearClient) ApplyTransition(ctx context.Context, key string, tr Transition) error {
	return c.update(ctx, map[string]any{"id": key, "stateId": tr.ID})
}

const linearLabelsQuery = `
query Labels($id: String!, $names: IssueLabelFilter, $lookup: Boolean!) {
  issue(id: $id) {
    id
    team { id }
    labels { nodes { id name } }
  }
  issueLabels(filter: $names, first: 250) @include(if: $lookup) { nodes { id name team { id } } }
}`

// UpdateLabels adds and removes labels by name, without regard to case. Linear
// sets an issue's labels as a whole, so the new set is worked out from the
// current one and written in one request, which applies whole or not at all.
// The labels to add are looked up by name, and each must already exist on the
// issue's team or workspace-wide; one that does not is an error naming it, and
// nothing is written.
func (c *linearClient) UpdateLabels(ctx context.Context, key string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}

	var res struct {
		Issue struct {
			ID   string `json:"id"`
			Team struct {
				ID string `json:"id"`
			} `json:"team"`
			Labels struct {
				Nodes []struct{ ID, Name string } `json:"nodes"`
			} `json:"labels"`
		} `json:"issue"`
		IssueLabels struct {
			Nodes []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
				Team *struct {
					ID string `json:"id"`
				} `json:"team"`
			} `json:"nodes"`
		} `json:"issueLabels"`
	}
	vars := map[string]any{"id": key, "lookup": len(add) > 0}
	if len(add) > 0 {
		var byName []map[string]any
		for _, name := range add {
			byName = append(byName, map[string]any{"name": map[string]any{"eqIgnoreCase": name}})
		}
		vars["names"] = map[string]any{"or": byName}
	}
	if err := c.run(ctx, linearLabelsQuery, vars, &res); err != nil {
		return err
	}

	var current []string
	for _, l := range res.Issue.Labels.Nodes {
		current = append(current, l.ID)
	}
	ids := []string{}
	for _, l := range res.Issue.Labels.Nodes {
		if !containsFold(remove, l.Name) {
			ids = append(ids, l.ID)
		}
	}
	for _, name := range add {
		found := ""
		for _, l := range res.IssueLabels.Nodes {
			if strings.EqualFold(l.Name, name) && (l.Team == nil || l.Team.ID == res.Issue.Team.ID) {
				found = l.ID
				break
			}
		}
		if found == "" {
			return fmt.Errorf("label %q does not exist in Linear on %s's team or workspace", name, key)
		}
		if !slices.Contains(ids, found) {
			ids = append(ids, found)
		}
	}
	if slices.Equal(ids, current) {
		return nil
	}
	return c.update(ctx, map[string]any{"id": res.Issue.ID, "labelIds": ids})
}
