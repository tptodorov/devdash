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

// Editor is a Tracker whose tickets can also be changed from the dashboard:
// moved to another status and relabelled. Every tracker devdash reads from
// implements it, so the status picker and Symphony scheduling work on all of
// them.
type Editor interface {
	// Transitions lists the moves the ticket may make right now.
	Transitions(ctx context.Context, key string) ([]Transition, error)
	// ApplyTransition performs one of those moves. A move with Required fields
	// carries the chosen answers in its Values.
	ApplyTransition(ctx context.Context, key string, tr Transition) error
	// UpdateLabels adds and removes labels without touching other fields.
	UpdateLabels(ctx context.Context, key string, add, remove []string) error
}

// trackerSource pairs a configured Tracker with the query that selects its
// tickets.
type trackerSource struct {
	tracker Tracker
	query   string
}
