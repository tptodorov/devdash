package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// realWorkflowFrontMatter mirrors this repository's WORKFLOW.md, including the
// decoys: polling.interval_ms and hooks.timeout_ms are numbers in other
// sections, and the hook bodies are shell scripts full of arbitrary text.
const realWorkflowFrontMatter = `---
# Symphony workflow
tracker:
  kind: jira
  provider:
    base_url: $JIRA_URL
    project_key: MOD
polling:
  interval_ms: 30000
workspace:
  root: .worktrees
server:
  port: 10000
  pull_requests:
    provider: github
    github_repository: acme/platform
    cache_ttl_ms: 60000
hooks:
  timeout_ms: 300000
  after_create: |
    set -eu
    port: 9999
    echo "not config"
---

# Prose after the front matter

Some text mentioning port: 1234 which must be ignored.
`

func writeWorkflow(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, workflowFile)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestSymphonyServer(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		wantHost string
		wantPort int
		wantErr  bool
	}{
		{
			name:     "real workflow shape",
			content:  realWorkflowFrontMatter,
			wantHost: "127.0.0.1",
			wantPort: 10000,
		},
		{
			name:     "explicit host is honoured",
			content:  "---\nserver:\n  host: 0.0.0.0\n  port: 4000\n---\n",
			wantHost: "0.0.0.0", wantPort: 4000,
		},
		{
			// A port in another section must not be mistaken for the server's.
			name:    "port in another section is ignored",
			content: "---\npolling:\n  port: 8080\nhooks:\n  port: 9090\n---\n",
			wantErr: true,
		},
		{
			// Nested deeper than server.port, e.g. server.pull_requests.port.
			name:    "port nested deeper is ignored",
			content: "---\nserver:\n  pull_requests:\n    port: 8080\n---\n",
			wantErr: true,
		},
		{
			name:    "no front matter at all",
			content: "# Just prose\n\nserver:\n  port: 10000\n",
			wantErr: true,
		},
		{
			name:    "server block with no port",
			content: "---\nserver:\n  pull_requests:\n    provider: github\n---\n",
			wantErr: true,
		},
		{
			name:    "empty file",
			content: "",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeWorkflow(t, tc.content)
			host, port, err := symphonyServer(filepath.Join(dir, workflowFile))

			if tc.wantErr {
				if err == nil {
					t.Errorf("symphonyServer() = %s:%d, want an error", host, port)
				}
				return
			}
			if err != nil {
				t.Fatalf("symphonyServer() error = %v", err)
			}
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("symphonyServer() = %s:%d, want %s:%d", host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}

func TestSymphonyEndpoint(t *testing.T) {
	dir := writeWorkflow(t, realWorkflowFrontMatter)

	got, err := symphonyEndpoint(dir)
	if err != nil {
		t.Fatalf("symphonyEndpoint() error = %v", err)
	}
	if want := "http://127.0.0.1:10000"; got != want {
		t.Errorf("symphonyEndpoint() = %q, want %q", got, want)
	}

	// Found by walking up, so the tool works from a subdirectory.
	deep := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err = symphonyEndpoint(deep); err != nil || got != "http://127.0.0.1:10000" {
		t.Errorf("from subdirectory = %q err=%v", got, err)
	}

	// No WORKFLOW.md is not an error condition worth a banner, but it must fail.
	if _, err := symphonyEndpoint(t.TempDir()); err == nil {
		t.Error("expected an error with no WORKFLOW.md")
	}
}

func TestSymphonyState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != symphonyStatePath {
			t.Errorf("path = %q, want %q", r.URL.Path, symphonyStatePath)
		}
		io.WriteString(w, `{
      "running":[{"issue_identifier":"PROJ-17538","last_message":"turn started"},
                 {"issue_identifier":"proj-100"}],
      "blocked":[{"issue_identifier":"PROJ-200"}],
      "retrying":[{"issue_identifier":"PROJ-300"},{"issue_identifier":""}]
    }`)
	}))
	defer srv.Close()

	got, err := SymphonyState(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("SymphonyState() error = %v", err)
	}

	want := map[string]string{
		"PROJ-17538": SymphonyRunning,
		"PROJ-100":   SymphonyRunning, // keys are upper-cased to match ticket keys
		"PROJ-200":   SymphonyBlocked,
		"PROJ-300":   SymphonyRetrying,
	}
	if len(got) != len(want) {
		t.Fatalf("state = %v, want %v", got, want)
	}
	for key, state := range want {
		if got[key] != state {
			t.Errorf("state[%s] = %q, want %q", key, got[key], state)
		}
	}
}

