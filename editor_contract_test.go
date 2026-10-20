package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// The Editor contract, checked against every implementation through its
// public interface. Each implementation runs against a stateful fake of its
// tracker's API that models the real API's behaviour, so every promise is
// checked by its effect: what Tickets reports afterwards.
//
// The clauses are the interface's documented promises plus what its callers
// rely on:
//   - the status picker shows Transition.To, colours it by ToCategory, and
//     applies Transition.ID with values for any Required fields;
//   - applySchedule moves a ticket by finding the transition whose To names
//     an active state, then judges eligibility by the Status Tickets reports;
//   - planToggle removes the labels WORKFLOW.md requires, spelled as
//     WORKFLOW.md spells them, from a ticket matched without regard to case;
//   - planSchedule adds required labels the ticket lacks, which may not exist
//     in the tracker yet.

type trackerEditor interface {
	Tracker
	Editor
}

// contractFixture is one implementation wired to a fresh fake holding a single
// open ticket in status To Do, labelled "keep" and "Mixed-Case".
type contractFixture struct {
	ed      trackerEditor
	key     string   // the seeded ticket
	unknown []string // keys naming no ticket: well formed, and another tracker's form
	query   string   // passed to Tickets: the tracker's default scope
	writes  func() int
	// meanwhile has someone else add a label to the ticket right after the
	// editor next reads it, as a concurrent edit in the tracker would.
	meanwhile func(label string)
}

var contractImplementations = []struct {
	name string
	new  func(t *testing.T) contractFixture
}{
	{"JIRA", newJIRAContractFixture},
	{"Linear", newLinearContractFixture},
	{"GitHub", newGitHubContractFixture},
}

func TestEditorContract(t *testing.T) {
	for _, impl := range contractImplementations {
		t.Run(impl.name, func(t *testing.T) {
			t.Run("transitions name a target and a known category", func(t *testing.T) {
				f := impl.new(t)
				trs := mustTransitions(t, f)
				if len(trs) == 0 {
					t.Fatal("no transitions offered for an open ticket")
				}
				for _, tr := range trs {
					if tr.ID == "" || tr.To == "" {
						t.Errorf("transition %+v lacks an ID or a target", tr)
					}
					if _, ok := categoryRank[tr.ToCategory]; !ok {
						t.Errorf("transition to %q has category %q, want To Do, In Progress or Done", tr.To, tr.ToCategory)
					}
				}
			})

			t.Run("every offered transition applies and is what Tickets then reports", func(t *testing.T) {
				for i := range len(mustTransitions(t, impl.new(t))) {
					f := impl.new(t) // a fresh ticket for each move
					tr := mustTransitions(t, f)[i]
					tr.Values = requiredValues(tr)
					if err := f.ed.ApplyTransition(context.Background(), f.key, tr); err != nil {
						t.Errorf("applying %q: %v", tr.To, err)
						continue
					}
					got, found := reported(t, f)
					switch {
					case found && !strings.EqualFold(got.Status, tr.To):
						t.Errorf("after moving to %q, Tickets reports status %q", tr.To, got.Status)
					case !found && tr.ToCategory != "Done":
						t.Errorf("after moving to %q (%s), the ticket left the default scope", tr.To, tr.ToCategory)
					}
				}
			})

			t.Run("a transition that was not offered is refused", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.ApplyTransition(context.Background(), f.key, Transition{ID: "no-such-move"}); err == nil {
					t.Error("ApplyTransition accepted a transition the ticket was never offered")
				}
				if got, _ := reported(t, f); got.Status != "To Do" && got.Status != "Todo" && got.Status != "Open" {
					t.Errorf("status changed to %q by a refused move", got.Status)
				}
			})

			t.Run("labels are added and removed, others untouched", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.UpdateLabels(context.Background(), f.key, []string{"symphony-ready"}, []string{"keep"}); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				assertLabels(t, f, []string{"Mixed-Case", "symphony-ready"})
			})

			t.Run("removing matches the label without regard to case", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.UpdateLabels(context.Background(), f.key, nil, []string{"mixed-case"}); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				assertLabels(t, f, []string{"keep"})
			})

			t.Run("adding a label the tracker has never seen works, or says which label and writes nothing", func(t *testing.T) {
				f := impl.new(t)
				err := f.ed.UpdateLabels(context.Background(), f.key, []string{"brand-new"}, []string{"keep"})
				if err != nil {
					if !strings.Contains(err.Error(), `"brand-new"`) {
						t.Errorf("error %q does not name the label", err)
					}
					if n := f.writes(); n != 0 {
						t.Errorf("made %d writes despite failing", n)
					}
					assertLabels(t, f, []string{"Mixed-Case", "keep"})
					return
				}
				assertLabels(t, f, []string{"Mixed-Case", "brand-new"})
			})

			t.Run("adding a label the ticket has in another case keeps one copy", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.UpdateLabels(context.Background(), f.key, []string{"MIXED-CASE"}, nil); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				assertLabels(t, f, []string{"Mixed-Case", "keep"})
			})

			t.Run("a label someone else adds meanwhile is kept", func(t *testing.T) {
				f := impl.new(t)
				f.meanwhile("theirs")
				if err := f.ed.UpdateLabels(context.Background(), f.key, []string{"symphony-ready"}, []string{"keep"}); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				assertLabels(t, f, []string{"Mixed-Case", "symphony-ready", "theirs"})
			})

			t.Run("removing a label the ticket lacks is not an error", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.UpdateLabels(context.Background(), f.key, nil, []string{"never-there"}); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				assertLabels(t, f, []string{"Mixed-Case", "keep"})
			})

			t.Run("an empty label update writes nothing", func(t *testing.T) {
				f := impl.new(t)
				if err := f.ed.UpdateLabels(context.Background(), f.key, nil, nil); err != nil {
					t.Fatalf("UpdateLabels: %v", err)
				}
				if n := f.writes(); n != 0 {
					t.Errorf("made %d writes, want none", n)
				}
			})

			t.Run("a key naming no ticket is an error, never a silent success", func(t *testing.T) {
				f := impl.new(t)
				ctx := context.Background()
				for _, key := range f.unknown {
					if _, err := f.ed.Transitions(ctx, key); err == nil {
						t.Errorf("Transitions(%q) succeeded", key)
					}
					if err := f.ed.ApplyTransition(ctx, key, Transition{ID: "x"}); err == nil {
						t.Errorf("ApplyTransition(%q) succeeded", key)
					}
					if err := f.ed.UpdateLabels(ctx, key, []string{"a"}, nil); err == nil {
						t.Errorf("UpdateLabels(%q) succeeded", key)
					}
				}
				if n := f.writes(); n != 0 {
					t.Errorf("made %d writes for tickets that do not exist", n)
				}
			})
		})
	}
}

