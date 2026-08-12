package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
)

// FieldOption is one selectable value of a required transition field.
type FieldOption struct {
	ID   string
	Name string
}

// RequiredField is a field JIRA insists on when applying a transition, such as
// the resolution demanded by a Close transition.
type RequiredField struct {
	Key     string // JIRA field key, e.g. "resolution"
	Name    string // human label, e.g. "Resolution"
	Type    string // JIRA schema type, e.g. "resolution", "option"
	Allowed []FieldOption
}

// Transition is one move JIRA will allow for an issue in its current state.
// Name is the transition's own label, which is not always the status it lands
// in: in the MOD workflow the "Done" transition moves to "Awaiting
// Verification", so To is what the dashboard shows.
type Transition struct {
	ID         string
	Name       string
	To         string
	ToCategory string
	Required   []RequiredField
}

// Blocked reports the required fields this tool cannot supply, because JIRA
// offers no list of values to choose from.
func (t Transition) Blocked() []string {
	var out []string
	for _, f := range t.Required {
		if len(f.Allowed) == 0 {
			out = append(out, f.Name)
		}
	}
	return out
}

// NeedsInput reports whether applying the transition requires picking values.
func (t Transition) NeedsInput() bool {
	for _, f := range t.Required {
		if len(f.Allowed) > 0 {
			return true
		}
	}
	return false
}

type transitionsResponse struct {
	Transitions []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		To   struct {
			Name           string `json:"name"`
			StatusCategory struct {
				Name string `json:"name"`
			} `json:"statusCategory"`
		} `json:"to"`
		Fields map[string]struct {
			Required bool   `json:"required"`
			Name     string `json:"name"`
			Schema   struct {
				Type string `json:"type"`
			} `json:"schema"`
			AllowedValues []struct {
				ID    string `json:"id"`
				Name  string `json:"name"`
				Value string `json:"value"`
			} `json:"allowedValues"`
		} `json:"fields"`
	} `json:"transitions"`
}

// Transitions lists the moves allowed for an issue right now. The set is
// workflow- and state-specific, so it must be fetched per issue.
func (c *jiraClient) Transitions(ctx context.Context, key string) ([]Transition, error) {
	q := url.Values{}
	q.Set("expand", "transitions.fields")

	body, status, err := c.get(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", q)
	if err != nil {
		return nil, err
	}
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return nil, fmt.Errorf("not allowed to read transitions for %s (%d)", key, status)
	}
	if status != http.StatusOK {
		return nil, fmt.Errorf("jira transitions returned %d: %s", status, firstLine(body))
	}

	var parsed transitionsResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("decoding transitions: %w", err)
	}

	out := make([]Transition, 0, len(parsed.Transitions))
	for _, t := range parsed.Transitions {
		tr := Transition{
			ID:         t.ID,
			Name:       strings.TrimSpace(t.Name),
			To:         strings.TrimSpace(t.To.Name),
			ToCategory: t.To.StatusCategory.Name,
		}
		if tr.To == "" {
			tr.To = tr.Name
		}

		for fieldKey, f := range t.Fields {
			if !f.Required {
				continue
			}
			rf := RequiredField{Key: fieldKey, Name: f.Name, Type: f.Schema.Type}
			if rf.Name == "" {
				rf.Name = fieldKey
			}
			for _, v := range f.AllowedValues {
				name := v.Name
				if name == "" {
					name = v.Value
				}
				if name == "" {
					continue
				}
				rf.Allowed = append(rf.Allowed, FieldOption{ID: v.ID, Name: name})
			}
			tr.Required = append(tr.Required, rf)
		}
		// Field order comes from a map, so sort it for a stable prompt order.
		sort.Slice(tr.Required, func(i, j int) bool { return tr.Required[i].Key < tr.Required[j].Key })

		out = append(out, tr)
	}
	return out, nil
}

// ApplyTransition moves an issue, supplying any required field values keyed by
// JIRA field key.
func (c *jiraClient) ApplyTransition(ctx context.Context, key, transitionID string, fields map[string]any) error {
	payload := map[string]any{
		"transition": map[string]string{"id": transitionID},
	}
	if len(fields) > 0 {
		payload["fields"] = fields
	}

	body, status, err := c.post(ctx, "/rest/api/3/issue/"+url.PathEscape(key)+"/transitions", payload)
	if err != nil {
		return err
	}
	switch {
	case status == http.StatusNoContent || status == http.StatusOK:
		return nil
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return fmt.Errorf("not allowed to move %s (%d)", key, status)
	default:
		return fmt.Errorf("%s: %s", key, jiraErrorMessage(body, status))
	}
}

// jiraErrorMessage pulls the useful part out of a JIRA error response.
func jiraErrorMessage(body []byte, status int) string {
	var e struct {
		ErrorMessages []string          `json:"errorMessages"`
		Errors        map[string]string `json:"errors"`
	}
	if err := json.Unmarshal(body, &e); err == nil {
		if len(e.ErrorMessages) > 0 {
			return e.ErrorMessages[0]
		}
		keys := make([]string, 0, len(e.Errors))
		for k := range e.Errors {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) > 0 {
			return fmt.Sprintf("%s: %s", keys[0], e.Errors[keys[0]])
		}
	}
	return fmt.Sprintf("jira returned %d: %s", status, firstLine(body))
}

// fieldValue builds the JSON value JIRA expects for a chosen option.
func fieldValue(opt FieldOption) any {
	if opt.ID != "" {
		return map[string]string{"id": opt.ID}
	}
	return map[string]string{"name": opt.Name}
}