// A ticket that needs an operator must not be hidden behind a busier state.
func TestSymphonyStateBlockedOutranksRunning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{
      "running":[{"issue_identifier":"PROJ-1"}],
      "retrying":[{"issue_identifier":"PROJ-1"}],
      "blocked":[{"issue_identifier":"PROJ-1"}]}`)
	}))
	defer srv.Close()

	got, err := SymphonyState(context.Background(), srv.Client(), srv.URL)
	if err != nil {
		t.Fatalf("SymphonyState() error = %v", err)
	}
	if got["PROJ-1"] != SymphonyBlocked {
		t.Errorf("state = %q, want blocked to win", got["PROJ-1"])
	}
}

func TestSymphonyStateErrors(t *testing.T) {
	t.Run("non-200", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			io.WriteString(w, `{"error":{"code":"orchestrator_unavailable"}}`)
		}))
		defer srv.Close()

		if _, err := SymphonyState(context.Background(), srv.Client(), srv.URL); err == nil {
			t.Error("expected an error for 503")
		}
	})

	t.Run("not listening", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
		url := srv.URL
		srv.Close() // nothing is listening now

		if _, err := SymphonyState(context.Background(), http.DefaultClient, url); err == nil {
			t.Error("expected an error when nothing is listening")
		}
	})
}

func TestApplySymphony(t *testing.T) {
	tickets := []Ticket{
		{Key: "PROJ-17538"},
		{Key: "proj-100"}, // matched case-insensitively
		{Key: "PROJ-999"}, // Symphony is not on it
	}
	applySymphony(tickets, symphonyInfo{live: map[string]string{
		"PROJ-17538": SymphonyRunning,
		"PROJ-100":   SymphonyBlocked,
	}})

	if tickets[0].Symphony != SymphonyRunning {
		t.Errorf("PROJ-17538 = %q, want running", tickets[0].Symphony)
	}
	if tickets[1].Symphony != SymphonyBlocked {
		t.Errorf("proj-100 = %q, want blocked", tickets[1].Symphony)
	}
	if tickets[2].Symphony != "" {
		t.Errorf("PROJ-999 = %q, want empty", tickets[2].Symphony)
	}
}

// The column appears only when Symphony has something, and marks only those rows.
func TestSymphonyColumn(t *testing.T) {
	newApp := func(symphony string) *app {
		a := &app{
			width: 118,
			tickets: []Ticket{
				{Key: "PROJ-1", Summary: "worked on", Status: "In Progress",
					Category: "In Progress", Type: "Task", Symphony: symphony},
				{Key: "PROJ-2", Summary: "not worked on", Status: "In Progress",
					Category: "In Progress", Type: "Task"},
			},
		}
		a.settle()
		return a
	}

	t.Run("collapses when Symphony has nothing", func(t *testing.T) {
		a := newApp("")
		if got := a.measure().symW; got != 0 {
			t.Errorf("symW = %d, want 0", got)
		}
		body, _ := a.buildBody(a.layout())
		for _, line := range body {
			if strings.ContainsAny(line, symphonyIconRunning+symphonyIconBlocked+symphonyIconRetrying) {
				t.Errorf("unexpected marker with no sessions: %q", line)
			}
		}
	})

	for _, tc := range []struct {
		state string
		want  string
	}{
		{SymphonyRunning, symphonyIconRunning},
		{SymphonyBlocked, symphonyIconBlocked},
		{SymphonyRetrying, symphonyIconRetrying},
	} {
		t.Run("marks the row when "+tc.state, func(t *testing.T) {
			a := newApp(tc.state)
			if got := a.measure().symW; got != 1 {
				t.Fatalf("symW = %d, want 1", got)
			}

			lay := a.layout()
			body, rowLine := a.buildBody(lay)
			marked, unmarked := body[rowLine[0]], body[rowLine[1]]

			if !strings.Contains(marked, tc.want) {
				t.Errorf("PROJ-1 row = %q, want marker %q", marked, tc.want)
			}
			if strings.Contains(unmarked, tc.want) {
				t.Errorf("PROJ-2 row should carry no marker: %q", unmarked)
			}

			// The column sits at the right-hand edge: the marker must be the last
			// visible character on the row, after the pull request cell.
			plain := strings.TrimRight(ansi.Strip(marked), " ")
			if !strings.HasSuffix(plain, tc.want) {
				t.Errorf("marker is not the last thing on the row: %q", plain)
			}
			if idx := strings.Index(plain, "no PR"); idx >= 0 {
				if strings.Index(plain, tc.want) < idx {
					t.Errorf("marker appears before the PR cell: %q", plain)
				}
			}
			// The column must not push rows off the edge.
			for i, line := range body {
				if w := lipgloss.Width(line); w > lay.width {
					t.Errorf("line %d is %d wide, over %d", i, w, lay.width)
				}
			}
		})
	}
}

// A ticket can be queued for Symphony without Symphony having picked it up:
// Symphony polls, so this is the ordinary state right after pressing S. The two
// must look different, or scheduling gives no feedback until work starts.
func TestScheduledIsDistinctFromActivelyWorked(t *testing.T) {
	info := symphonyInfo{
		haveCfg: true,
		cfg:     testConfig(),
		live:    map[string]string{"PROJ-2": SymphonyRunning},
	}
	tickets := []Ticket{
		// Eligible, no session yet.
		{Key: "PROJ-1", Status: "To Do", Labels: []string{"symphony-ready"}},
		// Eligible and being worked.
		{Key: "PROJ-2", Status: "In Progress", Labels: []string{"symphony-ready"}},
		// Not eligible: no label.
		{Key: "PROJ-3", Status: "To Do"},
		// Not eligible: parked in a state Symphony ignores.
		{Key: "PROJ-4", Status: "Awaiting CR", Labels: []string{"symphony-ready"}},
		// Not eligible: finished.
		{Key: "PROJ-5", Status: "Done", Labels: []string{"symphony-ready"}},
	}
	applySymphony(tickets, info)

	want := []string{SymphonyScheduled, SymphonyRunning, "", "", ""}
	for i, w := range want {
		if tickets[i].Symphony != w {
			t.Errorf("%s = %q, want %q", tickets[i].Key, tickets[i].Symphony, w)
		}
	}

	// Scheduled is grey, like a draft pull request; running is the accent colour.
	schedIcon, schedStyle := symphonyMarker(SymphonyScheduled)
	runIcon, runStyle := symphonyMarker(SymphonyRunning)
	if schedIcon != runIcon {
		t.Errorf("scheduled and running should share the glyph, got %q and %q", schedIcon, runIcon)
	}
	if schedStyle.Render("x") == runStyle.Render("x") {
		t.Error("scheduled and running render identically; the states are indistinguishable")
	}
	if faint := faintStyle.Render("x"); schedStyle.Render("x") != faint {
		t.Errorf("scheduled should use the same grey as a draft PR")
	}
}

// A live session outranks eligibility, and with no config nothing is scheduled.
func TestApplySymphonyPrecedence(t *testing.T) {
	eligible := Ticket{Key: "PROJ-1", Status: "To Do", Labels: []string{"symphony-ready"}}

	blocked := []Ticket{eligible}
	applySymphony(blocked, symphonyInfo{
		haveCfg: true, cfg: testConfig(),
		live: map[string]string{"PROJ-1": SymphonyBlocked},
	})
	if blocked[0].Symphony != SymphonyBlocked {
		t.Errorf("= %q, want a live session to outrank scheduled", blocked[0].Symphony)
	}

	// Without WORKFLOW.md there are no conditions to judge against.
	noCfg := []Ticket{eligible}
	applySymphony(noCfg, symphonyInfo{})
	if noCfg[0].Symphony != "" {
		t.Errorf("= %q, want nothing without a configuration", noCfg[0].Symphony)
	}
}