func mustTransitions(t *testing.T, f contractFixture) []Transition {
	t.Helper()
	trs, err := f.ed.Transitions(context.Background(), f.key)
	if err != nil {
		t.Fatalf("Transitions: %v", err)
	}
	return trs
}

// requiredValues answers every required field with its first allowed value,
// as the picker would after the user chose one.
func requiredValues(tr Transition) map[string]any {
	if len(tr.Required) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, f := range tr.Required {
		if len(f.Allowed) > 0 {
			out[f.Key] = fieldValue(f.Allowed[0])
		}
	}
	return out
}

func reported(t *testing.T, f contractFixture) (Ticket, bool) {
	t.Helper()
	tickets, err := f.ed.Tickets(context.Background(), f.query)
	if err != nil {
		t.Fatalf("Tickets: %v", err)
	}
	for _, tk := range tickets {
		if strings.EqualFold(tk.Key, f.key) {
			return tk, true
		}
	}
	return Ticket{}, false
}

func assertLabels(t *testing.T, f contractFixture, want []string) {
	t.Helper()
	got, found := reported(t, f)
	if !found {
		t.Fatal("the ticket is no longer reported")
	}
	labels := slices.Clone(got.Labels)
	slices.Sort(labels)
	if !slices.Equal(labels, want) {
		t.Errorf("labels = %v, want %v", labels, want)
	}
}

// ---------- JIRA ----------

// jiraFake models JIRA Cloud: a transition moves to its target status, the
// default JQL leaves out the Done category, and labels are case-sensitive
// strings that need no prior definition.
type jiraFake struct {
	mu      sync.Mutex
	status  string
	labels  []string
	writes  int
	pending string // added by someone else right after the next read
}

var jiraFakeStatuses = []struct{ id, name, category string }{
	{"11", "To Do", "To Do"}, {"21", "In Progress", "In Progress"}, {"31", "Done", "Done"},
}

