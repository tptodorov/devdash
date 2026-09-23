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

// waitForNewOutput is like waitForOutput but only looks at bytes rendered
// after since, so a stale match from an earlier assertion in the same test
// (e.g. a flash message still sitting in the accumulated buffer) cannot make
// a later, unrelated check pass vacuously.
func waitForNewOutput(t *testing.T, out *syncBuf, since int, substr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if full := out.String(); len(full) >= since && strings.Contains(full[since:], substr) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for new rendered output to contain %q; output since marker:\n%s", timeout, substr, out.String()[min(since, out.Len()):])
}

// newJIRAFixture serves the two JIRA endpoints fetchTickets needs: the ticket
// search and the child-count search. It reports category/status from the
// atomic swapped so a test can trigger a reordering refresh.
func newJIRAFixture(t *testing.T, hits chan<- struct{}, swapped *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
func newLinearFixture(t *testing.T, swapped *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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

// TestLiveOpenPickerAndScheduleRefuseATrackerKeyCollision drives the real
// binary's event loop end to end: it fetches a JIRA ENG-1 (In Progress) and a
// Linear ENG-1 (To Do) from local fixture servers, moves the cursor onto the
// Linear row (which sorts after the JIRA row), and confirms both JIRA-only
// actions ('s' status-change, 'S' Symphony schedule) refuse it live instead
// of silently resolving the JIRA ticket that shares its key.
func TestLiveOpenPickerAndScheduleRefuseATrackerKeyCollision(t *testing.T) {
	var swapped atomic.Bool // JIRA starts "In Progress" (sorts first), Linear "To Do"
	hits := make(chan struct{}, 8)
	jiraSrv := newJIRAFixture(t, hits, &swapped)
	linearSrv := newLinearFixture(t, &swapped)

	a := newCollisionApp(t, jiraSrv.URL, linearSrv.URL)
	h := startLiveApp(t, a)

	waitForOutput(t, h.out, "jira ticket", 5*time.Second)
	waitForOutput(t, h.out, "linear ticket", 5*time.Second)

	// JIRA's "In Progress" row sorts before Linear's "To Do" row, so one down
	// move lands the cursor on the colliding Linear row.
	h.send("j")

	mark := h.out.Len()
	h.send("s")
	waitForNewOutput(t, h.out, mark, "only supported for JIRA tickets", 5*time.Second)
	if strings.Contains(h.out.String()[mark:], "Loading transitions") {
		t.Error("openPicker opened a JIRA picker for the Linear row instead of refusing it")
	}

	mark = h.out.Len()
	h.send("S")
	waitForNewOutput(t, h.out, mark, "only supported for JIRA tickets", 5*time.Second)
}

// TestLiveCursorStaysOnItsOwnTrackersTicketAcrossAReorderingRefresh
// reproduces the cursor-restore collision a prior review round on this same
// change caught: the cursor starts on the Linear ENG-1 row (its group sorts
// first), then a refresh reorders the groups so the JIRA ENG-1 row sorts
// first instead. settle()'s id-based cursor restore must follow the Linear
// ticket, not silently land on the JIRA ticket that now shares its old
// position. The JIRA-only guard is used as the live, observable proxy for
// "which tracker is the cursor actually on": if it fires after the refresh,
// the cursor is still on the Linear row.
func TestLiveCursorStaysOnItsOwnTrackersTicketAcrossAReorderingRefresh(t *testing.T) {
	hits := make(chan struct{}, 8)

	// JIRA needs to start "To Do" while Linear starts "In Progress" — the
	// reverse of the other test — so each fixture gets its own swap flag
	// instead of sharing one.
	jiraSwapped := &atomic.Bool{}
	jiraSwapped.Store(true) // swapped=true -> JIRA "To Do" initially
	linearSwapped := &atomic.Bool{}
	linearSwapped.Store(true) // swapped=true -> Linear "In Progress" initially

	jiraSrv := newJIRAFixture(t, hits, jiraSwapped)
	linearSrv := newLinearFixture(t, linearSwapped)

	a := newCollisionApp(t, jiraSrv.URL, linearSrv.URL)
	h := startLiveApp(t, a)

	waitForOutput(t, h.out, "jira ticket", 5*time.Second)
	waitForOutput(t, h.out, "linear ticket", 5*time.Second)
	<-hits // drain the initial fetch signal

	// Cursor defaults to row 0, which is the Linear "In Progress" row.
	// Confirm that directly: the JIRA-only guard must refuse it now, before
	// any refresh is involved.
	mark := h.out.Len()
	h.send("s")
	waitForNewOutput(t, h.out, mark, "only supported for JIRA tickets", 5*time.Second)

	// Flip both fixtures so JIRA now reports "In Progress" (sorts first) and
	// Linear reports "To Do" — the exact reorder settle()'s cursor-restore
	// exists to survive.
	jiraSwapped.Store(false)
	linearSwapped.Store(false)
	h.send("r")

	select {
	case <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the refresh to hit the JIRA fixture")
	}
	// settle() runs synchronously inside Update() on ticketsMsg; give the
	// Program's own goroutine a moment to process the message it just sent
	// itself before driving the next key.
	time.Sleep(150 * time.Millisecond)

	// If the cursor incorrectly followed the id collision onto the JIRA row,
	// this guard would not fire (a real transitions fetch would start against
	// the JIRA fixture instead), and this check — scoped to only the output
	// rendered after the refresh — would time out. This uses 'S' rather than
	// the 's' already checked above: bubbletea only writes bytes for what
	// actually changed on screen, and a second identical flash message would
	// render byte-for-byte the same as the first, making a same-key repeat
	// produce no new output to observe even when the guard fires correctly.
	mark = h.out.Len()
	h.send("S")
	waitForNewOutput(t, h.out, mark, "only supported for JIRA tickets", 5*time.Second)
}
