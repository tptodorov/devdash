package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/term"
)

// snapshot fetches once, renders a single frame and writes it out. It shares
// the interactive renderer, so what you see here is what the TUI draws.
func (a *app) snapshot(w io.Writer) error {
	a.width, a.height = terminalSize()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if !a.demo {
		a.fetchOnce(ctx)
	}

	a.autoRefresh = false
	a.snapshotMode = true
	a.settle()
	a.cursor = -1 // a static snapshot has no cursor to highlight

	// Size the frame to the content so a snapshot is never truncated.
	body, _ := a.buildBody(a.layout())
	a.height = len(body) + 6
	if a.jiraErr != nil {
		a.height++
	}
	if a.ghErr != nil {
		a.height++
	}

	if _, err := fmt.Fprintln(w, a.View()); err != nil {
		return err
	}
	if a.jiraErr != nil || a.ghErr != nil {
		return fmt.Errorf("one or more sources failed")
	}
	return nil
}

// fetchOnce loads both sources concurrently for a single frame.
func (a *app) fetchOnce(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if a.jira == nil {
			return
		}
		tickets, err := a.jira.Tickets(ctx, a.jql)
		a.tickets, a.jiraErr = tickets, err
		if err != nil {
			return
		}
		counts, warn := a.jira.ChildCounts(ctx, childCandidates(tickets))
		if warn == nil {
			applyChildCounts(a.tickets, counts)
		} else {
			a.jiraWarn = fmt.Errorf("child counts unavailable: %w", warn)
		}
		cwd, _ := os.Getwd()
		info := symphonyLookup(ctx, cwd)
		applySymphony(a.tickets, info)
		a.symphonyURL = info.endpoint
	}()
	go func() {
		defer wg.Done()
		if a.gh == nil {
			return
		}
		var prs []PullRequest
		var err error
		if a.explicitQuery {
			prs, err = a.gh.PullRequests(ctx, a.ghQuery, a.includeArchived)
		} else {
			prs, err = a.gh.OpenAndMergedPRs(ctx, a.ghQuery,
				mergedPRQuery(a.ghQuery, time.Now(), mergedWindow), a.includeArchived)
		}
		a.prs, a.ghErr = prs, err
	}()
	wg.Wait()
}

// terminalSize reports the output terminal's dimensions, falling back to a
// sensible default when stdout is redirected.
func terminalSize() (int, int) {
	if w, h, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w, h
	}
	if v := os.Getenv("COLUMNS"); v != "" {
		var w int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &w); err == nil && w > 0 {
			return w, 40
		}
	}
	return 120, 40
}
