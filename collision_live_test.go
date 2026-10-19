package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// These tests drive the real *app through an actual tea.Program event loop —
// real key parsing, real Update()/View() dispatch, real HTTP round trips to
// local fixture servers standing in for JIRA and Linear — rather than calling
// a.openPicker()/a.scheduleForSymphony()/a.settle() directly the way the
// table tests above do. They reproduce the exact scenario this PR fixes: a
// JIRA project and a Linear team both issuing ticket key "ENG-1".

// syncBuf is a concurrency-safe io.Writer the test goroutine can poll while
// the Program renders to it from its own goroutine.
type syncBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}

func (s *syncBuf) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Len()
}

func waitForOutput(t *testing.T, out *syncBuf, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(out.String(), substr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for rendered output to contain %q; last frame:\n%s", timeout, substr, out.String())
}

// newJIRAFixture serves the two JIRA endpoints fetchTickets needs: the ticket
// search and the child-count search. It reports category/status from the
// atomic swapped so a test can trigger a reordering refresh.
func newJIRAFixture(t *testing.T, hits chan<- struct{}, swapped *atomic.Bool, transitions *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/transitions") {
			transitions.Add(1)
			fmt.Fprint(w, `{"transitions":[]}`)
			return
		}
		jql := r.URL.Query().Get("jql")
		if strings.Contains(jql, "parent in") {
			fmt.Fprint(w, `{"issues":[]}`)
			return
		}
		status, category := "In Progress", "In Progress"
		if swapped.Load() {
			status, category = "To Do", "To Do"
		}
		fmt.Fprintf(w, `{"issues":[{"key":"ENG-1","fields":{"summary":"jira ticket","status":{"name":%q,"statusCategory":{"name":%q}},"issuetype":{"name":"Task","subtask":false},"labels":[]}}]}`, status, category)
		select {
		case hits <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// newLinearFixture serves the Linear GraphQL endpoint, reporting the opposite
// category/status from the JIRA fixture so exactly one ENG-1 row sits in each
// column, with source swapped alongside the JIRA fixture.
func newLinearFixture(t *testing.T, swapped *atomic.Bool, states *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if body, _ := io.ReadAll(r.Body); strings.Contains(string(body), "States") {
			states.Add(1)
			fmt.Fprint(w, `{"data":{"issue":{"id":"u","state":{"id":"s1"},"team":{"states":{"nodes":[]}}}}}`)
			return
		}
		name, stateType := "To Do", "unstarted"
		if swapped.Load() {
			name, stateType = "In Progress", "started"
		}
		fmt.Fprintf(w, `{"data":{"viewer":{"assignedIssues":{"nodes":[{"identifier":"ENG-1","title":"linear ticket","url":"https://linear.app/x/issue/ENG-1","state":{"name":%q,"type":%q},"labels":{"nodes":[]},"parent":null}],"pageInfo":{"hasNextPage":false,"endCursor":""}}}}}`, name, stateType)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// liveHarness boots a with a real tea.Program wired to in-memory pipes
// instead of a terminal, so key bytes and rendered frames flow exactly as
// they would for a person typing at the real binary, without needing a pty.
type liveHarness struct {
	out  *syncBuf
	send func(string)
	stop func()
}

func startLiveApp(t *testing.T, a *app) *liveHarness {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()

	out := &syncBuf{}
	go io.Copy(out, outR)

	prog := tea.NewProgram(a, tea.WithInput(inR), tea.WithOutput(outW), tea.WithoutSignalHandler())
	done := make(chan error, 1)
	go func() { _, err := prog.Run(); done <- err }()

	h := &liveHarness{
		out: out,
		send: func(keys string) {
			if _, err := inW.Write([]byte(keys)); err != nil {
				t.Fatalf("writing input: %v", err)
			}
		},
	}
	h.stop = func() {
		h.send("q")
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			prog.Kill()
			<-done
		}
		inW.Close()
		outW.Close()
	}
	t.Cleanup(h.stop)
	return h
}

func newCollisionApp(t *testing.T, jiraURL, linearURL string) *app {
	t.Helper()
	for _, key := range []string{"JIRA_URL", "JIRA_USERNAME", "JIRA_API_TOKEN", "LINEAR_API_KEY", "GITHUB_TOKEN", "GH_TOKEN"} {
		t.Setenv(key, "")
	}
	a := newApp("", "", "", 0, true, false)
	a.jira = &jiraClient{baseURL: jiraURL, user: "test", token: "test", http: http.DefaultClient}
	linear := &linearClient{apiKey: "test", endpoint: linearURL, http: http.DefaultClient}
	a.trackers = []trackerSource{{tracker: a.jira, query: ""}, {tracker: linear, query: ""}}
	a.gh, a.ghCfg, a.ghErr = nil, nil, nil
	return a
}

// waitForCount polls until n reaches want, or fails.
func waitForCount(t *testing.T, n *atomic.Int32, want int32, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if n.Load() >= want {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (got %d, want %d)", what, n.Load(), want)
}

// TestLiveOpenPickerAsksTheCursorsOwnTrackerAcrossAKeyCollision drives the real
// event loop end to end: it fetches a JIRA ENG-1 (In Progress) and a Linear
// ENG-1 (To Do) from local fixture servers, moves the cursor onto the Linear
// row (which sorts after the JIRA row), and confirms 's' asks Linear for the
// moves, not the JIRA ticket that shares its key.
func TestLiveOpenPickerAsksTheCursorsOwnTrackerAcrossAKeyCollision(t *testing.T) {
	var swapped atomic.Bool // JIRA starts "In Progress" (sorts first), Linear "To Do"
	var jiraMoves, linearMoves atomic.Int32
	hits := make(chan struct{}, 8)
	jiraSrv := newJIRAFixture(t, hits, &swapped, &jiraMoves)
	linearSrv := newLinearFixture(t, &swapped, &linearMoves)

	a := newCollisionApp(t, jiraSrv.URL, linearSrv.URL)
	h := startLiveApp(t, a)

	waitForOutput(t, h.out, "jira ticket", 5*time.Second)
	waitForOutput(t, h.out, "linear ticket", 5*time.Second)

	// JIRA's "In Progress" row sorts before Linear's "To Do" row, so one down
	// move lands the cursor on the colliding Linear row.
	h.send("j")
	h.send("s")

	waitForCount(t, &linearMoves, 1, "Linear to be asked for transitions")
	if n := jiraMoves.Load(); n != 0 {
		t.Errorf("JIRA was asked for transitions %d times for the Linear row", n)
	}
}

// TestLiveCursorStaysOnItsOwnTrackersTicketAcrossAReorderingRefresh
// reproduces the cursor-restore collision a prior review round caught: the
// cursor starts on the Linear ENG-1 row (its group sorts first), then a
// refresh reorders the groups so the JIRA ENG-1 row sorts first. settle()'s
// id-based cursor restore must follow the Linear ticket, not land on the JIRA
// ticket that now shares its old position. Which tracker 's' asks afterwards
// is the live, observable proxy for which row the cursor is on.
func TestLiveCursorStaysOnItsOwnTrackersTicketAcrossAReorderingRefresh(t *testing.T) {
	hits := make(chan struct{}, 8)
	var jiraMoves, linearMoves atomic.Int32

	// JIRA needs to start "To Do" while Linear starts "In Progress" — the
	// reverse of the other test — so each fixture gets its own swap flag.
	jiraSwapped := &atomic.Bool{}
	jiraSwapped.Store(true) // swapped=true -> JIRA "To Do" initially
	linearSwapped := &atomic.Bool{}
	linearSwapped.Store(true) // swapped=true -> Linear "In Progress" initially

	jiraSrv := newJIRAFixture(t, hits, jiraSwapped, &jiraMoves)
	linearSrv := newLinearFixture(t, linearSwapped, &linearMoves)

	a := newCollisionApp(t, jiraSrv.URL, linearSrv.URL)
	h := startLiveApp(t, a)

	waitForOutput(t, h.out, "jira ticket", 5*time.Second)
	waitForOutput(t, h.out, "linear ticket", 5*time.Second)
	<-hits // drain the initial fetch signal

	// Cursor defaults to row 0, the Linear "In Progress" row: 's' asks Linear.
	h.send("s")
	waitForCount(t, &linearMoves, 1, "Linear to be asked before the refresh")
	// The fixture offers no moves, so the picker shows an error: the first
	// escape dismisses it and the second closes the picker.
	time.Sleep(150 * time.Millisecond)
	h.send("\x1b")
	time.Sleep(150 * time.Millisecond)
	h.send("\x1b")
	time.Sleep(150 * time.Millisecond)

	// Flip both fixtures so JIRA now reports "In Progress" (sorts first) and
	// Linear reports "To Do" — the reorder settle()'s cursor-restore exists to
	// survive.
	jiraSwapped.Store(false)
	linearSwapped.Store(false)
	h.send("r")

	select {
	case <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the refresh to hit the JIRA fixture")
	}
	// settle() runs inside Update() on ticketsMsg; let the Program process the
	// message it sent itself before driving the next key.
	time.Sleep(150 * time.Millisecond)

	// If the cursor wrongly followed the id collision onto the JIRA row, this
	// would ask JIRA instead.
	h.send("s")
	waitForCount(t, &linearMoves, 2, "Linear to be asked after the refresh")
	if n := jiraMoves.Load(); n != 0 {
		t.Errorf("JIRA was asked for transitions %d times; the cursor moved off the Linear ticket", n)
	}
}
