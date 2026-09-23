package main

import "context"

// Tracker fetches the tickets assigned to the current user from one issue
// tracking system, so devdash can show several trackers side by side rather
// than being wired to JIRA alone.
type Tracker interface {
	// Name identifies the tracker in status and error messages, e.g. "JIRA".
	Name() string
	// Tickets returns the tickets matching query, a tracker-specific filter
	// (JIRA's JQL, Linear's IssueFilter JSON); an empty query uses that
	// tracker's own default of "assigned to me, not done".
	Tickets(ctx context.Context, query string) ([]Ticket, error)
}

// trackerSource pairs a configured Tracker with the query that selects its
// tickets.
type trackerSource struct {
	tracker Tracker
	query   string
}
