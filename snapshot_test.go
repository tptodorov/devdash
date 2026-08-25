package main

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

// The -once path is what anyone scripting devdash relies on, and demo mode is
// what a new user sees before they have any credentials. Neither may touch an API.
func TestSnapshotInDemoMode(t *testing.T) {
	a := newAppForTest()
	a.loadDemo()

	var out bytes.Buffer
	if err := a.snapshot(&out); err != nil {
		t.Fatalf("snapshot() error = %v", err)
	}
	got := out.String()

	// The frame carries the header, every status group, and the correlated rows.
	for _, want := range []string{
		"devdash", "AWAITING CR", "IN PROGRESS", "BACKLOG",
		"PROJ-482", "PROJ-455", "TEAM-1204",
		"platform #1099", "platform #1103",
		"PRS WITHOUT AN ACTIVE TICKET", "sandbox #5",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("snapshot is missing %q", want)
		}
	}

	// A static snapshot has no cursor, so nothing may be highlighted.
	if strings.Contains(got, bgEscape) {
		t.Error("snapshot should not highlight a row")
	}
	// No error banner: demo mode reaches no API, so nothing can fail.
	for _, banner := range []string{"! jira:", "! github:", "~ jira:"} {
		if strings.Contains(got, banner) {
			t.Errorf("snapshot shows %q in demo mode", banner)
		}
	}
}

// A ticket's second pull request belongs on its own line under the first.
func TestSnapshotShowsContinuationLine(t *testing.T) {
	a := newAppForTest()
	a.loadDemo()

	var out bytes.Buffer
	if err := a.snapshot(&out); err != nil {
		t.Fatalf("snapshot() error = %v", err)
	}

	lines := strings.Split(out.String(), "\n")
	var registry, continuation int
	for i, l := range lines {
		if strings.Contains(l, "PROJ-455") {
			registry = i
		}
		if strings.Contains(l, "platform #1091") {
			continuation = i
		}
	}
	if registry == 0 || continuation == 0 {
		t.Fatalf("expected PROJ-455 and its second PR in the frame")
	}
	if continuation != registry+1 {
		t.Errorf("second PR is on line %d, want directly under PROJ-455 on %d", continuation, registry+1)
	}
	// It is a continuation, so it must not repeat the ticket key.
	if strings.Contains(lines[continuation], "PROJ-455") {
		t.Errorf("continuation line repeats the ticket key: %q", lines[continuation])
	}
}

// demoData is what the README screenshot is generated from, so it has to keep
// covering every case the documentation claims to show.
func TestDemoDataCoversEveryState(t *testing.T) {
	tickets, prs := demoData()

	statuses := map[string]bool{}
	var withChildren, withSymphony int
	for _, tk := range tickets {
		statuses[tk.Status] = true
		if tk.ChildCount > 0 {
			withChildren++
		}
		if tk.Symphony != "" {
			withSymphony++
		}
	}
	if len(statuses) < 4 {
		t.Errorf("demo covers %d statuses, want at least 4", len(statuses))
	}
	if withChildren == 0 {
		t.Error("demo has no ticket with children")
	}
	if withSymphony == 0 {
		t.Error("demo has no Symphony session")
	}
	// Scheduled and actively worked look different, so the demo shows both.
	var symStates = map[string]bool{}
	for _, tk := range tickets {
		if tk.Symphony != "" {
			symStates[tk.Symphony] = true
		}
	}
	for _, want := range []string{SymphonyRunning, SymphonyScheduled} {
		if !symStates[want] {
			t.Errorf("demo has no %s Symphony ticket", want)
		}
	}

	// The attention column is the first thing read on the dashboard, so the demo
	// has to exercise every marker or the screenshot understates it.
	groups, orphans := build(tickets, prs)
	seen := map[attention]bool{}
	for _, g := range groups {
		for _, tk := range g.Tickets {
			seen[attentionFor(tk)] = true
		}
	}
	for _, pr := range orphans {
		seen[attentionForPR(pr)] = true
	}
	for want, name := range map[attention]string{
		attnNone:    "nothing wanted",
		attnMerge:   "ready to merge",
		attnChanges: "changes requested",
		attnAlert:   "failing or blocked",
	} {
		if !seen[want] {
			t.Errorf("demo has no row in the %q attention state", name)
		}
	}

	states := map[string]bool{}
	var drafts, failing, pending, approved, changes int
	for _, pr := range prs {
		states[pr.State] = true
		if pr.Draft {
			drafts++
		}
		switch pr.CI {
		case "FAILURE":
			failing++
		case "PENDING":
			pending++
		}
		switch pr.Review {
		case "CHANGES_REQUESTED":
			changes++
		case "APPROVED":
			approved++
		}
	}
	for _, want := range []string{"OPEN", "MERGED"} {
		if !states[want] {
			t.Errorf("demo has no %s pull request", want)
		}
	}
	for name, n := range map[string]int{
		"draft": drafts, "failing checks": failing, "pending checks": pending,
		"approved": approved, "changes requested": changes,
	} {
		if n == 0 {
			t.Errorf("demo has no PR with %s", name)
		}
	}

	// The screenshot shows a PR with no ticket; correlation must produce one.
	if len(orphans) == 0 {
		t.Error("demo produces no pull request without a ticket")
	}
}

func TestOSC52Copy(t *testing.T) {
	var out bytes.Buffer
	const payload = "PROJ-482 — a snippet\nhttps://example.test/browse/PROJ-482"

	if err := osc52Copy(&out, payload); err != nil {
		t.Fatalf("osc52Copy() error = %v", err)
	}
	got := out.String()

	if !strings.HasPrefix(got, "\x1b]52;c;") || !strings.HasSuffix(got, "\x07") {
		t.Fatalf("osc52Copy wrote %q, want an OSC 52 sequence", got)
	}
	encoded := strings.TrimSuffix(strings.TrimPrefix(got, "\x1b]52;c;"), "\x07")
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("payload is not valid base64: %v", err)
	}
	if string(decoded) != payload {
		t.Errorf("decoded %q, want %q", decoded, payload)
	}
}

// The clipboard is reached through whichever tool the platform provides. Failing
// to find one must be reported, not silently swallowed.
func TestClipboardCommand(t *testing.T) {
	cmd := clipboardCommand()
	if cmd == nil {
		t.Skip("no clipboard tool on this machine; the OSC 52 fallback covers it")
	}
	if cmd.Path == "" {
		t.Error("clipboardCommand returned a command with no path")
	}
	if len(cmd.Args) == 0 {
		t.Error("clipboardCommand returned a command with no args")
	}
}

// newAppForTest builds an app without touching the environment for credentials.
func newAppForTest() *app {
	return &app{width: 104, height: 44, now: time.Now()}
}