func newJIRAContractFixture(t *testing.T) contractFixture {
	f := &jiraFake{status: "To Do", labels: []string{"keep", "Mixed-Case"}}
	mux := http.NewServeMux()
	issue := func(w http.ResponseWriter, r *http.Request) bool {
		if r.PathValue("key") != "ENG-1" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"errorMessages":["Issue does not exist or you do not have permission to see it."]}`)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /rest/api/3/myself", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"accountId":"me"}`)
	})
	mux.HandleFunc("GET /rest/api/3/search/jql", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		cat := ""
		for _, s := range jiraFakeStatuses {
			if s.name == f.status {
				cat = s.category
			}
		}
		if cat == "Done" {
			io.WriteString(w, `{"issues":[]}`)
			return
		}
		labels, _ := json.Marshal(f.labels)
		fmt.Fprintf(w, `{"issues":[{"key":"ENG-1","fields":{"summary":"s","status":{"name":%q,"statusCategory":{"name":%q}},"issuetype":{"name":"Task"},"labels":%s}}]}`,
			f.status, cat, labels)
	})
	mux.HandleFunc("GET /rest/api/3/issue/{key}/transitions", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		var trs []string
		for _, s := range jiraFakeStatuses {
			if s.name != f.status {
				trs = append(trs, fmt.Sprintf(`{"id":%q,"name":%q,"to":{"name":%q,"statusCategory":{"name":%q}}}`, s.id, s.name, s.name, s.category))
			}
		}
		fmt.Fprintf(w, `{"transitions":[%s]}`, strings.Join(trs, ","))
	})
	mux.HandleFunc("POST /rest/api/3/issue/{key}/transitions", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		var req struct {
			Transition struct{ ID string } `json:"transition"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, s := range jiraFakeStatuses {
			if s.id == req.Transition.ID && s.name != f.status {
				f.status = s.name
				f.writes++
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		w.WriteHeader(http.StatusBadRequest)
		io.WriteString(w, `{"errorMessages":["Transition id is not valid for this issue."]}`)
	})
	mux.HandleFunc("GET /rest/api/3/issue/{key}", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		labels, _ := json.Marshal(f.labels)
		fmt.Fprintf(w, `{"key":"ENG-1","fields":{"labels":%s}}`, labels)
		if f.pending != "" {
			f.labels, f.pending = append(f.labels, f.pending), ""
		}
	})
	mux.HandleFunc("PUT /rest/api/3/issue/{key}", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		var req struct {
			Update struct {
				Labels []map[string]string `json:"labels"`
			} `json:"update"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, op := range req.Update.Labels {
			if l, ok := op["add"]; ok && !slices.Contains(f.labels, l) {
				f.labels = append(f.labels, l)
			}
			if l, ok := op["remove"]; ok {
				f.labels = slices.DeleteFunc(f.labels, func(x string) bool { return x == l })
			}
		}
		f.writes++
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return contractFixture{
		ed:        &jiraClient{baseURL: srv.URL, user: "u", token: "t", http: srv.Client()},
		key:       "ENG-1",
		unknown:   []string{"ENG-404", "#1"},
		query:     DefaultJQL,
		writes:    func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.writes },
		meanwhile: func(l string) { f.mu.Lock(); defer f.mu.Unlock(); f.pending = l },
	}
}

// ---------- Linear ----------

// linearFake models Linear's GraphQL API: an issue is found by identifier or
// id, moves to any state of its team, carries only labels that already exist
// on its team or workspace, and an unknown entity is a GraphQL error.
type linearFake struct {
	mu      sync.Mutex
	stateID string
	labels  []string // label ids
	writes  int
	pending string // label id added by someone else right after the next read
}

// linearFakeLabelOpRE reads one aliased label mutation out of a request.
var linearFakeLabelOpRE = regexp.MustCompile(`(\w+): issue(Add|Remove)Label\(id: \$id, labelId: \$(\w+)\)`)

var (
	linearFakeStates = []struct{ id, name, typ string }{
		{"s-todo", "Todo", "unstarted"}, {"s-prog", "In Progress", "started"}, {"s-done", "Done", "completed"},
	}
	linearFakeLabels = map[string]string{"l-keep": "keep", "l-mixed": "Mixed-Case", "l-ready": "symphony-ready", "l-theirs": "theirs"}
)

