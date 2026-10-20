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
mutation Update($id: String!, $stateId: String!) {
  issueUpdate(id: $id, input: { stateId: $stateId }) { success }
}`

// update sends an issueUpdate moving an issue to another workflow state.
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

// UpdateLabels adds and removes labels by name, without regard to case, one
// label at a time so that labels it was not asked about are never touched.
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

	// Work out the single-label changes: labels the issue carries that are to
	// go, and labels to add that it does not carry yet, by name and without
	// regard to case.
	var ops []linearLabelOp
	var carried []string
	for _, l := range res.Issue.Labels.Nodes {
		carried = append(carried, l.Name)
		if containsFold(remove, l.Name) {
			ops = append(ops, linearLabelOp{remove: true, id: l.ID, name: l.Name})
		}
	}
	for _, name := range add {
		if containsFold(carried, name) {
			continue
		}
		found := -1
		for i, l := range res.IssueLabels.Nodes {
			if strings.EqualFold(l.Name, name) && (l.Team == nil || l.Team.ID == res.Issue.Team.ID) {
				found = i
				break
			}
		}
		if found < 0 {
			return fmt.Errorf("label %q does not exist in Linear on %s's team or workspace", name, key)
		}
		l := res.IssueLabels.Nodes[found]
		if !slices.ContainsFunc(ops, func(op linearLabelOp) bool { return op.id == l.ID }) {
			ops = append(ops, linearLabelOp{id: l.ID, name: l.Name})
		}
	}
	if len(ops) == 0 {
		return nil
	}
	return c.applyLabelOps(ctx, key, res.Issue.ID, ops)
}

// linearLabelOp adds or removes one label.
type linearLabelOp struct {
	remove   bool
	id, name string
}

// applyLabelOps sends every label change in one request, each its own
// issueAddLabel or issueRemoveLabel, so a label someone else set in the
// meantime is left alone rather than overwritten by a stale full set. Linear
// runs them one by one, not as a transaction, so when any fails the error says
// exactly which were applied and which were not.
func (c *linearClient) applyLabelOps(ctx context.Context, key, issueID string, ops []linearLabelOp) error {
	var params, fields []string
	vars := map[string]any{"id": issueID}
	for i, op := range ops {
		alias, mutation := fmt.Sprintf("a%d", i), "issueAddLabel"
		if op.remove {
			mutation = "issueRemoveLabel"
		}
		params = append(params, "$"+alias+": String!")
		fields = append(fields, fmt.Sprintf("  %s: %s(id: $id, labelId: $%s) { success }", alias, mutation, alias))
		vars[alias] = op.id
	}
	query := fmt.Sprintf("mutation Labels($id: String!, %s) {\n%s\n}", strings.Join(params, ", "), strings.Join(fields, "\n"))

	body, status, err := c.do(ctx, query, vars)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("linear returned %d: %s", status, firstLine(body))
	}
	var res struct {
		Data   map[string]*struct{ Success bool } `json:"data"`
		Errors []struct {
			Message string `json:"message"`
			Path    []any  `json:"path"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		return fmt.Errorf("decoding linear response: %w", err)
	}

	why := map[string]string{} // alias -> failure
	for _, e := range res.Errors {
		if len(e.Path) > 0 {
			if alias, ok := e.Path[0].(string); ok {
				why[alias] = e.Message
				continue
			}
		}
		why[""] = e.Message // not tied to one change: none can be trusted
	}
	var done, failed []string
	for i, op := range ops {
		alias := fmt.Sprintf("a%d", i)
		verb, past := "add", "added"
		if op.remove {
			verb, past = "remove", "removed"
		}
		r := res.Data[alias]
		switch msg, bad := why[alias]; {
		case bad:
			failed = append(failed, fmt.Sprintf("could not %s %q: %s", verb, op.name, msg))
		case r == nil || !r.Success:
			failed = append(failed, fmt.Sprintf("could not %s %q: %s", verb, op.name, cmp.Or(why[""], "linear did not apply it")))
		default:
			done = append(done, fmt.Sprintf("%s %q", past, op.name))
		}
	}
	if len(failed) == 0 {
		return nil
	}
	return fmt.Errorf("%s: %s", key, strings.Join(append(done, failed...), "; "))
}
