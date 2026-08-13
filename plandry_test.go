package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// Read-only: prints what S would do for every real ticket, changing nothing.
func TestDryRunSchedulePlans(t *testing.T) {
	dir := os.Getenv("DEVDASH_DRYRUN_DIR")
	if dir == "" {
		t.Skip("set DEVDASH_DRYRUN_DIR to a repo with a WORKFLOW.md")
	}
	cfg, err := readSymphonyConfig(dir)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	t.Logf("conditions: project=%s labels=%v active=%v terminal=%v",
		cfg.Tracker.Provider.ProjectKey, cfg.Tracker.RequiredLabels,
		cfg.Tracker.ActiveStates, cfg.Tracker.TerminalStates)

	jira, err := newJIRAClient(&http.Client{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("jira: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	tickets, err := jira.Tickets(ctx, DefaultJQL)
	if err != nil {
		t.Fatalf("tickets: %v", err)
	}
	for _, tk := range tickets {
		p := planSchedule(cfg, tk)
		switch {
		case p.refusal != "":
			t.Logf("  %-11s %-14s REFUSE  %s", tk.Key, tk.Status, p.refusal)
		case p.nothingToDo():
			t.Logf("  %-11s %-14s ready   already scheduled", tk.Key, tk.Status)
		default:
			t.Logf("  %-11s %-14s WOULD   %s", tk.Key, tk.Status, p.summary())
		}
	}
}