func newLinearContractFixture(t *testing.T) contractFixture {
	f := &linearFake{stateID: "s-todo", labels: []string{"l-keep", "l-mixed"}}
	notFound := `{"errors":[{"message":"Entity not found: Issue"}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()

		state := linearFakeStates[slices.IndexFunc(linearFakeStates, func(s struct{ id, name, typ string }) bool { return s.id == f.stateID })]
		labelNodes := func(ids []string) string {
			var out []string
			for _, id := range ids {
				out = append(out, fmt.Sprintf(`{"id":%q,"name":%q}`, id, linearFakeLabels[id]))
			}
			return "[" + strings.Join(out, ",") + "]"
		}
		id, _ := req.Variables["id"].(string)
		known := id == "ENG-1" || id == "uuid-1"

		switch {
		case strings.Contains(req.Query, "assignedIssues"):
			nodes := ""
			if state.typ != "completed" && state.typ != "canceled" {
				var names []string
				for _, l := range f.labels {
					names = append(names, fmt.Sprintf(`{"name":%q}`, linearFakeLabels[l]))
				}
				nodes = fmt.Sprintf(`{"identifier":"ENG-1","title":"s","url":"u","state":{"name":%q,"type":%q},"labels":{"nodes":[%s]},"parent":null}`,
					state.name, state.typ, strings.Join(names, ","))
			}
			fmt.Fprintf(w, `{"data":{"viewer":{"assignedIssues":{"nodes":[%s],"pageInfo":{"hasNextPage":false}}}}}`, nodes)
		case !known:
			io.WriteString(w, notFound)
		case strings.Contains(req.Query, "issueUpdate"):
			if s, ok := req.Variables["stateId"].(string); ok {
				if !slices.ContainsFunc(linearFakeStates, func(x struct{ id, name, typ string }) bool { return x.id == s }) {
					io.WriteString(w, `{"errors":[{"message":"Entity not found: WorkflowState"}]}`)
					return
				}
				f.stateID = s
			}
			f.writes++
			io.WriteString(w, `{"data":{"issueUpdate":{"success":true}}}`)
		case strings.Contains(req.Query, "issueAddLabel") || strings.Contains(req.Query, "issueRemoveLabel"):
			var data, errs []string
			for _, m := range linearFakeLabelOpRE.FindAllStringSubmatch(req.Query, -1) {
				alias, op, label := m[1], m[2], req.Variables[m[3]].(string)
				if _, ok := linearFakeLabels[label]; !ok {
					data = append(data, fmt.Sprintf(`%q:null`, alias))
					errs = append(errs, fmt.Sprintf(`{"message":"Entity not found: IssueLabel","path":[%q]}`, alias))
					continue
				}
				f.labels = slices.DeleteFunc(f.labels, func(x string) bool { return x == label })
				if op == "Add" {
					f.labels = append(f.labels, label)
				}
				data = append(data, fmt.Sprintf(`%q:{"success":true}`, alias))
			}
			f.writes++
			resp := `{"data":{` + strings.Join(data, ",") + `}`
			if len(errs) > 0 {
				resp += `,"errors":[` + strings.Join(errs, ",") + `]`
			}
			io.WriteString(w, resp+"}")
		case strings.Contains(req.Query, "issueLabels"):
			var all []string
			for id, name := range linearFakeLabels {
				all = append(all, fmt.Sprintf(`{"id":%q,"name":%q,"team":{"id":"team-1"}}`, id, name))
			}
			fmt.Fprintf(w, `{"data":{"issue":{"id":"uuid-1","team":{"id":"team-1"},"labels":{"nodes":%s}},"issueLabels":{"nodes":[%s]}}}`,
				labelNodes(f.labels), strings.Join(all, ","))
			if f.pending != "" {
				f.labels, f.pending = append(f.labels, f.pending), ""
			}
		case strings.Contains(req.Query, "states"):
			var states []string
			for i, s := range linearFakeStates {
				states = append(states, fmt.Sprintf(`{"id":%q,"name":%q,"type":%q,"position":%d}`, s.id, s.name, s.typ, i))
			}
			fmt.Fprintf(w, `{"data":{"issue":{"id":"uuid-1","state":{"id":%q},"team":{"states":{"nodes":[%s]}}}}}`,
				f.stateID, strings.Join(states, ","))
		default:
			t.Errorf("unexpected Linear query: %s", req.Query)
		}
	}))
	t.Cleanup(srv.Close)
	return contractFixture{
		ed:      &linearClient{apiKey: "k", endpoint: srv.URL, http: srv.Client()},
		key:     "ENG-1",
		unknown: []string{"ENG-404", "#1"},
		query:   "",
		writes:  func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.writes },
		meanwhile: func(l string) {
			f.mu.Lock()
			defer f.mu.Unlock()
			for id, name := range linearFakeLabels {
				if name == l {
					f.pending = id
				}
			}
		},
	}
}

// ---------- GitHub ----------

// githubFake models GitHub: search sees only open issues, closing takes a
// state_reason of completed or not_planned, adding a label creates it, label
// names match without regard to case, and removing a label the issue lacks
// answers 404.
type githubFake struct {
	mu      sync.Mutex
	open    bool
	labels  []string
	writes  int
	pending string // added by someone else right after the next read
}

func newGitHubContractFixture(t *testing.T) contractFixture {
	f := &githubFake{open: true, labels: []string{"keep", "Mixed-Case"}}
	mux := http.NewServeMux()
	issue := func(w http.ResponseWriter, r *http.Request) bool {
		if r.PathValue("n") != "1" {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Not Found"}`)
			return false
		}
		return true
	}
	mux.HandleFunc("GET /repos/acme/app", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"full_name":"acme/app"}`)
	})
	mux.HandleFunc("POST /graphql", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		nodes := ""
		if f.open {
			var names []string
			for _, l := range f.labels {
				names = append(names, fmt.Sprintf(`{"name":%q}`, l))
			}
			nodes = fmt.Sprintf(`{"number":1,"title":"s","url":"https://github.com/acme/app/issues/1","labels":{"nodes":[%s]},"subIssues":{"totalCount":0},"closedByPullRequestsReferences":{"nodes":[]}}`,
				strings.Join(names, ","))
		}
		fmt.Fprintf(w, `{"data":{"search":{"nodes":[%s],"pageInfo":{"hasNextPage":false}}}}`, nodes)
	})
	mux.HandleFunc("GET /repos/acme/app/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		state := "closed"
		if f.open {
			state = "open"
		}
		var labels []string
		for _, l := range f.labels {
			labels = append(labels, fmt.Sprintf(`{"name":%q}`, l))
		}
		fmt.Fprintf(w, `{"number":1,"state":%q,"labels":[%s]}`, state, strings.Join(labels, ","))
		if f.pending != "" {
			f.labels, f.pending = append(f.labels, f.pending), ""
		}
	})
	mux.HandleFunc("PUT /repos/acme/app/issues/{n}/labels", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		var req struct{ Labels []string }
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		f.labels = req.Labels
		f.writes++
		io.WriteString(w, `[]`)
	})
	mux.HandleFunc("PATCH /repos/acme/app/issues/{n}", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		var req map[string]string
		json.NewDecoder(r.Body).Decode(&req)
		if req["state"] != "closed" || (req["state_reason"] != "completed" && req["state_reason"] != "not_planned") {
			w.WriteHeader(http.StatusUnprocessableEntity)
			io.WriteString(w, `{"message":"Validation Failed"}`)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		f.open = false
		f.writes++
		io.WriteString(w, `{}`)
	})
	mux.HandleFunc("POST /repos/acme/app/issues/{n}/labels", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		var req struct{ Labels []string }
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, l := range req.Labels {
			if !containsFold(f.labels, l) {
				f.labels = append(f.labels, l)
			}
		}
		f.writes++
		io.WriteString(w, `[]`)
	})
	mux.HandleFunc("DELETE /repos/acme/app/issues/{n}/labels/{name}", func(w http.ResponseWriter, r *http.Request) {
		if !issue(w, r) {
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		name := r.PathValue("name")
		if !containsFold(f.labels, name) {
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"Label does not exist"}`)
			return
		}
		f.labels = slices.DeleteFunc(f.labels, func(x string) bool { return strings.EqualFold(x, name) })
		f.writes++
		io.WriteString(w, `[]`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	gh := &githubClient{token: "t", http: srv.Client(), api: srv.URL}
	return contractFixture{
		ed:        newGitHubIssues(gh, "acme", "app"),
		key:       "#1",
		unknown:   []string{"#" + strconv.Itoa(404), "ENG-1"},
		query:     "",
		writes:    func() int { f.mu.Lock(); defer f.mu.Unlock(); return f.writes },
		meanwhile: func(l string) { f.mu.Lock(); defer f.mu.Unlock(); f.pending = l },
	}
}
